// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0
//
//go:build ignore
// +build ignore

// Generator for receiver/journaldreceiver/testdata/native/small.journal.
//
// One-shot tool that emits a deterministic, valid systemd journal binary
// containing 5 ENTRY objects in non-compact layout (no DATA, hash tables
// zero-filled). The Reader API only consumes ENTRY objects via a linear
// ParseObjectHeader scan, so DATA absence is fine for reader-level tests
// — Reader.ReadEntry never dereferences EntryItem.ObjectOffset, only
// ReadDataField does, and that path is exercised by entry_test.go using
// in-memory buffers.
//
// The fixture is intentionally tiny (~700 bytes) so it can be checked into
// git without bloating the repo. Per the implementation plan, fixtures
// must stay ≤500 KB each and ≤5 MB total under testdata/native/.
//
// Regeneration:
//
//	cd receiver/journaldreceiver/testdata/native
//	CGO_ENABLED=0 go run generate/gen_small_journal.go small.journal
//
// The output bytes are deterministic — running the generator should
// produce a byte-identical file unless this source is modified.
package main

import (
	"encoding/binary"
	"fmt"
	"os"
)

const (
	headerSize          uint64 = 224
	objectHeaderSize    uint64 = 16
	entryFixedSize      uint64 = 48
	entryItemSize       uint64 = 16 // non-compact: offset(8) + hash(8)
	objectAlignment     uint64 = 8
	objectTypeEntry     byte   = 3
	headerStateOnline   byte   = 1
	smallJournalEntries        = 5
	seqnumStart                = 1000
	realtimeStartUS            = 1_700_000_000_000_000 // 2023-11-14 22:13:20 UTC
	intervalUS                 = 1_000_000             // 1 s between entries
	monotonicStartUS           = 100_000_000           // 100 s since boot
)

var signature = [8]byte{'L', 'P', 'K', 'S', 'H', 'H', 'R', 'H'}

var bootID = [16]byte{
	0xC0, 0xC1, 0xC2, 0xC3, 0xC4, 0xC5, 0xC6, 0xC7,
	0xC8, 0xC9, 0xCA, 0xCB, 0xCC, 0xCD, 0xCE, 0xCF,
}

func makeEntryObject(seq, realtime, monotonic, xorHash uint64, items [][2]uint64) []byte {
	totalSize := objectHeaderSize + entryFixedSize + uint64(len(items))*entryItemSize
	buf := make([]byte, totalSize)
	buf[0] = objectTypeEntry
	le := binary.LittleEndian
	le.PutUint64(buf[8:16], totalSize)
	le.PutUint64(buf[16:24], seq)
	le.PutUint64(buf[24:32], realtime)
	le.PutUint64(buf[32:40], monotonic)
	copy(buf[40:56], bootID[:])
	le.PutUint64(buf[56:64], xorHash)
	pos := uint64(64)
	for _, it := range items {
		le.PutUint64(buf[pos:pos+8], it[0])
		le.PutUint64(buf[pos+8:pos+16], it[1])
		pos += 16
	}
	return buf
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gen_small_journal <output_path>")
		os.Exit(2)
	}
	out := os.Args[1]

	// Build entries first so we know arena_size.
	var entryBytes []byte
	var firstSeq, lastSeq, firstRT, lastRT uint64
	// lastObjectOffset tracks the absolute file offset (header-relative) of
	// the most-recently-appended ENTRY object. systemd records this in the
	// header's tail_object_offset field; the Reader's linear scan uses it
	// to know where real objects end (everything past it on an active
	// journal is preallocated zeros). Earlier revisions of this generator
	// incorrectly set tail_object_offset = headerSize (the FIRST object),
	// which both misrepresented real journals and masked the active-journal
	// zero-tail bug found on real AL2023 hosts.
	var lastObjectOffset uint64
	for i := 0; i < smallJournalEntries; i++ {
		seq := uint64(seqnumStart + i)
		rt := uint64(realtimeStartUS + i*intervalUS)
		mt := uint64(monotonicStartUS + i*intervalUS)
		// Two items per entry; offsets are deliberately bogus (no DATA
		// objects in the file) — Reader.ReadEntry never dereferences
		// them.
		items := [][2]uint64{
			{0x4000 + uint64(i)*64, 0xAA00 + uint64(i)},
			{0x8000 + uint64(i)*64, 0xBB00 + uint64(i)},
		}
		// Offset of THIS entry = headerSize + bytes emitted so far.
		lastObjectOffset = headerSize + uint64(len(entryBytes))
		entryBytes = append(entryBytes, makeEntryObject(seq, rt, mt, 0xDEADBEEF+uint64(i), items)...)
		if i == 0 {
			firstSeq = seq
			firstRT = rt
		}
		lastSeq = seq
		lastRT = rt
	}
	arenaSize := uint64(len(entryBytes))

	buf := make([]byte, headerSize+arenaSize)
	copy(buf[0:8], signature[:])
	le := binary.LittleEndian
	le.PutUint32(buf[8:12], 0)  // CompatibleFlags
	le.PutUint32(buf[12:16], 0) // IncompatibleFlags (non-compact, no compression)
	buf[16] = headerStateOnline
	for i := 0; i < 16; i++ {
		buf[24+i] = byte(0xA0 + i) // FileID
		buf[40+i] = byte(0xB0 + i) // MachineID
		buf[56+i] = bootID[i]      // BootID
		buf[72+i] = byte(0xD0 + i) // SeqnumID
	}
	le.PutUint64(buf[88:96], headerSize)            // HeaderSize
	le.PutUint64(buf[96:104], arenaSize)            // ArenaSize
	le.PutUint64(buf[104:112], 0)                   // DataHashTableOffset
	le.PutUint64(buf[112:120], 0)                   // DataHashTableSize
	le.PutUint64(buf[120:128], 0)                   // FieldHashTableOffset
	le.PutUint64(buf[128:136], 0)                   // FieldHashTableSize
	le.PutUint64(buf[136:144], lastObjectOffset)    // TailObjectOffset (last ENTRY)
	le.PutUint64(buf[144:152], smallJournalEntries) // NObjects
	le.PutUint64(buf[152:160], smallJournalEntries) // NEntries
	le.PutUint64(buf[160:168], lastSeq)             // TailEntrySeqnum
	le.PutUint64(buf[168:176], firstSeq)            // HeadEntrySeqnum
	le.PutUint64(buf[176:184], 0)                   // EntryArrayOffset
	le.PutUint64(buf[184:192], firstRT)             // HeadEntryRealtime
	le.PutUint64(buf[192:200], lastRT)              // TailEntryRealtime
	le.PutUint64(buf[200:208], monotonicStartUS+(smallJournalEntries-1)*intervalUS)
	le.PutUint64(buf[208:216], 0) // NData
	le.PutUint64(buf[216:224], 0) // NFields

	copy(buf[headerSize:], entryBytes)

	if err := os.WriteFile(out, buf, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", out, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d bytes, %d entries, arena=%d)\n",
		out, len(buf), smallJournalEntries, arenaSize)
}
