// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package native // import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/input/journald/native"

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// Reader is a forward-only iterator over the ENTRY objects of a single
// systemd journal file. It wraps an *os.File together with a parsed
// *Header and tracks a cursor offset that advances on each ReadEntry call.
//
// # Iteration semantics
//
// ReadEntry returns entries in on-disk order via a sequential ENTRY-object
// scan: starting just past the journal header, the cursor walks the arena
// 8-byte-aligned object-by-object, returning every object whose Type ==
// ObjectEntry and skipping all other object kinds (DATA, FIELD, hash
// tables, EntryArray, TAG). This matches the linear-scan strategy that the
// Phase-1 feasibility spike used to validate against the real fixture.
//
// The on-disk order is also chronological for journal files that were
// written by a single boot of a single host — systemd always appends new
// ENTRY objects at the tail. Files that were merged or rotated may
// interleave object types, but ENTRY objects themselves remain in seqnum
// order. Readers that need strict chronological order across rotation
// should use the EntryArray-indexed traversal added in Phase 2.
//
// On EOF (the cursor reaches the end of the file or runs past the arena),
// ReadEntry returns (nil, io.EOF). The cursor is left at the file end so
// repeated calls keep returning io.EOF without advancing further; this
// makes Reader safe to reuse as a one-shot iterator without an extra "are
// we done?" flag.
//
// Reader is NOT safe for concurrent use; callers needing parallelism
// should open separate Readers on the same file (each gets its own
// *os.File and cursor) or wrap a single Reader in their own mutex.
//
// # Bounded reads
//
// Each ReadEntry call performs at most one ParseObjectHeader plus, for
// ENTRY objects, one ParseEntry. ParseEntry itself reads the entry body in
// a single ReadAt; payload-resolution (DATA objects) is the caller's
// responsibility via ReadDataField, so a Reader.ReadEntry call performs no
// per-item I/O.
//
// # Build constraint
//
// This file builds with CGO_ENABLED=0 — no syscalls beyond os.Open and
// (*os.File).ReadAt are used.
type Reader struct {
	f       *os.File
	hdr     *Header
	compact bool
	// arenaEnd is the highest valid file offset for an object header read.
	// Once cursor >= arenaEnd, ReadEntry returns io.EOF.
	arenaEnd uint64
	// tailObjectOffset is Header.TailObjectOffset: the offset of the most
	// recently written object. On an ACTIVE (online) journal the arena is
	// preallocated and zero-filled past the write head, so the region
	// between the last real object and arenaEnd is zeros (type=0, size=0
	// objects). The linear scan must stop once it advances past the tail
	// object; otherwise it walks into the zero region and ParseObjectHeader
	// fails with ErrObjectTooSmall. Zero means "not advertised" — fall back
	// to scanning to arenaEnd (archived files set it; some online files may
	// not have flushed it). See ReadEntry.
	tailObjectOffset uint64
	// cursor is the next file offset to attempt ParseObjectHeader from.
	// Always 8-byte aligned (ObjectAlignment) after the first read.
	cursor uint64
	// closed indicates Close has been called; subsequent calls are no-ops
	// and ReadEntry returns ErrReaderClosed.
	closed bool

	// --- Phase 2 indexed-traversal state ---
	//
	// When useIndexedTraversal is true, ReadEntry dispatches to
	// iterateViaEntryArray (defined in entry_array.go) instead of the
	// sequential arena scan below. The remaining fields hold that
	// strategy's iteration cursor; they are zero-valued and unused when
	// the linear scan strategy is selected (the default).

	// useIndexedTraversal selects EntryArray-chain traversal over the
	// linear arena scan. Set via WithIndexedTraversal(true) at Open time.
	useIndexedTraversal bool
	// currentArray is the EntryArray currently being drained. nil before
	// the first call (when arrayInited is false) and after the chain has
	// been fully traversed (terminal io.EOF state).
	currentArray *EntryArray
	// arrayItemIdx is the index into currentArray.Items of the next item
	// to consume. Reset to 0 each time the iterator advances to a new
	// array in the chain.
	arrayItemIdx int
	// arrayInited indicates whether the head EntryArray load attempt has
	// happened. Distinguishes "before first call" from "chain finished".
	arrayInited bool
	// arrayVisited records the offsets of every EntryArray we have loaded
	// so far so the iterator can detect cycles. Allocated lazily on the
	// first iterateViaEntryArray call to keep the linear-scan path
	// allocation-free.
	arrayVisited map[uint64]struct{}

	// --- Phase 3 follow-mode state ---
	//
	// These fields are written exclusively by Follow (defined in
	// follow.go) and read by tests to assert which change-detection path
	// was selected. They are zero-valued and unused before Follow is
	// invoked.

	// followStrategy records which mechanism the most recent Follow call
	// selected for change detection. See FollowStrategy constants.
	followStrategy FollowStrategy
	// followForcePoll, when true, makes Follow skip the fsnotify probe
	// and use the time.Ticker poll fallback unconditionally. Test-only
	// hook configured via WithFollowForcePoll. Default false.
	followForcePoll bool
	// followPollInterval overrides the default poll cadence used by the
	// time.Ticker fallback. Zero means use FollowDefaultPollInterval.
	// Configured via WithFollowPollInterval; primarily a test hook.
	followPollInterval time.Duration

	// --- Phase 3 cursor / checkpoint state ---
	//
	// lastEntry retains the most recently returned ENTRY so that
	// Cursor() can serialize it into systemd's wire format without an
	// extra seek. Both iteration strategies (linear scan and indexed
	// traversal) update this field on every successful ReadEntry.
	// Nil before the first ReadEntry and after a failing seek.
	lastEntry *Entry
}

// errors returned by Reader. Exported so callers can branch with errors.Is.
var (
	// ErrReaderClosed indicates ReadEntry was called on a Reader whose
	// Close method has already returned. Reusing a closed Reader is a
	// programming error rather than an I/O failure, but we surface it as
	// an error for ergonomic use of defer.
	ErrReaderClosed = errors.New("journal reader closed")
	// ErrReaderArenaOverflow indicates an ObjectHeader's declared Size
	// would push the cursor past the end of the file. This signals a
	// truncated or corrupted journal — systemd never writes an object
	// whose size exceeds the remaining arena.
	ErrReaderArenaOverflow = errors.New("journal reader: object size exceeds arena")
)

// Open opens the journal file at path read-only, parses and validates its
// header, and returns a Reader positioned at the first object after the
// header (i.e. cursor = header.HeaderSize, which the systemd format
// guarantees is already 8-byte aligned).
//
// The returned Reader owns the *os.File and will close it on Close. On any
// error the file is closed before returning so the caller need not perform
// cleanup on the error path.
//
// Open does NOT scan the entire file; the cost is one ParseHeader (256
// bytes from offset 0) plus the os.Open syscall. Subsequent ReadEntry
// calls amortize the per-object scan.
//
// Options (variadic) configure iteration strategy and other per-Reader
// settings. See WithIndexedTraversal for the EntryArray-based traversal
// added in Phase 2.
func Open(path string, opts ...Option) (*Reader, error) {
	f, err := os.Open(path) //#nosec G304 -- path supplied by trusted operator config; receiver validates upstream.
	if err != nil {
		return nil, fmt.Errorf("open journal file %q: %w", path, err)
	}

	hdr, err := ParseHeader(f)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("parse journal header in %q: %w", path, err)
	}

	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("stat journal file %q: %w", path, err)
	}
	fileSize := uint64(fi.Size())

	// arena_size advertised in the header is informational; some files
	// have it set to zero (e.g. crashed writers). Cap the iteration end
	// at min(file_size, header_size + arena_size) when arena_size > 0,
	// otherwise use the full file size. Either way we never read past
	// fileSize, which os.File.ReadAt would short-read anyway.
	arenaEnd := fileSize
	if hdr.ArenaSize > 0 {
		declared := hdr.HeaderSize + hdr.ArenaSize
		if declared < arenaEnd {
			arenaEnd = declared
		}
	}

	r := &Reader{
		f:                f,
		hdr:              hdr,
		compact:          hdr.IsCompact(),
		arenaEnd:         arenaEnd,
		tailObjectOffset: hdr.TailObjectOffset,
		cursor:           hdr.HeaderSize,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

// Header returns the parsed file header. The returned pointer is owned by
// the Reader and MUST NOT be mutated by callers; treat it as read-only.
func (r *Reader) Header() *Header {
	return r.hdr
}

// reopenSamePath closes the Reader's current *os.File and re-opens the
// SAME path, re-parsing the header and resetting the cursor to the new
// file's head. It is used by Follow to transparently continue across a
// journal rotation: systemd renames the active system.journal to an
// archived name and creates a fresh system.journal at the original path,
// so re-opening the path lands on the new file. The Reader's options
// (e.g. indexed traversal) are preserved; iteration state is reset so the
// next ReadEntry walks the new file from its first object.
//
// The caller (Follow) is responsible for draining the old file BEFORE
// calling this — once we close the old fd we can no longer read whatever
// trailing entries it held. reopenSamePath itself only swaps to the new
// file; it does not emit anything.
func (r *Reader) reopenSamePath() error {
	if r.closed {
		return ErrReaderClosed
	}
	path := r.f.Name()
	f, err := os.Open(path) //#nosec G304 -- same trusted path the Reader was opened with.
	if err != nil {
		return fmt.Errorf("reopen journal file %q after rotation: %w", path, err)
	}
	hdr, err := ParseHeader(f)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("parse journal header in %q after rotation: %w", path, err)
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("stat journal file %q after rotation: %w", path, err)
	}
	fileSize := uint64(fi.Size())
	arenaEnd := fileSize
	if hdr.ArenaSize > 0 {
		if declared := hdr.HeaderSize + hdr.ArenaSize; declared < arenaEnd {
			arenaEnd = declared
		}
	}

	// Swap in the new file and reset all iteration state to its head.
	_ = r.f.Close()
	r.f = f
	r.hdr = hdr
	r.compact = hdr.IsCompact()
	r.arenaEnd = arenaEnd
	r.tailObjectOffset = hdr.TailObjectOffset
	r.cursor = hdr.HeaderSize
	r.lastEntry = nil
	// Reset indexed-traversal state so a re-armed chain walk starts clean.
	r.currentArray = nil
	r.arrayItemIdx = 0
	r.arrayInited = false
	r.arrayVisited = nil
	return nil
}

// Compact reports whether the underlying file uses
// HEADER_INCOMPATIBLE_COMPACT semantics. Cached at Open time so callers
// (and ReadEntry's per-call ParseEntry invocation) avoid re-checking the
// header flag.
func (r *Reader) Compact() bool {
	return r.compact
}

// Offset returns the current scan offset. Useful for tests and
// diagnostics. The Phase-3 cursor-checkpoint code (see cursor.go)
// captures the most recent ENTRY rather than this raw byte offset
// because cursors must be portable across files of different sizes.
func (r *Reader) Offset() uint64 {
	return r.cursor
}

// ReadEntry advances the internal cursor through the file, returning the
// next ENTRY object's parsed body. Non-ENTRY objects (DATA, FIELD, hash
// tables, EntryArray, TAG, UNUSED) are skipped silently — they participate
// in the on-disk byte layout but do not represent log records.
//
// The cursor is advanced past the returned ENTRY before this method
// returns, so a subsequent ReadEntry call resumes at the next object.
//
// When the Reader was opened with WithIndexedTraversal(true), ReadEntry
// dispatches to iterateViaEntryArray, walking the chain rooted at
// Header.EntryArrayOffset rather than scanning the arena. The exposed
// behavior is identical from the caller's point of view — the only
// observable differences are entry order on rotated/merged files and
// the value reported by Offset() (which only tracks the linear-scan
// strategy and remains at HeaderSize when indexed traversal is in use).
//
// Errors:
//
//   - io.EOF when the cursor reaches arenaEnd. The Reader remains usable
//     but every subsequent call will return io.EOF without advancing.
//   - ErrReaderClosed if Close has already been called.
//   - ErrReaderArenaOverflow if an object header declares a Size that
//     would step past arenaEnd. The cursor is not advanced in this case
//     so callers can inspect r.Offset() to find the bad object.
//   - ErrEntryArrayCycle (indexed traversal only) if the EntryArray chain
//     points at an offset that has already been loaded.
//   - Any error from ParseObjectHeader, ParseEntry, or ParseEntryArray,
//     wrapped with the offset where the failure occurred.
func (r *Reader) ReadEntry() (*Entry, error) {
	if r.closed {
		return nil, ErrReaderClosed
	}

	if r.useIndexedTraversal {
		return r.iterateViaEntryArray()
	}

	for {
		if r.cursor >= r.arenaEnd {
			return nil, io.EOF
		}

		// On an active journal the arena is preallocated and zero-filled
		// past the most-recently-written object (TailObjectOffset). Once
		// the cursor has advanced beyond the tail object there are no more
		// real objects — only zeros — so stop cleanly rather than walking
		// into the zero region (which ParseObjectHeader rejects as
		// ErrObjectTooSmall). Guarded on tailObjectOffset > 0 because some
		// files (freshly created, or crashed writers) leave it unset, in
		// which case we fall back to scanning to arenaEnd.
		//
		// IMPORTANT for follow mode: do NOT advance the cursor to arenaEnd
		// here. The writer appends new objects in this same region (below
		// arenaEnd) and bumps TailObjectOffset; Follow's refreshTail picks
		// up the higher tail offset, and the next drain must resume from
		// THIS parked cursor to read the newly-written object. Jumping to
		// arenaEnd would skip every future append. The EOF is idempotent:
		// while cursor stays > tailObjectOffset, repeated calls return EOF
		// without reading anything new.
		if r.tailObjectOffset > 0 && r.cursor > r.tailObjectOffset {
			return nil, io.EOF
		}

		// All journal objects sit on 8-byte boundaries. The header
		// guarantees the first cursor value (HeaderSize) is aligned,
		// and we keep it aligned via NextOffset below; this guard is
		// defense-in-depth in case a caller manipulated the cursor
		// directly via a future Seek API.
		aligned := alignUp(r.cursor, ObjectAlignment)
		if aligned >= r.arenaEnd {
			r.cursor = r.arenaEnd
			return nil, io.EOF
		}
		r.cursor = aligned

		oh, err := ParseObjectHeader(r.f, r.cursor)
		if err != nil {
			// A short read at end-of-file is reported by
			// ParseObjectHeader as io.ErrUnexpectedEOF wrapped in
			// the read error. Convert that into a clean io.EOF so
			// callers can use the canonical sentinel.
			if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
				r.cursor = r.arenaEnd
				return nil, io.EOF
			}
			// A zero/sub-minimum object (type=0, size=0) is the
			// signature of the preallocated, zero-filled tail of an
			// active journal — not corruption. Treat it as clean EOF
			// rather than a fatal parse error. This backstops the
			// TailObjectOffset guard above for files that leave the
			// tail offset unset but still preallocate the arena.
			//
			// Leave the cursor parked at this offset (do NOT jump to
			// arenaEnd) so follow mode resumes here and reads the real
			// object once the writer fills this slot. See the
			// TailObjectOffset guard above for the same rationale.
			if errors.Is(err, ErrObjectTooSmall) {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("read object at %d: %w", r.cursor, err)
		}

		// Reject objects whose size would walk us past the arena. The
		// systemd writer never emits such objects; treating them as
		// fatal protects against truncation and crafted-file attacks
		// without needing a separate length-validation pass.
		next := oh.NextOffset()
		if next > r.arenaEnd || oh.Size > r.arenaEnd-r.cursor {
			return nil, fmt.Errorf("%w: offset=%d size=%d arena_end=%d",
				ErrReaderArenaOverflow, r.cursor, oh.Size, r.arenaEnd)
		}

		if oh.Type != ObjectEntry {
			// Skip non-entry objects in O(1): the common header
			// already told us where the next object starts.
			r.cursor = next
			continue
		}

		entry, err := ParseEntry(r.f, r.cursor, r.compact)
		if err != nil {
			return nil, fmt.Errorf("parse entry at %d: %w", r.cursor, err)
		}
		r.cursor = next
		r.lastEntry = entry
		return entry, nil
	}
}

// Close releases the underlying file. It is safe to call Close multiple
// times; the second and subsequent calls are no-ops and return nil. After
// Close, ReadEntry returns ErrReaderClosed.
func (r *Reader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	if r.f == nil {
		return nil
	}
	return r.f.Close()
}

// ReadDataField is a Reader-method convenience wrapper around the
// package-level ReadDataField. It dispatches to the same parser using
// the Reader's open file handle and the compact-mode flag captured at
// Open time, so callers iterating an Entry's Items don't have to thread
// either through their loop. This is the bridge the journald input
// operator (input_native.go) uses to populate body fields on emitted
// log records.
//
// Note: r.f is the same *os.File that ParseEntry / ParseObjectHeader
// already use under the hood, so concurrent calls to ReadDataField from
// multiple goroutines on the same Reader carry the same caveat as
// concurrent ReadEntry: not safe. Spawn a separate Reader for each
// goroutine that needs independent iteration.
func (r *Reader) ReadDataField(offset uint64) (field, value string, err error) {
	if r.closed {
		return "", "", ErrReaderClosed
	}
	return ReadDataField(r.f, offset, r.compact)
}
