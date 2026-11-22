# Reusing directory handles during MIME detection

This unreleased follow-up avoids resolving the parent path for each MIME probe.
`ListDirectory` keeps its root-confined directory file descriptor open through
metadata collection and MIME inspection. Child opens accept only one basename
and use `openat` with `O_NOFOLLOW`, `O_NONBLOCK`, `O_CLOEXEC` and `O_NOCTTY`.
The opened file must still be regular and match the metadata snapshot before
content is read. Contents, MIME types and listings are not cached.

Parent paths are still opened through `os.Root`; traversal and outside-directory
symlinks remain rejected. Holding the descriptor anchors the original directory
if its pathname is replaced during a listing. Internal directory symlinks remain
usable, and listed file symlinks retain their metadata and generic MIME type.
Only the listing owns/closes the parent descriptor. Each child descriptor is
closed after inspection. Directory opens are nonblocking, including when a path
has become a named pipe.

## Matched component results

Measured on 22 November 2026 against main revision
`ac7370966e660abde757c544737f8139a3c3f858`, which has v1.3-beta's filesystem
implementation. The candidate implementation patch has SHA-256
`ae46c46d810ef88054c966b4e59fac3be227704cda6a3f43e1c493b2a492ad02`.
All samples and environment details are in the
[raw report](results/directory-handles-20261122.json).

Medians of five measured batches per variant; time reduction is `1 - after / before`.
Fixtures contain 1,000 regular 256-byte text files except `entries_10`. `Nested`
uses `plugins/example/config`; `Unique` uses distinct text contents. `api_json`
serializes the endpoint's `StatResponse` objects without HTTP/authentication work.

| Case | Before (ms) | After (ms) | Time reduction | Before B/op | After B/op | Before allocs/op | After allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| /entries_10/read | 0.050 | 0.049 | 2.1% | 9,770 | 8,966 | 132 | 112 |
| /entries_10/read_and_json | 0.061 | 0.059 | 2.0% | 17,283 | 16,478 | 205 | 185 |
| /entries_1000/read | 5.238 | 5.141 | 1.9% | 958,023 | 861,746 | 12,020 | 10,020 |
| /entries_1000/read_and_json | 6.590 | 6.424 | 2.5% | 2,455,968 | 2,479,566 | 19,052 | 17,054 |
| Nested/read | 8.781 | 5.156 | 41.3% | 1,126,498 | 861,663 | 18,024 | 10,024 |
| Nested/api_json | 9.386 | 5.706 | 39.2% | 1,776,145 | 1,446,510 | 22,039 | 14,035 |
| Unique/read | 5.281 | 5.180 | 1.9% | 941,697 | 861,658 | 12,020 | 10,020 |
| Unique/api_json | 5.929 | 5.848 | 1.4% | 1,634,604 | 1,475,039 | 16,036 | 14,032 |

Nested paths benefit most from avoiding repeated resolution. Root timing changes
are small and may be within workstation noise. The root 1,000-entry JSON case
allocates about 1% more bytes despite fewer allocations; serializer/pool behaviour
affects this figure. These results do not demonstrate an HTTP, memory-working-set
or game/player capacity improvement. They are separate from the published
Manta/Wings comparison and the historical [v1.3-beta measurement](LISTING.md).

## Reproduction and checks

Build identical, unchanged `BenchmarkDirectoryListing*` sources in the baseline
and candidate trees using Go 1.27.4 on Linux, with `go test -c -trimpath`. Run in
the same Debian Bookworm container with a two-CPU/2-GiB quota, `GOMAXPROCS=2` and
fixtures on Linux temporary storage. Discard one single-iteration warm-up for
each variant, then alternate five 500-ms batches per variant. No other test/build
jobs ran during timing; unrelated workstation services remained running.

From each tree's `server/filesystem` directory, with its own compiled binary:

```bash
GOMAXPROCS=2 /tmp/manta-list.test -test.run='^$' \
  -test.bench='^BenchmarkDirectoryListing' -test.benchmem -test.benchtime=500ms
```

Rebuild this table with:

```bash
python benchmarks/summarize_listing.py benchmarks/results/directory-handles-20261122.json
```

Regression tests cover file and parent replacement, outside symlinks, internal
directory symlinks, traversal, invalid basenames, closed descriptors and named
pipes, alongside existing MIME, sorting and changed-content cases. Local Linux
validation passed `go test ./...`, `go test -race ./...`, `go vet ./...` and
golangci-lint v2.15.0. Native macOS validation remains in CI.
