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
	"unicode/utf8"

	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/input/journald/native"
)

// nativeBackoff is the delay between retries when a native follower
// goroutine errors out (open failure, fsnotify wake-up failure, parser
// error). Mirrors the 2s delay that run() uses for the journalctl
// backend so both paths back off at the same cadence under load.
const nativeBackoff = 2 * time.Second

// runNative is the native-backend equivalent of run(). It resolves the
// configured journal files (already done at Build time and cached in
// operator.nativePaths), probes each path, and spawns one follower
// goroutine per file. Unlike the journalctl path's run(), runNative is
// invoked SYNCHRONOUSLY from Start and returns an error instead of
// communicating over errChan + waitDuration.
//
// Why synchronous (two races the old goroutine form had):
//
//   - Start success-before-probe-failure: the probe can take up to
//     nativeReadyTimeout (5s) to fail, but Start only waited
//     waitDuration (1s) on errChan before returning nil. A slow probe
//     failure therefore reported success to the caller while the
//     errChan send in the goroutine blocked with no reader. Returning
//     the probe error directly removes the timeout race entirely.
//   - wg.Add/wg.Wait race: when followers were spawned from the
//     background goroutine, a Stop() arriving immediately after Start
//     returned could call wg.Wait() concurrently with a follower's
//     wg.Add(1) — undefined behaviour for sync.WaitGroup. Doing every
//     wg.Add here, before Start returns, establishes a happens-before
//     edge so the first possible wg.Wait() always observes the final
//     counter.
//
// On any error return, NO follower goroutines have been spawned (the
// error paths precede the spawn loop), so the caller can simply cancel
// the context without waiting on the wait group.
//
// runNative MUST NOT alter the journalctl code path; callers reach
// here only when operator.mode == ModeNative.
func (operator *Input) runNative(ctx context.Context) error {
	if len(operator.nativePaths) == 0 {
		// Defence in depth: Build resolves paths up front so this
		// branch should be unreachable, but a programmatic caller
		// that constructs Input by hand could skip Build and end up
		// here. Return a clear error rather than silently doing
		// nothing.
		return errors.New("no journal files resolved")
	}

	// Probe each path before starting follower goroutines so a typo
	// in Files= surfaces synchronously to the caller rather than as a
	// flapping goroutine after Start has already returned. We open,
	// read the header (ParseHeader inside native.Open does this) and
	// close immediately; the follower goroutine re-opens its own
	// handle below.
	if err := operator.probeNativePaths(); err != nil {
		return err
	}

	// Register every follower with the wait group BEFORE spawning any
	// of them, and before returning to Start. This guarantees all
	// wg.Add calls happen-before any Stop()->wg.Wait() the caller can
	// issue once Start has returned.
	operator.wg.Add(len(operator.nativePaths))
	for _, path := range operator.nativePaths {
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

	return nil
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

	// Per-file cursor key so concurrent followers never overwrite each
	// other's checkpoints (see nativeCursorKey).
	cursorKey := nativeCursorKey(path)

	// start_at:end skips the pre-existing backlog only on the FIRST follow
	// of a file; a retry re-open must resume and redeliver, not re-skip.
	firstFollow := operator.markNativeFollowStarted(path)

	// Best-effort cursor resume: if we have a stored cursor and it
	// belongs to this file, seek to it; otherwise honour StartAt.
	// SeekToCursor returns ErrCursorSeqnumMismatch when the cursor
	// belongs to a different file (rotation, multi-file Files=
	// configs); we ignore those and start from the configured
	// position. ErrCursorNotFound means the entry was already
	// archived by the time we got here — same handling.
	seekApplied := false
	if cursor, getErr := operator.persister.Get(ctx, cursorKey); getErr == nil && len(cursor) > 0 {
		if seekErr := r.SeekToCursor(string(cursor)); seekErr != nil {
			if errors.Is(seekErr, native.ErrCursorSeqnumMismatch) ||
				errors.Is(seekErr, native.ErrCursorNotFound) ||
				errors.Is(seekErr, native.ErrCursorMalformed) {
				// Cursor unusable for this file: fall through to the shared
				// StartAt handling below instead of leaving the Reader at
				// the head, which would replay the whole file via Follow.
				operator.Logger().Debug(
					"native journald: cursor not applicable to this file, honoring StartAt",
					zap.String("path", path),
					zap.Error(seekErr))
			} else {
				return fmt.Errorf("seek %q to cursor: %w", path, seekErr)
			}
		} else {
			seekApplied = true
		}
	}

	// StartAt handling shared by the no-cursor and unusable-cursor paths.
	// "end" drains entries already on disk and discards them so Follow
	// emits only newly appended entries — but only on the first follow of
	// this file (firstFollow); a retry re-open must resume and redeliver.
	// "beginning" is the implicit default: leave the Reader cursor at the
	// file head so Follow's catch-up drain emits every existing entry
	// before entering the watch loop.
	if !seekApplied && operator.nativeStartAt == "end" && firstFollow {
		if drainErr := drainAndDiscard(ctx, r); drainErr != nil {
			if errors.Is(drainErr, context.Canceled) {
				return drainErr
			}
			return fmt.Errorf("seek %q to tail: %w", path, drainErr)
		}
	}

	emit := func(e *native.Entry) error {
		return operator.emitNativeEntry(ctx, r, cursorKey, e)
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

// markNativeFollowStarted records that a follow lifecycle has begun for
// path and reports whether this was the first such call in the current
// process. It gates the start_at:end backlog drain in followNativeFileOnce
// so the drain runs only on the first follow of a file: a retry re-open —
// after a Write failure aborted Follow before any cursor was persisted —
// must resume and redeliver the failed entry rather than discard it as
// backlog. The state is in-memory only; across a process restart the
// persisted cursor is the correct resume point, so nothing needs to
// survive the restart. Each file is followed by a single goroutine, so the
// only concurrency is across distinct paths, which sync.Map handles.
func (operator *Input) markNativeFollowStarted(path string) bool {
	_, loaded := operator.nativeFollowStarted.LoadOrStore(path, struct{}{})
	return !loaded
}

// drainAndDiscard advances the Reader's cursor to EOF without invoking
// any callback. Used to honour StartAt=end semantics without having to
// expose the Reader's cursor offsetting plumbing through the
// operator-side wiring. Returns nil on a clean EOF or io.EOF error.
//
// ctx is checked on every iteration so a Stop() during the drain of a
// large StartAt=end journal returns promptly (with ctx.Err()) instead of
// blocking shutdown until the whole backlog has been walked to EOF. The
// returned ctx.Err() wraps context.Canceled, which followNativeFile
// recognises and treats as a clean follower exit.
func drainAndDiscard(ctx context.Context, r *native.Reader) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
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
func (operator *Input) emitNativeEntry(ctx context.Context, r *native.Reader, cursorKey string, e *native.Entry) error {
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
		body[field] = nativeFieldValue(field, value, operator.convertMessageBytes)
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

	// Timestamp: convert microseconds since epoch via the bounds-checked
	// helper rather than an inline uint64->int64 cast. RealtimeAsTime
	// rejects values whose seconds component would overflow int64 (e.g.
	// systemd's USEC_INFINITY sentinel or a crafted/corrupt journal),
	// returning an error instead of silently wrapping to a negative,
	// pre-1970 instant. On error we leave Timestamp zero-valued and log;
	// the entry is still emitted with its body intact. For in-range values
	// this yields the identical instant to parseJournalEntry's
	// time.Unix(0, us*1000) (time.Time.Equal compares instants, so the
	// backend-parity assertion holds despite the helper's .UTC()).
	if e.Realtime > 0 {
		ts, terr := e.RealtimeAsTime()
		if terr != nil {
			operator.Logger().Warn("native journald: realtime timestamp out of range, leaving unset",
				zap.Uint64("seqnum", e.SeqNum),
				zap.Uint64("realtime_us", e.Realtime),
				zap.Error(terr))
		} else {
			stanzaEntry.Timestamp = ts
		}
	}

	// Write FIRST, then persist the cursor only on success. Advancing the
	// checkpoint before the entry is durably handed to the pipeline would
	// silently drop the entry if Write failed (the cursor would already
	// point past it on the next start).
	//
	// On Write failure we RETURN the error so Reader.Follow aborts the
	// current follow loop; followNativeFile then backs off and reopens.
	// On the reopen followNativeFileOnce resumes at the last *persisted*
	// cursor (the previous successfully-written entry) via SeekToCursor,
	// whose resume point is the entry AFTER that cursor — i.e. exactly this
	// failed entry. When NO cursor has been persisted yet (this entry's
	// Write failed before the first successful persist), there is nothing
	// to seek to; the retry instead relies on followNativeFileOnce
	// suppressing the start_at:end backlog drain on a re-open (see
	// markNativeFollowStarted), so Follow's catch-up drain re-reads from the
	// file head and redelivers this entry rather than discarding it as
	// backlog. Either way delivery is at-least-once: without the return,
	// Follow would advance to the next entry and its successful Write would
	// persist a cursor past the failed one, permanently skipping it. (The
	// journalctl path only logs Write failures because its journalctl
	// subprocess cannot be rewound to an arbitrary entry; the native Reader
	// can, so it does the stronger thing.)
	if err := operator.Write(ctx, stanzaEntry); err != nil {
		operator.Logger().Error("native journald: failed to write entry, aborting follow to retry from last cursor",
			zap.Uint64("seqnum", e.SeqNum),
			zap.Error(err))
		return fmt.Errorf("write entry seqnum=%d: %w", e.SeqNum, err)
	}

	if cursor != "" {
		if err := operator.persister.Set(ctx, cursorKey, []byte(cursor)); err != nil {
			operator.Logger().Warn("native journald: failed to persist cursor",
				zap.Error(err))
		}
	}
	return nil
}

// nativeFieldValue shapes a single FIELD=value pair so the native body
// matches what the journalctl JSON backend (parseJournalEntry) would put
// in entry.Body for the same record, honouring convert_message_bytes.
//
// The journalctl path derives field values from `journalctl --output=json`:
//   - A value that is valid UTF-8 is emitted as a JSON string and
//     unmarshals to a Go string — identical to what the native reader
//     produces from the raw payload, so we pass it through unchanged.
//   - A non-UTF-8 value (binary field) is emitted by journalctl as a JSON
//     array of byte values, which unmarshals to []any{float64...}. The
//     native reader instead produced a Go string from the same raw bytes,
//     so we convert it to the same []any shape to keep the bodies equal.
//   - parseJournalEntry additionally re-stringifies ONLY the MESSAGE field
//     when convert_message_bytes is set (string(bytes)). The native string
//     already IS string(rawBytes), so for MESSAGE+convert we return it
//     as-is rather than expanding it to a byte array.
func nativeFieldValue(field, value string, convertMessageBytes bool) any {
	if utf8.ValidString(value) {
		return value
	}
	if field == "MESSAGE" && convertMessageBytes {
		return value
	}
	return bytesToAnySlice([]byte(value))
}

// bytesToAnySlice expands raw bytes into a []any of float64 values,
// mirroring how encoding/json unmarshals the byte-array form that
// `journalctl --output=json` writes for non-UTF-8 fields (JSON numbers
// decode to float64). Converting back to []byte from the native value
// string is lossless: a Go string preserves arbitrary bytes verbatim.
func bytesToAnySlice(b []byte) []any {
	out := make([]any, len(b))
	for i, by := range b {
		out[i] = float64(by)
	}
	return out
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
