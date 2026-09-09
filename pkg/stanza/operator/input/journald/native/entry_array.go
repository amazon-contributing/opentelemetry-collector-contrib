// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package native // import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/input/journald/native"

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Layout constants for ENTRY_ARRAY objects.
//
// After the 16-byte common ObjectHeader, the body of an ENTRY_ARRAY
// consists of a single uint64 next_entry_array_offset (always 8 bytes,
// even in compact-mode files), followed by the items[] array.
//
// Per-item width depends on HEADER_INCOMPATIBLE_COMPACT:
//
//   - Non-compact (legacy): each item is 8 bytes (le64 object offset).
//   - Compact (systemd 252+): each item is 4 bytes (le32 object offset).
//
// References:
//   - https://systemd.io/JOURNAL_FILE_FORMAT/#entry-array-objects
//   - systemd src/libsystemd/sd-journal/journal-def.h "EntryArrayObject"
const (
	// EntryArrayHeaderSize is the size of the next_entry_array_offset
	// field that precedes the items array, in bytes.
	EntryArrayHeaderSize uint64 = 8
	// EntryArrayItemSize is the on-disk size of one item in non-compact
	// mode (le64 object offset).
	EntryArrayItemSize uint64 = 8
	// EntryArrayItemSizeCompact is the on-disk size of one item in
	// HEADER_INCOMPATIBLE_COMPACT mode (le32 object offset).
	EntryArrayItemSizeCompact uint64 = 4
)

// EntryArray is the parsed body of an ENTRY_ARRAY object. systemd
// maintains a singly-linked list of EntryArray objects rooted at
// Header.EntryArrayOffset; the items concatenated in chain order yield
// every ENTRY in chronological (write) order — including across
// rotation, which a sequential arena scan cannot guarantee.
//
// Items with value 0 are sparse slots (deleted or never-written entries)
// and are NOT filtered out by ParseEntryArray; callers walking the chain
// should skip them. iterateViaEntryArray below does this for you.
type EntryArray struct {
	// NextEntryArrayOffset is the file offset of the next EntryArray in
	// the chain, or 0 if this is the tail.
	NextEntryArrayOffset uint64
	// Items is the list of ENTRY object offsets recorded in this array.
	// In compact-mode files the on-disk values are le32 but they are
	// widened to uint64 here for caller convenience.
	Items []uint64
	// Offset is where this EntryArray's ObjectHeader was parsed from.
	// Useful for diagnostics and cycle-detection in iterateViaEntryArray.
	Offset uint64
}

// errors returned by ParseEntryArray and iterateViaEntryArray. Exported
// so callers can branch with errors.Is.
var (
	// ErrEntryArrayWrongType indicates ParseEntryArray was called on an
	// offset whose ObjectHeader.Type is not ObjectEntryArray.
	ErrEntryArrayWrongType = errors.New("not an entry_array object")
	// ErrEntryArrayMalformed indicates the entry_array payload is too
	// small to hold the next_entry_array_offset field, or the items
	// section length is not a multiple of the per-item size.
	ErrEntryArrayMalformed = errors.New("malformed entry_array body")
	// ErrEntryArrayCycle indicates the iterator revisited an EntryArray
	// offset it had already loaded. systemd never writes a cycle
	// deliberately; this protects against corrupted or crafted journals.
	ErrEntryArrayCycle = errors.New("entry_array linked list contains a cycle")
)

// ParseEntryArray reads and validates an ENTRY_ARRAY object at offset.
//
// Validation performed:
//
//   - ObjectHeader.Type == ObjectEntryArray.
//   - ObjectHeader.PayloadSize() >= EntryArrayHeaderSize (room for at
//     least the next_entry_array_offset field).
//   - The bytes between the header field and the end of the object form
//     a whole number of items at the per-mode width.
//
// The function performs ParseObjectHeader plus exactly one ReadAt of the
// object payload; no per-item I/O is required.
func ParseEntryArray(r io.ReaderAt, offset uint64, compact bool) (*EntryArray, error) {
	oh, err := ParseObjectHeader(r, offset)
	if err != nil {
		return nil, fmt.Errorf("parse entry_array header at %d: %w", offset, err)
	}
	if oh.Type != ObjectEntryArray {
		return nil, fmt.Errorf("%w: type=%d at offset=%d",
			ErrEntryArrayWrongType, oh.Type, offset)
	}

	payloadSize := oh.PayloadSize()
	if payloadSize < EntryArrayHeaderSize {
		return nil, fmt.Errorf("%w: payload=%d below minimum %d at offset=%d",
			ErrEntryArrayMalformed, payloadSize, EntryArrayHeaderSize, offset)
	}

	itemSize := EntryArrayItemSize
	if compact {
		itemSize = EntryArrayItemSizeCompact
	}
	itemsBytes := payloadSize - EntryArrayHeaderSize
	// Zero-item arrays are valid (systemd writes a sentinel head
	// EntryArray with 0 items for empty journals), but any non-zero
	// items section must be a whole multiple of the per-mode width.
	if itemsBytes%itemSize != 0 {
		return nil, fmt.Errorf("%w: items_bytes=%d not multiple of %d (compact=%v) at offset=%d",
			ErrEntryArrayMalformed, itemsBytes, itemSize, compact, offset)
	}
	nItems := itemsBytes / itemSize

	body := make([]byte, payloadSize)
	n, err := r.ReadAt(body, int64(oh.PayloadOffset()))
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read entry_array body at %d: %w", offset, err)
	}
	if uint64(n) < payloadSize {
		return nil, fmt.Errorf("read entry_array body at %d: only %d of %d bytes: %w",
			offset, n, payloadSize, io.ErrUnexpectedEOF)
	}

	le := binary.LittleEndian
	next := le.Uint64(body[0:8])

	items := make([]uint64, 0, nItems)
	cursor := EntryArrayHeaderSize
	for i := uint64(0); i < nItems; i++ {
		var v uint64
		if compact {
			v = uint64(le.Uint32(body[cursor : cursor+4]))
			cursor += 4
		} else {
			v = le.Uint64(body[cursor : cursor+8])
			cursor += 8
		}
		items = append(items, v)
	}

	return &EntryArray{
		NextEntryArrayOffset: next,
		Items:                items,
		Offset:               offset,
	}, nil
}

// Option configures a Reader at Open time. Pass options as variadic
// arguments to Open: Open(path, WithIndexedTraversal(true)).
type Option func(*Reader)

// WithIndexedTraversal selects the iteration strategy used by ReadEntry.
//
// When enabled is true, ReadEntry walks the EntryArray linked list rooted
// at Header.EntryArrayOffset, returning entries in chronological (write)
// order. This is the only strategy that yields strict chronological
// ordering across rotated/merged files.
//
// When enabled is false (the default), ReadEntry performs a sequential
// arena scan and returns ENTRY objects in on-disk order. For files
// written by a single boot of a single host this also matches
// chronological order, but it is cheaper because it skips the EntryArray
// chain.
//
// Both strategies yield the same set of entries for typical single-boot
// files; choose indexed traversal when correctness across rotation
// matters or when you need to honor the file's declared head/tail.
func WithIndexedTraversal(enabled bool) Option {
	return func(r *Reader) {
		r.useIndexedTraversal = enabled
	}
}

// iterateViaEntryArray returns the next ENTRY parsed body via the
// EntryArray linked list rooted at Header.EntryArrayOffset. It is the
// indexed-traversal counterpart of the linear scan in ReadEntry.
//
// State machine:
//
//   - First call: ParseEntryArray(seed) where seed = header.entry_array_offset.
//     If seed is zero, the file has no entries and we return io.EOF.
//   - On each call: pop one item from r.currentArray.Items, skip zero items
//     (sparse slots), and ParseEntry at the popped offset.
//   - When the current array is exhausted, follow NextEntryArrayOffset to
//     the next array. If that is zero, return io.EOF and remain pinned at
//     EOF for subsequent calls.
//
// Cycle detection: every loaded EntryArray.Offset is recorded in
// r.arrayVisited. If the chain ever points at an offset we have already
// loaded, ErrEntryArrayCycle is returned. This is defense-in-depth against
// corrupted or crafted files; systemd never writes a cycle on its own.
//
// This method is unexported (it's a private implementation detail of the
// indexed-traversal strategy) but is invoked by ReadEntry whenever
// WithIndexedTraversal(true) was passed to Open.
func (r *Reader) iterateViaEntryArray() (*Entry, error) {
	if r.closed {
		return nil, ErrReaderClosed
	}

	for {
		// Lazy-init: load the head EntryArray on the first call.
		if !r.arrayInited {
			r.arrayInited = true
			seed := r.hdr.EntryArrayOffset
			if seed == 0 {
				// Empty journal — no entry_array chain to walk.
				return nil, io.EOF
			}
			ea, err := ParseEntryArray(r.f, seed, r.compact)
			if err != nil {
				return nil, err
			}
			r.arrayVisited = map[uint64]struct{}{seed: {}}
			r.currentArray = ea
			r.arrayItemIdx = 0
		}

		// Indexed-traversal EOF: head was loaded, every array in the
		// chain has been drained, and the most recent NextOffset was
		// zero. The currentArray field is nil'd at chain end below.
		if r.currentArray == nil {
			return nil, io.EOF
		}

		// Drain the current array. When all items in it have been
		// emitted, follow NextEntryArrayOffset to the next array.
		if r.arrayItemIdx >= len(r.currentArray.Items) {
			next := r.currentArray.NextEntryArrayOffset
			if next == 0 {
				r.currentArray = nil
				return nil, io.EOF
			}
			if _, seen := r.arrayVisited[next]; seen {
				return nil, fmt.Errorf("%w: revisiting offset %d",
					ErrEntryArrayCycle, next)
			}
			ea, err := ParseEntryArray(r.f, next, r.compact)
			if err != nil {
				return nil, err
			}
			r.arrayVisited[next] = struct{}{}
			r.currentArray = ea
			r.arrayItemIdx = 0
			continue
		}

		offset := r.currentArray.Items[r.arrayItemIdx]
		r.arrayItemIdx++
		if offset == 0 {
			// Sparse slot — systemd never writes a deliberate zero
			// offset, but recovery tools may null out deleted
			// entries. Skip silently.
			continue
		}

		entry, err := ParseEntry(r.f, offset, r.compact)
		if err != nil {
			return nil, fmt.Errorf("parse entry at %d (via entry_array): %w",
				offset, err)
		}
		r.lastEntry = entry
		return entry, nil
	}
}
