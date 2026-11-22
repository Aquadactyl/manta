# Directory-listing follow-up

Measured on 22 November 2026 against Manta revision
`80f84fbda9a7513c995d5a0456085df5adcd9c71`. The candidate uses the implementation
patch identified by SHA-256
`7782deb24e6776a52e2c0ab19c3de4aa250e722388d829de23550b51d01eb349`
in the [raw report](results/listing-20261122.json).

Directory metadata now comes from the open directory descriptor, avoiding a
separate root-relative parent-path traversal for every entry. MIME detection
continues through the root-confined, nonblocking file opener. It checks that the
opened file matches the metadata snapshot before reading it. Contents, MIME
results and directory lists are not cached.

## Matched component results

Medians of five samples per variant; time reduction is `1 - after / before`.
Each fixture has 1,000 regular 256-byte text files except `entries_10`. `Nested`
uses `plugins/example/config`; `Unique` uses distinct text contents. `read_and_json`
is the existing legacy `[]Stat` serialization benchmark. `api_json` serializes
`StatResponse` objects as the HTTP endpoint does, without HTTP/authentication work.

| Case | Before (ms) | After (ms) | Time reduction | Before B/op | After B/op | Before allocs/op | After allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| /entries_10/read | 0.049 | 0.041 | 15.6% | 13,775 | 9,768 | 232 | 132 |
| /entries_10/read_and_json | 0.060 | 0.052 | 13.0% | 21,296 | 17,284 | 305 | 205 |
| /entries_1000/read | 5.250 | 4.395 | 16.3% | 1,359,450 | 958,120 | 22,021 | 12,020 |
| /entries_1000/read_and_json | 6.491 | 5.672 | 12.6% | 2,640,143 | 2,451,658 | 29,049 | 19,052 |
| Nested/read | 9.355 | 6.710 | 28.3% | 1,582,428 | 1,126,575 | 28,025 | 18,024 |
| Nested/api_json | 10.068 | 7.358 | 26.9% | 2,416,425 | 1,822,826 | 32,047 | 22,040 |
| Unique/read | 5.282 | 4.426 | 16.2% | 1,343,228 | 941,930 | 22,021 | 12,020 |
| Unique/api_json | 5.917 | 5.031 | 15.0% | 2,144,349 | 1,625,194 | 26,041 | 16,035 |

The 1,000-entry root read uses 45.4% fewer allocations and 29.5% fewer allocated
bytes. JSON allocation sizes vary with buffer pools, so retain all raw samples.
These results measure the listing function on this machine, not live HTTP latency,
game performance or a new comparison against Wings. Historical published
comparisons remain unchanged until those complete workloads are rerun.

## Method and reproduction

Both variants used Go 1.27.4 on Linux/amd64, the same dependencies and benchmark
sources, and separate precompiled test binaries built with `-trimpath`. The
container had 2 CPUs, `GOMAXPROCS=2` and 2 GiB memory on an Intel i7-14700KF
Windows/WSL2 workstation. Fixtures used Linux temporary storage, not a Windows
source bind mount. Image ID and kernel are retained in the report.

One discarded single-iteration warm-up per variant preceded five sequential
500 ms batches each, alternating which variant ran first. There were no competing
test/build jobs; unrelated existing workstation services remained running. This
is a warm filesystem/cache component test, not a cold-disk or endurance test.
Repeat on the target host to assess different storage and contention.

Copy `server/filesystem/list_nested_benchmark_test.go` into the baseline revision
so both trees have identical benchmark sources. Build each binary with:

```bash
go test -c -trimpath -o /tmp/manta-list.test ./server/filesystem
```

Run from each tree's `server/filesystem` directory, with separate binary paths:

```bash
GOMAXPROCS=2 /tmp/manta-list.test -test.run='^$' \
  -test.bench='^BenchmarkDirectoryListing' -test.benchmem -test.benchtime=500ms
```

Alternate variants between batches. To rebuild the recorded table:

```bash
python benchmarks/summarize_listing.py
```

## Behaviour and validation

Listings remain sorted with folders first and preserve symlink metadata. Like
other filesystem listings, they are snapshots: files removed during enumeration
may be omitted; entries replaced before MIME inspection retain their snapshot
metadata with a generic MIME fallback. Root confinement remains the security
boundary even when entries or parent paths change.

Regression tests replace a listed regular file with a directory, internal/external
symlink, named pipe or deletion and verify that inspection returns without
blocking. Existing traversal, symlink, ordering and changed-content tests also
pass. Validation included `go test ./...`, `go test -race ./...`, `go vet ./...`
and golangci-lint v2.15.0 on Linux with an isolated Docker engine for container
tests. Native macOS validation remains part of CI.
