// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package native // import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/input/journald/native"

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ObjectType identifies the kind of journal object that follows an
// ObjectHeader. See https://systemd.io/JOURNAL_FILE_FORMAT/#objects.
type ObjectType uint8

// Object type values, matching systemd's enum order.
const (
	// ObjectUnused marks a deleted or never-initialized object slot.
	ObjectUnused ObjectType = 0
	// ObjectData carries a single FIELD=value payload referenced by one
	// or more entries.
	ObjectData ObjectType = 1
	// ObjectField names a field family (e.g. MESSAGE) and chains its
	// associated DATA objects via the field hash table.
	ObjectField ObjectType = 2
	// ObjectEntry is a single log record; it points at the DATA objects
	// that make up its key/value pairs.
	ObjectEntry ObjectType = 3
	// ObjectDataHashTable indexes DATA objects by hash for O(1) lookup
	// during writes and cursor resolution.
	ObjectDataHashTable ObjectType = 4
	// ObjectFieldHashTable indexes FIELD objects by hash.
	ObjectFieldHashTable ObjectType = 5
	// ObjectEntryArray is a linked-list chunk of entry offsets used for
	// ordered traversal of all ENTRY objects.
	ObjectEntryArray ObjectType = 6
	// ObjectTag carries a forward-secure sealing MAC (FSS); only present
	// when HEADER_COMPATIBLE_SEALED is set.
	ObjectTag ObjectType = 7
)

// Object header flags. The compression flags are mutually exclusive and only
// meaningful on ObjectData payloads.
const (
	// ObjectCompressedXZ marks a DATA payload compressed with XZ.
	ObjectCompressedXZ uint8 = 1 << 0
	// ObjectCompressedLZ4 marks a DATA payload compressed with LZ4.
	ObjectCompressedLZ4 uint8 = 1 << 1
	// ObjectCompressedZSTD marks a DATA payload compressed with ZSTD.
	ObjectCompressedZSTD uint8 = 1 << 2
)

// ObjectHeaderSize is the wire size of the common 16-byte object header.
//
// Layout (little-endian):
//
//	offset 0:  type     uint8
//	offset 1:  flags    uint8
//	offset 2:  reserved [6]byte (must be zero)
//	offset 8:  size     uint64  (total object size, including this header)
//
// Every object in the file starts with these 16 bytes; type-specific
// payload immediately follows.
const ObjectHeaderSize uint64 = 16

// ObjectAlignment is the on-disk alignment for every object. systemd writes
// each object so that its starting offset is a multiple of 8.
const ObjectAlignment uint64 = 8

// ObjectHeader is the parsed common header for any journal object. The
// type-specific payload is not read here; callers should dispatch on Type
// and read the body via offset+ObjectHeaderSize.
type ObjectHeader struct {
	// Type identifies the object kind (DATA, ENTRY, ...).
	Type ObjectType
	// Flags holds object-level bits. For DATA objects, the
	// ObjectCompressed* bits select the payload compression algorithm.
	Flags uint8
	// Reserved holds the 6 bytes between Flags and Size. Captured so
	// callers can detect non-zero reserved bytes if they want to be
	// strict, but the parser does not enforce zero by default (systemd
	// permits implementations to ignore them).
	Reserved [6]byte
	// Size is the total on-disk size of the object in bytes, including
	// the 16-byte common header.
	Size uint64
	// Offset is where this header was read from. Captured for caller
	// convenience so they can compute the start of the type-specific
	// payload (Offset+ObjectHeaderSize) and the next object
	// (Offset+Size, then aligned upward to ObjectAlignment).
	Offset uint64
}

// PayloadOffset returns the offset of the byte immediately after the common
// header, where type-specific payload begins.
func (o *ObjectHeader) PayloadOffset() uint64 {
	return o.Offset + ObjectHeaderSize
}

// PayloadSize returns the size of the type-specific payload (Size minus the
// 16-byte common header). Returns 0 if Size is somehow below the header
// size, which ParseObjectHeader rejects up-front.
func (o *ObjectHeader) PayloadSize() uint64 {
	if o.Size <= ObjectHeaderSize {
		return 0
	}
	return o.Size - ObjectHeaderSize
}

// NextOffset returns the offset of the next object after this one, aligned
// upward to ObjectAlignment per the systemd format spec.
func (o *ObjectHeader) NextOffset() uint64 {
	return alignUp(o.Offset+o.Size, ObjectAlignment)
}

// IsCompressed reports whether any of the ObjectCompressed* flags are set.
// Only meaningful for ObjectData; other types should not have these bits.
func (o *ObjectHeader) IsCompressed() bool {
	return o.Flags&(ObjectCompressedXZ|ObjectCompressedLZ4|ObjectCompressedZSTD) != 0
}

// CompressionFlag returns the single ObjectCompressed* bit set on this
// object, or 0 if none. The systemd format guarantees at most one
// compression algorithm per object.
func (o *ObjectHeader) CompressionFlag() uint8 {
	return o.Flags & (ObjectCompressedXZ | ObjectCompressedLZ4 | ObjectCompressedZSTD)
}

// errors returned by ParseObjectHeader. Exported so callers can branch with
// errors.Is.
var (
	// ErrObjectMisaligned indicates the supplied offset is not a multiple
	// of ObjectAlignment, violating the systemd format spec.
	ErrObjectMisaligned = errors.New("journal object offset not 8-byte aligned")
	// ErrObjectTooSmall indicates the declared object size is below the
	// 16-byte common header size and therefore cannot describe a valid
	// object.
	ErrObjectTooSmall = errors.New("journal object size below header minimum")
	// ErrObjectUnknownType indicates the object type byte is outside the
	// range defined by systemd. Returned only by callers that opt into
	// strict validation; ParseObjectHeader itself accepts any value so
	// that scanners can skip unknown types via Size.
	ErrObjectUnknownType = errors.New("journal object has unknown type")
)

// ParseObjectHeader reads and validates the 16-byte common object header at
// the given offset.
//
// Validation performed:
//
//   - offset is a multiple of ObjectAlignment (8 bytes); systemd writes
//     every object on an 8-byte boundary.
//   - Size >= ObjectHeaderSize; an object that claims to be smaller than
//     its own header is malformed.
//
// Type and Flags are read verbatim with no range checking so that callers
// scanning a file can step over unknown object types via Size. Use
// ValidateKnownType for strict-mode parsers.
//
// The function performs a single ReadAt of ObjectHeaderSize bytes. It
// surfaces io.ErrUnexpectedEOF if the file is shorter than offset+16.
func ParseObjectHeader(r io.ReaderAt, offset uint64) (*ObjectHeader, error) {
	if offset%ObjectAlignment != 0 {
		return nil, fmt.Errorf("%w: offset=%d", ErrObjectMisaligned, offset)
	}

	buf := make([]byte, ObjectHeaderSize)
	n, err := r.ReadAt(buf, int64(offset))
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read object header at %d: %w", offset, err)
	}
	if uint64(n) < ObjectHeaderSize {
		return nil, fmt.Errorf("read object header at %d: only %d of %d bytes available: %w",
			offset, n, ObjectHeaderSize, io.ErrUnexpectedEOF)
	}

	o := &ObjectHeader{
		Type:   ObjectType(buf[0]),
		Flags:  buf[1],
		Size:   binary.LittleEndian.Uint64(buf[8:16]),
		Offset: offset,
	}
	copy(o.Reserved[:], buf[2:8])

	if o.Size < ObjectHeaderSize {
		return nil, fmt.Errorf("%w: type=%d size=%d", ErrObjectTooSmall, o.Type, o.Size)
	}

	return o, nil
}

// ValidateKnownType returns ErrObjectUnknownType if Type is outside the
// range defined by systemd (ObjectUnused..ObjectTag). Callers that want to
// reject malformed files up-front can invoke this after ParseObjectHeader.
func (o *ObjectHeader) ValidateKnownType() error {
	if o.Type > ObjectTag {
		return fmt.Errorf("%w: type=%d", ErrObjectUnknownType, o.Type)
	}
	return nil
}

// alignUp rounds v upward to the next multiple of align. align must be a
// power of two; ObjectAlignment (8) is the only caller in this package.
func alignUp(v, align uint64) uint64 {
	return (v + align - 1) &^ (align - 1)
}
