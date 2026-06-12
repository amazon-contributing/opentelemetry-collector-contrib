// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package native // import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/input/journald/native"

import (
	"context"
	"errors"
	"fmt"
	"io"
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
//   - File-rotation events (Rename / Remove) are surfaced as errors so
//     the caller can re-Open the rotated file. Follow does NOT attempt
//     to follow rotation transparently because the new file's header
//     and cursor would diverge from the closed one.
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
			// Rename / Remove invalidate our open file descriptor —
			// the journal has been rotated. Surface this so the
			// caller can re-Open the new file; transparent rotation
			// handling is intentionally out of scope for Phase 3.
			if ev.Op&(fsnotify.Rename|fsnotify.Remove) != 0 {
				return fmt.Errorf("native: Follow: file rotated (%s) at %s", ev.Op, ev.Name)
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
