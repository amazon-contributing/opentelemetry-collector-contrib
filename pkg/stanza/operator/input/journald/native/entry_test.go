// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package native

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

// makeEntryObjectBytes builds a complete ENTRY object — 16-byte common
// header followed by the 48-byte fixed prefix and an items[] array — sized
// for either compact (4-byte items) or non-compact (16-byte items) layout.
//
// The returned slice is exactly the object's on-disk size. Callers that
// need to embed it inside a wider buffer must pad themselves.
func makeEntryObjectBytes(
	seqnum, realtime, monotonic, xorHash uint64,
	bootID [16]byte,
	items []EntryItem,
	compact bool,
) []byte {
	itemSize := EntryItemSize
	if compact {
		itemSize = EntryItemSizeCompact
	}
	totalSize := ObjectHeaderSize + EntryFixedSize + uint64(len(items))*itemSize
	buf := make([]byte, totalSize)

	// Common object header.
	buf[0] = byte(ObjectEntry)
	// buf[1] flags = 0; buf[2:8] reserved = 0
	binary.LittleEndian.PutUint64(buf[8:16], totalSize)

	// Fixed prefix (offsets relative to start of object).
	le := binary.LittleEndian
	le.PutUint64(buf[16:24], seqnum)
	le.PutUint64(buf[24:32], realtime)
	le.PutUint64(buf[32:40], monotonic)
	copy(buf[40:56], bootID[:])
	le.PutUint64(buf[56:64], xorHash)

	// Items[] starts at offset 64 (= ObjectHeaderSize + EntryFixedSize).
	pos := uint64(ObjectHeaderSize + EntryFixedSize)
	for _, item := range items {
		if compact {
			le.PutUint32(buf[pos:pos+4], uint32(item.ObjectOffset))
			pos += 4
		} else {
			le.PutUint64(buf[pos:pos+8], item.ObjectOffset)
			le.PutUint64(buf[pos+8:pos+16], item.Hash)
			pos += 16
		}
	}
	return buf
}

// makeDataObjectBytes builds a complete DATA object containing the given
// FIELD=value payload. Header flags are zero (no compression). The body
// layout differs by mode: non-compact has 48 bytes of internal fields
// before payload; compact has 56 bytes. Internal fields are zero-filled
// because ReadDataField does not consult them.
func makeDataObjectBytes(field, value string, compact bool) []byte {
	payload := []byte(field + "=" + value)
	preamble := DataPayloadOffset
	if compact {
		preamble = DataCompactPayloadOffset
	}
	totalSize := preamble + uint64(len(payload))
	buf := make([]byte, totalSize)
	buf[0] = byte(ObjectData)
	binary.LittleEndian.PutUint64(buf[8:16], totalSize)
	copy(buf[preamble:], payload)
	return buf
}

// padCanonicalBootID returns a deterministic 16-byte boot id used across
// the synthetic entry tests so output diffs are easy to read.
func padCanonicalBootID() [16]byte {
	var id [16]byte
	for i := range id {
		id[i] = byte(0xC0 + i)
	}
	return id
}

// placeAt copies src into a new len-`size` buffer at offset `at`. Used to
// stitch synthetic ENTRY+DATA objects into a single ReaderAt where each
// object lives at its declared offset.
func placeAt(size, at uint64, src []byte) []byte {
	out := make([]byte, size)
	copy(out[at:], src)
	return out
}

// stitch builds a single buffer from a slice of {offset, payload} pairs.
// The resulting buffer is sized to fit the highest end-offset.
func stitch(parts []struct {
	off uint64
	buf []byte
}) []byte {
	var size uint64
	for _, p := range parts {
		end := p.off + uint64(len(p.buf))
		if end > size {
			size = end
		}
	}
	out := make([]byte, size)
	for _, p := range parts {
		copy(out[p.off:], p.buf)
	}
	return out
}

// TestParseEntry_NonCompactSynthetic is the deterministic happy path for
// the legacy 16-byte-per-item layout. It verifies all fixed-prefix fields
// decode correctly and that two items survive the parser intact.
func TestParseEntry_NonCompactSynthetic(t *testing.T) {
	items := []EntryItem{
		{ObjectOffset: 4096, Hash: 0xDEADBEEFDEADBEEF},
		{ObjectOffset: 8192, Hash: 0xCAFEBABECAFEBABE},
	}
	bootID := padCanonicalBootID()
	objBytes := makeEntryObjectBytes(
		/*seqnum*/ 600,
		/*realtime*/ 1_700_000_000_000_000,
		/*monotonic*/ 5_555_555,
		/*xorHash*/ 0xABCDEF0123456789,
		bootID, items /*compact*/, false,
	)

	e, err := ParseEntry(bytes.NewReader(objBytes), 0, false)
	if err != nil {
		t.Fatalf("ParseEntry: %v", err)
	}
	if e.SeqNum != 600 {
		t.Errorf("SeqNum = %d, want 600", e.SeqNum)
	}
	if e.Realtime != 1_700_000_000_000_000 {
		t.Errorf("Realtime = %d, want 1700000000000000", e.Realtime)
	}
	if e.Monotonic != 5_555_555 {
		t.Errorf("Monotonic = %d, want 5555555", e.Monotonic)
	}
	if e.XorHash != 0xABCDEF0123456789 {
		t.Errorf("XorHash = 0x%x, want 0xABCDEF0123456789", e.XorHash)
	}
	if e.BootID != bootID {
		t.Errorf("BootID = %x, want %x", e.BootID, bootID)
	}
	if e.Compact {
		t.Errorf("Compact = true, want false")
	}
	if e.Offset != 0 {
		t.Errorf("Offset = %d, want 0", e.Offset)
	}
	if len(e.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2", len(e.Items))
	}
	if e.Items[0].ObjectOffset != 4096 || e.Items[0].Hash != 0xDEADBEEFDEADBEEF {
		t.Errorf("Items[0] = %+v, want {4096, 0xDEADBEEFDEADBEEF}", e.Items[0])
	}
	if e.Items[1].ObjectOffset != 8192 || e.Items[1].Hash != 0xCAFEBABECAFEBABE {
		t.Errorf("Items[1] = %+v, want {8192, 0xCAFEBABECAFEBABE}", e.Items[1])
	}
}

// TestParseEntry_CompactSynthetic is the deterministic happy path for the
// systemd-252+ 4-byte-per-item layout (HEADER_INCOMPATIBLE_COMPACT). The
// per-item Hash field is omitted on disk; ParseEntry must leave it zero.
func TestParseEntry_CompactSynthetic(t *testing.T) {
	items := []EntryItem{
		{ObjectOffset: 1024},
		{ObjectOffset: 2048},
		{ObjectOffset: 3072},
	}
	bootID := padCanonicalBootID()
	objBytes := makeEntryObjectBytes(
		/*seqnum*/ 42,
		/*realtime*/ 1_700_000_000_111_111,
		/*monotonic*/ 1_111_111,
		/*xorHash*/ 0,
		bootID, items /*compact*/, true,
	)

	e, err := ParseEntry(bytes.NewReader(objBytes), 0, true)
	if err != nil {
		t.Fatalf("ParseEntry: %v", err)
	}
	if !e.Compact {
		t.Errorf("Compact = false, want true")
	}
	if len(e.Items) != 3 {
		t.Fatalf("len(Items) = %d, want 3", len(e.Items))
	}
	for i, want := range []uint64{1024, 2048, 3072} {
		if e.Items[i].ObjectOffset != want {
			t.Errorf("Items[%d].ObjectOffset = %d, want %d",
				i, e.Items[i].ObjectOffset, want)
		}
		if e.Items[i].Hash != 0 {
			t.Errorf("Items[%d].Hash = 0x%x, want 0 (compact omits hash)",
				i, e.Items[i].Hash)
		}
	}
}

// TestParseEntry_AtNonZeroOffset confirms the parser honours the supplied
// offset and the resulting Entry.Offset captures it.
func TestParseEntry_AtNonZeroOffset(t *testing.T) {
	const startOff uint64 = 256
	bootID := padCanonicalBootID()
	objBytes := makeEntryObjectBytes(
		7, 1_700_000_000_222_222, 222, 0, bootID,
		[]EntryItem{{ObjectOffset: 9999, Hash: 0x1}}, false,
	)
	buf := placeAt(startOff+uint64(len(objBytes)), startOff, objBytes)
	// Sentinel bytes before the object so a misread of offset 0 is loud.
	for i := uint64(0); i < startOff; i++ {
		buf[i] = 0xEE
	}

	e, err := ParseEntry(bytes.NewReader(buf), startOff, false)
	if err != nil {
		t.Fatalf("ParseEntry@%d: %v", startOff, err)
	}
	if e.Offset != startOff {
		t.Errorf("Offset = %d, want %d", e.Offset, startOff)
	}
	if e.SeqNum != 7 {
		t.Errorf("SeqNum = %d, want 7", e.SeqNum)
	}
	if len(e.Items) != 1 || e.Items[0].ObjectOffset != 9999 {
		t.Errorf("Items = %+v, want one item at offset 9999", e.Items)
	}
}

// TestParseEntry_DeletedItemsSkipped: systemd uses object_offset==0 as a
// tombstone for deleted item slots. ParseEntry must drop those entries so
// callers never chase a NULL pointer into the journal header region.
func TestParseEntry_DeletedItemsSkipped(t *testing.T) {
	bootID := padCanonicalBootID()
	items := []EntryItem{
		{ObjectOffset: 100, Hash: 1},
		{ObjectOffset: 0, Hash: 0}, // deleted slot
		{ObjectOffset: 200, Hash: 2},
		{ObjectOffset: 0, Hash: 0}, // deleted slot
	}
	objBytes := makeEntryObjectBytes(1, 0, 0, 0, bootID, items, false)
	e, err := ParseEntry(bytes.NewReader(objBytes), 0, false)
	if err != nil {
		t.Fatalf("ParseEntry: %v", err)
	}
	if len(e.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2 (deleted slots pruned)", len(e.Items))
	}
	if e.Items[0].ObjectOffset != 100 || e.Items[1].ObjectOffset != 200 {
		t.Errorf("Items = %+v, want offsets 100 and 200", e.Items)
	}
}

// TestParseEntry_WrongType: passing the offset of a non-ENTRY object must
// surface ErrEntryWrongType so callers branching on errors.Is can fall
// through to a different decoder.
func TestParseEntry_WrongType(t *testing.T) {
	dataObj := makeDataObjectBytes("MESSAGE", "hello", false)
	_, err := ParseEntry(bytes.NewReader(dataObj), 0, false)
	if !errors.Is(err, ErrEntryWrongType) {
		t.Errorf("err = %v, want ErrEntryWrongType", err)
	}
}

// TestParseEntry_TooSmall: an object whose declared Size is below the
// 16+48 fixed-prefix minimum must be rejected up-front rather than read
// off the end of the slice.
func TestParseEntry_TooSmall(t *testing.T) {
	// Just the 16-byte common header + 1 byte of body (well under the
	// 48-byte fixed prefix).
	buf := make([]byte, ObjectHeaderSize+1)
	buf[0] = byte(ObjectEntry)
	binary.LittleEndian.PutUint64(buf[8:16], ObjectHeaderSize+1)

	_, err := ParseEntry(bytes.NewReader(buf), 0, false)
	if !errors.Is(err, ErrEntryTooSmall) {
		t.Errorf("err = %v, want ErrEntryTooSmall", err)
	}
}

// TestParseEntry_HeaderReadError confirms a ReaderAt failure during the
// initial ParseObjectHeader call is wrapped, not silently translated to
// EOF. Uses errReaderAt declared in object_test.go (same package).
func TestParseEntry_HeaderReadError(t *testing.T) {
	sentinel := errors.New("synthetic disk fault")
	_, err := ParseEntry(errReaderAt{err: sentinel}, 0, false)
	if err == nil {
		t.Fatal("err = nil, want wrapped sentinel")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want errors.Is(sentinel)", err)
	}
}

// TestParseEntry_ShortBody: ObjectHeader claims a larger body than the
// underlying ReaderAt actually has. The body read should fail rather than
// return a partial Entry. Uses bytes.NewReader on a truncated buffer.
func TestParseEntry_ShortBody(t *testing.T) {
	bootID := padCanonicalBootID()
	full := makeEntryObjectBytes(1, 0, 0, 0, bootID,
		[]EntryItem{{ObjectOffset: 64, Hash: 0}}, false)
	// Lie: keep declared Size at full length but truncate the buffer to
	// just the header. The body ReadAt must error.
	truncated := full[:ObjectHeaderSize]
	_, err := ParseEntry(bytes.NewReader(truncated), 0, false)
	if err == nil {
		t.Fatal("err = nil, want body read error")
	}
}

// TestEntryParser_M1 is the regression test for the spike's M1 bug:
// it iterated entry items unconditionally with a 16-byte stride, which
// silently mis-parses HEADER_INCOMPATIBLE_COMPACT journals (systemd 252+,
// default on AL2023) where each item is 4 bytes.
//
// The test uses a count chosen so the buggy stride would necessarily fail:
// 3 compact items occupy 12 bytes, which is not a multiple of 16 — the old
// loop would either drop trailing bytes or read garbage past the body. The
// fix branches on the compact flag and reads at the correct stride.
func TestEntryParser_M1(t *testing.T) {
	// 3 items × 4 bytes = 12 bytes of items (not a multiple of 16).
	items := []EntryItem{
		{ObjectOffset: 0x1111},
		{ObjectOffset: 0x2222},
		{ObjectOffset: 0x3333},
	}
	bootID := padCanonicalBootID()
	objBytes := makeEntryObjectBytes(
		7, 1_700_000_000_000_000, 0, 0, bootID, items, true)

	// Correct path: compact=true must yield exactly 3 items at the
	// declared offsets.
	e, err := ParseEntry(bytes.NewReader(objBytes), 0, true)
	if err != nil {
		t.Fatalf("compact=true: ParseEntry: %v", err)
	}
	if len(e.Items) != 3 {
		t.Fatalf("compact=true: len(Items) = %d, want 3", len(e.Items))
	}
	for i, want := range []uint64{0x1111, 0x2222, 0x3333} {
		if e.Items[i].ObjectOffset != want {
			t.Errorf("compact=true: Items[%d].ObjectOffset = 0x%x, want 0x%x",
				i, e.Items[i].ObjectOffset, want)
		}
	}

	// Buggy path simulation: parsing the same compact bytes with
	// compact=false would treat 12 bytes of items as a 16-byte stride,
	// which fails the M2 divisibility check (12 % 16 != 0). Asserting
	// this guards against the spike's behaviour ever sneaking back in:
	// any caller that forgets to pass compact=true on a real AL2023
	// journal will get a loud error, not silent data corruption.
	_, err = ParseEntry(bytes.NewReader(objBytes), 0, false)
	if !errors.Is(err, ErrEntryItemsMisaligned) {
		t.Errorf("compact=false on compact bytes: err = %v, want ErrEntryItemsMisaligned",
			err)
	}
}

// TestEntryParser_M2 is the regression test for the spike's M2 bug:
// the loop bound `pos+itemSize <= bodySize` silently dropped trailing
// bytes when (bodySize-48) was not a multiple of itemSize. systemd never
// emits a partial item, so a non-zero remainder signals corruption and
// must be reported, not swallowed.
//
// Strategy: hand-craft an ENTRY object whose declared Size yields a body
// that does not divide evenly by the non-compact 16-byte item stride,
// then assert ParseEntry returns ErrEntryItemsMisaligned.
func TestEntryParser_M2(t *testing.T) {
	// Object size = 16 (header) + 48 (fixed) + 24 (items_bytes).
	// items_bytes = 24, 24 % 16 = 8 → misaligned.
	const totalSize uint64 = ObjectHeaderSize + EntryFixedSize + 24
	buf := make([]byte, totalSize)
	buf[0] = byte(ObjectEntry)
	binary.LittleEndian.PutUint64(buf[8:16], totalSize)
	// Fixed prefix can be all zeros; the parser does not validate them.
	// items[] region (last 24 bytes) is zero too — content is irrelevant
	// because the alignment check fires before any item read.

	_, err := ParseEntry(bytes.NewReader(buf), 0, false)
	if !errors.Is(err, ErrEntryItemsMisaligned) {
		t.Fatalf("err = %v, want ErrEntryItemsMisaligned", err)
	}

	// Symmetric check for compact mode: 17 items_bytes @ 4-byte stride
	// (17 % 4 = 1) must also trip the alignment guard.
	const totalSizeCompact uint64 = ObjectHeaderSize + EntryFixedSize + 17
	buf2 := make([]byte, totalSizeCompact)
	buf2[0] = byte(ObjectEntry)
	binary.LittleEndian.PutUint64(buf2[8:16], totalSizeCompact)
	_, err = ParseEntry(bytes.NewReader(buf2), 0, true)
	if !errors.Is(err, ErrEntryItemsMisaligned) {
		t.Errorf("compact misalignment: err = %v, want ErrEntryItemsMisaligned",
			err)
	}
}

// TestEntryParser_M3 is the regression test for the spike's M3 bug:
// `time.Unix(int64(usec/1_000_000), int64((usec%1_000_000)*1000))` cast
// `usec/1_000_000` from uint64 → int64 unconditionally. The documented
// concern was that for very large usec the cast wraps and yields a
// negative time.Time pointing at a pre-1970 instant — silently breaking
// downstream timestamp comparisons.
//
// The actual safety property the fix MUST provide for any uint64 input
// (including the math.MaxUint64 sentinel systemd uses for USEC_INFINITY):
//
//  1. usecToTime never returns a negative / pre-1970 time.Time. The
//     buggy cast pattern would silently flip far-future values into the
//     past; the fix preserves the invariant that t.Unix() >= 0 for any
//     non-zero input.
//  2. usec=0 maps to the zero time.Time with no error (the systemd
//     "no value" convention).
//  3. Normal realtimes round-trip through time.Unix without loss.
//  4. The returned time.Time is in UTC, matching the helper's contract.
//
// The current implementation enforces these properties via an explicit
// uint64-to-int64 bounds check before the cast. This test pins the
// behaviour so any regression that re-introduces the unsafe direct cast
// (or a far-future wrap) is caught.
func TestEntryParser_M3(t *testing.T) {
	// (2) usec=0 → zero time, no error.
	if got, err := usecToTime(0); err != nil || !got.IsZero() {
		t.Errorf("usecToTime(0) = (%v, %v), want (zero time, nil)", got, err)
	}

	// (3) Normal value round-trips through time.Unix.
	const realUsec = uint64(1_700_000_000_000_000)
	got, err := usecToTime(realUsec)
	if err != nil {
		t.Fatalf("usecToTime(%d): %v", realUsec, err)
	}
	wantSec := int64(realUsec / 1_000_000)
	if got.Unix() != wantSec {
		t.Errorf("usecToTime(%d).Unix() = %d, want %d",
			realUsec, got.Unix(), wantSec)
	}
	// (4) UTC location.
	if got.Location() != time.UTC {
		t.Errorf("usecToTime location = %v, want UTC", got.Location())
	}

	// (1) The core invariant: no uint64 input may produce a pre-1970 /
	// negative time.Time. The spike's `int64(usec/1_000_000)` does NOT
	// in fact wrap for any uint64 input (MaxUint64/1e6 ≈ 1.84e13, well
	// below MaxInt64 ≈ 9.22e18), but a future refactor that drops the
	// bounds check or changes the integer arithmetic could regress
	// here. Pin the invariant across the full uint64 range.
	for _, name := range []string{"max", "near_max", "large_realistic"} {
		var usec uint64
		switch name {
		case "max":
			usec = math.MaxUint64
		case "near_max":
			usec = math.MaxUint64 - 1_000_000
		case "large_realistic":
			usec = 9_999_999_999_999_999 // year ~2286
		}
		t.Run("no_pre_1970_wrap_"+name, func(t *testing.T) {
			tm, err := usecToTime(usec)
			if err != nil {
				// An explicit overflow error is also an acceptable
				// way to honour the M3 invariant — it just must not
				// silently return a pre-1970 time.
				return
			}
			if tm.Unix() < 0 {
				t.Errorf("usecToTime(%d) = %s (Unix=%d), want non-negative",
					usec, tm, tm.Unix())
			}
			if tm.Year() < 1970 {
				t.Errorf("usecToTime(%d).Year() = %d, want >= 1970",
					usec, tm.Year())
			}
		})
	}

	// Wired through Entry.RealtimeAsTime: round-trip on a realistic
	// timestamp matches the direct helper's output.
	e := &Entry{Realtime: realUsec}
	tm, err := e.RealtimeAsTime()
	if err != nil {
		t.Fatalf("Entry.RealtimeAsTime: %v", err)
	}
	if tm.Unix() != wantSec {
		t.Errorf("Entry.RealtimeAsTime.Unix() = %d, want %d", tm.Unix(), wantSec)
	}
	// And: even the MaxUint64 sentinel routed through the Entry method
	// must obey the no-pre-1970 invariant.
	e.Realtime = math.MaxUint64
	if tm, err := e.RealtimeAsTime(); err == nil && tm.Unix() < 0 {
		t.Errorf("Entry.RealtimeAsTime(MaxUint64) = %s, want non-negative or error",
			tm)
	}
}

// TestUsecToTime_NanosOverflowBound pins the corrected bound: the binding
// limit is the us*1000 nanosecond product passed to time.Unix, NOT the
// seconds component. A seconds-only guard (usec/1_000_000 > MaxInt64) never
// fires for any uint64 (max seconds ≈ 1.84e13 ≪ MaxInt64 ≈ 9.2e18), so
// USEC_INFINITY would have silently produced a year ~586,524 timestamp.
// The fix rejects any usec > MaxInt64/1000 and accepts everything up to it.
func TestUsecToTime_NanosOverflowBound(t *testing.T) {
	const maxUsec = uint64(math.MaxInt64) / 1_000 // year ~2262 boundary

	// USEC_INFINITY and any value past the boundary must error, NOT return
	// a far-future timestamp.
	for _, usec := range []uint64{math.MaxUint64, maxUsec + 1} {
		if _, err := usecToTime(usec); err == nil {
			t.Errorf("usecToTime(%d) = nil error, want overflow rejection", usec)
		}
	}

	// The exact boundary value must still convert (no off-by-one rejection
	// of a legal timestamp).
	if tm, err := usecToTime(maxUsec); err != nil {
		t.Errorf("usecToTime(%d) at boundary = %v, want success", maxUsec, err)
	} else if tm.Unix() < 0 {
		t.Errorf("usecToTime(%d) at boundary = %s, want non-negative", maxUsec, tm)
	}

	// A realistic 2026-era timestamp is well within range.
	const realUsec = uint64(1_780_000_000_000_000)
	if _, err := usecToTime(realUsec); err != nil {
		t.Errorf("usecToTime(%d) realistic = %v, want success", realUsec, err)
	}
}

// TestReadDataField_NonCompact is the happy path for the legacy DATA
// payload offset (16+48 = 64). The field/value split must follow the
// first '=' byte, with everything after preserved verbatim — including
// '=' characters inside the value.
func TestReadDataField_NonCompact(t *testing.T) {
	dataObj := makeDataObjectBytes(
		"MESSAGE", "hello=world=42 multi==equals", false)
	field, value, err := ReadDataField(bytes.NewReader(dataObj), 0, false)
	if err != nil {
		t.Fatalf("ReadDataField: %v", err)
	}
	if field != "MESSAGE" {
		t.Errorf("field = %q, want MESSAGE", field)
	}
	if value != "hello=world=42 multi==equals" {
		t.Errorf("value = %q", value)
	}
}

// TestReadDataField_Compact verifies the compact-mode payload offset
// (DataCompactPayloadOffset = 72) is honoured. A buggy parser reading at
// offset 64 would prepend 8 bytes of zero into the field name.
func TestReadDataField_Compact(t *testing.T) {
	dataObj := makeDataObjectBytes("PRIORITY", "6", true)
	field, value, err := ReadDataField(bytes.NewReader(dataObj), 0, true)
	if err != nil {
		t.Fatalf("ReadDataField: %v", err)
	}
	if field != "PRIORITY" {
		t.Errorf("field = %q, want PRIORITY", field)
	}
	if value != "6" {
		t.Errorf("value = %q, want 6", value)
	}
}

// TestReadDataField_CompressedRandomFails: pointing ReadDataField at a
// DATA object whose payload bytes are not a valid LZ4/ZSTD/XZ stream must
// surface a non-nil decompression error rather than a panic or a silently
// corrupted FIELD=value. The "happy path" round-trips for each algorithm
// live in compression_test.go (added in task 17) where real compressed
// fixtures are emitted; this test only asserts the error path.
//
// Each subtest constructs an uncompressed DATA object's bytes via
// makeDataObjectBytes ("MSG=x"), then flips the appropriate
// ObjectCompressed* bit on the object header so the decompressor runs.
// The 1-byte payload is too short to be a valid LZ4 prefix and is not a
// ZSTD or XZ frame, so each decoder must reject it.
func TestReadDataField_CompressedRandomFails(t *testing.T) {
	for _, tc := range []struct {
		name string
		flag uint8
	}{
		{"xz", ObjectCompressedXZ},
		{"lz4", ObjectCompressedLZ4},
		{"zstd", ObjectCompressedZSTD},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataObj := makeDataObjectBytes("MSG", "x", false)
			dataObj[1] = tc.flag
			_, _, err := ReadDataField(bytes.NewReader(dataObj), 0, false)
			if err == nil {
				t.Fatalf("err = nil, want non-nil decompression error")
			}
			// LZ4 is too short to even contain the 8-byte size
			// prefix, so we expect the prefix-missing sentinel
			// specifically. ZSTD/XZ surface decoder errors that
			// are not exported as sentinels — we only assert the
			// error is non-nil and is NOT the legacy "not yet
			// supported" string.
			if tc.flag == ObjectCompressedLZ4 && !errors.Is(err, ErrLZ4PrefixMissing) {
				t.Errorf("lz4 err = %v, want ErrLZ4PrefixMissing", err)
			}
			if got := err.Error(); strings.Contains(got, "decompression not yet supported") {
				t.Errorf("err mentions legacy 'not yet supported' wording: %v", err)
			}
		})
	}
}

// TestReadDataField_WrongType: pointing ReadDataField at an ENTRY object
// must surface ErrDataWrongType (symmetric to ParseEntry's wrong-type
// guard).
func TestReadDataField_WrongType(t *testing.T) {
	bootID := padCanonicalBootID()
	entryObj := makeEntryObjectBytes(1, 0, 0, 0, bootID,
		[]EntryItem{{ObjectOffset: 100, Hash: 0}}, false)
	_, _, err := ReadDataField(bytes.NewReader(entryObj), 0, false)
	if !errors.Is(err, ErrDataWrongType) {
		t.Errorf("err = %v, want ErrDataWrongType", err)
	}
}

// TestReadDataField_NoSeparator: a payload missing the '=' byte is
// malformed per the systemd format spec.
func TestReadDataField_NoSeparator(t *testing.T) {
	// Build a DATA object whose payload has no '=' separator.
	const payload = "no-equals-here"
	totalSize := DataPayloadOffset + uint64(len(payload))
	buf := make([]byte, totalSize)
	buf[0] = byte(ObjectData)
	binary.LittleEndian.PutUint64(buf[8:16], totalSize)
	copy(buf[DataPayloadOffset:], payload)

	_, _, err := ReadDataField(bytes.NewReader(buf), 0, false)
	if !errors.Is(err, ErrDataPayloadMalformed) {
		t.Errorf("err = %v, want ErrDataPayloadMalformed", err)
	}
}

// TestReadDataField_PayloadTooLarge: a DATA object that claims a payload
// over MaxDataPayloadSize must be rejected before the parser tries to
// allocate. systemd splits payloads larger than 64 KiB in real journals
// so this is a denial-of-service guard, not a functional limit.
func TestReadDataField_PayloadTooLarge(t *testing.T) {
	// Forge an object header that lies about size. We do not actually
	// allocate the giant buffer — the size guard fires before any
	// ReadAt of the payload.
	totalSize := DataPayloadOffset + MaxDataPayloadSize + 1
	hdr := make([]byte, ObjectHeaderSize)
	hdr[0] = byte(ObjectData)
	binary.LittleEndian.PutUint64(hdr[8:16], totalSize)
	// Provide enough bytes for ParseObjectHeader to succeed but no more;
	// the size guard runs before the body ReadAt.
	_, _, err := ReadDataField(bytes.NewReader(hdr), 0, false)
	if !errors.Is(err, ErrDataPayloadTooLarge) {
		t.Errorf("err = %v, want ErrDataPayloadTooLarge", err)
	}
}

// TestParseEntry_StitchedRoundTripWithReadDataField builds a small
// in-memory journal — one ENTRY plus two DATA objects at the offsets
// referenced by the entry — and walks ParseEntry → ReadDataField the
// same way a real reader will. Confirms the offset arithmetic between
// the two functions is consistent.
func TestParseEntry_StitchedRoundTripWithReadDataField(t *testing.T) {
	bootID := padCanonicalBootID()

	const dataAOff uint64 = 256
	const dataBOff uint64 = 512
	dataA := makeDataObjectBytes("MESSAGE", "hello", false)
	dataB := makeDataObjectBytes("PRIORITY", "6", false)
	entryObj := makeEntryObjectBytes(
		99, 1_700_000_000_333_333, 333, 0, bootID,
		[]EntryItem{
			{ObjectOffset: dataAOff, Hash: 1},
			{ObjectOffset: dataBOff, Hash: 2},
		},
		false,
	)

	full := stitch([]struct {
		off uint64
		buf []byte
	}{
		{0, entryObj},
		{dataAOff, dataA},
		{dataBOff, dataB},
	})

	r := bytes.NewReader(full)
	e, err := ParseEntry(r, 0, false)
	if err != nil {
		t.Fatalf("ParseEntry: %v", err)
	}
	if len(e.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2", len(e.Items))
	}

	got := map[string]string{}
	for _, item := range e.Items {
		field, value, err := ReadDataField(r, item.ObjectOffset, false)
		if err != nil {
			t.Fatalf("ReadDataField@%d: %v", item.ObjectOffset, err)
		}
		got[field] = value
	}
	if got["MESSAGE"] != "hello" || got["PRIORITY"] != "6" {
		t.Errorf("fields = %v, want MESSAGE=hello PRIORITY=6", got)
	}
}

// TestParseEntry_RealFixture is the spec-mandated happy path: open the
// real systemd user journal, walk objects until we find the first ENTRY,
// parse it, and assert plausible field values. Skipped cleanly when no
// fixture is available (CI, fresh dev hosts).
func TestParseEntry_RealFixture(t *testing.T) {
	path := pickRealFixture(t)
	if path == "" {
		t.Skipf("no readable .journal fixture under /var/log/journal; skipping")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("cannot open fixture %s: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	fileSize := uint64(fi.Size())

	h, err := ParseHeader(f)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	if h.NEntries == 0 {
		t.Skipf("fixture %s has zero entries; nothing to parse", path)
	}
	compact := h.IsCompact()

	// Linear walk: find the first ENTRY object.
	offset := h.HeaderSize
	var entryOff uint64
	var entryOH *ObjectHeader
	const scanLimit = 1024
	for i := 0; i < scanLimit && offset < fileSize; i++ {
		offset = (offset + ObjectAlignment - 1) &^ (ObjectAlignment - 1)
		oh, err := ParseObjectHeader(f, offset)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			t.Fatalf("ParseObjectHeader@%d: %v", offset, err)
		}
		if oh.Size < ObjectHeaderSize || oh.Size > fileSize-offset {
			break
		}
		if oh.Type == ObjectEntry {
			entryOff = offset
			entryOH = oh
			break
		}
		offset += oh.Size
	}
	if entryOH == nil {
		t.Skipf("no ENTRY object found in first %d objects of %s", scanLimit, path)
	}

	e, err := ParseEntry(f, entryOff, compact)
	if err != nil {
		t.Fatalf("ParseEntry@%d: %v", entryOff, err)
	}

	// SeqNum must be within the file's declared head/tail range.
	if e.SeqNum < h.HeadEntrySeqnum || e.SeqNum > h.TailEntrySeqnum {
		t.Errorf("SeqNum = %d, outside [head=%d, tail=%d]",
			e.SeqNum, h.HeadEntrySeqnum, h.TailEntrySeqnum)
	}
	// Realtime must convert without overflow and be a sane modern value.
	rt, err := e.RealtimeAsTime()
	if err != nil {
		t.Fatalf("RealtimeAsTime: %v", err)
	}
	if rt.Year() < 2020 || rt.Year() > 2100 {
		t.Errorf("Realtime = %s, want a 2020s-era timestamp", rt)
	}
	// Items[] must contain at least one DATA pointer.
	if len(e.Items) == 0 {
		t.Errorf("len(Items) = 0, want > 0")
	}
	if e.Compact != compact {
		t.Errorf("Compact = %v, want %v", e.Compact, compact)
	}

	// At least one item must resolve to a readable FIELD=value pair.
	// Compressed payloads now round-trip via DecompressPayload; on a
	// real systemd journal that means LZ4/ZSTD payloads also produce
	// FIELD=value strings. Items that fail to decode (legitimately
	// malformed entries, unsupported flag combinations) are logged but
	// not fatal — the test only requires at least one successful
	// resolution to confirm the read path is wired end-to-end.
	var resolved int
	var sample string
	for _, item := range e.Items {
		field, value, err := ReadDataField(f, item.ObjectOffset, compact)
		if err != nil {
			t.Logf("ReadDataField@%d: %v", item.ObjectOffset, err)
			continue
		}
		if field == "" || strings.ContainsRune(field, '\x00') {
			t.Errorf("ReadDataField@%d returned empty/NUL field %q",
				item.ObjectOffset, field)
		}
		resolved++
		if sample == "" {
			sample = fmt.Sprintf("%s=%s", field, value)
		}
	}
	if resolved == 0 {
		t.Errorf("zero items resolved to FIELD=value; want >= 1 (sample fixture has uncompressed data)")
	}
	t.Logf("entry@%d: seqnum=%d realtime=%s items=%d resolved=%d sample=%q",
		entryOff, e.SeqNum, rt.Format(time.RFC3339), len(e.Items), resolved, sample)
}
