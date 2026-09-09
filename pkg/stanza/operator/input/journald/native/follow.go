// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package native // import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/input/journald/native"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/fsnotify/fsnotify"
)

// FollowDefaultPollInterval is the cadence used by the time.Ticker
// fallback when fsnotify is unavailable. 25 ms keeps the worst-case
// detection latency below 50 ms — one polling period plus a single
// ReadEntry round-trip — which is the bound enforced by the Phase-3
// systemd-cat integration test (task 22 of the rollout plan).
const FollowDefaultPollInterval = 25 * time.Millisecond

// FollowStrategy identifies which change-detection mechanism the most
// recent Follow call selected. Exposed via Reader.LastFollowStrategy()
// for diagnostics and tests that want to assert one path was taken.
type FollowStrategy uint8

// FollowStrategy values.
const (
	// FollowStrategyUnset is the zero value reported before Follow has
	// been called on a Reader.
	FollowStrategyUnset FollowStrategy = iota
	// FollowStrategyInotify indicates Follow successfully attached an
	// fsnotify (inotify on Linux) watch to the journal file.
	FollowStrategyInotify
	// FollowStrategyPoll indicates Follow is using the time.Ticker
	// fallback. This happens when fsnotify.NewWatcher fails (e.g. inotify
	// instances exhausted), Watcher.Add fails (NFS / FUSE / older
	// kernel), or WithFollowForcePoll(true) was passed to Open.
	FollowStrategyPoll
)

// String returns a human-readable form of the strategy. Used in error
// messages, log lines, and the Phase-3 documentation tests.
func (s FollowStrategy) String() string {
	switch s {
	case FollowStrategyInotify:
		return "inotify"
	case FollowStrategyPoll:
		return "poll"
	default:
		return "unset"
	}
}

// LastFollowStrategy returns the change-detection mechanism that the most
// recent Follow call selected. Returns FollowStrategyUnset before the
// first call. The value is updated synchronously inside Follow before
// the watch loop begins, so tests may read it after the goroutine has
// returned (or, when running under WithFollowForcePoll, immediately).
func (r *Reader) LastFollowStrategy() FollowStrategy {
	return r.followStrategy
}

// WithFollowForcePoll forces Follow() to skip the fsnotify probe and use
// the time.Ticker fallback unconditionally. Primarily a test hook for
// exercising the poll path on platforms where inotify would otherwise be
// chosen. Production callers should leave this disabled and let Follow
// pick the best available strategy automatically.
func WithFollowForcePoll(force bool) Option {
	return func(r *Reader) {
		r.followForcePoll = force
	}
}

// WithFollowPollInterval overrides the cadence used by the time.Ticker
// fallback. Values <= 0 are ignored and FollowDefaultPollInterval is
// used. Useful for tests that want a faster (or slower) poll loop.
func WithFollowPollInterval(d time.Duration) Option {
	return func(r *Reader) {
		r.followPollInterval = d
	}
}

// Follow blocks until ctx is canceled, invoking fn for each newly
// appended ENTRY object. It returns ctx.Err() on cancellation, the first
// error returned by fn, or any I/O failure encountered while parsing.
//
// On the first invocation Follow performs a full catch-up drain: every
// entry currently visible via ReadEntry is delivered before the watch
// loop begins. After the catch-up phase completes, Follow watches the
// underlying journal file for modifications using one of two strategies:
//
//   - fsnotify (inotify on Linux) — the preferred path. Lower latency,
//     no busy polling, and triggers exactly once per kernel buffer flush.
//   - time.Ticker poll — the fallback. Re-stats the file every
//     FollowDefaultPollInterval (or the WithFollowPollInterval override)
//     and triggers a drain when the size or mtime changes.
//
// # Strategy selection
//
// Follow first calls fsnotify.NewWatcher and, if successful,
// Watcher.Add(file). Failure of either step (older kernel, ENOSPC on
// inotify instances, NFS / FUSE mounts that do not propagate file events
// to inotify) triggers a transparent fallback to the poll loop. The
// chosen strategy is recorded on the Reader and queryable via
// LastFollowStrategy(). WithFollowForcePoll(true) skips the probe.
//
// # Tail re-parse semantics
//
// On each detected change, Follow calls ParseHeader on the open file to
// pick up the writer's updates to tail_object_offset and arena_size,
// then extends arenaEnd accordingly. The systemd journal writer updates
// tail_object_offset before flipping the file state to indicate a new
// object is visible, so re-reading the header gives a strict upper bound
// on what we are allowed to read without observing a half-written object.
// arenaEnd is only ever extended, never shrunk — truncation of an open
// journal file is not a normal systemd operation and shrinking would
// risk skipping entries the linear scan has already advanced past.
//
// # Concurrency
//
// fn is invoked synchronously on Follow's goroutine. The Reader's cursor
// state is mutated inside Follow, so callers MUST NOT call ReadEntry,
// ParseEntryArray, or any other Reader method on the same instance
// while Follow is running. Use a separate Reader for parallel reads.
//
// # Build constraint
//
// This file builds with CGO_ENABLED=0. fsnotify itself is pure Go on
// Linux (it talks to inotify via golang.org/x/sys/unix syscalls) so the
// import does not pull in any C code.
//
// # Errors
//
//   - ErrReaderClosed if Close has already been called on this Reader.
//   - "nil callback" if fn is nil.
//   - File rotation (Rename / Remove of the active journal, e.g. when a
//     volatile /run journal fills and systemd archives it) is handled
//     transparently and losslessly: Follow drains the archived file's
//     tail, re-opens the fresh file at the same path, re-arms the watch,
//     and continues. See handleRotation. (Both the inotify and poll
//     strategies handle this.)
//   - Any error returned by ReadEntry, ParseHeader, or fn is propagated
//     unchanged after wrapping with the offset where it occurred.
func (r *Reader) Follow(ctx context.Context, fn func(*Entry) error) error {
	if r.closed {
		return ErrReaderClosed
	}
	if fn == nil {
		return errors.New("native: Follow: nil callback")
	}
	if ctx == nil {
		return errors.New("native: Follow: nil context")
	}

	// Initial catch-up drain — emit every entry currently visible before
	// we begin watching for changes. This guarantees the caller never
	// misses an entry that was written between Open and Follow.
	if err := r.drainOnce(fn); err != nil {
		return err
	}

	if r.followForcePoll {
		r.followStrategy = FollowStrategyPoll
		return r.followPoll(ctx, fn, r.pollInterval())
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		// Inotify instances exhausted or unsupported on this platform.
		// The poll loop continues to function so this is non-fatal.
		r.followStrategy = FollowStrategyPoll
		return r.followPoll(ctx, fn, r.pollInterval())
	}
	if addErr := watcher.Add(r.f.Name()); addErr != nil {
		_ = watcher.Close()
		r.followStrategy = FollowStrategyPoll
		return r.followPoll(ctx, fn, r.pollInterval())
	}
	defer watcher.Close()

	r.followStrategy = FollowStrategyInotify
	return r.followWatch(ctx, watcher, fn)
}

// pollInterval returns the configured poll cadence, falling back to the
// default when none was set or a non-positive value was provided.
func (r *Reader) pollInterval() time.Duration {
	if r.followPollInterval > 0 {
		return r.followPollInterval
	}
	return FollowDefaultPollInterval
}

// drainOnce calls ReadEntry until io.EOF, invoking fn for each entry.
// Returns nil on a clean drain to EOF, or any error from ReadEntry or
// fn (with fn errors propagated unchanged so callers can branch on
// sentinels).
func (r *Reader) drainOnce(fn func(*Entry) error) error {
	for {
		e, err := r.ReadEntry()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if cbErr := fn(e); cbErr != nil {
			return cbErr
		}
	}
}

// refreshTail re-parses the journal header to pick up any extension of
// tail_object_offset / arena_size that the writer made since the file
// was opened, and extends arenaEnd accordingly. arenaEnd is only ever
// extended, never shrunk: truncation isn't a normal journal operation
// and shrinking would risk missing entries the linear scan has already
// crossed.
func (r *Reader) refreshTail() error {
	hdr, err := ParseHeader(r.f)
	if err != nil {
		return fmt.Errorf("native: Follow: refresh header: %w", err)
	}
	fi, err := r.f.Stat()
	if err != nil {
		return fmt.Errorf("native: Follow: stat: %w", err)
	}
	fileSize := uint64(fi.Size())

	arenaEnd := fileSize
	if hdr.ArenaSize > 0 {
		declared := hdr.HeaderSize + hdr.ArenaSize
		if declared < arenaEnd {
			arenaEnd = declared
		}
	}
	if arenaEnd > r.arenaEnd {
		r.arenaEnd = arenaEnd
	}
	// Extend the tail-object bound so the linear scan in ReadEntry will
	// advance into the region the writer just filled. Without this the
	// scan stays parked at the original TailObjectOffset and Follow never
	// emits newly-appended entries. Only ever extended, never shrunk, for
	// the same reason arenaEnd is monotonic.
	if hdr.TailObjectOffset > r.tailObjectOffset {
		r.tailObjectOffset = hdr.TailObjectOffset
	}
	r.hdr = hdr
	return nil
}

// followWatch implements the inotify-driven follow loop. Write events
// trigger a tail refresh and a drain; Rename / Remove events are
// surfaced as errors so the caller can re-Open the rotated file.
//
// fsnotify may emit several Write events for a single journal append
// (one per page flush). We do not debounce because drainOnce is cheap
// when the cursor is already at EOF — ParseObjectHeader reads 16 bytes
// and returns io.EOF, so a spurious wake costs at most one ReadAt.
func (r *Reader) followWatch(ctx context.Context, w *fsnotify.Watcher, fn func(*Entry) error) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err, ok := <-w.Errors:
			if !ok {
				return errors.New("native: Follow: watcher errors channel closed")
			}
			if err != nil {
				return fmt.Errorf("native: Follow: watcher error: %w", err)
			}
		case ev, ok := <-w.Events:
			if !ok {
				return errors.New("native: Follow: watcher events channel closed")
			}
			// Rename / Remove mean the journal rotated: systemd renamed
			// the active system.journal to an archived name and created a
			// fresh system.journal at the same path. Handle this
			// transparently and losslessly rather than aborting (the
			// previous behavior stopped the follower and, under
			// start_at:end re-open, silently dropped the post-rotation
			// backlog). See handleRotation.
			if ev.Op&(fsnotify.Rename|fsnotify.Remove) != 0 {
				if rotErr := r.handleRotation(w, fn); rotErr != nil {
					return rotErr
				}
				continue
			}
			// Only Write / Create events advance the journal. Chmod
			// is ignored.
			if ev.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}
			if err := r.refreshTail(); err != nil {
				return err
			}
			if err := r.drainOnce(fn); err != nil {
				return err
			}
		}
	}
}

// rotationReopenAttempts bounds how many times handleRotation retries the
// re-open of the journal path after a rename. systemd creates the fresh
// system.journal immediately after archiving the old one, but the Create
// event for the new file can arrive a few milliseconds after the Rename of
// the old. A short bounded retry covers that window without blocking.
const rotationReopenAttempts = 50

// rotationReopenDelay is the per-attempt sleep between re-open retries.
// 50 attempts x 20ms = up to 1s, generous for the rename->create gap.
const rotationReopenDelay = 20 * time.Millisecond

// handleRotation continues following across a journal rotation without
// losing entries. The sequence is:
//
//  1. Drain the OLD (now-renamed/archived) file to EOF, emitting any
//     trailing entries the writer flushed before rotating. We still hold a
//     valid fd to it — on Linux a rename does not invalidate an open
//     descriptor — so refreshTail+drainOnce reads whatever remains.
//  2. Re-open the original PATH, which now resolves to the fresh
//     system.journal systemd just created. Retried briefly because the new
//     file may not be visible the instant the Rename event fires.
//  3. Re-arm the fsnotify watch on the new fd (the watch followed the old
//     inode away on rename, so future writes to the new file would
//     otherwise go unnoticed).
//  4. Drain the new file from its head so entries already written to it
//     before the watch was re-armed are emitted immediately.
//
// Ordering (drain-old before swap) guarantees no entry is skipped across
// the boundary; the cursor checkpoint in the operator layer additionally
// dedupes on restart.
func (r *Reader) handleRotation(w *fsnotify.Watcher, fn func(*Entry) error) error {
	oldPath := r.f.Name()

	// 1. Drain trailing entries from the archived file.
	if err := r.refreshTail(); err != nil {
		// A refresh failure here is non-fatal: the old file may already
		// be gone (Remove rather than Rename). Fall through to reopen.
		_ = err
	} else if err := r.drainOnce(fn); err != nil {
		return fmt.Errorf("native: Follow: drain pre-rotation tail of %q: %w", oldPath, err)
	}

	// 2. Re-open the path (now the fresh file), with a short retry for the
	// rename->create gap.
	var reopenErr error
	for attempt := 0; attempt < rotationReopenAttempts; attempt++ {
		if reopenErr = r.reopenSamePath(); reopenErr == nil {
			break
		}
		time.Sleep(rotationReopenDelay)
	}
	if reopenErr != nil {
		return fmt.Errorf("native: Follow: reopen after rotation: %w", reopenErr)
	}

	// 3. Re-arm the watch on the new fd. Remove the stale watch first
	// (best-effort; it may already be gone with the old inode).
	_ = w.Remove(oldPath)
	if err := w.Add(r.f.Name()); err != nil {
		return fmt.Errorf("native: Follow: re-arm watch on %q after rotation: %w", r.f.Name(), err)
	}

	// 4. Drain the new file from its head.
	if err := r.drainOnce(fn); err != nil {
		return fmt.Errorf("native: Follow: drain new file %q after rotation: %w", r.f.Name(), err)
	}
	return nil
}

// followPoll implements the time.Ticker fallback. It re-stats the open
// file every interval; on size or mtime change it refreshes the tail
// and drains new entries. The default 25 ms cadence keeps detection
// latency below 50 ms in the worst case.
func (r *Reader) followPoll(ctx context.Context, fn func(*Entry) error, interval time.Duration) error {
	if interval <= 0 {
		interval = FollowDefaultPollInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()

	var (
		lastSize  int64
		lastMTime time.Time
	)
	if fi, err := r.f.Stat(); err == nil {
		lastSize = fi.Size()
		lastMTime = fi.ModTime()
	} else {
		return fmt.Errorf("native: Follow: initial stat: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			fi, err := r.f.Stat()
			if err != nil {
				return fmt.Errorf("native: Follow: stat: %w", err)
			}
			// Rotation detection for the poll path: if the file now at our
			// path is a DIFFERENT inode than the fd we hold, the journal
			// rotated (systemd archived our file and created a fresh one).
			// The held fd would otherwise keep returning the frozen
			// archived size and the loop would silently stall. os.Stat on
			// the path follows the new inode; os.SameFile compares dev+ino.
			if pathFI, statErr := os.Stat(r.f.Name()); statErr == nil && !os.SameFile(fi, pathFI) {
				if rotErr := r.handlePollRotation(fn); rotErr != nil {
					return rotErr
				}
				// Reset the change baseline to the new file and continue.
				if nfi, e := r.f.Stat(); e == nil {
					lastSize = nfi.Size()
					lastMTime = nfi.ModTime()
				}
				continue
			}
			size := fi.Size()
			mtime := fi.ModTime()
			if size == lastSize && mtime.Equal(lastMTime) {
				continue
			}
			lastSize = size
			lastMTime = mtime
			if err := r.refreshTail(); err != nil {
				return err
			}
			if err := r.drainOnce(fn); err != nil {
				return err
			}
		}
	}
}

// handlePollRotation is the poll-strategy counterpart to handleRotation:
// it drains the archived file's tail, then re-opens the fresh file at the
// same path. There is no fsnotify watch to re-arm in the poll path, so it
// is a strict subset of handleRotation. Draining the old file first keeps
// the boundary lossless.
func (r *Reader) handlePollRotation(fn func(*Entry) error) error {
	oldPath := r.f.Name()
	if err := r.refreshTail(); err == nil {
		if err := r.drainOnce(fn); err != nil {
			return fmt.Errorf("native: Follow: drain pre-rotation tail of %q: %w", oldPath, err)
		}
	}
	var reopenErr error
	for attempt := 0; attempt < rotationReopenAttempts; attempt++ {
		if reopenErr = r.reopenSamePath(); reopenErr == nil {
			break
		}
		time.Sleep(rotationReopenDelay)
	}
	if reopenErr != nil {
		return fmt.Errorf("native: Follow: reopen after rotation: %w", reopenErr)
	}
	if err := r.drainOnce(fn); err != nil {
		return fmt.Errorf("native: Follow: drain new file %q after rotation: %w", r.f.Name(), err)
	}
	return nil
}
