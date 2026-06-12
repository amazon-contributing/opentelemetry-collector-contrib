// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package native // import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/input/journald/native"

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

// Layout constants for ENTRY objects.
//
// Both layouts share a 48-byte fixed prefix immediately after the common
// ObjectHeader: seqnum(8)+realtime(8)+monotonic(8)+boot_id(16)+xor_hash(8).
// What differs is the per-item size:
//
//   - Non-compact (legacy): each item is 16 bytes — object_offset(le64) +
//     hash(le64).
//   - Compact (HEADER_INCOMPATIBLE_COMPACT, systemd 252+): each item is
//     4 bytes — object_offset(le32). The hash is omitted because the data
//     hash table indexes DATA objects by hash directly.
const (
	// EntryFixedSize is the size of the entry's fixed prefix (everything
	// before the variable-length items array), in bytes.
	EntryFixedSize uint64 = 48
	// EntryItemSize is the on-disk size of one EntryItem in non-compact
	// mode (8-byte object_offset + 8-byte hash).
	EntryItemSize uint64 = 16
	// EntryItemSizeCompact is the on-disk size of one EntryItem in
	// HEADER_INCOMPATIBLE_COMPACT mode (4-byte object_offset only).
	EntryItemSizeCompact uint64 = 4
)

// Layout constants for DATA objects.
//
// Non-compact DATA bodies (after the 16-byte ObjectHeader) consist of:
// hash(8) + next_hash_offset(8) + next_field_offset(8) + entry_offset(8) +
// entry_array_offset(8) + n_entries(8) = 48 bytes, then payload.
//
// Compact DATA bodies append two le32 fields (tail_entry_array_offset,
// tail_entry_array_n_entries) before the payload, an extra 8 bytes.
//
// References:
//   - https://systemd.io/JOURNAL_FILE_FORMAT/
//   - systemd src/libsystemd/sd-journal/journal-def.h "DataObject"
const (
	// DataPayloadOffset is the offset of the payload byte 0 within a
	// non-compact DATA object, measured from the start of the object.
	DataPayloadOffset uint64 = 64
	// DataCompactPayloadOffset is the offset of the payload byte 0 within
	// a compact-mode DATA object, measured from the start of the object.
	DataCompactPayloadOffset uint64 = 72
	// MaxDataPayloadSize caps the per-payload allocation. systemd writes
	// up to 64 KiB of payload before splitting; we mirror that as the
	// hard upper bound. Any DATA object that claims more is treated as
	// malformed.
	MaxDataPayloadSize uint64 = 64 * 1024
)

// EntryItem is one element of an Entry's items[] array. In compact-mode
// journals the Hash field is always zero because the format omits it on
// disk; the data hash table is consulted instead.
type EntryItem struct {
	// ObjectOffset points to the DATA object whose payload contributes
	// one FIELD=value pair to this entry. Zero indicates a deleted slot
	// and is skipped by ParseEntry.
	ObjectOffset uint64
	// Hash is the keyed hash of the referenced DATA payload. Zero in
	// compact-mode entries.
	Hash uint64
}

// Entry is the parsed representation of an ENTRY object's body. It does NOT
// include the 16-byte common ObjectHeader; callers that need the wrapping
// header should use ParseObjectHeader first.
//
// Field order matches the on-disk layout for ease of cross-reference with
// the systemd source. Items are returned with zero offsets filtered out
// (those slots are deleted; systemd never points at offset 0 deliberately).
type Entry struct {
	// SeqNum is the entry sequence number, monotonically increasing
	// within a SeqnumID space.
	SeqNum uint64
	// Realtime is the wall-clock time the entry was logged, in
	// microseconds since the Unix epoch.
	Realtime uint64
	// Monotonic is the monotonic clock time the entry was logged, in
	// microseconds since boot.
	Monotonic uint64
	// BootID is the systemd boot identifier active when the entry was
	// logged.
	BootID [16]byte
	// XorHash is an XOR fold of the hashes of the entry's DATA items;
	// systemd uses it as a cheap integrity check.
	XorHash uint64
	// Items lists the DATA-object offsets that contribute FIELD=value
	// payloads to this entry. Zero-offset slots are pruned during parse.
	Items []EntryItem
	// Offset is the on-disk byte offset of the wrapping ENTRY object
	// (i.e. the location of its ObjectHeader). Captured for convenience
	// so callers can build cursors without re-tracking it.
	Offset uint64
	// Compact records whether this entry was parsed with
	// HEADER_INCOMPATIBLE_COMPACT semantics. Affects how downstream code
	// must read DATA objects pointed at by Items.
	Compact bool
}

// errors returned by ParseEntry / ReadDataField.
var (
	// ErrEntryWrongType indicates the object at the requested offset is
	// not an ENTRY (Type != ObjectEntry). Use ParseObjectHeader and
	// dispatch on Type if uncertain.
	ErrEntryWrongType = errors.New("journal entry: wrong object type")
	// ErrEntryTooSmall indicates the ENTRY object's declared Size is
	// below the fixed-prefix minimum (16-byte header + 48-byte fixed
	// fields).
	ErrEntryTooSmall = errors.New("journal entry: object size below fixed prefix")
	// ErrEntryItemsMisaligned indicates the bytes available for items[]
	// (Size - 16 - 48) are not an exact multiple of the per-item size.
	// systemd never emits a partial item; a leftover tail signals
	// corruption.
	ErrEntryItemsMisaligned = errors.New("journal entry: items[] does not divide evenly")
	// ErrDataWrongType indicates the object at the offset referenced by
	// EntryItem.ObjectOffset is not a DATA object.
	ErrDataWrongType = errors.New("journal data: wrong object type")
	// ErrDataPayloadTooLarge indicates a DATA object claims a payload
	// larger than MaxDataPayloadSize. Treated as corruption to bound
	// allocation regardless of the file's declared sizes.
	ErrDataPayloadTooLarge = errors.New("journal data: payload exceeds maximum")
	// ErrDataPayloadMalformed indicates the DATA payload does not
	// contain the systemd-required FIELD=value separator.
	ErrDataPayloadMalformed = errors.New("journal data: payload missing '=' separator")
)

// ParseEntry reads an ENTRY object from r. The object's ObjectHeader is
// re-read inside ParseEntry so callers can pass just the offset that a
// scanner produced; this keeps the API symmetric with ParseObjectHeader.
//
// The compact flag selects the items[] layout per the journal header's
// HEADER_INCOMPATIBLE_COMPACT bit. The flag is a parameter rather than
// derived from a *Header so callers that already know the journal's
// compact-ness (e.g. a Reader holding a parsed Header) need not pay for an
// extra header read on every entry.
//
// FIX(M1): the original spike at journald-parser-spike/main.go iterated
// items unconditionally with a 16-byte stride — correct for legacy mode
// only. Compact-mode journals (systemd 252+, default on AL2023) use a
// 4-byte stride with le32 offsets and no per-item hash. This implementation
// branches on `compact` and reads each item at the correct width.
//
// FIX(M2): the spike's loop used `pos+16 <= bodySize` as the bound, which
// silently dropped trailing bytes if items[] did not divide evenly. systemd
// never emits a partial item, so a non-zero remainder signals corruption.
// We compute itemCount up-front and validate (bodySize-48) %
// itemSize == 0, returning ErrEntryItemsMisaligned otherwise.
//
// FIX(M3): see usecToTime — the spike cast `uint64 -> int64` without bounds
// checks, producing negative time.Time values for sentinel/far-future
// timestamps. The fix lives there because that's where the cast happens;
// ParseEntry preserves the raw uint64 and lets callers convert via the
// safe helper.
func ParseEntry(r io.ReaderAt, offset uint64, compact bool) (*Entry, error) {
	oh, err := ParseObjectHeader(r, offset)
	if err != nil {
		return nil, fmt.Errorf("entry@%d: %w", offset, err)
	}
	if oh.Type != ObjectEntry {
		return nil, fmt.Errorf("%w: type=%d offset=%d",
			ErrEntryWrongType, oh.Type, offset)
	}
	if oh.Size < ObjectHeaderSize+EntryFixedSize {
		return nil, fmt.Errorf("%w: size=%d offset=%d",
			ErrEntryTooSmall, oh.Size, offset)
	}

	bodySize := oh.Size - ObjectHeaderSize
	body := make([]byte, bodySize)
	if _, err := r.ReadAt(body, int64(offset+ObjectHeaderSize)); err != nil {
		// Short reads here are corruption: the object header claimed
		// Size bytes but the file is shorter.
		return nil, fmt.Errorf("read entry body at %d: %w",
			offset+ObjectHeaderSize, err)
	}

	le := binary.LittleEndian
	e := &Entry{
		Offset:    offset,
		Compact:   compact,
		SeqNum:    le.Uint64(body[0:8]),
		Realtime:  le.Uint64(body[8:16]),
		Monotonic: le.Uint64(body[16:24]),
		XorHash:   le.Uint64(body[40:48]),
	}
	copy(e.BootID[:], body[24:40])

	itemsBytes := bodySize - EntryFixedSize
	itemSize := EntryItemSize
	if compact {
		itemSize = EntryItemSizeCompact
	}
	// FIX(M2): exact-divisibility check. The spike used a loose
	// `pos+itemSize <= bodySize` bound that silently dropped trailing
	// bytes if items[] did not divide evenly.
	if itemsBytes%itemSize != 0 {
		return nil, fmt.Errorf("%w: items_bytes=%d item_size=%d offset=%d",
			ErrEntryItemsMisaligned, itemsBytes, itemSize, offset)
	}

	itemCount := itemsBytes / itemSize
	e.Items = make([]EntryItem, 0, itemCount)
	for i := uint64(0); i < itemCount; i++ {
		base := EntryFixedSize + i*itemSize
		var item EntryItem
		// FIX(M1): branch on compact to read items at the correct
		// stride. Compact items are le32 offsets only; non-compact are
		// le64 offset followed by le64 hash.
		if compact {
			item.ObjectOffset = uint64(le.Uint32(body[base : base+4]))
		} else {
			item.ObjectOffset = le.Uint64(body[base : base+8])
			item.Hash = le.Uint64(body[base+8 : base+16])
		}
		// systemd uses object_offset==0 as a deletion marker; skip
		// such items so callers do not chase a NULL pointer into the
		// header region.
		if item.ObjectOffset == 0 {
			continue
		}
		e.Items = append(e.Items, item)
	}

	return e, nil
}

// RealtimeAsTime converts e.Realtime (microseconds since Unix epoch) to a
// Go time.Time using the safe helper. See usecToTime for the M3 bug fix
// rationale.
func (e *Entry) RealtimeAsTime() (time.Time, error) {
	return usecToTime(e.Realtime)
}

// usecToTime converts a uint64 microsecond count (as systemd writes
// realtime/monotonic timestamps) to a Go time.Time.
//
// FIX(M3): the spike at journald-parser-spike/main.go cast `uint64 ->
// int64` directly:
//
//	t := time.Unix(int64(usec/1_000_000), int64((usec%1_000_000)*1000))
//
// For usec values above math.MaxInt64 (e.g. systemd's
// USEC_INFINITY = (uint64)-1 sentinel, or maliciously crafted journals),
// this wraps the cast and produces a negative time.Time pointing at a
// pre-1970 instant — silently corrupting downstream timestamp comparisons.
//
// We bound-check before the cast: if usec/1_000_000 would overflow int64,
// we return an error rather than wrap. The Realtime field on Entry stays a
// raw uint64 so callers can decide policy (drop, clamp, propagate).
func usecToTime(usec uint64) (time.Time, error) {
	if usec == 0 {
		return time.Time{}, nil
	}
	const maxSec = uint64(math.MaxInt64) // seconds component must fit int64
	secs := usec / 1_000_000
	if secs > maxSec {
		return time.Time{}, fmt.Errorf("usec=%d overflows int64 seconds", usec)
	}
	nsecs := (usec % 1_000_000) * 1_000
	// nsecs <= 999_999_000 fits int64 trivially; no overflow check needed.
	return time.Unix(int64(secs), int64(nsecs)).UTC(), nil
}

// ReadDataField resolves a single EntryItem.ObjectOffset to its DATA
// object's payload and splits the FIELD=value pair.
//
// The compact flag selects the DATA payload offset:
//
//   - non-compact: payload starts at object+16+48 (DataPayloadOffset).
//   - compact:     payload starts at object+16+48+8 (DataCompactPayloadOffset).
//
// If the DATA object carries any ObjectCompressed* flag, the on-disk
// payload is decompressed via DecompressPayload (LZ4/XZ/ZSTD) before the
// FIELD=value split. Compression failures are wrapped and surfaced to the
// caller.
//
// The on-disk payload is bounded by MaxDataPayloadSize to cap allocation
// regardless of the on-disk Size field. systemd splits payloads larger
// than 64 KiB across multiple DATA objects, so this is not a functional
// limit on real journals — it is purely a denial-of-service guard. The
// decompressed payload is bounded separately by MaxDecompressedSize inside
// DecompressPayload.
func ReadDataField(r io.ReaderAt, offset uint64, compact bool) (field, value string, err error) {
	oh, err := ParseObjectHeader(r, offset)
	if err != nil {
		return "", "", fmt.Errorf("data@%d: %w", offset, err)
	}
	if oh.Type != ObjectData {
		return "", "", fmt.Errorf("%w: type=%d offset=%d",
			ErrDataWrongType, oh.Type, offset)
	}

	payloadStart := offset + DataPayloadOffset
	if compact {
		payloadStart = offset + DataCompactPayloadOffset
	}
	if oh.Size < (payloadStart - offset) {
		return "", "", fmt.Errorf("data@%d: size=%d below payload offset %d",
			offset, oh.Size, payloadStart-offset)
	}
	payloadSize := oh.Size - (payloadStart - offset)
	if payloadSize == 0 {
		return "", "", fmt.Errorf("data@%d: empty payload", offset)
	}
	if payloadSize > MaxDataPayloadSize {
		return "", "", fmt.Errorf("%w: size=%d offset=%d",
			ErrDataPayloadTooLarge, payloadSize, offset)
	}

	payload := make([]byte, payloadSize)
	if _, err := r.ReadAt(payload, int64(payloadStart)); err != nil {
		return "", "", fmt.Errorf("read data payload at %d: %w",
			payloadStart, err)
	}

	// If the parent object is flagged compressed, hand the raw payload
	// to the compression dispatcher and replace it with the decoded
	// bytes before searching for the '=' separator. The split must run
	// against the decompressed FIELD=value, never the compressed wire
	// bytes.
	if oh.IsCompressed() {
		decompressed, derr := DecompressPayload(payload, oh.CompressionFlag())
		if derr != nil {
			return "", "", fmt.Errorf("decompress data@%d (flags=0x%x): %w",
				offset, oh.Flags, derr)
		}
		payload = decompressed
	}

	idx := strings.IndexByte(string(payload), '=')
	if idx < 0 {
		return "", "", fmt.Errorf("%w: offset=%d", ErrDataPayloadMalformed, offset)
	}
	return string(payload[:idx]), string(payload[idx+1:]), nil
}
