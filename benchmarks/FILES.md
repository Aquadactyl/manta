# Manta file browsing and streaming

15 October 2026. This pass targets file-manager responsiveness and HTTP file reads. It compares the preceding [event-conversion build](EVENTS.md) with this Manta build; earlier measurements remain separate.

## Changes and compatibility

The allocation profile attributed 68.45% of sampled allocation bytes to MIME-probe buffers during the 1,000-file listing/JSON workload. The implementation keeps the existing 4 KiB content probe and reuses its scratch buffer. It still opens and checks each regular file and reads current content; it caches no MIME results or directory responses. Smaller/empty files are detected using only the bytes actually read, and read errors keep the existing caller behavior.

Directory responses now encode plain metadata records in one JSON pass, avoiding a separate encode and validation/compaction for every file. The inherited ten fields, types, timestamp formatting, permissions, MIME values, HTML/Unicode escaping, and empty-array behavior are retained. A single comparator puts directories first and sorts names within each group.

HTTP file reads now use pooled 64 KiB streaming buffers through Gin's existing response writer. For the synthetic 16 MiB read, application writes drop from 4,096 to 256. The original content-length reader, filesystem containment, MIME detection/rewind, response headers, byte accounting, flushing, and error path remain. Request-time storage remains bounded per active read: the buffer is not sized to the file. Pools can retain scratch capacity between requests; lower allocated bytes/op do not imply equal retained-RAM savings.

The implementation stays in Go. Profiles identified repeated allocations, encoding, and small I/O operations that can be removed while retaining the existing API implementation. This pass supplies no evidence that a language rewrite would improve these operations further.

## Isolated measurements

Five 500 ms runs per case, Go 1.26.9 Linux amd64 and four CPU quota cores. Both versions use identical benchmark code/settings, and final isolated runs do not overlap the live workload or build/test jobs. The host remains shared. Fixtures contain 10 or 1,000 256-byte text files. Full API cases include routing, authentication, containment, filesystem work and JSON encoding with an in-memory response writer. File-read cases stream cached file data to a counting/discarding response writer and exclude network transport; their reported MB/s is not network throughput. The retained legacy Stat-JSON cases isolate filesystem improvements independently of the directory handler's new encoding.

| Isolated path | Before | After | Time change | Bytes/op before / after | Allocations/op before / after |
| --- | ---: | ---: | ---: | ---: | ---: |
| Status API (control) | 0.0041 ms | 0.0040 ms | -1.1% | 3,528 / 3,528 | 27 / 27 |
| Directory API, 10 files | 0.1102 ms | 0.0746 ms | -32.3% | 64,540 / 21,184 | 243 / 214 |
| Directory API, 1,000 files | 10.8687 ms | 8.3284 ms | -23.4% | 6,238,715 / 1,654,508 | 20096 / 17070 |
| File read API, 1 MiB | 0.1150 ms | 0.0422 ms | -63.3% | 10,712 / 2,564 | 39 / 37 |
| File read API, 16 MiB | 2.7184 ms | 1.1384 ms | -58.1% | 10,726 / 2,920 | 39 / 37 |
| List only, 10 files | 0.0708 ms | 0.0607 ms | -14.3% | 52,937 / 11,981 | 125 / 115 |
| List + legacy Stat JSON, 10 files | 0.0884 ms | 0.0766 ms | -13.3% | 59,374 / 18,355 | 187 / 177 |
| List only, 1,000 files | 8.6775 ms | 7.3003 ms | -15.9% | 4,817,522 / 721,418 | 11025 / 10022 |
| List + legacy Stat JSON, 1,000 files | 10.9816 ms | 8.7787 ms | -20.1% | 5,964,642 / 1,535,023 | 17056 / 16036 |

The status route is an unchanged control. CPU/allocation profile text is retained in `cpu-before/after.txt` and `alloc-before/after.txt`. Profiles are sampled and include fixture setup; their total allocated MB should not be compared as per-operation values because iteration counts differ. The table uses benchmark bytes/op instead.

## Live Docker comparison

Both Manta stages use Go 1.27.2, two CPU quota cores, 1 GiB RAM, and `GOMAXPROCS=2`, with the same benchmark Paper/Bedrock containers. Within the measured stages, stock Wings was unchanged and provided a host/workload control. Each stage runs three alternating Manta/Wings rounds. Each directory/status concurrency case contains 400 requests per round; 16 MiB reads contain five verified samples per round. Values below are medians of the three per-round p50/p95 values. Final live stages did not overlap our builds, profiles, isolated benchmarks, or regression tests. The optimized stage was measured first, followed by a fresh baseline-daemon run, then the optimized deployment was restored. An earlier exploratory baseline overlapped profiling and is retained as `exploratory-performance.json`; it is excluded from the table.

| Live operation | Daemon | Concurrency | Before p50 | After p50 | p50 change | Before / after p95 |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| List 1,000 files | manta | 1 | 10.454 ms | 7.362 ms | -29.6% | 12.907 / 8.990 ms |
| List 1,000 files | wings | 1 | 12.366 ms | 11.746 ms | -5.0% | 15.221 / 13.796 ms |
| List 1,000 files | manta | 16 | 96.209 ms | 54.069 ms | -43.8% | 221.559 / 136.868 ms |
| List 1,000 files | wings | 16 | 96.404 ms | 89.944 ms | -6.7% | 249.644 / 245.830 ms |
| List 1,000 files | manta | 32 | 176.897 ms | 108.946 ms | -38.4% | 453.271 / 276.008 ms |
| List 1,000 files | wings | 32 | 191.563 ms | 186.062 ms | -2.9% | 530.656 / 469.038 ms |
| Read 16 MiB | manta | 1 | 38.879 ms | 13.270 ms | -65.9% | 40.595 / 15.905 ms |
| Read 16 MiB | wings | 1 | 33.754 ms | 33.362 ms | -1.2% | 34.353 / 34.077 ms |
| Status control | manta | 1 | 0.239 ms | 0.185 ms | -22.6% | 0.364 / 0.309 ms |
| Status control | wings | 1 | 0.255 ms | 0.185 ms | -27.5% | 0.367 / 0.310 ms |

Shared-host scheduling, page cache, and daemon heap history can affect these timings. Stock-control changes must be considered when interpreting Manta changes. These results measure file operations rather than overall game speed; no player-capacity, TPS, startup-time, or whole-daemon CPU/RAM gain is established here.

## Verification and deployment

- 464 Go test cases in 22 packages passed with race detection, zero failures/skips, plus vet and zero golangci-lint 2.11.4 issues.
- MIME comparisons use the previous library reader as the reference and cover text, Unicode, JSON/HTML/XML, image/archive signatures, binary/empty data, short reads, read failures, probe limits and concurrent reuse. Filesystem tests cover fresh external writes, directory/name ordering, symlinks and FIFOs.
- HTTP tests cover concurrent readers, empty/small/buffer-boundary bodies, download headers, original-length enforcement when a file grows, and the directory's ten-field JSON contract and escaping.
- Each live stage completed 14,400 API requests with zero errors, 30 verified transfer/archive cases, and all 9,600 expected console messages. The after stage also received 192 automatic stats samples across 16 viewers on each of the four games, and all 16 Manta hardening/compatibility checks passed.
- The measured file build is version `1.13.3-manta-local-20261015-files`, Go `go1.27.2`, image `sha256:5ffd2ffcfd222f6e81d63d9774c28c07694eec017e6aa13267ea9c60cb75ba73`. The previous image is retained as `manta:before-files-20261015` (sha256:2528243f3ca11a3d4092220844eb1fea732116be5aea630231029c50c6c00f4c). During the measured stages, only the local daemon was recreated and the existing game containers stayed running.

Concurrent local rebuilds subsequently replaced both daemons. Current Manta is version `1.13.3-manta-local`, image `sha256:c05d956c4bf5b8b0cbac8730893eb5986ae46a88010e9e17d061e7882788db46`. That healthy binary contains the MIME/metadata helpers and a 64 KiB buffer type; separate current-runtime checks passed all 16 Manta hardening/compatibility cases and 192 automatic stats samples. Its binary SHA-256 is `8f2516afd6a9d8f745587c2e2eb65b8d20f9b6c75bdf09fd0aeac9c8d73fcd29`. An initial additional runtime check aborted on invalid streamed resource values after the concurrent rebuilds; benchmark-game start times also changed during this later window. The subsequent retry passed. These later observations are separate from the completed file comparison. The performance tables belong to the versioned file build retained as `manta:files-candidate-20261015`; timings are not relabeled as measurements of this later rebuild.

Raw evidence is under [results/files](results/files/). Rebuild this report with `python benchmarks/summarize_files.py`.

```sh
go test ./server/filesystem -run '^$' -bench BenchmarkDirectoryListing -benchmem -count=5 -benchtime=500ms
go test ./router -run '^$' -bench 'BenchmarkDirectoryAPI|BenchmarkServerStatus$|BenchmarkFileReadAPI' -benchmem -count=5 -benchtime=500ms
```

With the existing dedicated local test stack:

```powershell
.\benchmarks\Run-Benchmark.ps1 -Phases performance,events,security -OutputDirectory benchmarks/results/my-files-run
```
