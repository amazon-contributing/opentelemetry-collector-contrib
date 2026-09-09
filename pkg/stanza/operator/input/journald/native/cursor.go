// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package native // import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/input/journald/native"

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Cursor is the parsed representation of a systemd journal cursor. It
// uniquely identifies a single ENTRY object across reboots and rotations,
// and is the unit of checkpointing exposed by Reader.Cursor() /
// Reader.SeekToCursor().
//
// # Wire format
//
// Cursor.String() emits the same six-field, semicolon-separated format
// produced by sd_journal_get_cursor(3):
//
//	s=<seqnum_id>;i=<seqnum>;b=<boot_id>;m=<monotonic>;t=<realtime>;x=<xor_hash>
//
// Where:
//
//   - s — SeqnumID, 32 lowercase hex characters (the journal stream ID).
//   - i — Seqnum, lowercase hex (no padding, no "0x").
//   - b — BootID, 32 lowercase hex characters.
//   - m — Monotonic time in microseconds since boot, lowercase hex.
//   - t — Realtime in microseconds since the Unix epoch, lowercase hex.
//   - x — XorHash, lowercase hex.
//
// FileID is recorded for caller-side validation (e.g. asserting a cursor
// belongs to the same .journal file before seeking) but is NOT serialized
// into the wire format because systemd's cursor is intentionally
// file-portable: the same SeqnumID space spans multiple rotated files,
// and a cursor must be resumable on the next file even if the prior one
// has been archived. ParseCursor leaves FileID zero; populate it
// manually if you need that validation.
type Cursor struct {
	// FileID is a local-only field carrying the journal file identifier
	// observed when the cursor was produced. It is NOT part of the
	// systemd wire format and is preserved purely so callers can refuse
	// cursors that were written for a different file. Set to all-zero
	// by ParseCursor.
	FileID [16]byte
	// SeqnumID is the journal-stream identifier. All entries with the
	// same SeqnumID share a monotonic Seqnum sequence; this field is the
	// "s=" component of the wire format.
	SeqnumID [16]byte
	// Seqnum is the entry sequence number — the "i=" component.
	Seqnum uint64
	// BootID is the systemd boot identifier active when the entry was
	// logged — the "b=" component.
	BootID [16]byte
	// Monotonic is the monotonic clock value in microseconds since
	// boot — the "m=" component.
	Monotonic uint64
	// Realtime is the wall-clock time in microseconds since the Unix
	// epoch — the "t=" component.
	Realtime uint64
	// XorHash is the entry's xor_hash field — the "x=" component.
	XorHash uint64
}

// errors returned by ParseCursor / Reader.Cursor / Reader.SeekToCursor.
// Exported so callers can branch with errors.Is.
var (
	// ErrCursorMalformed indicates the cursor string failed structural
	// validation: missing fields, duplicate keys, unknown keys, empty
	// values, or component values that fail to parse.
	ErrCursorMalformed = errors.New("malformed journal cursor")
	// ErrCursorEmpty indicates the cursor string is the empty string.
	// Treated as malformed for diagnostic clarity.
	ErrCursorEmpty = errors.New("empty journal cursor")
	// ErrCursorNoEntry indicates Reader.Cursor was called before any
	// successful ReadEntry. There is no last-returned entry to encode.
	ErrCursorNoEntry = errors.New("journal cursor: no entry has been read yet")
	// ErrCursorSeqnumMismatch indicates SeekToCursor was called with a
	// cursor whose SeqnumID does not match this file's Header.SeqnumID
	// (i.e. the cursor was produced from a different journal stream).
	// The seek is refused without reading any entries.
	ErrCursorSeqnumMismatch = errors.New("journal cursor: seqnum_id does not match this file")
	// ErrCursorNotFound indicates SeekToCursor walked the file without
	// finding an ENTRY whose Seqnum matches the cursor. The reader is
	// reset to the file head so callers may retry or re-iterate from
	// the start.
	ErrCursorNotFound = errors.New("journal cursor: target seqnum not present in file")
)

// id128String renders a 16-byte ID as 32 lowercase hex characters,
// matching sd_id128_to_string. Lowercase is required because systemd's
// own parser is tolerant of either case but the canonical wire format
// is lowercase, and downstream cursor-equality checks are typically
// performed by string comparison.
func id128String(id [16]byte) string {
	return hex.EncodeToString(id[:])
}

// parseID128 parses 32 hex characters (case-insensitive) into a 16-byte
// ID. Any other length is rejected as malformed.
func parseID128(s string) ([16]byte, error) {
	var out [16]byte
	if len(s) != 32 {
		return out, fmt.Errorf("expected 32 hex chars, got %d", len(s))
	}
	if _, err := hex.Decode(out[:], []byte(s)); err != nil {
		return out, err
	}
	return out, nil
}

// String renders the cursor in systemd's wire format. The output is
// stable: round-tripping through ParseCursor returns an equivalent
// Cursor value (FileID becomes zero on parse, but every wire field is
// preserved bit-exactly).
//
// Numeric components use lowercase hex with no leading zeros, matching
// sd_journal_get_cursor's "%llx" formatting (we use "%x" on uint64
// which produces the same output in Go).
func (c *Cursor) String() string {
	var b strings.Builder
	// Pre-size: 6 keys × ("k="+";") = 18, plus 2 × 32-char IDs = 64,
	// plus 4 × up-to-16-char hex numbers = 64. 146 bytes covers it.
	b.Grow(146)
	b.WriteString("s=")
	b.WriteString(id128String(c.SeqnumID))
	b.WriteString(";i=")
	b.WriteString(strconv.FormatUint(c.Seqnum, 16))
	b.WriteString(";b=")
	b.WriteString(id128String(c.BootID))
	b.WriteString(";m=")
	b.WriteString(strconv.FormatUint(c.Monotonic, 16))
	b.WriteString(";t=")
	b.WriteString(strconv.FormatUint(c.Realtime, 16))
	b.WriteString(";x=")
	b.WriteString(strconv.FormatUint(c.XorHash, 16))
	return b.String()
}

// ParseCursor parses a systemd cursor string into a Cursor value.
//
// # Accepted format
//
// The cursor must be a non-empty string consisting of six "key=value"
// components separated by ";". Each key must appear exactly once; keys
// may appear in any order (matching sd_journal_seek_cursor's tolerance):
//
//   - s — SeqnumID, exactly 32 hex chars (case-insensitive).
//   - i — Seqnum as hex digits (lower or upper case, no "0x" prefix).
//   - b — BootID, exactly 32 hex chars (case-insensitive).
//   - m — Monotonic in microseconds, hex.
//   - t — Realtime in microseconds, hex.
//   - x — XorHash, hex.
//
// FileID is left zero; callers that need file-affinity validation should
// populate it after parsing.
//
// Trailing whitespace, embedded newlines, and stray characters are
// rejected outright — systemd writes these cursors unconditionally on
// one line and any deviation almost always indicates a corrupted
// checkpoint file.
func ParseCursor(s string) (*Cursor, error) {
	if s == "" {
		return nil, ErrCursorEmpty
	}

	c := &Cursor{}
	// Track which keys we have already seen so we can reject duplicates
	// (systemd does not emit them; reading two values for the same key
	// is almost certainly a sign of file concatenation or corruption).
	seen := make(map[byte]bool, 6)

	for _, part := range strings.Split(s, ";") {
		if part == "" {
			return nil, fmt.Errorf("%w: empty component in %q",
				ErrCursorMalformed, s)
		}
		eq := strings.IndexByte(part, '=')
		if eq < 1 {
			return nil, fmt.Errorf("%w: missing '=' in component %q",
				ErrCursorMalformed, part)
		}
		key := part[:eq]
		value := part[eq+1:]
		if len(key) != 1 {
			return nil, fmt.Errorf("%w: expected single-letter key, got %q",
				ErrCursorMalformed, key)
		}
		if value == "" {
			return nil, fmt.Errorf("%w: empty value for key %q",
				ErrCursorMalformed, key)
		}
		k := key[0]
		if seen[k] {
			return nil, fmt.Errorf("%w: duplicate key %q",
				ErrCursorMalformed, key)
		}
		seen[k] = true

		switch k {
		case 's':
			id, err := parseID128(value)
			if err != nil {
				return nil, fmt.Errorf("%w: bad seqnum_id %q: %v",
					ErrCursorMalformed, value, err)
			}
			c.SeqnumID = id
		case 'i':
			n, err := strconv.ParseUint(value, 16, 64)
			if err != nil {
				return nil, fmt.Errorf("%w: bad seqnum %q: %v",
					ErrCursorMalformed, value, err)
			}
			c.Seqnum = n
		case 'b':
			id, err := parseID128(value)
			if err != nil {
				return nil, fmt.Errorf("%w: bad boot_id %q: %v",
					ErrCursorMalformed, value, err)
			}
			c.BootID = id
		case 'm':
			n, err := strconv.ParseUint(value, 16, 64)
			if err != nil {
				return nil, fmt.Errorf("%w: bad monotonic %q: %v",
					ErrCursorMalformed, value, err)
			}
			c.Monotonic = n
		case 't':
			n, err := strconv.ParseUint(value, 16, 64)
			if err != nil {
				return nil, fmt.Errorf("%w: bad realtime %q: %v",
					ErrCursorMalformed, value, err)
			}
			c.Realtime = n
		case 'x':
			n, err := strconv.ParseUint(value, 16, 64)
			if err != nil {
				return nil, fmt.Errorf("%w: bad xor_hash %q: %v",
					ErrCursorMalformed, value, err)
			}
			c.XorHash = n
		default:
			return nil, fmt.Errorf("%w: unknown key %q",
				ErrCursorMalformed, key)
		}
	}

	// All six wire fields must be present. Without any one of them the
	// cursor cannot uniquely identify an entry.
	for _, k := range []byte{'s', 'i', 'b', 'm', 't', 'x'} {
		if !seen[k] {
			return nil, fmt.Errorf("%w: missing required key %q in %q",
				ErrCursorMalformed, string(k), s)
		}
	}

	return c, nil
}

// Cursor returns the systemd-format cursor for the most recently
// returned ENTRY. The cursor encodes seqnum_id, seqnum, boot_id,
// monotonic, realtime, and xor_hash — enough information to resume
// iteration from immediately after this entry on the next process run,
// even if the file is rotated to an archived sibling first.
//
// Returns ErrCursorNoEntry if the Reader has not yet returned an entry
// from ReadEntry. The recommended usage pattern is:
//
//	for {
//	    e, err := r.ReadEntry()
//	    if err == io.EOF { break }
//	    if err != nil { return err }
//	    process(e)
//	    cur, _ := r.Cursor() // cur reflects e
//	    persist(cur)
//	}
//
// The returned string is independent of the Reader: it can be persisted
// to disk, sent over the wire, or compared by string equality without
// holding any reference to the Reader.
//
// Note: this method shadows the Phase-1 placeholder of the same name
// that returned the raw byte offset. That value is now exposed via
// Reader.Offset(); callers that previously inspected the linear-scan
// cursor for diagnostics should switch to Offset().
func (r *Reader) Cursor() (string, error) {
	if r.lastEntry == nil {
		return "", ErrCursorNoEntry
	}
	c := &Cursor{
		FileID:    r.hdr.FileID,
		SeqnumID:  r.hdr.SeqnumID,
		Seqnum:    r.lastEntry.SeqNum,
		BootID:    r.lastEntry.BootID,
		Monotonic: r.lastEntry.Monotonic,
		Realtime:  r.lastEntry.Realtime,
		XorHash:   r.lastEntry.XorHash,
	}
	return c.String(), nil
}

// SeekToCursor positions the Reader so that the next ReadEntry call
// returns the ENTRY immediately after the one identified by cursorStr.
//
// The seek protocol is:
//
//  1. ParseCursor(cursorStr) — wire-format validation. Returns
//     ErrCursorMalformed (or ErrCursorEmpty) on any structural issue.
//  2. SeqnumID equality check against the file's Header.SeqnumID.
//     Mismatched streams are refused with ErrCursorSeqnumMismatch
//     before any entries are read; the cursor was produced from a
//     different journal and seeking it would silently land on the
//     wrong record.
//  3. Iteration: starting from the head of the file, read entries
//     using the Reader's currently configured strategy (linear scan
//     or indexed traversal). The first entry whose Seqnum equals the
//     cursor's Seqnum is taken as the match; iteration stops there.
//  4. Position update: r.lastEntry is set to the matched entry; for
//     the linear strategy r.cursor already points past it (ReadEntry
//     advanced it before returning), and for indexed traversal
//     r.arrayItemIdx already points at the next item.
//
// On a successful seek SeekToCursor returns nil. Subsequent
// Reader.Cursor() calls will return cursorStr until the next
// ReadEntry advances past it.
//
// On any failure the Reader is left in a defined state:
//
//   - Malformed cursor or SeqnumID mismatch: r is unchanged. Re-seeking
//     a different cursor or calling ReadEntry continues from wherever
//     the Reader was prior to the SeekToCursor call.
//   - Cursor seqnum not found in the file (ErrCursorNotFound) or any
//     I/O / parse error encountered during iteration: r is reset to
//     the file head (linear strategy: cursor=HeaderSize; indexed
//     strategy: arrayInited=false). Callers may then re-iterate from
//     the start, request a different cursor, or close the Reader.
//
// SeekToCursor is currently O(n) in the number of entries to skip;
// every entry between the head and the cursor is read and discarded.
// A future revision may use the data hash table to short-circuit the
// scan, but the linear approach is sufficient for checkpoints written
// within a single boot and avoids dependencies on indexed structures
// that older journals lack.
func (r *Reader) SeekToCursor(cursorStr string) error {
	if r.closed {
		return ErrReaderClosed
	}
	cur, err := ParseCursor(cursorStr)
	if err != nil {
		return err
	}
	if cur.SeqnumID != r.hdr.SeqnumID {
		return fmt.Errorf("%w: cursor seqnum_id=%s file seqnum_id=%s",
			ErrCursorSeqnumMismatch,
			id128String(cur.SeqnumID), id128String(r.hdr.SeqnumID))
	}

	// Reset iteration state so we walk from the head of the file
	// regardless of where the Reader was previously positioned.
	r.resetIteration()

	for {
		entry, err := r.ReadEntry()
		if errors.Is(err, io.EOF) {
			// Cursor target is not in this file. Reset the reader
			// to the head so the caller's next ReadEntry replays
			// from the top instead of returning EOF immediately.
			r.resetIteration()
			return fmt.Errorf("%w: seqnum=%d", ErrCursorNotFound, cur.Seqnum)
		}
		if err != nil {
			r.resetIteration()
			return fmt.Errorf("seek to cursor seqnum=%d: %w",
				cur.Seqnum, err)
		}
		if entry.SeqNum == cur.Seqnum {
			// Found it. Reader is already positioned past this
			// entry by ReadEntry, so the next call will return
			// the entry that follows. r.lastEntry is set so a
			// subsequent r.Cursor() reproduces cursorStr.
			return nil
		}
	}
}

// resetIteration rewinds both the linear and indexed traversal state to
// the head of the file. Used by SeekToCursor on entry (so the search
// scans from the start) and on a failed seek (so the caller can recover
// without an io.EOF on the next ReadEntry).
//
// Does not touch the file handle or header, so calling it on a closed
// Reader is harmless — but the subsequent ReadEntry will still fail
// with ErrReaderClosed.
func (r *Reader) resetIteration() {
	r.cursor = r.hdr.HeaderSize
	r.arrayInited = false
	r.currentArray = nil
	r.arrayItemIdx = 0
	r.arrayVisited = nil
	r.lastEntry = nil
}
