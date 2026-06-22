// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package native // import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/input/journald/native"

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/ulikunitz/xz"
)

// MaxDecompressedSize bounds the size of any decompressed DATA payload.
// systemd splits payloads larger than 64 KiB across multiple DATA objects, so
// real journals never produce a single payload that approaches this cap.
// Treat it as a denial-of-service guard against crafted journals that claim
// huge uncompressed sizes. All three paths enforce the cap DURING decode
// before fully materialising an oversized payload: LZ4 checks its declared
// size prefix before allocating, XZ reads through an io.LimitReader, and
// ZSTD configures the decoder with WithDecoderMaxMemory.
const MaxDecompressedSize uint64 = 64 * 1024 * 1024 // 64 MiB

// LZ4SizePrefixBytes is the on-disk size of the leading uncompressed-size
// prefix that systemd writes ahead of every LZ4 block. Matches the
// systemd-journal compress.c convention:
//
//	[ uint64 LE uncompressed_size ][ LZ4 block-compressed bytes ]
//
// ZSTD and XZ payloads carry their size inside the compressed frame and do
// not have a separate prefix.
const LZ4SizePrefixBytes = 8

// Errors returned by the decompression entry points. Exported so callers can
// branch with errors.Is for diagnostics or fall-back logic.
var (
	// ErrCompressionUnknown indicates the supplied compression flag bits
	// did not match any of the ObjectCompressed* constants. Returned when
	// a journal advertises a compression algorithm we do not recognise
	// (e.g. a future systemd extension).
	ErrCompressionUnknown = errors.New("journal data: unknown compression flag")
	// ErrLZ4PrefixMissing indicates the LZ4 payload was shorter than the
	// 8-byte uncompressed-size prefix systemd always writes ahead of the
	// LZ4 block. A truncated/corrupt object.
	ErrLZ4PrefixMissing = errors.New("journal data: lz4 payload missing 8-byte size prefix")
	// ErrLZ4SizeUnreasonable indicates the LZ4 prefix declares an
	// uncompressed size beyond MaxDecompressedSize. Returned BEFORE any
	// allocation so the cap is respected even on malicious input.
	ErrLZ4SizeUnreasonable = errors.New("journal data: lz4 declared size exceeds maximum")
	// ErrLZ4SizeMismatch indicates the actual decompressed length does
	// not match the size advertised in the 8-byte prefix. Per systemd
	// format, the two MUST agree exactly.
	ErrLZ4SizeMismatch = errors.New("journal data: lz4 decompressed size does not match prefix")
	// ErrDecompressedTooLarge indicates ZSTD or XZ produced more than
	// MaxDecompressedSize bytes. We refuse to surface oversized payloads
	// even when the underlying decoder succeeded.
	ErrDecompressedTooLarge = errors.New("journal data: decompressed size exceeds maximum")
)

// DecompressPayload takes the raw bytes of a DATA object's compressed
// payload (i.e. everything after DataPayloadOffset / DataCompactPayloadOffset
// inside the object) and the single ObjectCompressed* bit set on the parent
// object header, and returns the decompressed bytes.
//
// The caller is expected to have already verified that the object is of
// type ObjectData and that header.IsCompressed() is true. flag MUST contain
// exactly one of ObjectCompressedXZ / ObjectCompressedLZ4 / ObjectCompressedZSTD;
// any other value (including 0 or multiple bits) returns ErrCompressionUnknown.
//
// The returned slice is freshly allocated; it is safe to retain or modify
// without aliasing the input.
func DecompressPayload(payload []byte, flag uint8) ([]byte, error) {
	switch flag {
	case ObjectCompressedLZ4:
		return decompressLZ4Block(payload)
	case ObjectCompressedZSTD:
		return decompressZSTD(payload)
	case ObjectCompressedXZ:
		return decompressXZ(payload)
	default:
		return nil, fmt.Errorf("%w: flag=0x%x", ErrCompressionUnknown, flag)
	}
}

// decompressLZ4Block decodes a systemd-journal LZ4 payload. The wire format
// is an 8-byte little-endian uncompressed size followed by an LZ4 *block*
// (NOT the LZ4 frame format). We allocate dst sized to the prefix and call
// lz4.UncompressBlock; the returned length must match the prefix exactly,
// otherwise the journal is corrupt.
func decompressLZ4Block(payload []byte) ([]byte, error) {
	if len(payload) < LZ4SizePrefixBytes {
		return nil, fmt.Errorf("%w: payload=%d bytes", ErrLZ4PrefixMissing, len(payload))
	}
	declared := binary.LittleEndian.Uint64(payload[:LZ4SizePrefixBytes])
	if declared > MaxDecompressedSize {
		return nil, fmt.Errorf("%w: declared=%d max=%d",
			ErrLZ4SizeUnreasonable, declared, MaxDecompressedSize)
	}

	// An empty payload with declared==0 is legal (zero-length value).
	// Avoid the 0-length make+UncompressBlock round-trip; lz4 would
	// reject the empty src.
	if declared == 0 {
		return []byte{}, nil
	}

	dst := make([]byte, declared)
	n, err := lz4.UncompressBlock(payload[LZ4SizePrefixBytes:], dst)
	if err != nil {
		return nil, fmt.Errorf("lz4 uncompress block: %w", err)
	}
	if uint64(n) != declared {
		return nil, fmt.Errorf("%w: declared=%d got=%d",
			ErrLZ4SizeMismatch, declared, n)
	}
	return dst[:n], nil
}

// decompressZSTD decodes a systemd-journal ZSTD payload. systemd writes the
// payload as a standalone zstd frame with no surrounding wrapper, so we feed
// it to klauspost/compress/zstd's DecodeAll one-shot API. The decoder is
// constructed with concurrency=0 to keep the call synchronous and
// goroutine-free, matching the rest of the package's I/O model.
//
// WithDecoderMaxMemory bounds the in-memory decoded size so DecodeAll refuses
// to materialise more than MaxDecompressedSize bytes for a crafted frame,
// rather than allocating the full output first and rejecting it only after
// the fact. This mirrors the LZ4 pre-allocation check and the XZ
// io.LimitReader: the DoS guard fires DURING decode, not after.
func decompressZSTD(payload []byte) ([]byte, error) {
	dec, err := zstd.NewReader(nil,
		zstd.WithDecoderConcurrency(0),
		zstd.WithDecoderMaxMemory(MaxDecompressedSize))
	if err != nil {
		return nil, fmt.Errorf("zstd reader init: %w", err)
	}
	defer dec.Close()

	out, err := dec.DecodeAll(payload, nil)
	if err != nil {
		// A frame that decodes to more than MaxDecompressedSize is rejected
		// by the decoder as zstd.ErrDecoderSizeExceeded; surface it as our
		// own size-cap sentinel so callers branch uniformly across codecs.
		if errors.Is(err, zstd.ErrDecoderSizeExceeded) {
			return nil, fmt.Errorf("%w: zstd frame exceeds max=%d",
				ErrDecompressedTooLarge, MaxDecompressedSize)
		}
		return nil, fmt.Errorf("zstd decode: %w", err)
	}
	// Defensive: WithDecoderMaxMemory already caps the decode, but keep the
	// explicit length check so the invariant holds even if the decoder's
	// accounting ever diverges from a single DecodeAll call.
	if uint64(len(out)) > MaxDecompressedSize {
		return nil, fmt.Errorf("%w: size=%d max=%d",
			ErrDecompressedTooLarge, len(out), MaxDecompressedSize)
	}
	return out, nil
}

// decompressXZ decodes a systemd-journal XZ payload. We wrap the byte slice
// in a bytes.Reader and feed it to ulikunitz/xz, then read up to
// MaxDecompressedSize+1 bytes via io.LimitReader so that even a malicious
// stream advertising a huge length cannot blow up memory.
func decompressXZ(payload []byte) ([]byte, error) {
	rd, err := xz.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("xz reader init: %w", err)
	}
	limited := io.LimitReader(rd, int64(MaxDecompressedSize)+1)
	out, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("xz read: %w", err)
	}
	if uint64(len(out)) > MaxDecompressedSize {
		return nil, fmt.Errorf("%w: size=%d max=%d",
			ErrDecompressedTooLarge, len(out), MaxDecompressedSize)
	}
	return out, nil
}
