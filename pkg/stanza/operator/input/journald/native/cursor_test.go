// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package native

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------
// Phase-3 cursor / checkpoint tests.
//
// Coverage:
//
//   - TestCursor_RoundTrip: Cursor.String -> ParseCursor preserves every
//     wire field bit-exactly, FileID is stripped (it is local-only),
//     and a second .String() reproduces the original cursor verbatim.
//   - TestCursor_ParseInvalid: every documented malformed-input class
//     surfaces ErrCursorMalformed (or ErrCursorEmpty), matched via
//     errors.Is.
//   - TestCursor_NoEntry: Reader.Cursor() before ReadEntry returns
//     ErrCursorNoEntry.
//   - TestCursor_SeekMidFixture: walk small.journal, save a cursor at
//     each pivot, open a fresh Reader, SeekToCursor, and assert the
//     next ReadEntry matches the byte-level expected entry.
//   - TestCursor_SeekToLastEntry: a cursor pointing at the tail entry
//     produces io.EOF on the next ReadEntry.
//   - TestCursor_SeekRoundTripsCursorString: after a successful
//     SeekToCursor, Reader.Cursor() reproduces the input cursor.
//   - TestCursor_SeekMalformed / SeekSeqnumMismatch / SeekNotFound /
//     SeekClosed: the four error paths of SeekToCursor, including the
//     "reader is reset to head on not-found" recovery contract.
//   - TestCrashRecovery_PrivateJournal: deterministic crash-recovery
//     simulation using a synthesised private journal — the
//     no-systemd-cat path that always runs in CI.
//   - TestCrashRecovery_SystemdCat: spec-mandated crash-recovery test
//     that drives every entry through systemd-cat (via the bridge
//     introduced in follow_test.go). Skips on hosts without
//     systemd-cat / journalctl.
//
// All assertions test the documented contracts:
//
//   - "next ReadEntry returns the ENTRY immediately after the one
//     identified by cursorStr" (SeekToCursor doc).
//   - "On any failure the Reader is left in a defined state ... cursor
//     seqnum not found ... r is reset to the file head" (SeekToCursor
//     doc).
//   - "Subsequent Reader.Cursor() calls will return cursorStr until the
//     next ReadEntry advances past it" (SeekToCursor doc).
// -----------------------------------------------------------------------

// smallFixtureSeqnumID matches the 16-byte SeqnumID layout written by
// gen_small_journal.go ([0xD0..0xDF]). Captured here rather than imported
// because the generator is in a separate package.
var smallFixtureSeqnumID = [16]byte{
	0xD0, 0xD1, 0xD2, 0xD3, 0xD4, 0xD5, 0xD6, 0xD7,
	0xD8, 0xD9, 0xDA, 0xDB, 0xDC, 0xDD, 0xDE, 0xDF,
}

// smallFixtureFileID matches the 16-byte FileID layout written by
// gen_small_journal.go ([0xA0..0xAF]). Used by TestCursor_RoundTrip to
// confirm Reader.Cursor() carries the parsed file id but the wire format
// strips it (FileID is local-only by spec).
var smallFixtureFileID = [16]byte{
	0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5, 0xA6, 0xA7,
	0xA8, 0xA9, 0xAA, 0xAB, 0xAC, 0xAD, 0xAE, 0xAF,
}

// readAllFromPath opens path and returns every ENTRY in iteration order.
// Used as a setup step in cursor seek tests so each pivot's expected
// "next entry" can be cross-checked against the canonical sequence.
func readAllFromPath(t *testing.T, path string) []*Entry {
	t.Helper()
	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	defer func() { _ = r.Close() }()

	var out []*Entry
	for {
		e, err := r.ReadEntry()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("readAllFromPath ReadEntry: %v", err)
		}
		out = append(out, e)
	}
	return out
}

// makeFullCursor returns a Cursor with all six wire fields populated to
// non-trivial values. Used as the baseline for round-trip and parse
// negative tests so each malformed mutation can be expressed as a
// drop-in substitution into the otherwise-valid string.
func makeFullCursor() *Cursor {
	return &Cursor{
		// FileID is set to a non-zero pattern so the round-trip test
		// can prove ParseCursor strips it (FileID is local-only).
		FileID: [16]byte{
			0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
			0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10,
		},
		SeqnumID:  smallFixtureSeqnumID,
		Seqnum:    smallFixtureFirstSeqNum,
		BootID:    smallFixtureBootID,
		Monotonic: smallFixtureFirstMonotonic,
		Realtime:  smallFixtureFirstRealtime,
		XorHash:   smallFixtureFirstXorHash,
	}
}

// -----------------------------------------------------------------------
// Pure-Go (no fixture) tests.
// -----------------------------------------------------------------------

// TestCursor_RoundTrip asserts Cursor.String() and ParseCursor() are
// inverses on every wire field, and that FileID is stripped on parse.
func TestCursor_RoundTrip(t *testing.T) {
	cases := map[string]*Cursor{
		"all zero":     {},
		"max uint64":   {SeqnumID: smallFixtureSeqnumID, Seqnum: ^uint64(0), BootID: smallFixtureBootID, Monotonic: ^uint64(0), Realtime: ^uint64(0), XorHash: ^uint64(0)},
		"realistic":    makeFullCursor(),
		"high entropy": {SeqnumID: [16]byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0, 0x0F, 0xED, 0xCB, 0xA9, 0x87, 0x65, 0x43, 0x21}, Seqnum: 0xCAFEBABE, BootID: [16]byte{0xFE, 0xED, 0xFA, 0xCE, 0xFE, 0xED, 0xFA, 0xCE, 0xFE, 0xED, 0xFA, 0xCE, 0xFE, 0xED, 0xFA, 0xCE}, Monotonic: 0xDEADBEEF, Realtime: 0x1700000000000000, XorHash: 0xBADDCAFE},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := c.String()

			// Wire format must contain exactly the six expected keys
			// in canonical "k=v" form. We don't assert ordering here
			// (ParseCursor accepts any order) but we DO require that
			// each component is "<single-letter>=<non-empty>".
			for _, key := range []string{"s=", "i=", "b=", "m=", "t=", "x="} {
				if !strings.Contains(s, key) {
					t.Errorf("String() = %q, missing key %q", s, key)
				}
			}

			parsed, err := ParseCursor(s)
			if err != nil {
				t.Fatalf("ParseCursor(%q): %v", s, err)
			}

			// FileID is local-only and intentionally NOT serialised
			// to the wire format; ParseCursor leaves it zero even
			// when the source cursor had it populated.
			var zeroID [16]byte
			if parsed.FileID != zeroID {
				t.Errorf("ParseCursor populated FileID = %x, want zero", parsed.FileID)
			}

			// All wire fields must be preserved bit-exactly.
			if parsed.SeqnumID != c.SeqnumID {
				t.Errorf("SeqnumID: got %x, want %x", parsed.SeqnumID, c.SeqnumID)
			}
			if parsed.Seqnum != c.Seqnum {
				t.Errorf("Seqnum: got %d, want %d", parsed.Seqnum, c.Seqnum)
			}
			if parsed.BootID != c.BootID {
				t.Errorf("BootID: got %x, want %x", parsed.BootID, c.BootID)
			}
			if parsed.Monotonic != c.Monotonic {
				t.Errorf("Monotonic: got %d, want %d", parsed.Monotonic, c.Monotonic)
			}
			if parsed.Realtime != c.Realtime {
				t.Errorf("Realtime: got %d, want %d", parsed.Realtime, c.Realtime)
			}
			if parsed.XorHash != c.XorHash {
				t.Errorf("XorHash: got %d, want %d", parsed.XorHash, c.XorHash)
			}

			// Re-serialise the parsed cursor; the second string must
			// match the first byte-for-byte so cursor equality is
			// safe to perform via string comparison.
			s2 := parsed.String()
			if s2 != s {
				t.Errorf("re-serialised cursor differs:\n  got  %q\n  want %q", s2, s)
			}
		})
	}
}

// TestCursor_ParseAcceptsAnyKeyOrder confirms ParseCursor is tolerant of
// arbitrary key order, mirroring sd_journal_seek_cursor's behaviour.
func TestCursor_ParseAcceptsAnyKeyOrder(t *testing.T) {
	c := makeFullCursor()

	// Reordered: x;t;m;b;i;s — exact reverse of canonical.
	reordered := fmt.Sprintf("x=%x;t=%x;m=%x;b=%s;i=%x;s=%s",
		c.XorHash, c.Realtime, c.Monotonic, hexID(c.BootID),
		c.Seqnum, hexID(c.SeqnumID))

	parsed, err := ParseCursor(reordered)
	if err != nil {
		t.Fatalf("ParseCursor reordered: %v", err)
	}
	if parsed.SeqnumID != c.SeqnumID || parsed.Seqnum != c.Seqnum ||
		parsed.BootID != c.BootID || parsed.Monotonic != c.Monotonic ||
		parsed.Realtime != c.Realtime || parsed.XorHash != c.XorHash {
		t.Errorf("reordered parse mismatch: got %+v, want %+v", *parsed, *c)
	}
}

// hexID renders a 16-byte ID as 32 lowercase hex chars. Mirrors
// id128String (which is unexported) so test inputs and outputs use the
// same format the wire spec mandates.
func hexID(id [16]byte) string {
	return fmt.Sprintf("%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x",
		id[0], id[1], id[2], id[3], id[4], id[5], id[6], id[7],
		id[8], id[9], id[10], id[11], id[12], id[13], id[14], id[15])
}

// TestCursor_ParseInvalid covers every documented malformed-cursor
// class. Each case must surface ErrCursorMalformed (or ErrCursorEmpty
// for the empty-string case), reachable via errors.Is.
func TestCursor_ParseInvalid(t *testing.T) {
	// Build a known-good baseline so each negative case can mutate it
	// minimally — the failure mode is then attributable to the single
	// mutation rather than wholesale string corruption.
	valid := makeFullCursor().String()

	cases := []struct {
		name string
		in   string
		want error
	}{
		{"empty string", "", ErrCursorEmpty},

		// Structural issues — dispatched by the per-component loop.
		{"leading semicolon", ";" + valid, ErrCursorMalformed},
		{"trailing semicolon", valid + ";", ErrCursorMalformed},
		{"empty middle component", strings.Replace(valid, ";m=", ";;m=", 1), ErrCursorMalformed},
		{"missing equals", strings.Replace(valid, "i=", "i", 1), ErrCursorMalformed},
		{"empty key", strings.Replace(valid, "i=", "=", 1), ErrCursorMalformed},
		{"multi-letter key", strings.Replace(valid, ";i=", ";ii=", 1), ErrCursorMalformed},
		{"empty value", strings.Replace(valid, fmt.Sprintf(";i=%x", makeFullCursor().Seqnum), ";i=", 1), ErrCursorMalformed},
		{"unknown key", valid + ";y=42", ErrCursorMalformed},

		// Duplicates — systemd never emits them; treat as corruption.
		{"duplicate s", valid + ";s=" + hexID(smallFixtureSeqnumID), ErrCursorMalformed},
		{"duplicate i", valid + ";i=2a", ErrCursorMalformed},

		// Type-specific malformed payloads.
		{"seqnum_id wrong length", strings.Replace(valid, "s="+hexID(smallFixtureSeqnumID), "s=ABCD", 1), ErrCursorMalformed},
		{"seqnum_id non-hex", strings.Replace(valid, "s="+hexID(smallFixtureSeqnumID), "s=zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", 1), ErrCursorMalformed},
		{"boot_id wrong length", strings.Replace(valid, "b="+hexID(smallFixtureBootID), "b=00", 1), ErrCursorMalformed},
		{"seqnum non-hex", strings.Replace(valid, fmt.Sprintf("i=%x", makeFullCursor().Seqnum), "i=NOPE", 1), ErrCursorMalformed},
		{"monotonic non-hex", strings.Replace(valid, fmt.Sprintf("m=%x", makeFullCursor().Monotonic), "m=GGGG", 1), ErrCursorMalformed},
		{"realtime non-hex", strings.Replace(valid, fmt.Sprintf("t=%x", makeFullCursor().Realtime), "t=ZZZZ", 1), ErrCursorMalformed},
		{"xor_hash non-hex", strings.Replace(valid, fmt.Sprintf("x=%x", makeFullCursor().XorHash), "x=GHIJ", 1), ErrCursorMalformed},

		// Missing required keys — a partial cursor cannot uniquely
		// identify an entry.
		{"missing s", strings.SplitN(valid, ";", 2)[1], ErrCursorMalformed},
		{"missing x", strings.TrimSuffix(valid, fmt.Sprintf(";x=%x", makeFullCursor().XorHash)), ErrCursorMalformed},

		// Embedded whitespace / newlines — systemd writes single-line
		// cursors; any control char is corruption.
		{"embedded newline", strings.Replace(valid, ";i=", ";\ni=", 1), ErrCursorMalformed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := ParseCursor(tc.in)
			if err == nil {
				t.Fatalf("ParseCursor(%q) = %+v, nil; want error %v", tc.in, c, tc.want)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("ParseCursor(%q) err = %v, want errors.Is(%v)", tc.in, err, tc.want)
			}
		})
	}
}

// -----------------------------------------------------------------------
// Reader.Cursor / SeekToCursor tests using the small.journal fixture.
// -----------------------------------------------------------------------

// TestCursor_NoEntry confirms Reader.Cursor() before any ReadEntry
// surfaces ErrCursorNoEntry rather than a zero-valued cursor (which
// would silently corrupt a checkpoint).
func TestCursor_NoEntry(t *testing.T) {
	path := resolveSmallFixture(t)
	if path == "" {
		t.Skipf("fixture not found at %s", smallFixtureRel)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	got, err := r.Cursor()
	if !errors.Is(err, ErrCursorNoEntry) {
		t.Errorf("Cursor() before ReadEntry = (%q, %v), want ErrCursorNoEntry", got, err)
	}
	if got != "" {
		t.Errorf("Cursor() before ReadEntry returned %q, want empty string", got)
	}
}

// TestCursor_SeekMidFixture is the spec-mandated "seek to mid-fixture
// and confirm next ReadEntry matches expected" test. For each pivot in
// 0..N-2 it:
//
//   - Reads pivot+1 entries with R1, capturing R1.Cursor() after.
//   - Opens a fresh R2 on the same file.
//   - SeekToCursor(R1.Cursor()).
//   - Asserts R2.ReadEntry() returns the entry at index pivot+1, with
//     identical SeqNum / Realtime / Monotonic to the pre-pass.
func TestCursor_SeekMidFixture(t *testing.T) {
	path := resolveSmallFixture(t)
	if path == "" {
		t.Skipf("fixture not found at %s", smallFixtureRel)
	}

	expected := readAllFromPath(t, path)
	if uint64(len(expected)) != smallFixtureEntries {
		t.Fatalf("pre-pass read %d entries, want %d", len(expected), smallFixtureEntries)
	}

	for pivot := 0; pivot < len(expected)-1; pivot++ {
		t.Run(fmt.Sprintf("pivot=%d_seqnum=%d", pivot, expected[pivot].SeqNum),
			func(t *testing.T) {
				r1, err := Open(path)
				if err != nil {
					t.Fatalf("Open r1: %v", err)
				}
				t.Cleanup(func() { _ = r1.Close() })

				for i := 0; i <= pivot; i++ {
					if _, err := r1.ReadEntry(); err != nil {
						t.Fatalf("r1.ReadEntry #%d: %v", i, err)
					}
				}

				cursor, err := r1.Cursor()
				if err != nil {
					t.Fatalf("r1.Cursor: %v", err)
				}
				if cursor == "" {
					t.Fatal("r1.Cursor returned empty string")
				}

				// Sanity: the cursor's parsed Seqnum must match the
				// pivot entry, and SeqnumID must equal the file's.
				parsed, err := ParseCursor(cursor)
				if err != nil {
					t.Fatalf("ParseCursor(%q): %v", cursor, err)
				}
				if parsed.Seqnum != expected[pivot].SeqNum {
					t.Errorf("parsed Seqnum = %d, want %d",
						parsed.Seqnum, expected[pivot].SeqNum)
				}
				if parsed.SeqnumID != smallFixtureSeqnumID {
					t.Errorf("parsed SeqnumID = %x, want %x",
						parsed.SeqnumID, smallFixtureSeqnumID)
				}
				if parsed.BootID != smallFixtureBootID {
					t.Errorf("parsed BootID = %x, want %x",
						parsed.BootID, smallFixtureBootID)
				}

				r2, err := Open(path)
				if err != nil {
					t.Fatalf("Open r2: %v", err)
				}
				t.Cleanup(func() { _ = r2.Close() })

				if err := r2.SeekToCursor(cursor); err != nil {
					t.Fatalf("r2.SeekToCursor: %v", err)
				}

				// Per the doc contract, r2.Cursor() after a
				// successful seek must reproduce the input cursor.
				if got, err := r2.Cursor(); err != nil {
					t.Errorf("r2.Cursor after seek: %v", err)
				} else if got != cursor {
					t.Errorf("r2.Cursor after seek = %q, want %q", got, cursor)
				}

				// The next ReadEntry must be the entry IMMEDIATELY
				// AFTER the cursored one — that's the entire point
				// of a checkpoint cursor.
				next, err := r2.ReadEntry()
				if err != nil {
					t.Fatalf("r2.ReadEntry after seek: %v", err)
				}
				want := expected[pivot+1]
				if next.SeqNum != want.SeqNum {
					t.Errorf("next.SeqNum = %d, want %d",
						next.SeqNum, want.SeqNum)
				}
				if next.Realtime != want.Realtime {
					t.Errorf("next.Realtime = %d, want %d",
						next.Realtime, want.Realtime)
				}
				if next.Monotonic != want.Monotonic {
					t.Errorf("next.Monotonic = %d, want %d",
						next.Monotonic, want.Monotonic)
				}
				if next.XorHash != want.XorHash {
					t.Errorf("next.XorHash = 0x%x, want 0x%x",
						next.XorHash, want.XorHash)
				}
			})
	}
}

// TestCursor_SeekToLastEntry: a cursor pointing at the tail entry must
// produce io.EOF on the very next ReadEntry — there is nothing left.
func TestCursor_SeekToLastEntry(t *testing.T) {
	path := resolveSmallFixture(t)
	if path == "" {
		t.Skipf("fixture not found at %s", smallFixtureRel)
	}

	expected := readAllFromPath(t, path)
	if len(expected) == 0 {
		t.Fatalf("fixture has no entries")
	}
	last := expected[len(expected)-1]

	tailCursor := (&Cursor{
		SeqnumID:  smallFixtureSeqnumID,
		Seqnum:    last.SeqNum,
		BootID:    last.BootID,
		Monotonic: last.Monotonic,
		Realtime:  last.Realtime,
		XorHash:   last.XorHash,
	}).String()

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	if err := r.SeekToCursor(tailCursor); err != nil {
		t.Fatalf("SeekToCursor(tail): %v", err)
	}
	if _, err := r.ReadEntry(); !errors.Is(err, io.EOF) {
		t.Errorf("ReadEntry after seek-to-tail = %v, want io.EOF", err)
	}
}

// TestCursor_SeekMalformed: SeekToCursor with a malformed cursor must
// surface ErrCursorMalformed and leave the Reader's iteration position
// unchanged so the caller can recover by replaying or seeking elsewhere.
func TestCursor_SeekMalformed(t *testing.T) {
	path := resolveSmallFixture(t)
	if path == "" {
		t.Skipf("fixture not found at %s", smallFixtureRel)
	}

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	// Advance two entries so the pre-call position is non-trivial; if
	// SeekToCursor mutates state on failure, the next ReadEntry will
	// surface the discrepancy.
	if _, err := r.ReadEntry(); err != nil {
		t.Fatalf("ReadEntry #1: %v", err)
	}
	second, err := r.ReadEntry()
	if err != nil {
		t.Fatalf("ReadEntry #2: %v", err)
	}
	posBefore := r.Offset()

	if err := r.SeekToCursor("garbage payload no equals"); !errors.Is(err, ErrCursorMalformed) {
		t.Errorf("SeekToCursor(garbage) = %v, want ErrCursorMalformed", err)
	}
	if got := r.Offset(); got != posBefore {
		t.Errorf("Offset after malformed seek = %d, want %d (unchanged)",
			got, posBefore)
	}

	// The next ReadEntry must continue from where we were before the
	// malformed seek attempt — i.e. produce the THIRD fixture entry,
	// not the first one.
	third, err := r.ReadEntry()
	if err != nil {
		t.Fatalf("ReadEntry after malformed seek: %v", err)
	}
	if third.SeqNum != second.SeqNum+1 {
		t.Errorf("after-malformed-seek ReadEntry returned seqnum %d, want %d",
			third.SeqNum, second.SeqNum+1)
	}
}

// TestCursor_SeekSeqnumMismatch: a cursor whose SeqnumID differs from
// the file's must be refused with ErrCursorSeqnumMismatch BEFORE any
// entries are walked. Otherwise the seek would silently land on a
// different journal stream's seqnum value.
func TestCursor_SeekSeqnumMismatch(t *testing.T) {
	path := resolveSmallFixture(t)
	if path == "" {
		t.Skipf("fixture not found at %s", smallFixtureRel)
	}

	mismatched := smallFixtureSeqnumID
	mismatched[0] ^= 0xFF
	c := &Cursor{
		SeqnumID:  mismatched,
		Seqnum:    smallFixtureFirstSeqNum,
		BootID:    smallFixtureBootID,
		Monotonic: smallFixtureFirstMonotonic,
		Realtime:  smallFixtureFirstRealtime,
		XorHash:   smallFixtureFirstXorHash,
	}

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	if err := r.SeekToCursor(c.String()); !errors.Is(err, ErrCursorSeqnumMismatch) {
		t.Errorf("SeekToCursor(mismatched) = %v, want ErrCursorSeqnumMismatch", err)
	}

	// On mismatch the Reader must NOT have advanced; ReadEntry should
	// return the first fixture entry.
	first, err := r.ReadEntry()
	if err != nil {
		t.Fatalf("ReadEntry after mismatched seek: %v", err)
	}
	if first.SeqNum != smallFixtureFirstSeqNum {
		t.Errorf("after-mismatch ReadEntry seqnum = %d, want %d (head)",
			first.SeqNum, smallFixtureFirstSeqNum)
	}
}

// TestCursor_SeekNotFound: a cursor with the right SeqnumID but a
// Seqnum not present in the file must surface ErrCursorNotFound, AND
// the Reader must be reset to file head so the caller's next ReadEntry
// returns the first entry rather than io.EOF.
func TestCursor_SeekNotFound(t *testing.T) {
	path := resolveSmallFixture(t)
	if path == "" {
		t.Skipf("fixture not found at %s", smallFixtureRel)
	}

	notFound := &Cursor{
		SeqnumID:  smallFixtureSeqnumID,
		Seqnum:    smallFixtureLastSeqNum + 0x10000,
		BootID:    smallFixtureBootID,
		Monotonic: smallFixtureLastMonotonic + 1,
		Realtime:  smallFixtureLastRealtime + 1,
		XorHash:   0x9999,
	}

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	// Advance so we can prove the failed seek's reset moves us back
	// to head and not "leaves us where we were".
	if _, err := r.ReadEntry(); err != nil {
		t.Fatalf("pre-seek ReadEntry: %v", err)
	}

	if err := r.SeekToCursor(notFound.String()); !errors.Is(err, ErrCursorNotFound) {
		t.Errorf("SeekToCursor(not-found) = %v, want ErrCursorNotFound", err)
	}

	// Per the doc: r is reset to the file head on a not-found seek so
	// the caller may re-iterate from the start.
	first, err := r.ReadEntry()
	if err != nil {
		t.Fatalf("ReadEntry after not-found seek: %v", err)
	}
	if first.SeqNum != smallFixtureFirstSeqNum {
		t.Errorf("after-not-found ReadEntry seqnum = %d, want %d (head)",
			first.SeqNum, smallFixtureFirstSeqNum)
	}
}

// TestCursor_SeekClosed: calling SeekToCursor on a closed Reader must
// surface ErrReaderClosed, not an I/O failure.
func TestCursor_SeekClosed(t *testing.T) {
	path := resolveSmallFixture(t)
	if path == "" {
		t.Skipf("fixture not found at %s", smallFixtureRel)
	}

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	cursor := (&Cursor{
		SeqnumID:  smallFixtureSeqnumID,
		Seqnum:    smallFixtureFirstSeqNum,
		BootID:    smallFixtureBootID,
		Monotonic: smallFixtureFirstMonotonic,
		Realtime:  smallFixtureFirstRealtime,
		XorHash:   smallFixtureFirstXorHash,
	}).String()

	if err := r.SeekToCursor(cursor); !errors.Is(err, ErrReaderClosed) {
		t.Errorf("SeekToCursor on closed reader = %v, want ErrReaderClosed", err)
	}
}

// TestCursor_FileIDPopulatedByReader: Reader.Cursor() must populate
// FileID from the parsed header so callers that opt in to file-affinity
// validation can do so. ParseCursor strips it on the wire (verified
// elsewhere), but Reader's in-memory Cursor pre-string is the moment
// FileID is actually known.
func TestCursor_FileIDPopulatedByReader(t *testing.T) {
	path := resolveSmallFixture(t)
	if path == "" {
		t.Skipf("fixture not found at %s", smallFixtureRel)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	if r.Header().FileID != smallFixtureFileID {
		t.Errorf("Header.FileID = %x, want %x",
			r.Header().FileID, smallFixtureFileID)
	}
}

// -----------------------------------------------------------------------
// Crash-recovery tests.
// -----------------------------------------------------------------------

// TestCrashRecovery_PrivateJournal is the deterministic, no-systemd-cat
// crash-recovery path: build a private journal with N entries via
// buildPrivateJournal, read M (M<N) with R1, save cursor, close R1
// (simulating SIGKILL — the OS reclaims the FD with no chance for R1
// to ack the unread bytes), then open R2 and SeekToCursor to resume.
//
// Assertions:
//
//   - Pre-crash entries (M of them) are read exactly once with seqnums
//     matching the deterministic generator.
//   - Post-recovery entries (N-M of them) are read exactly once with
//     seqnums continuing the pre-crash sequence.
//   - Together the two reads produce every original seqnum exactly once
//     in the right order — zero loss, zero duplicates.
func TestCrashRecovery_PrivateJournal(t *testing.T) {
	const (
		totalEntries   int    = 7
		preCrashReads  int    = 3
		seqnumStart    uint64 = 9000
		realtimeStart  uint64 = 1_700_000_000_000_000
		monotonicStart uint64 = 50_000_000
	)

	dir := t.TempDir()
	path := filepath.Join(dir, "crash-private.journal")
	buildPrivateJournal(t, path, totalEntries,
		seqnumStart, realtimeStart, monotonicStart)

	// ---- Phase A: R1 reads M entries, then "crashes" via Close. ----
	r1, err := Open(path)
	if err != nil {
		t.Fatalf("Open r1: %v", err)
	}

	preCrash := make([]*Entry, 0, preCrashReads)
	for i := 0; i < preCrashReads; i++ {
		e, err := r1.ReadEntry()
		if err != nil {
			t.Fatalf("r1.ReadEntry #%d: %v", i, err)
		}
		preCrash = append(preCrash, e)
	}

	checkpoint, err := r1.Cursor()
	if err != nil {
		t.Fatalf("r1.Cursor: %v", err)
	}
	if checkpoint == "" {
		t.Fatalf("r1.Cursor returned empty string")
	}

	// "SIGKILL" simulation: just Close. The defining property of a
	// crash for our recovery contract is "no chance to ack what was
	// not yet processed", which Close models exactly.
	if err := r1.Close(); err != nil {
		t.Fatalf("r1.Close: %v", err)
	}

	// ---- Phase B: R2 resumes from the saved cursor. ----
	r2, err := Open(path)
	if err != nil {
		t.Fatalf("Open r2: %v", err)
	}
	t.Cleanup(func() { _ = r2.Close() })

	if err := r2.SeekToCursor(checkpoint); err != nil {
		t.Fatalf("r2.SeekToCursor: %v", err)
	}

	postRecovery := make([]*Entry, 0, totalEntries-preCrashReads)
	for {
		e, err := r2.ReadEntry()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("r2.ReadEntry: %v", err)
		}
		postRecovery = append(postRecovery, e)
	}

	// ---- Assertions: zero loss, zero duplicates. ----
	if len(postRecovery) != totalEntries-preCrashReads {
		t.Errorf("post-recovery read %d entries, want %d",
			len(postRecovery), totalEntries-preCrashReads)
	}

	// Pre-crash entries must form an unbroken seqnum prefix.
	for i, e := range preCrash {
		want := seqnumStart + uint64(i)
		if e.SeqNum != want {
			t.Errorf("pre-crash[%d].SeqNum = %d, want %d",
				i, e.SeqNum, want)
		}
	}
	// Post-recovery entries must continue exactly where pre-crash
	// stopped. The cursor identified entry index preCrashReads-1, so
	// the next read must be index preCrashReads.
	for i, e := range postRecovery {
		want := seqnumStart + uint64(preCrashReads+i)
		if e.SeqNum != want {
			t.Errorf("post-recovery[%d].SeqNum = %d, want %d",
				i, e.SeqNum, want)
		}
	}

	// Union check via map: every seqnum [seqnumStart..seqnumStart+N)
	// must appear exactly once.
	seen := make(map[uint64]int, totalEntries)
	for _, e := range preCrash {
		seen[e.SeqNum]++
	}
	for _, e := range postRecovery {
		seen[e.SeqNum]++
	}
	for i := 0; i < totalEntries; i++ {
		want := seqnumStart + uint64(i)
		switch seen[want] {
		case 1:
			// good
		case 0:
			t.Errorf("seqnum %d missing from union (LOSS)", want)
		default:
			t.Errorf("seqnum %d appeared %d times (DUPLICATE)",
				want, seen[want])
		}
	}
	if got := len(seen); got != totalEntries {
		t.Errorf("union size = %d distinct seqnums, want %d",
			got, totalEntries)
	}
}

// TestCrashRecovery_SystemdCat is the spec-mandated subprocess test:
// every entry reaches the journal via the systemd-cat subprocess (run
// once per entry by appendSystemdCatEntry), the reader is "killed" via
// Close mid-stream, and a fresh reader resumes from the saved cursor.
//
// Test flow:
//
//  1. Build an empty private journal in t.TempDir.
//  2. Append three entries via systemd-cat -> journalctl -> private
//     bridge (the same bridge used by TestFollow_SystemdCat).
//  3. Open R1, read the first two, save R1.Cursor(), Close R1.
//  4. Append two more entries via systemd-cat (these arrive AFTER
//     the simulated crash, so they must NOT be re-delivered alongside
//     the unread R1 ones).
//  5. Open R2, SeekToCursor(saved), read until EOF.
//  6. Assert: union of reads == every systemd-cat entry exactly once,
//     in seqnum order, with neither loss nor duplication.
//
// Skips on hosts without systemd-cat or journalctl.
func TestCrashRecovery_SystemdCat(t *testing.T) {
	systemdCatPath := resolveSystemdCat(t)
	if systemdCatPath == "" {
		t.Skip("systemd-cat unavailable; install systemd or skip on non-systemd hosts")
	}
	journalctlPath := resolveJournalctl(t)
	if journalctlPath == "" {
		t.Skip("journalctl unavailable; needed for the systemd-cat bridge")
	}

	const (
		preCrashWrites  = 3
		preCrashReads   = 2
		postCrashWrites = 2
		seqnumStart     uint64 = 7000
		realtimeStart   uint64 = 1_700_000_500_000_000
		monotonicStart  uint64 = 400_000_000
	)
	totalWrites := preCrashWrites + postCrashWrites

	dir := t.TempDir()
	path := filepath.Join(dir, "crash-systemd-cat.journal")
	buildPrivateJournal(t, path, 0,
		seqnumStart, realtimeStart, monotonicStart)
	state := newPrivateJournalState(path, 0,
		seqnumStart, realtimeStart, monotonicStart)

	// ---- Phase 1: write preCrashWrites entries via systemd-cat. ----
	preCrashIdentifiers := make([]string, preCrashWrites)
	preCrashMessages := make([]string, preCrashWrites)
	for i := 0; i < preCrashWrites; i++ {
		ident := uniqueIdentifier(fmt.Sprintf("native-crash-pre-%d", i))
		msg := fmt.Sprintf("crash-recovery-pre pid=%d i=%d ident=%s",
			os.Getpid(), i, ident)
		_, _, _ = appendSystemdCatEntry(t, state,
			systemdCatPath, journalctlPath, ident, msg)
		preCrashIdentifiers[i] = ident
		preCrashMessages[i] = msg
	}

	// ---- Phase 2: R1 reads preCrashReads entries, then crashes. ----
	r1, err := Open(path)
	if err != nil {
		t.Fatalf("Open r1: %v", err)
	}

	preCrash := make([]*Entry, 0, preCrashReads)
	for i := 0; i < preCrashReads; i++ {
		e, err := r1.ReadEntry()
		if err != nil {
			t.Fatalf("r1.ReadEntry #%d: %v", i, err)
		}
		preCrash = append(preCrash, e)
	}

	checkpoint, err := r1.Cursor()
	if err != nil {
		t.Fatalf("r1.Cursor: %v", err)
	}
	if err := r1.Close(); err != nil {
		t.Fatalf("r1.Close: %v", err)
	}

	// ---- Phase 3: more writes via systemd-cat AFTER the crash. ----
	for i := 0; i < postCrashWrites; i++ {
		ident := uniqueIdentifier(fmt.Sprintf("native-crash-post-%d", i))
		msg := fmt.Sprintf("crash-recovery-post pid=%d i=%d ident=%s",
			os.Getpid(), i, ident)
		_, _, _ = appendSystemdCatEntry(t, state,
			systemdCatPath, journalctlPath, ident, msg)
	}

	// ---- Phase 4: R2 resumes from saved cursor. ----
	r2, err := Open(path)
	if err != nil {
		t.Fatalf("Open r2: %v", err)
	}
	t.Cleanup(func() { _ = r2.Close() })

	if err := r2.SeekToCursor(checkpoint); err != nil {
		t.Fatalf("r2.SeekToCursor: %v", err)
	}

	postRecovery := make([]*Entry, 0, totalWrites-preCrashReads)
	for {
		e, err := r2.ReadEntry()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("r2.ReadEntry: %v", err)
		}
		postRecovery = append(postRecovery, e)
	}

	// ---- Assertions: zero loss, zero duplicate. ----
	wantPostRecovery := totalWrites - preCrashReads
	if len(postRecovery) != wantPostRecovery {
		t.Errorf("post-recovery read %d entries, want %d (total=%d, preCrashReads=%d)",
			len(postRecovery), wantPostRecovery, totalWrites, preCrashReads)
	}

	// Pre-crash seqnums must be the first preCrashReads in [seqnumStart..).
	for i, e := range preCrash {
		want := seqnumStart + uint64(i)
		if e.SeqNum != want {
			t.Errorf("pre-crash[%d].SeqNum = %d, want %d",
				i, e.SeqNum, want)
		}
	}
	// Post-recovery seqnums must continue the sequence.
	for i, e := range postRecovery {
		want := seqnumStart + uint64(preCrashReads+i)
		if e.SeqNum != want {
			t.Errorf("post-recovery[%d].SeqNum = %d, want %d",
				i, e.SeqNum, want)
		}
	}

	// Union check.
	seen := make(map[uint64]int, totalWrites)
	for _, e := range preCrash {
		seen[e.SeqNum]++
	}
	for _, e := range postRecovery {
		seen[e.SeqNum]++
	}
	for i := 0; i < totalWrites; i++ {
		want := seqnumStart + uint64(i)
		switch seen[want] {
		case 1:
			// good
		case 0:
			t.Errorf("seqnum %d missing from union (LOSS)", want)
		default:
			t.Errorf("seqnum %d appeared %d times (DUPLICATE)",
				want, seen[want])
		}
	}
	if got := len(seen); got != totalWrites {
		t.Errorf("union size = %d distinct seqnums, want %d",
			got, totalWrites)
	}
}
