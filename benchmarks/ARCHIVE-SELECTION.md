# Selected archive paths

This unreleased change replaces a linear scan of the selected path list for
each visited file with an operation-local lookup map and ancestor checks. Path
matching remains literal and respects directory-component boundaries. Tar/gzip
finalization is included in the full-stream measurements.

Measured on 23 November 2026 against
`59fd2a957ab2bcc74c1040c0cf714d09dd31a3cc`. The implementation patch for
`server/filesystem/archive.go` has SHA-256
`4e4576e03334bd4172af4f835a7fa1270eefadc54e5e56fd9bb0c061f3ab6325`.
The [raw report](results/archive-selection-20261123.json) retains every sample,
environment, fixture definitions and benchmark source hashes.

## Matched component results

Medians of five measured batches per variant. Time reduction is `1 - after / before`.

| Full archive stream, 1,000 root files | Before (ms) | After (ms) | Time reduction | Before B/op | After B/op | Before allocs/op | After allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 500 existing files selected | 14.350 | 3.222 | 77.5% | 6,183,510 | 6,208,817 | 15,580 | 15,582 |
| 1,000 existing files selected | 20.339 | 5.221 | 74.3% | 6,688,284 | 6,744,502 | 24,081 | 24,086 |
| One existing file selected | 1.171 | 1.152 | 1.7% | 4,564,317 | 4,564,907 | 7,090 | 7,093 |
| One existing + 999 missing selections | 24.004 | 1.184 | 95.1% | 4,580,113 | 4,635,985 | 7,091 | 7,097 |

The separate filter-only miss measurement excludes traversal, compression and
selection setup. For one selected path it changed from 31.09 to 33.53 ns/op
(7.8% slower, a 2.44-ns difference); for 1,000 selected paths it changed from
23,490 to 26.89 ns/op. Both recorded zero allocations per match. The map's setup
and memory costs are included in the full-stream cases above.

The one-file full-stream result is effectively unchanged. The larger selections
benefit most; the mostly-missing case deliberately stresses exclusions and is
reported separately. All streams still walk the directory tree. Allocated bytes
increase slightly due to the lookup map; it exists only for that operation.

These are warm local measurements with tiny files. They do not quantify large
file compression, cold disks, full backup throughput, HTTP latency or game/player
capacity. Ordinary full backups use the unfiltered/ignore path, so these selection
percentages do not apply to them. Public released Manta/Wings figures are separate.

## Reproduction

Use Go 1.27.4, Linux/WSL2 and the same Debian Bookworm container with a two-CPU,
2-GiB quota and `GOMAXPROCS=2`. Fixtures live in Linux temporary storage. The
existing-selection fixtures contain 1,000 files whose contents are their unique
names. Other full-stream fixtures use four-byte contents. Setup is outside the
timed loop; each full stream constructs its own Archive and selection map.

Copy the unchanged `archive_selection_benchmark_test.go` and
`archive_existing_benchmark_test.go` into both baseline and candidate trees.
Compile before timing with separate output paths:

```bash
go test -c -trimpath -o /tmp/manta-archive.test ./server/filesystem
```

From each tree's `server/filesystem` directory:

```bash
GOMAXPROCS=2 /tmp/manta-archive.test -test.run='^$' \
  -test.bench='^BenchmarkArchive(Selection|SelectedStream|SelectedExisting)$' \
  -test.benchmem -test.benchtime=500ms
```

Discard a single-iteration warm-up per variant, then alternate five 500-ms batches
per variant. No competing scan/test/build work ran during the recorded batches;
unrelated workstation services remained running. Regression tests separately
verify archive membership, contents, literal paths and gzip/tar finalization.
