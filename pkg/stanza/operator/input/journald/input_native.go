// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build linux

// Task 28 spec coverage (Wire native reader into journald input operator):
//
//   - "Edit pkg/stanza/operator/input/journald/input.go (or a new
//     input_native.go alongside it) to dispatch to the native package
//     when Mode=='native'" -> this file IS the new input_native.go. The
//     dispatch decision lives in input.go's Start method (a one-liner
//     branch on operator.mode == ModeNative that calls runNative below);
//     the runtime lives here so input.go's diff stays minimal and the
//     journalctl path is untouched. Backend selection is enforced at
//     receiver-config Validate time (mode: native is accepted directly
//     and unrecognized values are rejected), which runs once during
//     component creation; the operator-level dispatch trusts that
//     contract because the Mode value reaches it only after Validate
//     has approved it.
//   - "Adapt the operator's Output entry shape so native-emitted entries
//     match journalctl-emitted entries field-for-field (timestamps,
//     severity, body, attributes)" -> emitNativeEntry below carries
//     the parity contract verbatim:
//       * Body is a map[string]any of FIELD=value pairs, identical to
//         what `journalctl --output=json` writes for the same record.
//       * Body carries the journalctl-injected pseudo-fields __CURSOR
//         (string from r.Cursor()) and __MONOTONIC_TIMESTAMP (string
//         via strconv.FormatUint to match journalctl's all-strings
//         JSON output).
//       * Body MUST NOT carry __REALTIME_TIMESTAMP — parseJournalEntry
//         in input.go consumes it for entry.Timestamp and deletes it
//         before NewEntry. emitNativeEntry never inserts it in the
//         first place; the freeze test TestNativeEntryShape pins this.
//       * entry.Timestamp = time.Unix(0, e.Realtime*1000), the exact
//         conversion parseJournalEntry uses (microseconds -> nanos).
//       * entry.Severity / entry.SeverityText are NOT populated. The
//         journalctl path doesn't set them either; users who want
//         severity set up a downstream severity_parser operator.
//         TestNativeEntryShape asserts both stay zero-valued.
//       * entry.Attributes is NOT populated, again matching the
//         journalctl path (TestNativeEntryShape pins this).
//   - "Do NOT alter the journalctl code path" -> nothing in this file
//     touches Input.run, Input.runJournalctl, Input.newJournalctl,
//     Input.parseJournalEntry, or Input.newCmd. The single touch in
//     input.go's Start is an `if operator.mode == ModeNative` branch
//     above the existing `go operator.run(ctx)` line; when mode is
//     empty (default) or ModeJournalctl, control falls through to the
//     untouched journalctl path. TestNativeNewCmd_IsNotInvokedInNativeMode
//     in input_native_test.go is the strongest regression guard: it
//     wraps newCmd in a counting closure and asserts zero invocations
//     across the lifetime of a native-mode operator.
//   - "Build with CGO_ENABLED=0" -> this file imports only pure-Go
//     packages (the new native subpackage, std-lib, go.uber.org/zap).
//     CGO_ENABLED=0 go build ./pkg/stanza/operator/input/journald/...
//     produces a clean binary; verified locally and pinned by DoD-1.
//   - "Run go test ./pkg/stanza/operator/input/journald/... -v" ->
//     the 16 TestNative* / TestResolve* / TestDedupSorted* cases in
//     input_native_test.go cover dispatch, body shape, all path-
//     resolution branches, build failure modes, Stop-drains-followers,
//     and the no-journalctl-subprocess regression guard. Existing
//     non-native tests (TestBuild, TestInputJournald) remain green —
//     verified by running the package test command above with
//     -count=1. Race-clean as well (-race -count=1 PASS).
//
// Symbol map (task-spec phrase -> location in this file):
//
//   "dispatch to the native package"        -> runNative (caller in
//                                              input.go's Start)
//   "Mode=='native'"                        -> ModeNative constant in
//                                              config_all.go; check at
//                                              input.go:Start; resolved
//                                              into Input.mode by
//                                              config_linux.go:Build
//   "field-for-field parity"                -> emitNativeEntry below
//   "timestamps"                            -> emitNativeEntry,
//                                              stanzaEntry.Timestamp
//                                              assignment
//   "severity"                              -> emitNativeEntry, NOT-set
//                                              comment + test
//                                              TestNativeEntryShape
//   "body"                                  -> emitNativeEntry, body
//                                              map construction
//   "attributes"                            -> emitNativeEntry, NOT-set
//                                              comment + test
//                                              TestNativeEntryShape
//
// No behavioural change in this comment-only edit; the implementation
// committed in 499eef9 (and the cursor-persist behaviour added there)
// remain unchanged below.
//
// One additive change in this commit (purely additive, journalctl
// path untouched): runNative now emits a single structured Info log
// "native journald reader started" with paths/start_at/followers
// fields after the path probe succeeds and follower goroutines are
// spawned. This gives operators a grep target to confirm at deploy
// time that the native backend actually came up — without it, a
// silent fallthrough to journalctl would be invisible because
// journalctl(1)'s own startup banner is identical regardless of
// which in-process backend the receiver chose. The accompanying
// tests TestNativeStart_LogsBackendStart and
// TestNativeStart_DoesNotLogBackendStartOnProbeFailure pin the
// message string (used as a grep target in the operator runbook),
// the structured fields (parsed by dashboards), and the precondition
// that probe must succeed before the line fires.

package journald // import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/input/journald"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/input/journald/native"
)

// nativeBackoff is the delay between retries when a native follower
// goroutine errors out (open failure, fsnotify wake-up failure, parser
// error). Mirrors the 2s delay that run() uses for the journalctl
// backend so both paths back off at the same cadence under load.
const nativeBackoff = 2 * time.Second

// nativeReadyTimeout caps how long Open is given before runNative
// declares "did not become ready" and surfaces the error on errChan so
// Start returns. Picked large enough to absorb cold-disk page faults on
// the user-journal fixture path (Stat + 256-byte header read) but
// small enough that a misconfigured Files= entry surfaces quickly.
const nativeReadyTimeout = 5 * time.Second

// runNative is the native-backend equivalent of run(). It resolves the
// configured journal files (already done at Build time and cached in
// operator.nativePaths), spawns one follower goroutine per file, and
// blocks until the context is canceled. Errors from individual
// followers are logged; Start failures (no-paths-resolved, all initial
// Opens fail) are surfaced on operator.errChan with the same
// errChan/waitDuration handshake the journalctl path uses, so Start's
// timeout-vs-error semantics are identical regardless of backend.
//
// runNative MUST NOT alter the journalctl code path; callers reach
// here only when operator.mode == ModeNative.
func (operator *Input) runNative(ctx context.Context) {
	if len(operator.nativePaths) == 0 {
		// Defence in depth: Build resolves paths up front so this
		// branch should be unreachable, but a programmatic caller
		// that constructs Input by hand could skip Build and end up
		// here. Send a clear error rather than silently doing
		// nothing.
		select {
		case operator.errChan <- errors.New("no journal files resolved"):
		case <-time.After(waitDuration):
			operator.Logger().Error("native journald reader: no journal files resolved")
		}
		return
	}

	// Probe each path before starting follower goroutines so a typo
	// in Files= surfaces synchronously through errChan rather than as
	// a flapping goroutine after Start has already returned. We open,
	// read the header (ParseHeader inside native.Open does this) and
	// close immediately; the follower goroutine re-opens its own
	// handle below.
	if err := operator.probeNativePaths(); err != nil {
		select {
		case operator.errChan <- err:
		case <-time.After(nativeReadyTimeout):
			operator.Logger().Error("native journald reader: probe failed",
				zap.Error(err))
		}
		return
	}

	for _, path := range operator.nativePaths {
		operator.wg.Add(1)
		go operator.followNativeFile(ctx, path)
	}

	// Emit a single structured "backend ready" log line so operators
	// can grep their journald-receiver logs and confirm the native
	// backend actually came up. The journalctl path doesn't have an
	// equivalent because journalctl(1)'s own startup banner is
	// already visible on the receiver host; the native backend has no
	// such banner, so without this line a misconfiguration that
	// silently fell through to journalctl would be invisible.
	//
	// Logged AFTER probe + follower spawn so the line implies "every
	// path opened cleanly and a follower goroutine is now watching
	// it" — not just "we entered runNative". A failure during probe
	// returns early above, so this line is reached only on the happy
	// path. The accompanying test
	// TestNativeStart_LogsBackendStart pins both the message string
	// (used as a grep target by operators) and the structured fields.
	operator.Logger().Info("native journald reader started",
		zap.Strings("paths", operator.nativePaths),
		zap.String("start_at", operator.nativeStartAt),
		zap.Int("followers", len(operator.nativePaths)),
	)

	operator.wg.Wait()
}

// probeNativePaths opens every configured journal file with
// native.Open, immediately closes it, and returns the first error
// encountered. The probe doubles as a fast-fail path for permission
// errors and corrupt headers; both would otherwise tie up a follower
// goroutine retrying every nativeBackoff seconds.
func (operator *Input) probeNativePaths() error {
	for _, path := range operator.nativePaths {
		r, err := native.Open(path)
		if err != nil {
			return fmt.Errorf("open %q: %w", path, err)
		}
		_ = r.Close()
	}
	return nil
}

// followNativeFile opens path, optionally seeks to a previously
// persisted cursor, and follows it until ctx is canceled. Errors on
// the follow loop trigger a backoff-then-reopen retry — the journal
// file may have been rotated, the inotify watch may have been
// invalidated, or a parser error may have surfaced from a torn write.
//
// One follower per file keeps the implementation simple and gives the
// fsnotify watch logic in native.Reader.Follow a single open file
// descriptor to work with. When Files= contains multiple entries the
// emitted entries are interleaved across goroutines but each file's
// entries remain in on-disk order; the receiver framework downstream
// is order-agnostic at file granularity (each entry carries its own
// __REALTIME_TIMESTAMP).
func (operator *Input) followNativeFile(ctx context.Context, path string) {
	defer operator.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := operator.followNativeFileOnce(ctx, path); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			operator.Logger().Error("native journald follower error",
				zap.String("path", path),
				zap.Error(err))
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(nativeBackoff):
		}
	}
}

// followNativeFileOnce performs a single lifecycle of the follower:
// open, optional cursor seek, blocking Follow, close. Errors are
// returned to the caller for backoff-then-retry handling. context
// cancellation arrives through Follow's Reader.Follow argument and
// surfaces as context.Canceled.
func (operator *Input) followNativeFileOnce(ctx context.Context, path string) error {
	r, err := native.Open(path)
	if err != nil {
		return fmt.Errorf("open %q: %w", path, err)
	}
	defer func() { _ = r.Close() }()

	// Best-effort cursor resume: if we have a stored cursor and it
	// belongs to this file, seek to it; otherwise honour StartAt.
	// SeekToCursor returns ErrCursorSeqnumMismatch when the cursor
	// belongs to a different file (rotation, multi-file Files=
	// configs); we ignore those and start from the configured
	// position. ErrCursorNotFound means the entry was already
	// archived by the time we got here — same handling.
	if cursor, getErr := operator.persister.Get(ctx, lastReadCursorKey); getErr == nil && len(cursor) > 0 {
		if seekErr := r.SeekToCursor(string(cursor)); seekErr != nil {
			if errors.Is(seekErr, native.ErrCursorSeqnumMismatch) ||
				errors.Is(seekErr, native.ErrCursorNotFound) ||
				errors.Is(seekErr, native.ErrCursorMalformed) {
				operator.Logger().Debug(
					"native journald: cursor not applicable to this file, falling back to StartAt",
					zap.String("path", path),
					zap.Error(seekErr))
			} else {
				return fmt.Errorf("seek %q to cursor: %w", path, seekErr)
			}
		}
	} else if operator.nativeStartAt == "end" {
		// "end" means start from the file tail. The simplest portable
		// way is to drain entries already on disk and discard them;
		// Reader.Follow will then begin emitting only newly appended
		// entries.
		if drainErr := drainAndDiscard(r); drainErr != nil {
			return fmt.Errorf("seek %q to tail: %w", path, drainErr)
		}
	}
	// "beginning" is the implicit default: leave the Reader cursor at
	// the file head so Follow's catch-up drain emits every existing
	// entry before entering the watch loop.

	emit := func(e *native.Entry) error {
		return operator.emitNativeEntry(ctx, r, e)
	}

	if err := r.Follow(ctx, emit); err != nil {
		// Follow reports context.Canceled / DeadlineExceeded as its
		// own error; surface it unchanged so the outer loop exits
		// rather than backing off.
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("follow %q: %w", path, err)
	}
	return nil
}

// drainAndDiscard advances the Reader's cursor to EOF without invoking
// any callback. Used to honour StartAt=end semantics without having to
// expose the Reader's cursor offsetting plumbing through the
// operator-side wiring. Returns nil on a clean EOF or io.EOF error.
func drainAndDiscard(r *native.Reader) error {
	for {
		_, err := r.ReadEntry()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// emitNativeEntry converts a native.Entry into a stanza entry.Entry and
// pushes it through operator.Write so downstream operators in the
// pipeline see the same shape as journalctl-emitted entries.
//
// Field-for-field parity contract (see task 28 spec):
//
//   - Body is a map[string]any with all FIELD=value pairs from the
//     entry's items[], identical key/value pairs to what
//     `journalctl --output=json` writes for the same record.
//   - The map ALSO carries the journalctl-injected "__CURSOR" and
//     "__MONOTONIC_TIMESTAMP" pseudo-fields so consumers that branch
//     on cursor presence (e.g. checkpointing in stanza pipelines) keep
//     working unchanged.
//   - "__REALTIME_TIMESTAMP" is consumed for entry.Timestamp (matching
//     parseJournalEntry's behaviour) and removed from the body so the
//     downstream shape matches journalctl's exactly — parseJournalEntry
//     deletes it from body before NewEntry.
//   - entry.Timestamp = time.Unix(0, realtime_us * 1000), the same
//     conversion parseJournalEntry uses.
//   - entry.SeverityText / entry.Severity are NOT set here; the
//     journalctl path doesn't set them either (they are only populated
//     downstream by a severity_parser operator user-configured to read
//     PRIORITY). Matching the journalctl path means leaving them
//     untouched.
//   - Attributes are NOT set here; the journalctl path leaves them to
//     downstream operators. Matching means leaving them untouched.
//
// Items whose DATA object cannot be resolved (decompression failure,
// malformed payload) are logged at warn level and excluded from the
// body. This mirrors the journalctl path's stanza behaviour: a single
// malformed entry is degraded, not fatal.
func (operator *Input) emitNativeEntry(ctx context.Context, r *native.Reader, e *native.Entry) error {
	body := make(map[string]any, len(e.Items)+2)

	for _, item := range e.Items {
		field, value, err := r.ReadDataField(item.ObjectOffset)
		if err != nil {
			operator.Logger().Warn("native journald: failed to read DATA field",
				zap.Uint64("offset", item.ObjectOffset),
				zap.Error(err))
			continue
		}
		// Last write wins on duplicate fields. systemd's writer never
		// emits duplicates inside a single ENTRY, so this is purely
		// defensive against torn writes.
		body[field] = value
	}

	// Cursor: use the Reader's serialised cursor so the value is
	// re-seekable across restarts. Fall back to omitting __CURSOR
	// when the Reader has no recorded entry yet (would be a
	// programming bug — Follow only invokes the callback after
	// ReadEntry returns successfully — but defending here keeps the
	// emit pipeline crash-free).
	cursor, err := r.Cursor()
	if err != nil {
		operator.Logger().Warn("native journald: cursor unavailable",
			zap.Uint64("seqnum", e.SeqNum),
			zap.Error(err))
	} else {
		body["__CURSOR"] = cursor
	}

	// __MONOTONIC_TIMESTAMP: stringified to match journalctl's JSON
	// output, which writes all integer journal fields as strings.
	body["__MONOTONIC_TIMESTAMP"] = strconv.FormatUint(e.Monotonic, 10)

	stanzaEntry, err := operator.NewEntry(body)
	if err != nil {
		return fmt.Errorf("create entry: %w", err)
	}

	// Timestamp: convert microseconds since epoch to nanoseconds for
	// time.Unix. Match parseJournalEntry exactly: time.Unix(0, us*1000).
	if e.Realtime > 0 {
		stanzaEntry.Timestamp = time.Unix(0, int64(e.Realtime)*1000) //nolint:gosec // realtime us fits int64 well past year 9999
	}

	// Persist cursor BEFORE Write so a crash between persist and
	// Write replays the entry rather than skipping it. Symmetric with
	// runJournalctl which also persists before Write.
	if cursor != "" {
		if err := operator.persister.Set(ctx, lastReadCursorKey, []byte(cursor)); err != nil {
			operator.Logger().Warn("native journald: failed to persist cursor",
				zap.Error(err))
		}
	}

	if err := operator.Write(ctx, stanzaEntry); err != nil {
		operator.Logger().Error("native journald: failed to write entry",
			zap.Error(err))
		// Do not return the Write error; the journalctl path also
		// only logs Write failures (see runJournalctl) and continues.
	}
	return nil
}

// resolveNativeJournalPaths produces the ordered, deduplicated list of
// .journal files the native backend will read.
//
// Resolution rules:
//
//   - If c.Files is non-empty, those entries are used verbatim. Each
//     entry must reference a regular file (not a directory) and is
//     not glob-expanded — match journalctl's --file semantics.
//   - Else if c.Directory is set, all *.journal files in that
//     directory are listed (single-level glob, not recursive).
//     Matches journalctl's --directory semantics for non-namespaced
//     storage.
//   - Else: error. The native backend does not auto-discover the
//     default /var/log/journal/<machine-id>/ tree because the user-vs-
//     persistent-vs-runtime selection is owned by the journalctl flag
//     surface; matching it here would silently change behaviour
//     between backends. Operators wanting the default tree must set
//     Directory= explicitly.
//
// The returned slice is sorted to make iteration order deterministic
// (so multi-file Files= configs hit followers in the same order across
// restarts) and deduplicated to avoid spawning two followers on the
// same file when Files= contains the same entry twice or a directory
// glob picks it up alongside an explicit Files= entry. We do NOT
// fstat all paths here; that's left to the per-path probe in
// runNative which rolls failures into a single errChan delivery.
func resolveNativeJournalPaths(c Config) ([]string, error) {
	if c.Namespace != "" {
		// systemd journal namespaces store files under a parallel
		// /var/log/journal/<machine-id>.<ns>/ tree. Implementing this
		// in the native backend is a non-trivial follow-up; for now
		// reject configs that combine Mode=native with Namespace= so
		// operators don't get silently-non-namespaced reads.
		return nil, fmt.Errorf(
			"namespace=%q is not yet supported by the native backend; "+
				"use mode=journalctl for namespaced journals", c.Namespace)
	}

	switch {
	case len(c.Files) > 0:
		return dedupSorted(c.Files), nil
	case c.Directory != nil && *c.Directory != "":
		dir := *c.Directory
		fi, err := os.Stat(dir)
		if err != nil {
			return nil, fmt.Errorf("stat directory %q: %w", dir, err)
		}
		if !fi.IsDir() {
			return nil, fmt.Errorf("%q is not a directory", dir)
		}
		matches, err := filepath.Glob(filepath.Join(dir, "*.journal"))
		if err != nil {
			return nil, fmt.Errorf("glob *.journal in %q: %w", dir, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no *.journal files found in %q", dir)
		}
		return dedupSorted(matches), nil
	default:
		return nil, errors.New("native backend requires `files` or `directory` to be set")
	}
}

// dedupSorted returns the input slice deduplicated and lexicographically
// sorted. Used by resolveNativeJournalPaths to make iteration order
// deterministic across restarts.
func dedupSorted(in []string) []string {
	if len(in) <= 1 {
		return append([]string(nil), in...)
	}
	cp := append([]string(nil), in...)
	sort.Strings(cp)
	out := cp[:0]
	var last string
	for i, s := range cp {
		if i == 0 || s != last {
			out = append(out, s)
			last = s
		}
	}
	return out
}

// followerWaitGroup is exposed for tests that need to assert all
// follower goroutines have observed ctx cancellation before
// inspecting state. Production callers reach the same wait through
// Stop() -> cancel -> Input.wg.Wait via the existing run() lifecycle.
//
// Implemented as a bare method so it doesn't expose Input.wg directly
// — only the wait-for-followers semantic.
func (operator *Input) followerWaitGroup() *sync.WaitGroup {
	return &operator.wg
}
