# Native journald reader test fixtures

This directory holds binary `.journal` fixtures that exercise the pure-Go
reader at `pkg/stanza/operator/input/journald/native/`. They are committed
to git so tests are deterministic across machines without requiring access
to a running systemd or to `/var/log/journal/`.

Per the implementation plan, fixtures must stay ≤500 KB each and ≤5 MB
total under this directory.

## Files

| Fixture          | Bytes | Entries | Layout       | Compression | Notes                                    |
|------------------|-------|---------|--------------|-------------|------------------------------------------|
| `small.journal`  | 704   | 5       | non-compact  | none        | Synthetic; built by `generate/gen_small_journal.go` |
| `lz4.journal`    | 424   | 1       | non-compact  | LZ4         | Synthetic; one DATA(LZ4) + one ENTRY. Built by `generate/gen_compressed_journal.go` |
| `zstd.journal`   | 424   | 1       | non-compact  | ZSTD        | Synthetic; one DATA(ZSTD) + one ENTRY. Built by `generate/gen_compressed_journal.go` |
| `xz.journal`     | 472   | 1       | non-compact  | XZ          | Synthetic; one DATA(XZ) + one ENTRY. Built by `generate/gen_compressed_journal.go` |

The plaintext payload behind each compressed fixture is the constant
`"MESSAGE=hello journald compression fixture"` — `TestCompressionFixtures`
under the native package asserts byte-equality against this string after
decompression.

## Regeneration

The synthetic fixtures are deterministic — their generators emit
byte-identical files unless the generator source changes.

```bash
cd receiver/journaldreceiver/testdata/native
CGO_ENABLED=0 go run generate/gen_small_journal.go small.journal
CGO_ENABLED=0 go run generate/gen_compressed_journal.go lz4  lz4.journal
CGO_ENABLED=0 go run generate/gen_compressed_journal.go zstd zstd.journal
CGO_ENABLED=0 go run generate/gen_compressed_journal.go xz   xz.journal
```

If a fixture file diverges from its generator's output, the test that
opens it (e.g. `TestReader_OpensSmallJournalFixture`,
`TestCompressionFixtures`) will fail with a mismatch on entry count,
seqnum, realtime, or decompressed payload — that is the signal to
regenerate.

## Future fixtures

Phase 2 will add `compact.journal` (HEADER_INCOMPATIBLE_COMPACT layout).
Phase 3 adds `multi-array.journal` (multiple chained EntryArrays). Each
will be accompanied by a corresponding generator under `generate/` or by a
manual collection note in this README pointing at the source host.
