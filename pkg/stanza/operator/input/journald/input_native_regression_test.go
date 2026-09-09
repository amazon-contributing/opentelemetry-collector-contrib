// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package journald

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/input/journald/native"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/testutil"
)

// TestNativeFollow_UnusableCursorHonorsStartAtEnd is the regression guard
// for the cursor-seek-failure defect: when a persisted cursor exists but
// SeekToCursor rejects it (rotation / archived entry / corrupt checkpoint)
// under start_at:end, followNativeFileOnce must honor start_at by draining
// the on-disk backlog rather than falling through to Follow with the Reader
// still at the file head — which replayed the ENTIRE file.
//
// The test seeds a well-formed cursor whose seqnum_id cannot match the
// fixture's stream (forcing ErrCursorSeqnumMismatch), runs the operator
// with start_at:end, and asserts zero downstream deliveries. Before the fix
// the whole fixture was re-emitted.
func TestNativeFollow_UnusableCursorHonorsStartAtEnd(t *testing.T) {
	// Copy the committed fixture to a writable temp path so the per-file
	// cursor key (nativeCursorKey(path)) matches the followed file.
	src := fixtureSmallJournal(t)
	data, err := os.ReadFile(src)
	require.NoError(t, err)
	dst := filepath.Join(t.TempDir(), "small.journal")
	require.NoError(t, os.WriteFile(dst, data, 0o600))

	// A structurally valid cursor whose seqnum_id is all 0xEE cannot match
	// the fixture's stream, so SeekToCursor returns ErrCursorSeqnumMismatch.
	badCursor := (&native.Cursor{
		SeqnumID: [16]byte{
			0xEE, 0xEE, 0xEE, 0xEE, 0xEE, 0xEE, 0xEE, 0xEE,
			0xEE, 0xEE, 0xEE, 0xEE, 0xEE, 0xEE, 0xEE, 0xEE,
		},
		Seqnum:    1,
		BootID:    [16]byte{0x01},
		Monotonic: 1,
		Realtime:  1,
		XorHash:   1,
	}).String()

	persister := testutil.NewUnscopedMockPersister()
	require.NoError(t, persister.Set(context.Background(), nativeCursorKey(dst), []byte(badCursor)))

	cfg := NewConfigWithID("native_unusable_cursor_start_at_end")
	cfg.OutputIDs = []string{"output"}
	cfg.Mode = ModeNative
	cfg.Files = []string{dst}
	cfg.StartAt = "end"

	set := componenttest.NewNopTelemetrySettings()
	op, err := cfg.Build(set)
	require.NoError(t, err)

	var processCalls int64
	mockOutput := testutil.NewMockOperator("output")
	mockOutput.On("Process", mock.Anything, mock.Anything).
		Run(func(mock.Arguments) { atomic.AddInt64(&processCalls, 1) }).
		Return(nil)
	require.NoError(t, op.SetOutputs([]operator.Operator{mockOutput}))

	require.NoError(t, op.Start(persister))
	t.Cleanup(func() { require.NoError(t, op.Stop()) })

	// The follower opens, fails the seek, honors start_at:end, and enters
	// the watch loop. With the bug the file replays almost immediately, so a
	// generous settle window makes any replay decisive.
	time.Sleep(1500 * time.Millisecond)
	assert.Equal(t, int64(0), atomic.LoadInt64(&processCalls),
		"start_at:end with an unusable persisted cursor must not replay on-disk entries")
}

// TestNativeFollow_RetryRedeliversWriteFailedEntry is the regression guard
// for the write-failure-retry defect: under start_at:end, an entry whose
// first Write fails before any cursor has been persisted must be
// redelivered on the backoff retry, not discarded by a second start_at:end
// backlog drain.
//
// The scenario mirrors the verified reproduction: build an initially-empty
// journal, start following with start_at:end, wait for the follower to
// reach the watch loop, then append exactly one entry. The mock output
// fails the FIRST Write and succeeds on every later call, so a correct
// implementation records >= 2 Write attempts (one failure + at least one
// redelivery). Before the fix the retry re-drained the appended entry and
// only one Write was ever attempted.
func TestNativeFollow_RetryRedeliversWriteFailedEntry(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "retry.journal")
	writeEmptyRegressionJournal(t, dst)

	cfg := NewConfigWithID("native_retry_redelivers")
	cfg.OutputIDs = []string{"output"}
	cfg.Mode = ModeNative
	cfg.Files = []string{dst}
	cfg.StartAt = "end"

	set := componenttest.NewNopTelemetrySettings()
	op, err := cfg.Build(set)
	require.NoError(t, err)

	var writeCount int64
	mockOutput := testutil.NewMockOperator("output")
	mockOutput.On("Process", mock.Anything, mock.Anything).
		Run(func(mock.Arguments) { atomic.AddInt64(&writeCount, 1) }).
		Return(errors.New("induced write failure")).Once()
	mockOutput.On("Process", mock.Anything, mock.Anything).
		Run(func(mock.Arguments) { atomic.AddInt64(&writeCount, 1) }).
		Return(nil)
	require.NoError(t, op.SetOutputs([]operator.Operator{mockOutput}))

	require.NoError(t, op.Start(testutil.NewUnscopedMockPersister()))
	t.Cleanup(func() { require.NoError(t, op.Stop()) })

	// Let the follower open the empty file and reach Follow's watch loop
	// before appending, so the appended entry is a live append (delivered
	// via Follow) rather than pre-existing backlog (discarded under
	// start_at:end on the first follow).
	time.Sleep(750 * time.Millisecond)
	appendRegressionEntry(t, dst)

	// The first Write fails, Follow aborts, followNativeFile backs off
	// nativeBackoff and re-opens. With the fix the retry suppresses the
	// start_at:end drain and redelivers the entry.
	deadline := time.Now().Add(nativeBackoff + 5*time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&writeCount) >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assert.GreaterOrEqual(t, atomic.LoadInt64(&writeCount), int64(2),
		"a Write-failed entry must be redelivered on retry, not discarded by the start_at:end re-drain")
}

// --- Synthetic journal builder for TestNativeFollow_RetryRedeliversWriteFailedEntry ---
//
// Mirrors the native package's buildPrivateJournal / appendPlainEntry
// (native/follow_test.go) using the native package's exported byte-layout
// constants, so this operator-side test can materialize an initially-empty
// journal and append a single ENTRY after Follow is watching. The entry
// carries placeholder item offsets with no backing DATA objects, which is
// sufficient here: emitNativeEntry logs and skips unresolved items but
// still delivers the entry (with __CURSOR / __MONOTONIC_TIMESTAMP).

// regressionHeaderSize is the minimum supported systemd header layout
// (systemd 187, 224 bytes), matching native.MinHeaderSize.
const regressionHeaderSize uint64 = 224

// regressionBootID is embedded in the synthesized entry; the value is
// arbitrary but distinct from the committed fixtures.
var regressionBootID = [16]byte{
	0xB0, 0xB1, 0xB2, 0xB3, 0xB4, 0xB5, 0xB6, 0xB7,
	0xB8, 0xB9, 0xBA, 0xBB, 0xBC, 0xBD, 0xBE, 0xBF,
}

// makeRegressionEntryBytes serializes one non-compact ENTRY object: the
// 16-byte common object header, the 48-byte fixed prefix, then 16-byte
// items (le64 object_offset + le64 hash).
func makeRegressionEntryBytes(seqnum, realtime, monotonic, xorHash uint64, items []native.EntryItem) []byte {
	total := native.ObjectHeaderSize + native.EntryFixedSize + uint64(len(items))*native.EntryItemSize
	buf := make([]byte, total)
	le := binary.LittleEndian
	buf[0] = byte(native.ObjectEntry)
	le.PutUint64(buf[8:16], total)
	le.PutUint64(buf[16:24], seqnum)
	le.PutUint64(buf[24:32], realtime)
	le.PutUint64(buf[32:40], monotonic)
	copy(buf[40:56], regressionBootID[:])
	le.PutUint64(buf[56:64], xorHash)
	pos := native.ObjectHeaderSize + native.EntryFixedSize
	for _, it := range items {
		le.PutUint64(buf[pos:pos+8], it.ObjectOffset)
		le.PutUint64(buf[pos+8:pos+16], it.Hash)
		pos += 16
	}
	return buf
}

// writeEmptyRegressionJournal writes a valid, entry-free journal header so
// native.Open succeeds and the first catch-up drain sees no backlog.
func writeEmptyRegressionJournal(t *testing.T, path string) {
	t.Helper()
	buf := make([]byte, regressionHeaderSize)
	le := binary.LittleEndian
	copy(buf[0:8], native.Signature[:])
	buf[16] = native.HeaderStateOnline
	for i := 0; i < 16; i++ {
		buf[24+i] = byte(0x10 + i) // FileID
		buf[40+i] = byte(0x20 + i) // MachineID
		buf[56+i] = regressionBootID[i]
		buf[72+i] = byte(0x30 + i) // SeqnumID
	}
	le.PutUint64(buf[88:96], regressionHeaderSize)   // HeaderSize
	le.PutUint64(buf[96:104], 0)                     // ArenaSize (empty)
	le.PutUint64(buf[136:144], regressionHeaderSize) // TailObjectOffset (== head)
	require.NoError(t, os.WriteFile(path, buf, 0o600))
}

// appendRegressionEntry appends one ENTRY object at the head of the arena
// and patches the header so Reader.refreshTail surfaces it. arena_size is
// written LAST so a follow consumer never observes a partially written
// entry (mirrors native/follow_test.go's append ordering).
func appendRegressionEntry(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	entryOffset := regressionHeaderSize // 224 is already 8-byte aligned
	const (
		seqnum    = uint64(1)
		realtime  = uint64(1_700_000_000_000_000) // 2023-era microseconds
		monotonic = uint64(1_000_000)
		xorHash   = uint64(0xDEADBEEF)
	)
	items := []native.EntryItem{
		{ObjectOffset: 0x4000, Hash: 0xAA},
		{ObjectOffset: 0x8000, Hash: 0xBB},
	}
	entryBytes := makeRegressionEntryBytes(seqnum, realtime, monotonic, xorHash, items)
	_, err = f.WriteAt(entryBytes, int64(entryOffset))
	require.NoError(t, err)

	newArenaSize := uint64(len(entryBytes))
	le := binary.LittleEndian
	patch := make([]byte, 8)

	le.PutUint64(patch, entryOffset) // tail_object_offset @136
	_, err = f.WriteAt(patch, 136)
	require.NoError(t, err)
	le.PutUint64(patch, 1) // n_objects @144
	_, err = f.WriteAt(patch, 144)
	require.NoError(t, err)
	le.PutUint64(patch, 1) // n_entries @152
	_, err = f.WriteAt(patch, 152)
	require.NoError(t, err)
	le.PutUint64(patch, seqnum) // tail_entry_seqnum @160
	_, err = f.WriteAt(patch, 160)
	require.NoError(t, err)
	le.PutUint64(patch, realtime) // tail_entry_realtime @192
	_, err = f.WriteAt(patch, 192)
	require.NoError(t, err)
	le.PutUint64(patch, monotonic) // tail_entry_monotonic @200
	_, err = f.WriteAt(patch, 200)
	require.NoError(t, err)
	le.PutUint64(patch, newArenaSize) // arena_size @96 — written LAST
	_, err = f.WriteAt(patch, 96)
	require.NoError(t, err)

	require.NoError(t, f.Sync())
}
