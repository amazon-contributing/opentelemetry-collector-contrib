// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package native

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// smallFixturePath is the canonical relative path from this test's working
// directory (the package dir) to the committed small.journal fixture under
// receiver/journaldreceiver/testdata/native/. Tests resolve it through
// os.Stat first so a CI sandbox missing the receiver subtree skips
// cleanly rather than failing.
//
// pkg/stanza/operator/input/journald/native/  -> repo root needs 6 ../
//   1: native -> journald
//   2: journald -> input
//   3: input -> operator
//   4: operator -> stanza
//   5: stanza -> pkg
//   6: pkg -> repo root
const smallFixtureRel = "../../../../../../receiver/journaldreceiver/testdata/native/small.journal"

// Expected golden values for small.journal. These mirror the constants
// hard-coded in the generator at receiver/journaldreceiver/testdata/native/
// generate/gen_small_journal.go. If you edit the generator, update both
// the regenerated fixture AND these constants in the same change.
const (
	smallFixtureEntries        uint64 = 5
	smallFixtureFirstSeqNum    uint64 = 1000
	smallFixtureLastSeqNum     uint64 = 1004
	smallFixtureFirstRealtime  uint64 = 1_700_000_000_000_000
	smallFixtureLastRealtime   uint64 = 1_700_000_004_000_000
	smallFixtureFirstMonotonic uint64 = 100_000_000
	smallFixtureLastMonotonic  uint64 = 104_000_000
	smallFixtureFirstXorHash   uint64 = 0xDEADBEEF
	smallFixtureLastXorHash    uint64 = 0xDEADBEEF + 4
	smallFixtureItemsPerEntry         = 2
	smallFixtureCompact               = false
)

var smallFixtureBootID = [16]byte{
	0xC0, 0xC1, 0xC2, 0xC3, 0xC4, 0xC5, 0xC6, 0xC7,
	0xC8, 0xC9, 0xCA, 0xCB, 0xCC, 0xCD, 0xCE, 0xCF,
}

// resolveSmallFixture returns the absolute path to small.journal if the
// committed file is present, otherwise "" so the caller can skip.
func resolveSmallFixture(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(smallFixtureRel)
	if err != nil {
		t.Fatalf("filepath.Abs(%q): %v", smallFixtureRel, err)
	}
	if _, err := os.Stat(abs); err != nil {
		return ""
	}
	return abs
}

// TestReader_OpensSmallJournalFixture is the spec-mandated golden test:
// open the committed small.journal fixture, walk every ENTRY via
// ReadEntry until io.EOF, and assert (a) the count matches the generator's
// fixed value and (b) the first/last entries match a byte-level snapshot
// of seqnum / realtime / monotonic / boot_id / xor_hash / items.
//
// This test enforces DoD-7 — the reader is wired end-to-end and produces
// stable output for at least one committed fixture.
func TestReader_OpensSmallJournalFixture(t *testing.T) {
	path := resolveSmallFixture(t)
	if path == "" {
		t.Skipf("fixture not found at %s; commit small.journal or regenerate via testdata/native/generate/", smallFixtureRel)
	}

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = r.Close() })

	// Header sanity — counts must match the generator's blueprint.
	h := r.Header()
	if h.NEntries != smallFixtureEntries {
		t.Errorf("Header.NEntries = %d, want %d", h.NEntries, smallFixtureEntries)
	}
	if h.HeadEntrySeqnum != smallFixtureFirstSeqNum {
		t.Errorf("Header.HeadEntrySeqnum = %d, want %d",
			h.HeadEntrySeqnum, smallFixtureFirstSeqNum)
	}
	if h.TailEntrySeqnum != smallFixtureLastSeqNum {
		t.Errorf("Header.TailEntrySeqnum = %d, want %d",
			h.TailEntrySeqnum, smallFixtureLastSeqNum)
	}
	if h.HeadEntryRealtime != smallFixtureFirstRealtime {
		t.Errorf("Header.HeadEntryRealtime = %d, want %d",
			h.HeadEntryRealtime, smallFixtureFirstRealtime)
	}
	if h.TailEntryRealtime != smallFixtureLastRealtime {
		t.Errorf("Header.TailEntryRealtime = %d, want %d",
			h.TailEntryRealtime, smallFixtureLastRealtime)
	}
	if r.Compact() != smallFixtureCompact {
		t.Errorf("Compact() = %v, want %v", r.Compact(), smallFixtureCompact)
	}

	// Walk every entry. Capture first and last for snapshot assertion.
	var entries []*Entry
	for {
		e, err := r.ReadEntry()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("ReadEntry #%d: %v", len(entries), err)
		}
		entries = append(entries, e)
		// Defensive: don't loop forever if Reader misbehaves.
		if uint64(len(entries)) > 4*smallFixtureEntries {
			t.Fatalf("ReadEntry produced > %d entries; Reader cursor stuck?",
				4*smallFixtureEntries)
		}
	}

	if uint64(len(entries)) != smallFixtureEntries {
		t.Fatalf("read %d entries, want %d", len(entries), smallFixtureEntries)
	}

	// Repeated ReadEntry past EOF must keep returning io.EOF without
	// advancing or panicking.
	for i := 0; i < 3; i++ {
		if _, err := r.ReadEntry(); !errors.Is(err, io.EOF) {
			t.Errorf("ReadEntry past EOF #%d = %v, want io.EOF", i, err)
		}
	}

	// First-entry snapshot.
	first := entries[0]
	if first.SeqNum != smallFixtureFirstSeqNum {
		t.Errorf("first.SeqNum = %d, want %d",
			first.SeqNum, smallFixtureFirstSeqNum)
	}
	if first.Realtime != smallFixtureFirstRealtime {
		t.Errorf("first.Realtime = %d, want %d",
			first.Realtime, smallFixtureFirstRealtime)
	}
	if first.Monotonic != smallFixtureFirstMonotonic {
		t.Errorf("first.Monotonic = %d, want %d",
			first.Monotonic, smallFixtureFirstMonotonic)
	}
	if first.BootID != smallFixtureBootID {
		t.Errorf("first.BootID = %v, want %v",
			first.BootID, smallFixtureBootID)
	}
	if first.XorHash != smallFixtureFirstXorHash {
		t.Errorf("first.XorHash = 0x%x, want 0x%x",
			first.XorHash, smallFixtureFirstXorHash)
	}
	if len(first.Items) != smallFixtureItemsPerEntry {
		t.Errorf("len(first.Items) = %d, want %d",
			len(first.Items), smallFixtureItemsPerEntry)
	}
	if first.Compact != smallFixtureCompact {
		t.Errorf("first.Compact = %v, want %v",
			first.Compact, smallFixtureCompact)
	}
	// Cross-check item offsets/hashes with the generator's recipe:
	// items[0] = (0x4000+i*64, 0xAA00+i), items[1] = (0x8000+i*64, 0xBB00+i).
	if len(first.Items) >= 1 {
		if first.Items[0].ObjectOffset != 0x4000 {
			t.Errorf("first.Items[0].ObjectOffset = 0x%x, want 0x4000",
				first.Items[0].ObjectOffset)
		}
		if first.Items[0].Hash != 0xAA00 {
			t.Errorf("first.Items[0].Hash = 0x%x, want 0xAA00",
				first.Items[0].Hash)
		}
	}
	if len(first.Items) >= 2 {
		if first.Items[1].ObjectOffset != 0x8000 {
			t.Errorf("first.Items[1].ObjectOffset = 0x%x, want 0x8000",
				first.Items[1].ObjectOffset)
		}
		if first.Items[1].Hash != 0xBB00 {
			t.Errorf("first.Items[1].Hash = 0x%x, want 0xBB00",
				first.Items[1].Hash)
		}
	}
	// Realtime must round-trip through the safe converter.
	if got, err := first.RealtimeAsTime(); err != nil {
		t.Errorf("first.RealtimeAsTime: %v", err)
	} else if got.Year() != 2023 {
		t.Errorf("first.RealtimeAsTime year = %d, want 2023", got.Year())
	}

	// Last-entry snapshot.
	last := entries[len(entries)-1]
	if last.SeqNum != smallFixtureLastSeqNum {
		t.Errorf("last.SeqNum = %d, want %d",
			last.SeqNum, smallFixtureLastSeqNum)
	}
	if last.Realtime != smallFixtureLastRealtime {
		t.Errorf("last.Realtime = %d, want %d",
			last.Realtime, smallFixtureLastRealtime)
	}
	if last.Monotonic != smallFixtureLastMonotonic {
		t.Errorf("last.Monotonic = %d, want %d",
			last.Monotonic, smallFixtureLastMonotonic)
	}
	if last.XorHash != smallFixtureLastXorHash {
		t.Errorf("last.XorHash = 0x%x, want 0x%x",
			last.XorHash, smallFixtureLastXorHash)
	}
	if len(last.Items) != smallFixtureItemsPerEntry {
		t.Errorf("len(last.Items) = %d, want %d",
			len(last.Items), smallFixtureItemsPerEntry)
	}
	// Last entry's index = smallFixtureEntries-1 = 4
	if len(last.Items) >= 1 {
		wantOff := uint64(0x4000 + 4*64) // 0x4100
		if last.Items[0].ObjectOffset != wantOff {
			t.Errorf("last.Items[0].ObjectOffset = 0x%x, want 0x%x",
				last.Items[0].ObjectOffset, wantOff)
		}
		if last.Items[0].Hash != 0xAA04 {
			t.Errorf("last.Items[0].Hash = 0x%x, want 0xAA04",
				last.Items[0].Hash)
		}
	}

	// Entries must come back in seqnum order (the generator writes them
	// linearly, and the linear scan in Reader preserves that).
	for i := 1; i < len(entries); i++ {
		if entries[i].SeqNum != entries[i-1].SeqNum+1 {
			t.Errorf("entries[%d].SeqNum = %d, want %d (monotonic)",
				i, entries[i].SeqNum, entries[i-1].SeqNum+1)
		}
		if entries[i].Realtime <= entries[i-1].Realtime {
			t.Errorf("entries[%d].Realtime = %d, not strictly after %d",
				i, entries[i].Realtime, entries[i-1].Realtime)
		}
	}
}

// TestReader_OpenInvalid covers the failure modes of Open: missing file,
// non-journal content, signature mismatch.
func TestReader_OpenInvalid(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		_, err := Open(filepath.Join(t.TempDir(), "does-not-exist.journal"))
		if err == nil {
			t.Fatal("Open() = nil err, want error")
		}
	})

	t.Run("empty file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "empty.journal")
		if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Open(path)
		if err == nil {
			t.Fatal("Open(empty) = nil err, want error")
		}
	})

	t.Run("bad signature", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.journal")
		// Write 256 bytes of zeros — Signature mismatch.
		if err := os.WriteFile(path, make([]byte, 256), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Open(path)
		if err == nil {
			t.Fatal("Open(zeros) = nil err, want error")
		}
		if !errors.Is(err, ErrInvalidSignature) {
			t.Errorf("Open(zeros) err = %v, want ErrInvalidSignature", err)
		}
	})
}

// TestReader_CloseIdempotent confirms multiple Close calls are safe and
// that ReadEntry on a closed Reader returns ErrReaderClosed (not a file
// I/O error).
func TestReader_CloseIdempotent(t *testing.T) {
	path := resolveSmallFixture(t)
	if path == "" {
		t.Skipf("fixture not found at %s", smallFixtureRel)
	}

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := r.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if _, err := r.ReadEntry(); !errors.Is(err, ErrReaderClosed) {
		t.Errorf("ReadEntry after Close = %v, want ErrReaderClosed", err)
	}
}

// TestReader_OffsetAdvances asserts that Reader.Offset() moves forward on
// every successful ReadEntry and lands at arenaEnd after EOF.
func TestReader_OffsetAdvances(t *testing.T) {
	path := resolveSmallFixture(t)
	if path == "" {
		t.Skipf("fixture not found at %s", smallFixtureRel)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	prev := r.Offset()
	if prev == 0 {
		t.Errorf("Offset() at Open = 0, want HeaderSize > 0")
	}
	for i := uint64(0); ; i++ {
		_, err := r.ReadEntry()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("ReadEntry #%d: %v", i, err)
		}
		cur := r.Offset()
		if cur <= prev {
			t.Errorf("Offset() after entry #%d = %d, did not advance from %d",
				i, cur, prev)
		}
		prev = cur
	}
}

// TestReader_RealFixture is the bonus coverage path: open the system
// journal called out in the spec (or a fallback under the same dir) and
// verify Reader walks all entries reported by the file's own
// Header.NEntries. Skipped cleanly on machines without /var/log/journal
// (CI runners, fresh dev hosts).
//
// This test is the closest equivalent to the spike's "522 entries
// verified" check, but it adapts to whatever fixture happens to be
// present so the suite does not fail when journals rotate.
func TestReader_RealFixture(t *testing.T) {
	path := pickRealFixture(t)
	if path == "" {
		t.Skipf("no readable .journal fixture under /var/log/journal; skipping")
	}
	if _, err := os.Open(path); err != nil {
		t.Skipf("cannot open fixture %s: %v", path, err)
	}

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = r.Close() })

	expected := r.Header().NEntries
	if expected == 0 {
		t.Skipf("fixture %s has zero entries; nothing to walk", path)
	}

	var got uint64
	start := time.Now()
	for {
		_, err := r.ReadEntry()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// Real journals occasionally contain partially-written
			// objects at the tail when the writer crashes. Treat
			// any post-traversal error as a soft failure with
			// context so the test surfaces the offset rather than
			// pretending the file is fine.
			t.Logf("ReadEntry stopped at cursor=%d after %d entries: %v",
				r.Offset(), got, err)
			break
		}
		got++
		// Bound runtime on huge journals.
		if time.Since(start) > 30*time.Second {
			t.Fatalf("ReadEntry loop ran > 30s; aborting after %d entries", got)
		}
	}

	// We tolerate up to a 1% short-read against the header-declared
	// count to account for crashed-writer tail objects, but anything
	// worse than that signals a real Reader bug.
	tolerance := expected / 100
	if tolerance < 1 {
		tolerance = 1
	}
	if got+tolerance < expected {
		t.Errorf("walked %d entries, header declares %d (tolerance ±%d) — Reader missed entries",
			got, expected, tolerance)
	}
	t.Logf("Reader walked %d/%d entries from %s in %s",
		got, expected, filepath.Base(path), time.Since(start))
}
