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
	"testing"
)

// -----------------------------------------------------------------------
// In-memory fixture builders.
//
// These helpers synthesize valid systemd-journal byte sequences in memory
// so the EntryArray traversal can be exercised without depending on a
// running systemd. Every helper is local to the test file (lower-case
// names) and emits non-compact layout unless explicitly told otherwise —
// this matches the committed small.journal fixture and keeps the parity
// assertion symmetric across the linear and indexed strategies.
// -----------------------------------------------------------------------

// objectAlignmentMask is the bitmask used to round a buffer length up to
// the journal format's 8-byte alignment requirement.
const objectAlignmentMask = ObjectAlignment - 1

// padToAlign appends zero bytes to buf until len(buf) is a multiple of
// ObjectAlignment. systemd writes every object on an 8-byte boundary;
// our synthetic arenas must do the same so ParseObjectHeader does not
// reject downstream reads with ErrObjectMisaligned.
func padToAlign(buf []byte) []byte {
	n := uint64(len(buf))
	pad := (ObjectAlignment - (n & objectAlignmentMask)) & objectAlignmentMask
	for i := uint64(0); i < pad; i++ {
		buf = append(buf, 0)
	}
	return buf
}

// buildEntryBytes returns the bytes of a non-compact ENTRY object with
// the supplied seqnum / realtime / items. monotonic is derived from
// seqnum so each entry is distinguishable, and xor_hash is set to a
// deterministic mix of seqnum so parity tests catch cross-strategy
// drift on every field of the Entry struct.
func buildEntryBytes(seqnum, realtime uint64, items [][2]uint64) []byte {
	size := uint64(16) + EntryFixedSize + uint64(len(items))*EntryItemSize
	buf := make([]byte, size)
	le := binary.LittleEndian
	buf[0] = byte(ObjectEntry)
	le.PutUint64(buf[8:16], size)
	le.PutUint64(buf[16:24], seqnum)
	le.PutUint64(buf[24:32], realtime)
	le.PutUint64(buf[32:40], seqnum*1_000) // monotonic
	// boot_id at [40:56] left zero
	le.PutUint64(buf[56:64], 0xDEADBEEF^seqnum) // xor_hash
	pos := uint64(64)
	for _, it := range items {
		le.PutUint64(buf[pos:pos+8], it[0])
		le.PutUint64(buf[pos+8:pos+16], it[1])
		pos += 16
	}
	return buf
}

// buildEntryArrayBytes returns the bytes of an ENTRY_ARRAY object with
// the supplied next pointer and items, in either non-compact (le64) or
// compact (le32) mode.
func buildEntryArrayBytes(next uint64, items []uint64, compact bool) []byte {
	itemSz := EntryArrayItemSize
	if compact {
		itemSz = EntryArrayItemSizeCompact
	}
	size := uint64(16) + EntryArrayHeaderSize + uint64(len(items))*itemSz
	buf := make([]byte, size)
	le := binary.LittleEndian
	buf[0] = byte(ObjectEntryArray)
	le.PutUint64(buf[8:16], size)
	le.PutUint64(buf[16:24], next)
	pos := uint64(24)
	for _, off := range items {
		if compact {
			le.PutUint32(buf[pos:pos+4], uint32(off))
			pos += 4
		} else {
			le.PutUint64(buf[pos:pos+8], off)
			pos += 8
		}
	}
	return buf
}

// buildSyntheticHeaderBytes emits the 224-byte journal header for an
// in-memory fixture.  Layout matches the systemd 187 baseline (no n_tags
// / n_entry_arrays fields), which is the minimum ParseHeader accepts.
//
// The caller supplies arenaSize, nEntries, head/tail seqnum, and the
// entry_array_offset that Reader will follow when WithIndexedTraversal
// is enabled. Compatibility / incompatibility flags are zero — the
// resulting file is non-compact and uncompressed, identical to
// small.journal's flag set.
func buildSyntheticHeaderBytes(arenaSize, nEntries, headSeq, tailSeq, entryArrayOffset, headRT, tailRT uint64) []byte {
	buf := make([]byte, MinHeaderSize)
	le := binary.LittleEndian
	copy(buf[0:8], Signature[:])
	le.PutUint32(buf[8:12], 0)  // CompatibleFlags
	le.PutUint32(buf[12:16], 0) // IncompatibleFlags (non-compact)
	buf[16] = HeaderStateOnline
	// 128-bit identifiers at [24,40,56,72] left zero — the Reader does
	// not validate them.
	le.PutUint64(buf[88:96], MinHeaderSize)        // HeaderSize
	le.PutUint64(buf[96:104], arenaSize)           // ArenaSize
	le.PutUint64(buf[104:112], 0)                  // DataHashTableOffset
	le.PutUint64(buf[112:120], 0)                  // DataHashTableSize
	le.PutUint64(buf[120:128], 0)                  // FieldHashTableOffset
	le.PutUint64(buf[128:136], 0)                  // FieldHashTableSize
	// TailObjectOffset: offset of the last real object. These synthetic
	// arenas are tightly packed (no preallocated zero tail), so any value
	// at/after the final object works; using the arena end guarantees the
	// linear scan in ReadEntry covers every object. Must NOT be set to the
	// FIRST object (MinHeaderSize) — that makes the tail-object EOF guard
	// stop the scan after one object. See reader.go ReadEntry.
	le.PutUint64(buf[136:144], MinHeaderSize+arenaSize-1) // TailObjectOffset (last object)
	le.PutUint64(buf[144:152], nEntries+1)         // NObjects (entries + 1 EA)
	le.PutUint64(buf[152:160], nEntries)           // NEntries
	le.PutUint64(buf[160:168], tailSeq)            // TailEntrySeqnum
	le.PutUint64(buf[168:176], headSeq)            // HeadEntrySeqnum
	le.PutUint64(buf[176:184], entryArrayOffset)   // EntryArrayOffset
	le.PutUint64(buf[184:192], headRT)             // HeadEntryRealtime
	le.PutUint64(buf[192:200], tailRT)             // TailEntryRealtime
	le.PutUint64(buf[200:208], tailSeq*1_000)      // TailEntryMonotonic
	le.PutUint64(buf[208:216], 0)                  // NData
	le.PutUint64(buf[216:224], 0)                  // NFields
	return buf
}

// writeSyntheticJournal concatenates header + arena, writes the result
// to a temp file inside t.TempDir(), and returns the absolute path.
// Cleanup is delegated to t.TempDir's automatic teardown so callers do
// not need to defer os.Remove.
func writeSyntheticJournal(t *testing.T, hdr, arena []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "synthetic.journal")
	body := make([]byte, 0, len(hdr)+len(arena))
	body = append(body, hdr...)
	body = append(body, arena...)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write synthetic journal: %v", err)
	}
	return path
}

// -----------------------------------------------------------------------
// ParseEntryArray unit tests.
// -----------------------------------------------------------------------

// TestParseEntryArray_NonCompact verifies a single non-compact
// ENTRY_ARRAY object's NextEntryArrayOffset and Items[] are decoded
// from le64 fields exactly as written.
func TestParseEntryArray_NonCompact(t *testing.T) {
	want := []uint64{0x1000, 0x2000, 0x3000, 0x4000}
	buf := buildEntryArrayBytes(0x9999, want, false)
	r := bytes.NewReader(buf)

	ea, err := ParseEntryArray(r, 0, false)
	if err != nil {
		t.Fatalf("ParseEntryArray: %v", err)
	}
	if ea.NextEntryArrayOffset != 0x9999 {
		t.Errorf("NextEntryArrayOffset = 0x%x, want 0x9999", ea.NextEntryArrayOffset)
	}
	if ea.Offset != 0 {
		t.Errorf("Offset = %d, want 0", ea.Offset)
	}
	if len(ea.Items) != len(want) {
		t.Fatalf("len(Items) = %d, want %d", len(ea.Items), len(want))
	}
	for i, w := range want {
		if ea.Items[i] != w {
			t.Errorf("Items[%d] = 0x%x, want 0x%x", i, ea.Items[i], w)
		}
	}
}

// TestParseEntryArray_Compact verifies the compact-mode wire format
// reads each item as le32 and widens it to uint64. The
// NextEntryArrayOffset field is always le64 even in compact files.
func TestParseEntryArray_Compact(t *testing.T) {
	want := []uint64{0x100, 0x200, 0x300}
	buf := buildEntryArrayBytes(0x77777777, want, true)
	r := bytes.NewReader(buf)

	ea, err := ParseEntryArray(r, 0, true)
	if err != nil {
		t.Fatalf("ParseEntryArray: %v", err)
	}
	if ea.NextEntryArrayOffset != 0x77777777 {
		t.Errorf("NextEntryArrayOffset = 0x%x, want 0x77777777", ea.NextEntryArrayOffset)
	}
	if len(ea.Items) != len(want) {
		t.Fatalf("len(Items) = %d, want %d", len(ea.Items), len(want))
	}
	for i, w := range want {
		if ea.Items[i] != w {
			t.Errorf("Items[%d] = 0x%x, want 0x%x", i, ea.Items[i], w)
		}
	}
}

// TestParseEntryArray_EmptyArrayValid confirms that an EntryArray with
// zero items (only the next_entry_array_offset field) is accepted.
// systemd writes a sentinel head EntryArray with zero items for empty
// journals; rejecting it would break Reader on a fresh file.
func TestParseEntryArray_EmptyArrayValid(t *testing.T) {
	buf := buildEntryArrayBytes(0, nil, false)
	r := bytes.NewReader(buf)

	ea, err := ParseEntryArray(r, 0, false)
	if err != nil {
		t.Fatalf("ParseEntryArray: %v", err)
	}
	if ea.NextEntryArrayOffset != 0 {
		t.Errorf("NextEntryArrayOffset = %d, want 0", ea.NextEntryArrayOffset)
	}
	if len(ea.Items) != 0 {
		t.Errorf("len(Items) = %d, want 0", len(ea.Items))
	}
}

// TestParseEntryArray_WrongType ensures the parser refuses to decode an
// object whose ObjectHeader.Type is not ObjectEntryArray. Pass an ENTRY
// object's bytes — the wire shape happens to be wider than the EA header,
// so a less-strict implementation could silently misinterpret it.
func TestParseEntryArray_WrongType(t *testing.T) {
	entry := buildEntryBytes(1, 1_700_000_000_000_000, [][2]uint64{{0x4000, 0xAA}})
	r := bytes.NewReader(entry)

	_, err := ParseEntryArray(r, 0, false)
	if err == nil {
		t.Fatal("ParseEntryArray returned nil error for ENTRY object")
	}
	if !errors.Is(err, ErrEntryArrayWrongType) {
		t.Errorf("err = %v, want errors.Is(ErrEntryArrayWrongType)", err)
	}
}

// TestParseEntryArray_PayloadTooSmall covers the case where an
// ObjectHeader.Type=ENTRY_ARRAY claims a Size below the minimum
// (16-byte header + 8-byte next_entry_array_offset = 24 bytes).
// ParseEntryArray must reject this before issuing the body ReadAt.
func TestParseEntryArray_PayloadTooSmall(t *testing.T) {
	// Build a minimal 20-byte buffer: 16 hdr + 4 payload (below the
	// 8-byte EntryArrayHeaderSize threshold).
	buf := make([]byte, 20)
	le := binary.LittleEndian
	buf[0] = byte(ObjectEntryArray)
	le.PutUint64(buf[8:16], 20) // declared object size
	r := bytes.NewReader(buf)

	_, err := ParseEntryArray(r, 0, false)
	if err == nil {
		t.Fatal("ParseEntryArray returned nil error for undersized object")
	}
	if !errors.Is(err, ErrEntryArrayMalformed) {
		t.Errorf("err = %v, want errors.Is(ErrEntryArrayMalformed)", err)
	}
}

// TestParseEntryArray_ItemsMisaligned verifies the parser rejects an
// object whose items section is not an exact multiple of the per-mode
// item width. Using non-compact (8-byte items) with a payload of 12
// bytes after the header field leaves 4 leftover bytes, which signals
// corruption.
func TestParseEntryArray_ItemsMisaligned(t *testing.T) {
	// payload = 8 (next) + 12 (items area). 12 % 8 == 4 → malformed.
	const payloadSize = 20
	buf := make([]byte, 16+payloadSize)
	le := binary.LittleEndian
	buf[0] = byte(ObjectEntryArray)
	le.PutUint64(buf[8:16], 16+payloadSize)
	// Leave next/items zero — only the size relationship matters.
	r := bytes.NewReader(buf)

	_, err := ParseEntryArray(r, 0, false)
	if err == nil {
		t.Fatal("ParseEntryArray returned nil error for misaligned items")
	}
	if !errors.Is(err, ErrEntryArrayMalformed) {
		t.Errorf("err = %v, want errors.Is(ErrEntryArrayMalformed)", err)
	}
}

// TestParseEntryArray_TruncatedBody ensures the parser surfaces a short
// read (declared size larger than the underlying ReaderAt) as an
// io.ErrUnexpectedEOF, not a silent zero-padded result.
func TestParseEntryArray_TruncatedBody(t *testing.T) {
	// Object claims size 32 (16 hdr + 8 next + 8 item = one entry) but
	// only 24 bytes are present.
	buf := make([]byte, 24)
	le := binary.LittleEndian
	buf[0] = byte(ObjectEntryArray)
	le.PutUint64(buf[8:16], 32) // claimed size
	le.PutUint64(buf[16:24], 0) // next
	r := bytes.NewReader(buf)

	_, err := ParseEntryArray(r, 0, false)
	if err == nil {
		t.Fatal("ParseEntryArray returned nil error for truncated body")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("err = %v, want errors.Is(io.ErrUnexpectedEOF)", err)
	}
}

// -----------------------------------------------------------------------
// iterateViaEntryArray (Reader integration) tests.
// -----------------------------------------------------------------------

// openSyntheticIndexed is a convenience wrapper that opens path with
// WithIndexedTraversal(true) and registers cleanup via t.Cleanup.
func openSyntheticIndexed(t *testing.T, path string) *Reader {
	t.Helper()
	r, err := Open(path, WithIndexedTraversal(true))
	if err != nil {
		t.Fatalf("Open(%q, indexed): %v", path, err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// TestEntryArray_IteratorEmptyJournalEOF confirms that opening a file
// whose Header.EntryArrayOffset is zero (an empty journal) and reading
// in indexed mode returns io.EOF immediately, without walking any
// arena bytes. Repeated calls keep returning io.EOF; the Reader must
// not advance into invalid state.
func TestEntryArray_IteratorEmptyJournalEOF(t *testing.T) {
	hdr := buildSyntheticHeaderBytes(0, 0, 0, 0, 0, 0, 0)
	path := writeSyntheticJournal(t, hdr, nil)

	r := openSyntheticIndexed(t, path)
	for i := 0; i < 3; i++ {
		e, err := r.ReadEntry()
		if !errors.Is(err, io.EOF) {
			t.Fatalf("call %d: err = %v, want io.EOF", i, err)
		}
		if e != nil {
			t.Errorf("call %d: entry = %+v, want nil", i, e)
		}
	}
}

// TestEntryArray_IteratorWalksMultiArrayChain builds a journal whose
// entry_array chain spans two EntryArray objects (EA1 → EA2 → 0) and
// verifies the iterator emits all entries from both arrays in order.
// This is the test that catches a missing NextEntryArrayOffset follow.
func TestEntryArray_IteratorWalksMultiArrayChain(t *testing.T) {
	// Layout: 3 entries first, then EA1 (items E1,E2 → next EA2),
	// then EA2 (items E3 → next 0).
	const headerEnd = MinHeaderSize
	var arena []byte
	var entryOffsets []uint64

	for i := 0; i < 3; i++ {
		seq := uint64(2000 + i)
		off := headerEnd + uint64(len(arena))
		arena = append(arena, buildEntryBytes(seq, 1_700_000_000_000_000+uint64(i)*1_000_000,
			[][2]uint64{{uint64(0x4000 + i*16), uint64(0xAA00 + i)}})...)
		arena = padToAlign(arena)
		entryOffsets = append(entryOffsets, off)
	}

	// Build EA2 first so we know its offset before serializing EA1.
	ea2Offset := headerEnd + uint64(len(arena))
	ea2 := buildEntryArrayBytes(0, []uint64{entryOffsets[2]}, false)
	arena = append(arena, ea2...)
	arena = padToAlign(arena)

	ea1Offset := headerEnd + uint64(len(arena))
	ea1 := buildEntryArrayBytes(ea2Offset, entryOffsets[:2], false)
	arena = append(arena, ea1...)
	arena = padToAlign(arena)

	hdr := buildSyntheticHeaderBytes(uint64(len(arena)), 3, 2000, 2002, ea1Offset,
		1_700_000_000_000_000, 1_700_000_002_000_000)
	path := writeSyntheticJournal(t, hdr, arena)

	r := openSyntheticIndexed(t, path)
	var got []uint64
	for {
		e, err := r.ReadEntry()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("ReadEntry #%d: %v", len(got), err)
		}
		got = append(got, e.SeqNum)
		if len(got) > 10 {
			t.Fatalf("iterator failed to terminate: %v", got)
		}
	}
	want := []uint64{2000, 2001, 2002}
	if len(got) != len(want) {
		t.Fatalf("seqnums = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

// TestEntryArray_IteratorSkipsSparseSlots verifies that zero-valued
// items in an EntryArray (sparse / deleted slots) are silently skipped
// rather than triggering a wrong-type error or hanging the iterator.
func TestEntryArray_IteratorSkipsSparseSlots(t *testing.T) {
	const headerEnd = MinHeaderSize
	var arena []byte
	var entryOffsets []uint64
	for i := 0; i < 2; i++ {
		seq := uint64(3000 + i)
		off := headerEnd + uint64(len(arena))
		arena = append(arena, buildEntryBytes(seq,
			1_700_000_000_000_000+uint64(i)*1_000_000,
			[][2]uint64{{0x5000, 0x55}})...)
		arena = padToAlign(arena)
		entryOffsets = append(entryOffsets, off)
	}
	// Items: [E1, 0, E2, 0]. The two zero slots must be skipped.
	eaItems := []uint64{entryOffsets[0], 0, entryOffsets[1], 0}
	eaOffset := headerEnd + uint64(len(arena))
	arena = append(arena, buildEntryArrayBytes(0, eaItems, false)...)
	arena = padToAlign(arena)

	hdr := buildSyntheticHeaderBytes(uint64(len(arena)), 2, 3000, 3001, eaOffset,
		1_700_000_000_000_000, 1_700_000_001_000_000)
	path := writeSyntheticJournal(t, hdr, arena)

	r := openSyntheticIndexed(t, path)
	var got []uint64
	for {
		e, err := r.ReadEntry()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("ReadEntry #%d: %v", len(got), err)
		}
		got = append(got, e.SeqNum)
	}
	want := []uint64{3000, 3001}
	if len(got) != len(want) {
		t.Fatalf("seqnums = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

// TestEntryArray_IteratorDetectsCycle builds an EntryArray whose
// NextEntryArrayOffset points at itself and verifies the iterator
// returns ErrEntryArrayCycle rather than spinning forever. Defense in
// depth against corrupted or maliciously crafted journals.
func TestEntryArray_IteratorDetectsCycle(t *testing.T) {
	const headerEnd = MinHeaderSize
	var arena []byte

	// One entry so the iterator emits at least one body before hitting
	// the cycle. This exercises the full chain-follow path.
	seq := uint64(4000)
	entryOffset := headerEnd + uint64(len(arena))
	arena = append(arena, buildEntryBytes(seq, 1_700_000_000_000_000,
		[][2]uint64{{0x6000, 0x66}})...)
	arena = padToAlign(arena)

	eaOffset := headerEnd + uint64(len(arena))
	// EA points at itself: next = eaOffset, items = [E1].
	arena = append(arena, buildEntryArrayBytes(eaOffset, []uint64{entryOffset}, false)...)
	arena = padToAlign(arena)

	hdr := buildSyntheticHeaderBytes(uint64(len(arena)), 1, 4000, 4000, eaOffset,
		1_700_000_000_000_000, 1_700_000_000_000_000)
	path := writeSyntheticJournal(t, hdr, arena)

	r := openSyntheticIndexed(t, path)

	// First call must succeed and return the only entry.
	e, err := r.ReadEntry()
	if err != nil {
		t.Fatalf("first ReadEntry: %v", err)
	}
	if e.SeqNum != seq {
		t.Fatalf("first.SeqNum = %d, want %d", e.SeqNum, seq)
	}

	// Second call must detect the self-cycle when following Next.
	_, err = r.ReadEntry()
	if !errors.Is(err, ErrEntryArrayCycle) {
		t.Fatalf("second ReadEntry: err = %v, want errors.Is(ErrEntryArrayCycle)", err)
	}
}

// TestEntryArray_IteratorRefusesAfterClose verifies that Close +
// ReadEntry on an indexed-traversal Reader returns ErrReaderClosed,
// matching the linear-scan strategy's behavior.
func TestEntryArray_IteratorRefusesAfterClose(t *testing.T) {
	hdr := buildSyntheticHeaderBytes(0, 0, 0, 0, 0, 0, 0)
	path := writeSyntheticJournal(t, hdr, nil)

	r, err := Open(path, WithIndexedTraversal(true))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := r.ReadEntry(); !errors.Is(err, ErrReaderClosed) {
		t.Errorf("ReadEntry after Close: err = %v, want ErrReaderClosed", err)
	}
}

// TestEntryArray_IteratorRejectsBadSeed builds a header whose
// EntryArrayOffset points at an ENTRY (not an ENTRY_ARRAY) object and
// confirms the iterator surfaces the wrong-type error rather than
// silently producing no entries.
func TestEntryArray_IteratorRejectsBadSeed(t *testing.T) {
	const headerEnd = MinHeaderSize
	entryBytes := buildEntryBytes(5000, 1_700_000_000_000_000,
		[][2]uint64{{0x7000, 0x77}})
	arena := padToAlign(entryBytes)

	// Seed = entry offset (headerEnd), not an EA.
	hdr := buildSyntheticHeaderBytes(uint64(len(arena)), 1, 5000, 5000, headerEnd,
		1_700_000_000_000_000, 1_700_000_000_000_000)
	path := writeSyntheticJournal(t, hdr, arena)

	r := openSyntheticIndexed(t, path)
	_, err := r.ReadEntry()
	if !errors.Is(err, ErrEntryArrayWrongType) {
		t.Errorf("ReadEntry: err = %v, want errors.Is(ErrEntryArrayWrongType)", err)
	}
}

// -----------------------------------------------------------------------
// Linear-vs-indexed parity test (the core DoD assertion of task 19).
// -----------------------------------------------------------------------

// TestTraversalParity is the spec-mandated cross-strategy assertion:
// build a fixture once, walk it via the default linear ENTRY scan and
// again via WithIndexedTraversal(true), then verify both strategies
// emit the identical entry sequence (SeqNum / Realtime / Monotonic /
// XorHash / BootID / Items). When this test fails, either the indexed
// traversal is dropping entries or the two parsers disagree on item
// decoding — both are correctness regressions that block Phase 2.
func TestTraversalParity(t *testing.T) {
	const n = 7
	const headerEnd = MinHeaderSize
	const baseSeq uint64 = 6000
	const baseRT uint64 = 1_700_000_000_000_000

	var arena []byte
	var entryOffsets []uint64
	for i := 0; i < n; i++ {
		off := headerEnd + uint64(len(arena))
		seq := baseSeq + uint64(i)
		rt := baseRT + uint64(i)*1_000_000
		// Two items per entry — enough to exercise the EntryItem
		// stride for both strategies. Offsets are deliberately
		// fictitious; ParseEntry never dereferences them.
		items := [][2]uint64{
			{uint64(0x4000 + i*64), uint64(0xAA00 + i)},
			{uint64(0x8000 + i*64), uint64(0xBB00 + i)},
		}
		arena = append(arena, buildEntryBytes(seq, rt, items)...)
		arena = padToAlign(arena)
		entryOffsets = append(entryOffsets, off)
	}

	eaOffset := headerEnd + uint64(len(arena))
	arena = append(arena, buildEntryArrayBytes(0, entryOffsets, false)...)
	arena = padToAlign(arena)

	hdr := buildSyntheticHeaderBytes(uint64(len(arena)), uint64(n),
		baseSeq, baseSeq+uint64(n-1), eaOffset,
		baseRT, baseRT+uint64(n-1)*1_000_000)
	path := writeSyntheticJournal(t, hdr, arena)

	// Linear strategy.
	rLin, err := Open(path)
	if err != nil {
		t.Fatalf("Open linear: %v", err)
	}
	t.Cleanup(func() { _ = rLin.Close() })

	linear := drainAll(t, rLin, n)

	// Indexed strategy on the same file.
	rIdx, err := Open(path, WithIndexedTraversal(true))
	if err != nil {
		t.Fatalf("Open indexed: %v", err)
	}
	t.Cleanup(func() { _ = rIdx.Close() })

	indexed := drainAll(t, rIdx, n)

	if len(linear) != n {
		t.Fatalf("linear count = %d, want %d", len(linear), n)
	}
	if len(indexed) != n {
		t.Fatalf("indexed count = %d, want %d", len(indexed), n)
	}

	for i := 0; i < n; i++ {
		a, b := linear[i], indexed[i]
		if a.SeqNum != b.SeqNum {
			t.Errorf("entry %d SeqNum: linear=%d indexed=%d", i, a.SeqNum, b.SeqNum)
		}
		if a.Realtime != b.Realtime {
			t.Errorf("entry %d Realtime: linear=%d indexed=%d", i, a.Realtime, b.Realtime)
		}
		if a.Monotonic != b.Monotonic {
			t.Errorf("entry %d Monotonic: linear=%d indexed=%d", i, a.Monotonic, b.Monotonic)
		}
		if a.BootID != b.BootID {
			t.Errorf("entry %d BootID mismatch", i)
		}
		if a.XorHash != b.XorHash {
			t.Errorf("entry %d XorHash: linear=0x%x indexed=0x%x", i, a.XorHash, b.XorHash)
		}
		if a.Compact != b.Compact {
			t.Errorf("entry %d Compact: linear=%v indexed=%v", i, a.Compact, b.Compact)
		}
		if !equalEntryItems(a.Items, b.Items) {
			t.Errorf("entry %d Items: linear=%v indexed=%v", i, a.Items, b.Items)
		}
	}

	// Sanity: linear strategy advances Offset as it scans, indexed
	// strategy does not. This proves the two strategies took different
	// code paths inside the Reader rather than both falling back to
	// the same scanner.
	if rLin.Offset() == MinHeaderSize {
		t.Errorf("linear Reader.Offset() did not advance past header")
	}
	if rIdx.Offset() != MinHeaderSize {
		t.Errorf("indexed Reader.Offset() advanced (%d) — should remain at header end", rIdx.Offset())
	}
}

// drainAll calls ReadEntry until io.EOF, returning every yielded Entry.
// expected is an upper bound used as a runaway guard.
func drainAll(t *testing.T, r *Reader, expected int) []*Entry {
	t.Helper()
	out := make([]*Entry, 0, expected)
	for {
		e, err := r.ReadEntry()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("ReadEntry #%d: %v", len(out), err)
		}
		out = append(out, e)
		if len(out) > expected*4 {
			t.Fatalf("ReadEntry produced > %d entries — runaway iterator", expected*4)
		}
	}
}

// equalEntryItems compares two EntryItem slices for byte-equal
// ObjectOffset and Hash values. Used by TestTraversalParity to confirm
// the two strategies decode each entry's items identically.
func equalEntryItems(a, b []EntryItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ObjectOffset != b[i].ObjectOffset {
			return false
		}
		if a[i].Hash != b[i].Hash {
			return false
		}
	}
	return true
}
