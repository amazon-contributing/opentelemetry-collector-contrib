// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package native // import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/input/journald/native"

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Signature is the 8-byte magic that every systemd journal file begins with
// ("LPKSHHRH"). Reference: https://systemd.io/JOURNAL_FILE_FORMAT/.
var Signature = [8]byte{'L', 'P', 'K', 'S', 'H', 'H', 'R', 'H'}

// Compatible feature flags. See sd_journal_compatible_flags in systemd source.
const (
	HeaderCompatibleSealed           uint32 = 1 << 0
	HeaderCompatibleSealedContinuous uint32 = 1 << 1
	HeaderCompatibleTailEntryBootID  uint32 = 1 << 2
)

// Incompatible feature flags. A reader that does not understand any bit set
// in incompatible_flags must refuse to parse the file.
const (
	HeaderIncompatibleCompressedXZ   uint32 = 1 << 0
	HeaderIncompatibleCompressedLZ4  uint32 = 1 << 1
	HeaderIncompatibleKeyedHash      uint32 = 1 << 2
	HeaderIncompatibleCompressedZSTD uint32 = 1 << 3
	HeaderIncompatibleCompact        uint32 = 1 << 4
)

// HeaderState values describe the lifecycle position of a journal file.
const (
	HeaderStateOffline  uint8 = 0
	HeaderStateOnline   uint8 = 1
	HeaderStateArchived uint8 = 2
)

// MinHeaderSize is the smallest header layout that any version of systemd
// has emitted (systemd 187 introduced n_data / n_fields, bringing the header
// to 224 bytes; prior versions stopped at 208 bytes after
// tail_entry_monotonic). systemd 246 extended the header to 256 bytes by
// appending the data_hash_chain_depth and field_hash_chain_depth fields.
//
// The reader requires at least the systemd 187 layout (header_size >= 224)
// because earlier files lack the n_data / n_fields counters that downstream
// parsing relies on. Callers that need broader compatibility should relax
// this gate explicitly.
//
// MaxHeaderSize is the size of the largest header layout this parser decodes
// field-by-field (256 bytes, the systemd 246 layout). The on-disk header_size
// MAY legitimately EXCEED this: systemd appends new trailing fields in later
// versions (e.g. systemd 252 on AL2023 writes a 264-byte header). The parser
// reads the first MaxHeaderSize bytes, decodes the fields it understands, and
// IGNORES any trailing bytes. The arena scan begins at the true on-disk
// header.HeaderSize (see reader.go), so a larger header is forward-compatible
// and MUST NOT be rejected. The name is retained (rather than renamed to
// e.g. knownHeaderLayoutSize) for API stability with existing callers/tests.
const (
	MinHeaderSize uint64 = 224
	MaxHeaderSize uint64 = 256
)

// Header is the parsed representation of the systemd journal file header.
// All fields are stored in their native Go types after little-endian decode.
//
// 128-bit identifiers (file_id, machine_id, boot_id, seqnum_id) are kept as
// fixed-size byte arrays; callers that need a textual representation should
// format them as 32-character lowercase hex (matching systemd's CLI output).
//
// Field offsets follow the layout documented at
// https://systemd.io/JOURNAL_FILE_FORMAT/ and confirmed against gournal
// commit 6059064 (2024-05-07). See the package NOTICE for attribution.
type Header struct {
	// Signature is the 8-byte magic ("LPKSHHRH").
	Signature [8]byte
	// CompatibleFlags advertises optional features. A reader may ignore
	// unknown bits.
	CompatibleFlags uint32
	// IncompatibleFlags advertises required features. A reader MUST refuse
	// the file if any unknown bit is set.
	IncompatibleFlags uint32
	// State is the journal-file lifecycle state (offline / online /
	// archived).
	State uint8
	// FileID is the unique identifier for this journal file.
	FileID [16]byte
	// MachineID is the systemd machine-id of the host that produced the
	// file.
	MachineID [16]byte
	// BootID is the boot identifier active when the file was created.
	BootID [16]byte
	// SeqnumID is the identifier shared by every file participating in the
	// same sequence-number space.
	SeqnumID [16]byte
	// HeaderSize is the on-disk size of this header struct, in bytes.
	HeaderSize uint64
	// ArenaSize is the size of the arena that follows the header.
	ArenaSize uint64
	// DataHashTableOffset is the offset of the data hash table within the
	// arena, or zero if the table has not been initialized.
	DataHashTableOffset uint64
	// DataHashTableSize is the size of the data hash table in bytes.
	DataHashTableSize uint64
	// FieldHashTableOffset is the offset of the field hash table.
	FieldHashTableOffset uint64
	// FieldHashTableSize is the size of the field hash table in bytes.
	FieldHashTableSize uint64
	// TailObjectOffset is the offset of the most recent object written.
	TailObjectOffset uint64
	// NObjects is the total number of objects in the file.
	NObjects uint64
	// NEntries is the number of ENTRY objects in the file.
	NEntries uint64
	// TailEntrySeqnum is the seqnum of the last ENTRY in the file.
	TailEntrySeqnum uint64
	// HeadEntrySeqnum is the seqnum of the first ENTRY in the file.
	HeadEntrySeqnum uint64
	// EntryArrayOffset is the offset of the head EntryArray for indexed
	// traversal of entries in chronological order.
	EntryArrayOffset uint64
	// HeadEntryRealtime is the realtime timestamp (us since epoch) of the
	// first entry.
	HeadEntryRealtime uint64
	// TailEntryRealtime is the realtime timestamp (us since epoch) of the
	// last entry.
	TailEntryRealtime uint64
	// TailEntryMonotonic is the monotonic timestamp of the last entry.
	TailEntryMonotonic uint64
	// NData is the number of DATA objects (added in systemd 187).
	NData uint64
	// NFields is the number of FIELD objects (added in systemd 187).
	NFields uint64
	// NTags is the number of TAG objects (added in systemd 189). Zero if
	// the file's HeaderSize is too small to include this field.
	NTags uint64
	// NEntryArrays is the number of EntryArray objects (added in systemd
	// 189). Zero if HeaderSize is too small.
	NEntryArrays uint64
}

// IsCompact reports whether HEADER_INCOMPATIBLE_COMPACT is set, which selects
// the 4-byte-per-item entry layout introduced in systemd 252.
func (h *Header) IsCompact() bool {
	return h.IncompatibleFlags&HeaderIncompatibleCompact != 0
}

// CompressionFlags returns just the compression bits of incompatible_flags.
// Useful for object-level decompression dispatch.
func (h *Header) CompressionFlags() uint32 {
	return h.IncompatibleFlags & (HeaderIncompatibleCompressedXZ |
		HeaderIncompatibleCompressedLZ4 |
		HeaderIncompatibleCompressedZSTD)
}

// errors returned by ParseHeader. Exported as variables so callers can use
// errors.Is for branching.
var (
	// ErrInvalidSignature indicates the file does not begin with "LPKSHHRH".
	ErrInvalidSignature = errors.New("invalid journal signature")
	// ErrHeaderTooSmall indicates the file's declared header_size is below
	// the minimum supported layout (systemd 187, 224 bytes).
	ErrHeaderTooSmall = errors.New("journal header smaller than minimum supported size")
	// ErrHeaderTooLarge is retained for API/errors.Is compatibility but is no
	// longer returned by ParseHeader: a header_size larger than the layout we
	// decode (e.g. 264 bytes on systemd 252 / AL2023) is now accepted as
	// forward-compatible. See ParseHeader and MaxHeaderSize.
	//
	// Deprecated: ParseHeader never returns this; oversized headers are valid.
	ErrHeaderTooLarge = errors.New("journal header larger than maximum supported size")
	// ErrUnknownIncompatibleFlag indicates the file requires a feature that
	// this parser does not implement; per the systemd format spec the file
	// must be refused.
	ErrUnknownIncompatibleFlag = errors.New("journal header sets unknown incompatible flag")
)

// supportedIncompatibleFlags is the bit mask of incompatible_flags that this
// reader can handle. Any bit set outside this mask makes the file unreadable.
const supportedIncompatibleFlags = HeaderIncompatibleCompressedXZ |
	HeaderIncompatibleCompressedLZ4 |
	HeaderIncompatibleKeyedHash |
	HeaderIncompatibleCompressedZSTD |
	HeaderIncompatibleCompact

// ParseHeader reads and validates the 256-byte (or smaller) journal header
// from r at offset 0.
//
// Validation performed:
//
//   - Signature equals "LPKSHHRH".
//   - HeaderSize is >= MinHeaderSize. There is no upper bound: a header_size
//     larger than MaxHeaderSize (newer systemd) is accepted; only the known
//     MaxHeaderSize-byte prefix is decoded and trailing bytes are ignored.
//   - IncompatibleFlags only sets bits the parser understands.
//
// The function performs a single ReadAt(buf[:MaxHeaderSize], 0). It returns
// io.ErrUnexpectedEOF if the file is shorter than MinHeaderSize.
func ParseHeader(r io.ReaderAt) (*Header, error) {
	// Read the largest header layout we know how to decode. If the file is
	// smaller than that, ReadAt may return a short read with io.EOF; we fall
	// back to the declared header_size after sanity-checking the signature.
	// A larger on-disk header_size (newer systemd) is fine: we only need the
	// known-layout prefix here, and the arena scan starts at header_size.
	buf := make([]byte, MaxHeaderSize)
	n, err := r.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read journal header: %w", err)
	}
	if uint64(n) < MinHeaderSize {
		return nil, fmt.Errorf("read journal header: only %d of %d bytes available: %w",
			n, MinHeaderSize, io.ErrUnexpectedEOF)
	}
	buf = buf[:n]

	h := &Header{}
	copy(h.Signature[:], buf[0:8])
	if h.Signature != Signature {
		return nil, fmt.Errorf("%w: got %q, want %q",
			ErrInvalidSignature, string(h.Signature[:]), string(Signature[:]))
	}

	le := binary.LittleEndian
	h.CompatibleFlags = le.Uint32(buf[8:12])
	h.IncompatibleFlags = le.Uint32(buf[12:16])
	h.State = buf[16]
	// buf[17:24] is reserved; ignored.
	copy(h.FileID[:], buf[24:40])
	copy(h.MachineID[:], buf[40:56])
	copy(h.BootID[:], buf[56:72])
	copy(h.SeqnumID[:], buf[72:88])
	h.HeaderSize = le.Uint64(buf[88:96])
	h.ArenaSize = le.Uint64(buf[96:104])
	h.DataHashTableOffset = le.Uint64(buf[104:112])
	h.DataHashTableSize = le.Uint64(buf[112:120])
	h.FieldHashTableOffset = le.Uint64(buf[120:128])
	h.FieldHashTableSize = le.Uint64(buf[128:136])
	h.TailObjectOffset = le.Uint64(buf[136:144])
	h.NObjects = le.Uint64(buf[144:152])
	h.NEntries = le.Uint64(buf[152:160])
	h.TailEntrySeqnum = le.Uint64(buf[160:168])
	h.HeadEntrySeqnum = le.Uint64(buf[168:176])
	h.EntryArrayOffset = le.Uint64(buf[176:184])
	h.HeadEntryRealtime = le.Uint64(buf[184:192])
	h.TailEntryRealtime = le.Uint64(buf[192:200])
	h.TailEntryMonotonic = le.Uint64(buf[200:208])
	h.NData = le.Uint64(buf[208:216])
	h.NFields = le.Uint64(buf[216:224])

	// Optional fields added in systemd 189 (n_tags, n_entry_arrays).
	if uint64(n) >= 240 {
		h.NTags = le.Uint64(buf[224:232])
		h.NEntryArrays = le.Uint64(buf[232:240])
	}
	// Fields 240:256 (data_hash_chain_depth, field_hash_chain_depth) are
	// not exposed yet; the offsets are reserved for a future revision.

	if h.HeaderSize < MinHeaderSize {
		return nil, fmt.Errorf("%w: header_size=%d, minimum=%d",
			ErrHeaderTooSmall, h.HeaderSize, MinHeaderSize)
	}
	// A header_size larger than the layout we decode is expected on newer
	// systemd (e.g. 264 bytes on systemd 252 / AL2023). We deliberately do
	// NOT reject it: the extra trailing fields are unknown to us but harmless,
	// and the arena scan begins at h.HeaderSize. We only require that the
	// known-layout prefix we actually decode was fully read.
	mustRead := h.HeaderSize
	if mustRead > MaxHeaderSize {
		mustRead = MaxHeaderSize
	}
	if uint64(n) < mustRead {
		return nil, fmt.Errorf("read journal header: only %d of %d declared bytes available: %w",
			n, mustRead, io.ErrUnexpectedEOF)
	}

	if unsupported := h.IncompatibleFlags &^ supportedIncompatibleFlags; unsupported != 0 {
		return nil, fmt.Errorf("%w: 0x%x", ErrUnknownIncompatibleFlag, unsupported)
	}

	return h, nil
}
