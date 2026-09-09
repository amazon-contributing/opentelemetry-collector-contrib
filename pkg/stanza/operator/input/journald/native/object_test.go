// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package native

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"testing"
)

// makeObjectHeaderBytes builds a 16-byte common object header with the given
// type, flags and total size. Reserved bytes default to zero; tests that
// need non-zero reserved bytes must mutate the returned slice directly.
func makeObjectHeaderBytes(typ ObjectType, flags uint8, size uint64) []byte {
	buf := make([]byte, ObjectHeaderSize)
	buf[0] = byte(typ)
	buf[1] = flags
	// buf[2:8] reserved (zero)
	binary.LittleEndian.PutUint64(buf[8:16], size)
	return buf
}

// padToSize returns header bytes followed by zero padding so that the total
// length equals size. Tests use this so they can pass a ReaderAt that holds
// the entire object the header claims to describe.
func padToSize(header []byte, size uint64) []byte {
	if uint64(len(header)) >= size {
		return header
	}
	out := make([]byte, size)
	copy(out, header)
	return out
}

// TestParseObjectHeader_AllTypes covers every defined ObjectType and
// confirms ParseObjectHeader decodes Type, Flags, Size, and Offset
// correctly for each. Sizes are arbitrary but obey the >= ObjectHeaderSize
// rule and align to 8 bytes so a downstream NextOffset() check is meaningful.
func TestParseObjectHeader_AllTypes(t *testing.T) {
	cases := []struct {
		name string
		typ  ObjectType
		size uint64
	}{
		{"unused", ObjectUnused, ObjectHeaderSize},
		{"data", ObjectData, 64},
		{"field", ObjectField, 32},
		{"entry", ObjectEntry, 128},
		{"data_hash_table", ObjectDataHashTable, 1024},
		{"field_hash_table", ObjectFieldHashTable, 512},
		{"entry_array", ObjectEntryArray, 256},
		{"tag", ObjectTag, 48},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := padToSize(makeObjectHeaderBytes(tc.typ, 0, tc.size), tc.size)
			o, err := ParseObjectHeader(bytes.NewReader(buf), 0)
			if err != nil {
				t.Fatalf("ParseObjectHeader: %v", err)
			}
			if o.Type != tc.typ {
				t.Errorf("Type = %d, want %d", o.Type, tc.typ)
			}
			if o.Flags != 0 {
				t.Errorf("Flags = 0x%x, want 0", o.Flags)
			}
			if o.Size != tc.size {
				t.Errorf("Size = %d, want %d", o.Size, tc.size)
			}
			if o.Offset != 0 {
				t.Errorf("Offset = %d, want 0", o.Offset)
			}
			if got := o.PayloadOffset(); got != ObjectHeaderSize {
				t.Errorf("PayloadOffset() = %d, want %d", got, ObjectHeaderSize)
			}
			wantPayload := tc.size - ObjectHeaderSize
			if got := o.PayloadSize(); got != wantPayload {
				t.Errorf("PayloadSize() = %d, want %d", got, wantPayload)
			}
			// NextOffset on a size-aligned object equals offset+size.
			if got := o.NextOffset(); got != tc.size {
				t.Errorf("NextOffset() = %d, want %d", got, tc.size)
			}
			if o.IsCompressed() {
				t.Errorf("IsCompressed() = true, want false")
			}
			if got := o.CompressionFlag(); got != 0 {
				t.Errorf("CompressionFlag() = 0x%x, want 0", got)
			}
			if err := o.ValidateKnownType(); err != nil {
				t.Errorf("ValidateKnownType: %v", err)
			}
		})
	}
}

// TestParseObjectHeader_AtNonZeroOffset confirms ReadAt is honored and
// Offset is captured. Constructs a buffer with a sentinel byte before the
// header to prove the parser is not reading offset 0 by accident.
func TestParseObjectHeader_AtNonZeroOffset(t *testing.T) {
	const startOffset uint64 = 64
	buf := make([]byte, startOffset+ObjectHeaderSize+16)
	header := makeObjectHeaderBytes(ObjectEntry, 0, 32)
	copy(buf[startOffset:], header)
	// Pre-fill the slot before the header with a non-zero sentinel; if the
	// parser accidentally reads offset 0 it will see this and decode wrong.
	for i := uint64(0); i < startOffset; i++ {
		buf[i] = 0xEE
	}

	o, err := ParseObjectHeader(bytes.NewReader(buf), startOffset)
	if err != nil {
		t.Fatalf("ParseObjectHeader: %v", err)
	}
	if o.Offset != startOffset {
		t.Errorf("Offset = %d, want %d", o.Offset, startOffset)
	}
	if o.Type != ObjectEntry {
		t.Errorf("Type = %d, want %d", o.Type, ObjectEntry)
	}
	if got := o.PayloadOffset(); got != startOffset+ObjectHeaderSize {
		t.Errorf("PayloadOffset() = %d, want %d",
			got, startOffset+ObjectHeaderSize)
	}
}

// TestParseObjectHeader_UnalignedOffset confirms a misaligned offset is
// rejected with ErrObjectMisaligned for every non-multiple-of-8 input.
func TestParseObjectHeader_UnalignedOffset(t *testing.T) {
	buf := padToSize(makeObjectHeaderBytes(ObjectData, 0, 32), 64)

	for _, off := range []uint64{1, 2, 3, 4, 5, 6, 7, 9, 17, 31} {
		t.Run(fmt.Sprintf("offset_%d", off), func(t *testing.T) {
			_, err := ParseObjectHeader(bytes.NewReader(buf), off)
			if !errors.Is(err, ErrObjectMisaligned) {
				t.Errorf("err = %v, want ErrObjectMisaligned", err)
			}
		})
	}
}

// TestParseObjectHeader_ZeroOffsetAccepted ensures offset 0 is treated as
// aligned (0 % 8 == 0). Regression guard: a naive `offset > 0 && offset%8 !=
// 0` check would also accept misaligned offsets.
func TestParseObjectHeader_ZeroOffsetAccepted(t *testing.T) {
	buf := padToSize(makeObjectHeaderBytes(ObjectData, 0, 24), 24)
	if _, err := ParseObjectHeader(bytes.NewReader(buf), 0); err != nil {
		t.Errorf("offset=0: err = %v, want nil", err)
	}
}

// TestParseObjectHeader_MalformedSize covers every value below
// ObjectHeaderSize. systemd never emits an object that claims to be smaller
// than its own 16-byte header; the parser must reject such files up-front.
func TestParseObjectHeader_MalformedSize(t *testing.T) {
	for _, declared := range []uint64{0, 1, 8, 15} {
		t.Run(fmt.Sprintf("size_%d", declared), func(t *testing.T) {
			buf := makeObjectHeaderBytes(ObjectData, 0, declared)
			_, err := ParseObjectHeader(bytes.NewReader(buf), 0)
			if !errors.Is(err, ErrObjectTooSmall) {
				t.Errorf("err = %v, want ErrObjectTooSmall", err)
			}
		})
	}
}

// TestParseObjectHeader_SizeEqualsHeader confirms an object whose entire
// body is just the common header is accepted (zero-byte payload).
func TestParseObjectHeader_SizeEqualsHeader(t *testing.T) {
	buf := makeObjectHeaderBytes(ObjectUnused, 0, ObjectHeaderSize)
	o, err := ParseObjectHeader(bytes.NewReader(buf), 0)
	if err != nil {
		t.Fatalf("ParseObjectHeader: %v", err)
	}
	if o.PayloadSize() != 0 {
		t.Errorf("PayloadSize() = %d, want 0", o.PayloadSize())
	}
}

// TestParseObjectHeader_Truncated covers ReaderAt sources shorter than
// ObjectHeaderSize. The parser must surface io.ErrUnexpectedEOF so callers
// can branch on errors.Is.
func TestParseObjectHeader_Truncated(t *testing.T) {
	full := padToSize(makeObjectHeaderBytes(ObjectData, 0, 32), 32)
	for _, n := range []int{0, 1, 8, int(ObjectHeaderSize) - 1} {
		t.Run(fmt.Sprintf("len_%d", n), func(t *testing.T) {
			_, err := ParseObjectHeader(bytes.NewReader(full[:n]), 0)
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Errorf("err = %v, want io.ErrUnexpectedEOF", err)
			}
		})
	}
}

// errReaderAt is a ReaderAt that always returns a non-EOF error. It exercises
// the read-error branch of ParseObjectHeader, which io.EOF on bytes.Reader
// cannot reach.
type errReaderAt struct{ err error }

func (e errReaderAt) ReadAt(_ []byte, _ int64) (int, error) {
	return 0, e.err
}

// TestParseObjectHeader_ReadError confirms a non-EOF ReadAt error is wrapped
// rather than translated to io.ErrUnexpectedEOF.
func TestParseObjectHeader_ReadError(t *testing.T) {
	sentinel := errors.New("synthetic disk failure")
	_, err := ParseObjectHeader(errReaderAt{err: sentinel}, 0)
	if err == nil {
		t.Fatal("err = nil, want wrapped sentinel")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want errors.Is(sentinel)", err)
	}
}

// TestParseObjectHeader_CompressionFlags exercises every defined
// compression bit (XZ, LZ4, ZSTD) and the IsCompressed / CompressionFlag
// helpers. Combined flags are not produced by systemd but the helper must
// still surface the masked value rather than panic.
func TestParseObjectHeader_CompressionFlags(t *testing.T) {
	cases := []struct {
		name      string
		flag      uint8
		wantBit   uint8
		wantCompr bool
	}{
		{"none", 0, 0, false},
		{"xz", ObjectCompressedXZ, ObjectCompressedXZ, true},
		{"lz4", ObjectCompressedLZ4, ObjectCompressedLZ4, true},
		{"zstd", ObjectCompressedZSTD, ObjectCompressedZSTD, true},
		{
			"all_three_set_returns_mask",
			ObjectCompressedXZ | ObjectCompressedLZ4 | ObjectCompressedZSTD,
			ObjectCompressedXZ | ObjectCompressedLZ4 | ObjectCompressedZSTD,
			true,
		},
		{
			"non_compression_bit_ignored",
			1 << 7, 0, false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := padToSize(makeObjectHeaderBytes(ObjectData, tc.flag, 32), 32)
			o, err := ParseObjectHeader(bytes.NewReader(buf), 0)
			if err != nil {
				t.Fatalf("ParseObjectHeader: %v", err)
			}
			if got := o.CompressionFlag(); got != tc.wantBit {
				t.Errorf("CompressionFlag() = 0x%x, want 0x%x",
					got, tc.wantBit)
			}
			if got := o.IsCompressed(); got != tc.wantCompr {
				t.Errorf("IsCompressed() = %v, want %v",
					got, tc.wantCompr)
			}
			if o.Flags != tc.flag {
				t.Errorf("Flags = 0x%x, want 0x%x", o.Flags, tc.flag)
			}
		})
	}
}

// TestParseObjectHeader_ReservedBytesPreserved confirms the parser does not
// silently strip non-zero reserved bytes; downstream strict-mode validators
// need access to the raw bytes to decide policy.
func TestParseObjectHeader_ReservedBytesPreserved(t *testing.T) {
	buf := padToSize(makeObjectHeaderBytes(ObjectData, 0, 32), 32)
	for i := 0; i < 6; i++ {
		buf[2+i] = byte(0x10 + i)
	}
	o, err := ParseObjectHeader(bytes.NewReader(buf), 0)
	if err != nil {
		t.Fatalf("ParseObjectHeader: %v", err)
	}
	for i := 0; i < 6; i++ {
		if o.Reserved[i] != byte(0x10+i) {
			t.Errorf("Reserved[%d] = 0x%x, want 0x%x",
				i, o.Reserved[i], byte(0x10+i))
		}
	}
}

// TestObjectHeader_NextOffsetAlignment exercises the systemd 8-byte
// alignment rule on objects whose declared Size is not itself a multiple of
// 8. NextOffset() must round up.
func TestObjectHeader_NextOffsetAlignment(t *testing.T) {
	cases := []struct {
		name        string
		offset      uint64
		size        uint64
		wantNextOff uint64
	}{
		{"already_aligned", 0, 32, 32},
		{"size_25_rounds_to_32", 0, 25, 32},
		{"size_17_rounds_to_24", 0, 17, 24},
		{"offset_8_size_25_rounds_to_40", 8, 25, 40},
		{"size_exactly_header", 0, ObjectHeaderSize, 16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := &ObjectHeader{Offset: tc.offset, Size: tc.size}
			if got := o.NextOffset(); got != tc.wantNextOff {
				t.Errorf("NextOffset() = %d, want %d",
					got, tc.wantNextOff)
			}
		})
	}
}

// TestObjectHeader_PayloadSize_BelowHeader returns 0 rather than underflow.
// ParseObjectHeader rejects such objects, but the helper must remain safe
// in case a struct is constructed by hand or restored from a snapshot.
func TestObjectHeader_PayloadSize_BelowHeader(t *testing.T) {
	o := &ObjectHeader{Size: ObjectHeaderSize - 4}
	if got := o.PayloadSize(); got != 0 {
		t.Errorf("PayloadSize() = %d, want 0 (no underflow)", got)
	}
}

// TestObjectHeader_ValidateKnownType_Unknown confirms types beyond
// ObjectTag are rejected by the strict validator. ParseObjectHeader itself
// still accepts them so file scanners can step over unknown types via Size.
func TestObjectHeader_ValidateKnownType_Unknown(t *testing.T) {
	for _, raw := range []uint8{8, 9, 100, 255} {
		t.Run(fmt.Sprintf("type_%d", raw), func(t *testing.T) {
			buf := padToSize(
				makeObjectHeaderBytes(ObjectType(raw), 0, 24), 24)
			o, err := ParseObjectHeader(bytes.NewReader(buf), 0)
			if err != nil {
				t.Fatalf("ParseObjectHeader: %v", err)
			}
			if o.Type != ObjectType(raw) {
				t.Errorf("Type = %d, want %d", o.Type, raw)
			}
			if err := o.ValidateKnownType(); !errors.Is(
				err, ErrObjectUnknownType) {
				t.Errorf("ValidateKnownType: err = %v, want ErrObjectUnknownType",
					err)
			}
		})
	}
}
