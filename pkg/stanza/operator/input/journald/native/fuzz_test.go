// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package native

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// readSmallFixtureBytes returns the bytes of the committed small.journal
// fixture for use as a fuzz seed corpus, or nil when the fixture is not
// resolvable (CI sandboxes that don't ship receiver/journaldreceiver).
//
// Returning nil instead of skipping lets the fuzz harness still run with
// the synthetic seeds the each Fuzz* function adds; we only lose the
// real-world corpus diversity, not the fuzz coverage itself.
func readSmallFixtureBytes(tb testing.TB) []byte {
	tb.Helper()
	abs, err := filepath.Abs(smallFixtureRel)
	if err != nil {
		return nil
	}
	if _, err := os.Stat(abs); err != nil {
		return nil
	}
	data, err := os.ReadFile(abs) //#nosec G304 -- test-only path under repo workspace.
	if err != nil {
		return nil
	}
	return data
}

// makeMinHeaderBytes returns the smallest plausibly-valid header buffer
// (just signature + a zero-padded MinHeaderSize) so the fuzzer has a seed
// that nearly passes ParseHeader. Mutations from this seed exercise the
// flag-validation and size-arithmetic branches.
func makeMinHeaderBytes() []byte {
	buf := make([]byte, MinHeaderSize)
	copy(buf[0:8], Signature[:])
	// header_size at offset 88 must equal len(buf) for the header to
	// look declared-correct; the rest stays zero.
	buf[88] = byte(MinHeaderSize)
	buf[89] = byte(MinHeaderSize >> 8)
	return buf
}

// FuzzParseHeader exercises the file-header parser with arbitrary bytes.
// The contract under fuzz is: ParseHeader must never panic, regardless of
// signature, declared sizes, or feature flags. Returning an error is
// fine; crashing the process is not.
//
// Run: go test -run=^$ -fuzz=FuzzParseHeader -fuzztime=10s
//
//	./pkg/stanza/operator/input/journald/native/.
func FuzzParseHeader(f *testing.F) {
	// Seed 1: empty input -- exercises short-read path.
	f.Add([]byte{})
	// Seed 2: bare signature, nothing else -- exercises the
	// signature-vs-min-size branch.
	f.Add(append([]byte{}, Signature[:]...))
	// Seed 3: minimal would-be-valid header with zero flags.
	f.Add(makeMinHeaderBytes())
	// Seed 4: real journal bytes if the fixture is available; the
	// fixture is small enough (704B) to hand to the fuzzer cheaply.
	if real := readSmallFixtureBytes(f); len(real) > 0 {
		f.Add(real)
		// Also seed with just the header region of the real fixture
		// so the fuzzer has a clean header without arena bytes.
		if len(real) >= int(MaxHeaderSize) {
			f.Add(append([]byte{}, real[:MaxHeaderSize]...))
		}
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// bytes.Reader implements io.ReaderAt without touching disk.
		// ParseHeader's only mandate under fuzz: do not panic.
		_, err := ParseHeader(bytes.NewReader(data))
		// Allow any error; only panics fail. Reference err so the
		// compiler does not warn about unused values when assertions
		// are removed.
		_ = err
	})
}

// FuzzParseObject exercises the common-object-header parser. The parser
// is the single hottest pinch-point in the linear ENTRY scan: every
// object the Reader skips passes through ParseObjectHeader exactly once.
// A panic here would crash the agent on any malformed journal file.
//
// Run: go test -run=^$ -fuzz=FuzzParseObject -fuzztime=10s
//
//	./pkg/stanza/operator/input/journald/native/.
func FuzzParseObject(f *testing.F) {
	// Seed 1: empty -- exercises short-read.
	f.Add([]byte{})
	// Seed 2: 16 zero bytes -- type=UNUSED, flags=0, size=0
	// (which violates ErrObjectTooSmall, exercising the size guard).
	f.Add(make([]byte, ObjectHeaderSize))
	// Seed 3: minimal valid object header (DATA, size=ObjectHeaderSize).
	f.Add(makeObjectHeaderBytes(ObjectData, 0, ObjectHeaderSize))
	// Seed 4: ENTRY header with a plausible body size. Lets the fuzzer
	// mutate around the type byte and size word.
	f.Add(makeObjectHeaderBytes(ObjectEntry, 0, ObjectHeaderSize+EntryFixedSize))
	// Seed 5: object with a deliberately giant size, to exercise the
	// arithmetic in NextOffset/PayloadSize derivations.
	f.Add(makeObjectHeaderBytes(ObjectData, ObjectCompressedZSTD, ^uint64(0)))
	// Seed 6: real-fixture object bytes (skip past the 240-byte header
	// to land on the first object) when available.
	if real := readSmallFixtureBytes(f); len(real) > 240 {
		f.Add(append([]byte{}, real[240:]...))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// Always parse from offset 0 of the supplied data. The offset
		// argument is alignment-checked, but 0 is always aligned, so
		// every fuzz input is exercising header decoding rather than
		// the offset guard. Misaligned-offset coverage is exercised
		// in object_test.go's deterministic tests.
		_, err := ParseObjectHeader(bytes.NewReader(data), 0)
		_ = err
	})
}

// FuzzReadEntry exercises the end-to-end Reader: Open + repeated
// ReadEntry until EOF or error. Open requires a real *os.File because
// it Stat()s the file, so each fuzz iteration writes the candidate bytes
// to a temp file under t.TempDir().
//
// The contract is the same as the other Fuzz* funcs: arbitrary bytes
// must not panic. Errors (including ErrInvalidSignature, header guards,
// arena overflow, parse failures) are all acceptable outcomes; the
// fuzzer is hunting for crashes, slice-out-of-range, integer overflow,
// and infinite loops.
//
// Run: go test -run=^$ -fuzz=FuzzReadEntry -fuzztime=10s
//
//	./pkg/stanza/operator/input/journald/native/.
func FuzzReadEntry(f *testing.F) {
	// Seed 1: empty -- exercises Open's stat success but ParseHeader
	// short-read failure path.
	f.Add([]byte{})
	// Seed 2: minimal would-be-valid header, no arena. The Reader
	// should reach io.EOF on the first ReadEntry without crashing.
	f.Add(makeMinHeaderBytes())
	// Seed 3: real fixture bytes -- the most fertile seed since the
	// fuzzer can mutate it to produce realistic-shaped corruption.
	if real := readSmallFixtureBytes(f); len(real) > 0 {
		f.Add(real)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// One temp file per iteration; t.TempDir handles cleanup.
		dir := t.TempDir()
		path := filepath.Join(dir, "fuzz.journal")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write fuzz input: %v", err)
		}

		r, err := Open(path)
		if err != nil {
			// Open failures are acceptable; the contract is just
			// "no panic". Bail out cleanly.
			return
		}
		// Belt-and-suspenders: if Open returned no error but the
		// reader is somehow nil, the contract is broken.
		if r == nil {
			t.Fatalf("Open returned (nil, nil) for input len=%d", len(data))
		}

		// Cap iterations so a pathological input cannot livelock the
		// fuzzer. The arena is bounded by the on-disk file size, and
		// every successful ReadEntry advances cursor by at least
		// ObjectHeaderSize, so 1<<14 iterations is generous for any
		// fuzz input we'll realistically receive.
		const maxIter = 1 << 14
		for i := 0; i < maxIter; i++ {
			_, err := r.ReadEntry()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					// Any non-EOF error is acceptable; we
					// just stop iterating. Fall through to
					// Close.
				}
				break
			}
		}
		_ = r.Close()
	})
}
