// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package native

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// realFixturePath is the user journal called out in the task spec. Tests fall
// back to any readable user-21999815-* file under the same machine-id dir if
// the exact path has rotated, and skip cleanly when no fixture is available
// (CI, fresh dev hosts).
const realFixturePath = "/var/log/journal/ec2f19f81221e9365430f3aa6529964a/" +
	"user-21999815@628b2381b86e45d3ad01696737b998e5-" +
	"000000000388f381-0006516207dd8ed7.journal"

// TestParseHeader_Valid is the happy-path test required by the task: parse
// the real systemd user journal called out in the spec and assert the header
// is well-formed. Skipped when the fixture is unavailable.
func TestParseHeader_Valid(t *testing.T) {
	path := pickRealFixture(t)
	if path == "" {
		t.Skipf("no readable .journal fixture under /var/log/journal; skipping")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("cannot open fixture %s: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })

	h, err := ParseHeader(f)
	if err != nil {
		t.Fatalf("ParseHeader(%q): %v", path, err)
	}
	if h.Signature != Signature {
		t.Errorf("Signature = %q, want %q",
			string(h.Signature[:]), string(Signature[:]))
	}
	if h.HeaderSize < MinHeaderSize || h.HeaderSize > MaxHeaderSize {
		t.Errorf("HeaderSize = %d, want in [%d, %d]",
			h.HeaderSize, MinHeaderSize, MaxHeaderSize)
	}
	if h.NEntries == 0 {
		t.Errorf("NEntries = 0, want > 0 for a real journal")
	}
	if h.HeadEntrySeqnum == 0 || h.TailEntrySeqnum < h.HeadEntrySeqnum {
		t.Errorf("seqnum range invalid: head=%d tail=%d",
			h.HeadEntrySeqnum, h.TailEntrySeqnum)
	}
	// Helpers must not panic on a real header.
	_ = h.IsCompact()
	_ = h.CompressionFlags()
	t.Logf("parsed %s: header_size=%d n_entries=%d compact=%v compression=0x%x",
		filepath.Base(path), h.HeaderSize, h.NEntries,
		h.IsCompact(), h.CompressionFlags())
}

// TestParseHeader_ValidSynthetic gives us a deterministic happy-path that
// runs on every host (no /var/log/journal dependency). It also verifies field
// decoding by writing distinct bytes per ID array and reading them back.
func TestParseHeader_ValidSynthetic(t *testing.T) {
	buf := makeValidHeaderBytes()

	h, err := ParseHeader(bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	if h.HeaderSize != MinHeaderSize {
		t.Errorf("HeaderSize = %d, want %d", h.HeaderSize, MinHeaderSize)
	}
	if h.State != HeaderStateOnline {
		t.Errorf("State = %d, want %d", h.State, HeaderStateOnline)
	}
	if h.NEntries != 7 || h.NData != 5 || h.NFields != 3 {
		t.Errorf("counts = (entries=%d data=%d fields=%d), want (7,5,3)",
			h.NEntries, h.NData, h.NFields)
	}
	if h.HeadEntrySeqnum != 600 || h.TailEntrySeqnum != 700 {
		t.Errorf("seqnum = (head=%d tail=%d), want (600,700)",
			h.HeadEntrySeqnum, h.TailEntrySeqnum)
	}
	if h.EntryArrayOffset != 3072 {
		t.Errorf("EntryArrayOffset = %d, want 3072", h.EntryArrayOffset)
	}
	for i := range 16 {
		if h.FileID[i] != byte(0xA0+i) ||
			h.MachineID[i] != byte(0xB0+i) ||
			h.BootID[i] != byte(0xC0+i) ||
			h.SeqnumID[i] != byte(0xD0+i) {
			t.Errorf("ID byte %d mismatch: file=0x%x machine=0x%x boot=0x%x seq=0x%x",
				i, h.FileID[i], h.MachineID[i], h.BootID[i], h.SeqnumID[i])
		}
	}
	if h.IsCompact() {
		t.Errorf("IsCompact() = true, want false")
	}
	if h.CompressionFlags() != 0 {
		t.Errorf("CompressionFlags() = 0x%x, want 0", h.CompressionFlags())
	}
}

// TestParseHeader_ValidExtended verifies the systemd 246+ 256-byte layout
// reads NTags / NEntryArrays from the optional trailing region.
func TestParseHeader_ValidExtended(t *testing.T) {
	buf := append(makeValidHeaderBytes(),
		make([]byte, MaxHeaderSize-MinHeaderSize)...)
	le := binary.LittleEndian
	le.PutUint64(buf[88:96], MaxHeaderSize)
	le.PutUint64(buf[224:232], 17)  // NTags
	le.PutUint64(buf[232:240], 101) // NEntryArrays

	h, err := ParseHeader(bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	if h.HeaderSize != MaxHeaderSize {
		t.Errorf("HeaderSize = %d, want %d", h.HeaderSize, MaxHeaderSize)
	}
	if h.NTags != 17 || h.NEntryArrays != 101 {
		t.Errorf("(NTags, NEntryArrays) = (%d, %d), want (17, 101)",
			h.NTags, h.NEntryArrays)
	}
}

// TestParseHeader_WrongMagic confirms bad magic bytes are rejected with
// ErrInvalidSignature so callers can branch with errors.Is.
func TestParseHeader_WrongMagic(t *testing.T) {
	cases := []struct {
		name string
		sig  [8]byte
	}{
		{"all_zero", [8]byte{}},
		{"all_ff", [8]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}},
		{"bit_flip", [8]byte{'L', 'P', 'K', 'S', 'H', 'H', 'R', 'X'}},
		{"shifted", [8]byte{'P', 'K', 'S', 'H', 'H', 'R', 'H', 0}},
		{"elf_magic", [8]byte{'E', 'L', 'F', 0x7f, 0, 0, 0, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := makeValidHeaderBytes()
			copy(buf[0:8], tc.sig[:])
			_, err := ParseHeader(bytes.NewReader(buf))
			if !errors.Is(err, ErrInvalidSignature) {
				t.Errorf("err = %v, want ErrInvalidSignature", err)
			}
		})
	}
}

// TestParseHeader_Truncated covers files shorter than MinHeaderSize. ReadAt
// on bytes.Reader returns io.EOF on a short read; ParseHeader must surface
// io.ErrUnexpectedEOF wrapped in its error.
func TestParseHeader_Truncated(t *testing.T) {
	full := makeValidHeaderBytes()
	for _, n := range []int{0, 1, 8, 100, int(MinHeaderSize) - 1} {
		t.Run(fmt.Sprintf("len_%d", n), func(t *testing.T) {
			_, err := ParseHeader(bytes.NewReader(full[:n]))
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Errorf("err = %v, want io.ErrUnexpectedEOF", err)
			}
		})
	}
}

// TestParseHeader_HeaderSizeMismatch covers the three ways the declared
// header_size can be wrong: too small, too large, or longer than the file.
func TestParseHeader_HeaderSizeMismatch(t *testing.T) {
	t.Run("too_small", func(t *testing.T) {
		buf := makeValidHeaderBytes()
		binary.LittleEndian.PutUint64(buf[88:96], MinHeaderSize-1)
		_, err := ParseHeader(bytes.NewReader(buf))
		if !errors.Is(err, ErrHeaderTooSmall) {
			t.Errorf("err = %v, want ErrHeaderTooSmall", err)
		}
	})

	// Regression for the systemd 252 / AL2023 header: header_size=264 (8 bytes
	// past the layout we decode). The parser MUST accept it, decode the known
	// 256-byte prefix, and ignore the trailing bytes. Found on real-host
	// testing (jourd-al23) where the native reader previously rejected the
	// host's own system.journal with "header larger than maximum supported".
	t.Run("larger_than_known_layout_is_accepted", func(t *testing.T) {
		const onDiskHeaderSize = 264 // systemd 252
		// Build a file: 264-byte header region + arena so reads past the
		// header succeed. Known fields live in the first 256 bytes.
		buf := append(makeValidHeaderBytes(),
			make([]byte, onDiskHeaderSize-MinHeaderSize)...)
		buf = append(buf, make([]byte, 4096)...) // arena
		binary.LittleEndian.PutUint64(buf[88:96], onDiskHeaderSize) // HeaderSize
		h, err := ParseHeader(bytes.NewReader(buf))
		if err != nil {
			t.Fatalf("ParseHeader rejected systemd-252 header_size=%d: %v", onDiskHeaderSize, err)
		}
		if h.HeaderSize != onDiskHeaderSize {
			t.Errorf("HeaderSize = %d, want %d", h.HeaderSize, onDiskHeaderSize)
		}
		// Known fields from the 256-byte prefix must still decode correctly.
		if h.NData != 5 || h.NFields != 3 {
			t.Errorf("known prefix fields wrong: NData=%d NFields=%d, want 5/3", h.NData, h.NFields)
		}
	})

	t.Run("declared_larger_than_file", func(t *testing.T) {
		buf := makeValidHeaderBytes()
		binary.LittleEndian.PutUint64(buf[88:96], MaxHeaderSize)
		_, err := ParseHeader(bytes.NewReader(buf))
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("err = %v, want io.ErrUnexpectedEOF", err)
		}
	})
}

// TestParseHeader_UnknownIncompatibleFlag confirms the parser refuses files
// that demand features it does not implement, per the systemd format spec.
func TestParseHeader_UnknownIncompatibleFlag(t *testing.T) {
	buf := makeValidHeaderBytes()
	binary.LittleEndian.PutUint32(buf[12:16], 1<<31)
	_, err := ParseHeader(bytes.NewReader(buf))
	if !errors.Is(err, ErrUnknownIncompatibleFlag) {
		t.Errorf("err = %v, want ErrUnknownIncompatibleFlag", err)
	}
}

// TestParseHeader_KnownIncompatibleFlagsAccepted is the symmetric positive
// case: every supported flag combination must parse without error.
func TestParseHeader_KnownIncompatibleFlagsAccepted(t *testing.T) {
	flags := uint32(HeaderIncompatibleCompressedXZ |
		HeaderIncompatibleCompressedLZ4 |
		HeaderIncompatibleKeyedHash |
		HeaderIncompatibleCompressedZSTD |
		HeaderIncompatibleCompact)

	buf := makeValidHeaderBytes()
	binary.LittleEndian.PutUint32(buf[12:16], flags)

	h, err := ParseHeader(bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	if h.IncompatibleFlags != flags {
		t.Errorf("IncompatibleFlags = 0x%x, want 0x%x",
			h.IncompatibleFlags, flags)
	}
	if !h.IsCompact() {
		t.Errorf("IsCompact() = false, want true")
	}
	wantC := uint32(HeaderIncompatibleCompressedXZ |
		HeaderIncompatibleCompressedLZ4 |
		HeaderIncompatibleCompressedZSTD)
	if got := h.CompressionFlags(); got != wantC {
		t.Errorf("CompressionFlags() = 0x%x, want 0x%x", got, wantC)
	}
}

// makeValidHeaderBytes builds a minimal but well-formed journal header used
// as the baseline for failure-mode mutation tests. Uses the systemd 187
// layout (header_size = 224) to keep buffers small.
func makeValidHeaderBytes() []byte {
	buf := make([]byte, MinHeaderSize)
	copy(buf[0:8], Signature[:])
	le := binary.LittleEndian
	le.PutUint32(buf[8:12], 0)  // CompatibleFlags
	le.PutUint32(buf[12:16], 0) // IncompatibleFlags
	buf[16] = HeaderStateOnline
	for i := range 16 {
		buf[24+i] = byte(0xA0 + i) // FileID
		buf[40+i] = byte(0xB0 + i) // MachineID
		buf[56+i] = byte(0xC0 + i) // BootID
		buf[72+i] = byte(0xD0 + i) // SeqnumID
	}
	le.PutUint64(buf[88:96], MinHeaderSize) // HeaderSize
	le.PutUint64(buf[96:104], 4096)         // ArenaSize
	le.PutUint64(buf[104:112], 256)         // DataHashTableOffset
	le.PutUint64(buf[112:120], 1024)        // DataHashTableSize
	le.PutUint64(buf[120:128], 1280)        // FieldHashTableOffset
	le.PutUint64(buf[128:136], 512)         // FieldHashTableSize
	le.PutUint64(buf[136:144], 2048)        // TailObjectOffset
	le.PutUint64(buf[144:152], 42)          // NObjects
	le.PutUint64(buf[152:160], 7)           // NEntries
	le.PutUint64(buf[160:168], 700)         // TailEntrySeqnum
	le.PutUint64(buf[168:176], 600)         // HeadEntrySeqnum
	le.PutUint64(buf[176:184], 3072)        // EntryArrayOffset
	le.PutUint64(buf[184:192], 1700000000000000)
	le.PutUint64(buf[192:200], 1700000000999999)
	le.PutUint64(buf[200:208], 123456789)
	le.PutUint64(buf[208:216], 5) // NData
	le.PutUint64(buf[216:224], 3) // NFields
	return buf
}

// pickRealFixture returns the spec-mandated journal path if it exists,
// otherwise falls back to any readable user-21999815-* journal under the
// same machine-id directory. Returns "" to signal skip.
func pickRealFixture(t *testing.T) string {
	t.Helper()
	if isReadableFile(realFixturePath) {
		return realFixturePath
	}
	dir := filepath.Dir(realFixturePath)
	for _, glob := range []string{"user-21999815*.journal", "user-*.journal", "*.journal"} {
		matches, err := filepath.Glob(filepath.Join(dir, glob))
		if err != nil {
			continue
		}
		for _, p := range matches {
			if isReadableFile(p) {
				return p
			}
		}
	}
	return ""
}

func isReadableFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}
