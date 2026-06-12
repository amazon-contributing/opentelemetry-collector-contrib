// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package native is a pure-Go reader for the systemd journal binary format.
//
// It is intended as a CGO-free backend for the journaldreceiver in the
// Amazon-contributing fork of opentelemetry-collector-contrib, used by the
// Amazon CloudWatch Agent. Unlike the default journalctl-subprocess backend,
// this package reads .journal files directly and does not require systemd or
// libsystemd to be installed at runtime.
//
// # Build constraint
//
// This package MUST build with CGO_ENABLED=0. It does not link against
// libsystemd or any C library and contains no cgo directives. The CWA
// distribution ships a static, scratch-based container image, so any
// dependency that would force CGO is unacceptable here.
//
// # Scope
//
// The package implements:
//
//   - Journal file header parsing (signature, flags, offsets).
//   - Object header parsing for the seven object types defined by the
//     systemd journal format (DATA, FIELD, ENTRY, DATA_HASH_TABLE,
//     FIELD_HASH_TABLE, ENTRY_ARRAY, TAG).
//   - Entry parsing for both legacy and HEADER_INCOMPATIBLE_COMPACT layouts.
//   - DATA-object decompression (LZ4, XZ, ZSTD) using pure-Go libraries.
//   - Indexed traversal via EntryArray for efficient seek.
//   - Follow mode (tail -f equivalent) using fsnotify with a polling fallback.
//   - Cursor serialization compatible with systemd's wire format
//     (`s=...;i=...;b=...;m=...;t=...;x=...`) for crash-safe checkpointing.
//
// Filtering, journal rotation discovery, and remote journal access are out of
// scope.
//
// # Third-party attribution
//
// Portions of this package — specifically the on-disk struct definitions,
// object type constants, compression flag constants, and the
// HEADER_INCOMPATIBLE_COMPACT flag layout — are derived from the gournal
// project by Dominik Rosiek (sumo-drosiek), reused under the Apache License,
// Version 2.0:
//
//	https://github.com/sumo-drosiek/gournal
//	commit 6059064 (2024-05-07)
//
// gournal is NOT a Go module dependency of this package. The relevant code
// fragments were copied into this tree and are maintained here. See the
// NOTICE file in this directory for the full attribution and the list of
// modifications relative to the upstream commit.
package native
