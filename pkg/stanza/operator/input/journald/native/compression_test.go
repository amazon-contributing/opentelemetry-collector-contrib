// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package native

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/ulikunitz/xz"
)

// encodeLZ4Block produces the systemd-journal LZ4 wire format:
//
//	[ uint64 LE uncompressed_size ][ LZ4 block-compressed bytes ]
//
// Used by happy-path decompression tests; the real fixture-based tests
// arrive in task 17.
func encodeLZ4Block(t *testing.T, plaintext []byte) []byte {
	t.Helper()
	dst := make([]byte, lz4.CompressBlockBound(len(plaintext)))
	var c lz4.Compressor
	n, err := c.CompressBlock(plaintext, dst)
	if err != nil {
		t.Fatalf("lz4 compress: %v", err)
	}
	out := make([]byte, LZ4SizePrefixBytes+n)
	binary.LittleEndian.PutUint64(out[:LZ4SizePrefixBytes], uint64(len(plaintext)))
	copy(out[LZ4SizePrefixBytes:], dst[:n])
	return out
}

// encodeZSTD wraps plaintext in a single zstd frame.
func encodeZSTD(t *testing.T, plaintext []byte) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	defer enc.Close()
	return enc.EncodeAll(plaintext, nil)
}

// encodeXZ wraps plaintext in a single xz stream.
func encodeXZ(t *testing.T, plaintext []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatalf("xz writer: %v", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		t.Fatalf("xz write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("xz close: %v", err)
	}
	return buf.Bytes()
}

// TestDecompressPayload_Roundtrip verifies that DecompressPayload returns
// the original plaintext for each of the three supported algorithms when
// given a freshly-encoded compressed payload. Real journal fixtures land in
// compression_test.go in task 17; this test only proves the LZ4/ZSTD/XZ
// adapters are wired correctly to their decoders.
func TestDecompressPayload_Roundtrip(t *testing.T) {
	plaintext := []byte("MESSAGE=this is a journal log line that should round-trip cleanly")
	for _, tc := range []struct {
		name   string
		flag   uint8
		encode func(*testing.T, []byte) []byte
	}{
		{"lz4", ObjectCompressedLZ4, encodeLZ4Block},
		{"zstd", ObjectCompressedZSTD, encodeZSTD},
		{"xz", ObjectCompressedXZ, encodeXZ},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compressed := tc.encode(t, plaintext)
			out, err := DecompressPayload(compressed, tc.flag)
			if err != nil {
				t.Fatalf("DecompressPayload(%s): %v", tc.name, err)
			}
			if !bytes.Equal(out, plaintext) {
				t.Fatalf("round-trip mismatch:\n got = %q\nwant = %q", out, plaintext)
			}
		})
	}
}

// TestDecompressPayload_UnknownFlag rejects flag bytes outside the three
// known ObjectCompressed* bits, including 0 and combinations.
func TestDecompressPayload_UnknownFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		flag uint8
	}{
		{"zero", 0},
		{"high_bit", 0x80},
		{"two_bits", ObjectCompressedLZ4 | ObjectCompressedZSTD},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecompressPayload([]byte{0, 0, 0}, tc.flag)
			if !errors.Is(err, ErrCompressionUnknown) {
				t.Errorf("err = %v, want ErrCompressionUnknown", err)
			}
		})
	}
}

// TestDecompressPayload_LZ4PrefixMissing verifies the LZ4 path rejects a
// payload shorter than the 8-byte uncompressed-size prefix.
func TestDecompressPayload_LZ4PrefixMissing(t *testing.T) {
	for _, payload := range [][]byte{
		nil,
		{0x00},
		{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07}, // 7 bytes
	} {
		_, err := DecompressPayload(payload, ObjectCompressedLZ4)
		if !errors.Is(err, ErrLZ4PrefixMissing) {
			t.Errorf("payload=%d bytes: err = %v, want ErrLZ4PrefixMissing",
				len(payload), err)
		}
	}
}

// TestDecompressPayload_LZ4SizeUnreasonable: a prefix that declares a size
// beyond MaxDecompressedSize must be rejected before any allocation.
func TestDecompressPayload_LZ4SizeUnreasonable(t *testing.T) {
	payload := make([]byte, LZ4SizePrefixBytes+4)
	binary.LittleEndian.PutUint64(payload[:LZ4SizePrefixBytes], MaxDecompressedSize+1)
	_, err := DecompressPayload(payload, ObjectCompressedLZ4)
	if !errors.Is(err, ErrLZ4SizeUnreasonable) {
		t.Errorf("err = %v, want ErrLZ4SizeUnreasonable", err)
	}
}

// TestDecompressPayload_LZ4SizeZero accepts a legal empty payload (declared
// size = 0) and returns an empty slice without invoking the decoder.
func TestDecompressPayload_LZ4SizeZero(t *testing.T) {
	payload := make([]byte, LZ4SizePrefixBytes)
	out, err := DecompressPayload(payload, ObjectCompressedLZ4)
	if err != nil {
		t.Fatalf("DecompressPayload empty: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("len(out) = %d, want 0", len(out))
	}
}

// TestDecompressPayload_LZ4SizeMismatch: when the prefix lies about the
// uncompressed length, the decoder reports the mismatch.
func TestDecompressPayload_LZ4SizeMismatch(t *testing.T) {
	plaintext := []byte("FIELD=value")
	good := encodeLZ4Block(t, plaintext)
	// Inflate the declared size beyond the actual decompressed length.
	binary.LittleEndian.PutUint64(good[:LZ4SizePrefixBytes], uint64(len(plaintext)+10))
	_, err := DecompressPayload(good, ObjectCompressedLZ4)
	if !errors.Is(err, ErrLZ4SizeMismatch) && !strings.Contains(errOrEmpty(err), "lz4 uncompress block") {
		// Either the size guard fires or the underlying lz4 call
		// rejects the truncated stream. Both are acceptable; only a
		// silent success is wrong.
		t.Errorf("err = %v, want ErrLZ4SizeMismatch or lz4 uncompress error", err)
	}
}

// TestDecompressPayload_LZ4Garbage: random bytes that pass the prefix
// check must surface a non-nil error (lz4 uncompress fails or the size
// check fails).
func TestDecompressPayload_LZ4Garbage(t *testing.T) {
	payload := make([]byte, LZ4SizePrefixBytes+16)
	binary.LittleEndian.PutUint64(payload[:LZ4SizePrefixBytes], 4)
	for i := range payload[LZ4SizePrefixBytes:] {
		payload[LZ4SizePrefixBytes+i] = byte(i + 0xa0)
	}
	if _, err := DecompressPayload(payload, ObjectCompressedLZ4); err == nil {
		t.Errorf("err = nil, want non-nil error on garbage lz4 payload")
	}
}

// TestDecompressPayload_ZSTDGarbage rejects bytes that are not a zstd
// frame.
func TestDecompressPayload_ZSTDGarbage(t *testing.T) {
	payload := []byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x01, 0x02, 0x03}
	if _, err := DecompressPayload(payload, ObjectCompressedZSTD); err == nil {
		t.Errorf("err = nil, want non-nil zstd decode error")
	}
}

// TestDecompressPayload_XZGarbage rejects bytes that are not an xz frame.
func TestDecompressPayload_XZGarbage(t *testing.T) {
	payload := []byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x01, 0x02, 0x03}
	if _, err := DecompressPayload(payload, ObjectCompressedXZ); err == nil {
		t.Errorf("err = nil, want non-nil xz decode error")
	}
}

// TestDecompressPayload_ZSTDOverLimit proves the ZSTD DoS guard fires
// DURING decode rather than after fully materializing the output. We
// build a single zstd frame whose decompressed size exceeds
// MaxDecompressedSize (highly compressible zero bytes keep the compressed
// input tiny) and assert decompressZSTD rejects it with our
// ErrDecompressedTooLarge sentinel. Before the WithDecoderMaxMemory fix
// this input would allocate the full oversized buffer first.
func TestDecompressPayload_ZSTDOverLimit(t *testing.T) {
	// MaxDecompressedSize + 1 byte of zeros compresses to a few bytes but
	// would decode to just over the cap.
	plaintext := make([]byte, MaxDecompressedSize+1)
	compressed := encodeZSTD(t, plaintext)

	_, err := DecompressPayload(compressed, ObjectCompressedZSTD)
	if err == nil {
		t.Fatalf("err = nil, want ErrDecompressedTooLarge for an over-cap zstd frame")
	}
	if !errors.Is(err, ErrDecompressedTooLarge) {
		t.Fatalf("err = %v, want errors.Is(err, ErrDecompressedTooLarge)", err)
	}
}

// TestDecompressPayload_ZSTDAtLimit confirms the guard does not reject a
// legitimate payload that decodes to exactly MaxDecompressedSize — the
// boundary value must still round-trip.
func TestDecompressPayload_ZSTDAtLimit(t *testing.T) {
	plaintext := make([]byte, MaxDecompressedSize)
	for i := range plaintext {
		plaintext[i] = byte(i) // defeat trivial RLE so the frame is realistic
	}
	compressed := encodeZSTD(t, plaintext)

	out, err := DecompressPayload(compressed, ObjectCompressedZSTD)
	if err != nil {
		t.Fatalf("DecompressPayload at exactly MaxDecompressedSize: %v", err)
	}
	if uint64(len(out)) != MaxDecompressedSize {
		t.Fatalf("len(out) = %d, want %d", len(out), MaxDecompressedSize)
	}
}

func errOrEmpty(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ----------------------------------------------------------------------------
// Fixture-based tests (task 17)
// ----------------------------------------------------------------------------
//
// The tests below open the committed binary fixtures under
// receiver/journaldreceiver/testdata/native/ — each is a minimal valid
// journal containing exactly one DATA object (with its FIELD=value payload
// compressed using the named algorithm) and one ENTRY object whose single
// item points at that DATA. They exercise the full Open + ReadEntry +
// ReadDataField path, which transparently invokes DecompressPayload via
// ObjectHeader.IsCompressed / CompressionFlag.
//
// If a fixture file is absent (e.g. checked-out tree without the binary
// blob), the test calls t.Skipf rather than failing — the in-memory
// round-trip tests above still cover the decoder behavior.

// compressionFixtureRel is the relative path from the package's test
// working directory (which `go test` sets to the package directory) to
// the testdata/native/ directory. Each fixture lives directly inside it.
const compressionFixtureRel = "../../../../../../receiver/journaldreceiver/testdata/native"

// fixturePlaintext mirrors the constant the generator at
// receiver/journaldreceiver/testdata/native/generate/gen_compressed_journal.go
// uses as the DATA payload before compression. If you change one, change
// both — the test asserts byte-equality of the decompressed payload.
const fixturePlaintext = "MESSAGE=hello journald compression fixture"

// resolveCompressionFixture returns the absolute path to the requested
// fixture. The fixtures are committed canon and have a deterministic
// generator at receiver/journaldreceiver/testdata/native/generate/
// gen_compressed_journal.go, so an absent file is a hard failure rather
// than a skip — silently skipping would mask a regression where the
// binary blobs got accidentally deleted.
func resolveCompressionFixture(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join(compressionFixtureRel, name))
	if err != nil {
		t.Fatalf("filepath.Abs(%q): %v", name, err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("compression fixture %s missing at %s: %v\n"+
			"regenerate via: cd receiver/journaldreceiver/testdata/native && "+
			"go run generate/gen_compressed_journal.go <lz4|zstd|xz> %s",
			name, abs, err, name)
	}
	return abs
}

// TestCompressionFixtures opens each of the three compressed binary
// fixtures and asserts that the FIELD=value pair recovered through
// Open + ReadEntry + ReadDataField equals fixturePlaintext exactly.
//
// Each subtest is independent — a missing fixture skips just that
// algorithm's row rather than aborting the whole test. The IncompatibleFlags
// header bit is also verified so that a fixture regenerated with the
// wrong flag mask trips a clear failure here rather than a confusing
// downstream parse error.
func TestCompressionFixtures(t *testing.T) {
	cases := []struct {
		name           string
		filename       string
		objectFlag     uint8
		incompatibleFl uint32
	}{
		{
			name:           "lz4",
			filename:       "lz4.journal",
			objectFlag:     ObjectCompressedLZ4,
			incompatibleFl: HeaderIncompatibleCompressedLZ4,
		},
		{
			name:           "zstd",
			filename:       "zstd.journal",
			objectFlag:     ObjectCompressedZSTD,
			incompatibleFl: HeaderIncompatibleCompressedZSTD,
		},
		{
			name:           "xz",
			filename:       "xz.journal",
			objectFlag:     ObjectCompressedXZ,
			incompatibleFl: HeaderIncompatibleCompressedXZ,
		},
	}

	wantField := "MESSAGE"
	wantValue := strings.TrimPrefix(fixturePlaintext, wantField+"=")

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			path := resolveCompressionFixture(t, tc.filename)

			r, err := Open(path)
			if err != nil {
				t.Fatalf("Open(%s): %v", path, err)
			}
			defer func() {
				if cerr := r.Close(); cerr != nil {
					t.Errorf("Close: %v", cerr)
				}
			}()

			// The header MUST advertise the matching IncompatibleFlags
			// bit; otherwise a future spec-strict reader would refuse
			// the file even though the DATA object is decodable.
			gotFlags := r.Header().IncompatibleFlags
			if gotFlags&tc.incompatibleFl == 0 {
				t.Fatalf("fixture %s missing IncompatibleFlag 0x%x in 0x%x",
					tc.filename, tc.incompatibleFl, gotFlags)
			}

			entry, err := r.ReadEntry()
			if err != nil {
				t.Fatalf("ReadEntry: %v", err)
			}
			if len(entry.Items) != 1 {
				t.Fatalf("entry items = %d, want 1", len(entry.Items))
			}

			// Confirm the parent object's compression flag survived
			// the on-disk round-trip. ParseObjectHeader is reused so
			// this also re-validates ObjectHeader.CompressionFlag().
			oh, err := ParseObjectHeader(openReadAt(t, path), entry.Items[0].ObjectOffset)
			if err != nil {
				t.Fatalf("ParseObjectHeader(data): %v", err)
			}
			if !oh.IsCompressed() {
				t.Fatalf("DATA object missing compression flag bits: 0x%x", oh.Flags)
			}
			if got := oh.CompressionFlag(); got != tc.objectFlag {
				t.Fatalf("CompressionFlag = 0x%x, want 0x%x", got, tc.objectFlag)
			}

			field, value, err := ReadDataField(openReadAt(t, path),
				entry.Items[0].ObjectOffset, r.Compact())
			if err != nil {
				t.Fatalf("ReadDataField: %v", err)
			}
			if field != wantField || value != wantValue {
				t.Fatalf("decompressed mismatch:\n got = %q=%q\nwant = %q=%q",
					field, value, wantField, wantValue)
			}

			// One entry in the fixture; the next ReadEntry must
			// return io.EOF so this stays a tight golden test.
			if _, err := r.ReadEntry(); !errors.Is(err, io.EOF) {
				t.Fatalf("second ReadEntry = %v, want io.EOF", err)
			}
		})
	}
}

// openReadAt returns an *os.File opened read-only against path. The caller
// uses it as an io.ReaderAt for ParseObjectHeader / ReadDataField — Go's
// runtime will close the underlying fd on test completion via the
// t.Cleanup hook.
func openReadAt(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("os.Open(%s): %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}
