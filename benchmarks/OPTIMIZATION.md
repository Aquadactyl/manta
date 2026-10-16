# Manta performance follow-up

15 October 2026. This follow-up compares the previous Manta build with the optimized build on the same four local Paper/Bedrock servers. The [original Wings comparison](REPORT.md) remains intact. The clearest gains are in request processing and ready-receiver event fanout; these are not game tick or player-capacity speed-ups.

A subsequent [lookup and request-allocation pass](SCALABILITY.md) measures further changes. This report retains its original measurements.

## Changes and evidence

- Read only the resolved authentication credentials under the configuration lock, rather than copying the full configuration per request. Credentials are read again for every request, including after rotation; constant-time comparison remains.
- Authenticate protected server routes once through an explicit protected group. Public signed-token/WebSocket routes retain their own authentication.
- Bypass debug access-log preparation at normal log levels. Debug mode still logs requests with query redaction and request IDs; custom logger implementations keep the full logging path.
- Send console/event data directly to receivers with space. Only blocked receivers allocate a goroutine and timer; the concurrent 10 ms wait and oldest-message eviction remain. Pool locking still protects channel removal/closure.

Allocation profiling of the original status-route benchmark attributed about 30% of allocated bytes to `config.Get`. The optimized allocation profile no longer lists configuration copies among the significant sources. Both profile summaries are retained alongside the raw measurements.

## Isolated measurements

Five runs per benchmark, median shown, Linux amd64, Go 1.26.9, four CPU quota cores. The status benchmark exercises routing, authentication, configuration/resource serialization, and response writing using an in-memory HTTP recorder; it excludes network and Docker latency. Fanout includes sending and draining each ready buffered receiver. Empty/ready queues are the measured case; saturated queues retain the existing timeout behavior. Negative percentages mean lower time.

| Local benchmark | Before | After | Time change | Bytes/op before → after | Allocations/op before → after |
| --- | ---: | ---: | ---: | ---: | ---: |
| BenchmarkServerStatus | 6628.00 ns | 4244.00 ns | -36.0% | 7774 → 4488 | 58 → 38 |
| BenchmarkSinkPoolReady/0 | 25.97 ns | 9.84 ns | -62.1% | 16 → 0 | 1 → 0 |
| BenchmarkSinkPoolReady/1 | 693.40 ns | 34.21 ns | -95.1% | 336 → 0 | 6 → 0 |
| BenchmarkSinkPoolReady/16 | 13148.00 ns | 396.00 ns | -97.0% | 5136 → 0 | 81 → 0 |
| BenchmarkSinkPoolReady/64 | 41934.00 ns | 1548.00 ns | -96.3% | 20496 → 0 | 321 → 0 |

## Live Docker measurements

The previous and optimized daemons both use Go 1.27.2, two CPU quota cores, 1 GiB memory, and `GOMAXPROCS=2`. Each manages the same Paper and Bedrock server. Game containers, worlds, binaries, limits, and stock Wings image remain the same. Each pass uses three alternating-order repetitions of the original workload. This is sequential before/after observation on a shared development host; it is not a randomized crossover or a production load test. Additional Panel benchmark containers appeared on the host between passes. The unchanged stock results are included to expose variation; live timing changes cannot all be attributed to this patch.

| Live metric | Manta before | Manta after | Manta change | Stock before | Stock after |
| --- | ---: | ---: | ---: | ---: | ---: |
| Status API, concurrency 1 | 0.223 ms | 0.240 ms | +7.6% | 0.190 ms | 0.259 ms |
| Status API, concurrency 1, p95 | 0.352 ms | 0.374 ms | +6.2% | 0.319 ms | 0.390 ms |
| Status API, concurrency 16 | 0.302 ms | 0.209 ms | -30.8% | 0.229 ms | 0.235 ms |
| Status API, concurrency 16, p95 | 1.606 ms | 1.262 ms | -21.4% | 0.864 ms | 0.507 ms |
| Status API, concurrency 32 | 0.661 ms | 0.466 ms | -29.5% | 0.432 ms | 0.598 ms |
| Status API, concurrency 32, p95 | 2.396 ms | 1.670 ms | -30.3% | 1.423 ms | 1.954 ms |
| List 1,000 files, concurrency 1 | 10.281 ms | 10.595 ms | +3.1% | 12.216 ms | 12.145 ms |
| List 1,000 files, concurrency 1, p95 | 13.421 ms | 13.219 ms | -1.5% | 15.174 ms | 14.422 ms |
| List 1,000 files, concurrency 16 | 85.739 ms | 93.068 ms | +8.5% | 102.860 ms | 104.460 ms |
| List 1,000 files, concurrency 16, p95 | 192.431 ms | 206.043 ms | +7.1% | 265.704 ms | 260.724 ms |
| List 1,000 files, concurrency 32 | 162.789 ms | 175.553 ms | +7.8% | 189.163 ms | 199.982 ms |
| List 1,000 files, concurrency 32, p95 | 436.095 ms | 434.761 ms | -0.3% | 516.191 ms | 533.095 ms |
| HTTP write 16 MiB | 15.966 ms | 14.495 ms | -9.2% | 42.552 ms | 15.134 ms |
| HTTP read 16 MiB | 34.427 ms | 32.937 ms | -4.3% | 28.985 ms | 33.316 ms |
| SFTP write 16 MiB | 132.361 ms | 136.800 ms | +3.4% | 124.228 ms | 138.686 ms |
| SFTP read 16 MiB | 64.534 ms | 69.132 ms | +7.1% | 75.589 ms | 71.060 ms |
| Log delivery, 16 subscribers | 9.541 ms | 8.792 ms | -7.9% | 10.479 ms | 11.005 ms |
| Chmod 1,000 files | 4.801 ms | 5.047 ms | +5.1% | 5.732 ms | 4.534 ms |
| Rename 1,000 files | 16.249 ms | 18.408 ms | +13.3% | 18.655 ms | 18.536 ms |
| Compress 16 MiB + 1,000 files | 17.087 ms | 20.056 ms | +17.4% | 20.125 ms | 23.218 ms |
| Extract 16 MiB + 1,000 files | 60.746 ms | 64.675 ms | +6.5% | 56.423 ms | 59.699 ms |

Manta's status medians improved at concurrency 16 and 32, while the single-request median increased and file-operation timings remained mixed. One optimized concurrency-32 status run had a p95 of 52.298 ms; the table shows the median of the three per-run p95 values, so that tail spike remains visible in the raw data. These observations support reducing internal overhead, not a uniform end-to-end speed-up.

The optimized Manta daemon was recreated between passes and reattached to its running games. It completed the same file/control workload before resource sampling. The earlier daemon had a longer prior workload history, and resource windows are only about 30 seconds each. Consequently, these working-set/CPU observations cannot establish a retained-memory reduction. The raw resource files also contain game CPU/memory, native queries, and Paper tick reports; no game-engine configuration was tuned for this change.

| Window | Variant | Daemon CPU (% of one core) | Daemon mean working memory |
| --- | --- | ---: | ---: |
| before, idle | manta | 0.421% | 49.002 MiB |
| before, idle | wings | 0.482% | 66.388 MiB |
| before, command_load | manta | 1.561% | 49.166 MiB |
| before, command_load | wings | 1.607% | 56.252 MiB |
| after, idle | manta | 0.450% | 48.819 MiB |
| after, idle | wings | 0.452% | 70.909 MiB |
| after, command_load | manta | 1.711% | 38.699 MiB |
| after, command_load | wings | 1.710% | 58.378 MiB |

## Validation and availability

- Go race suite: 383 cases in 22 packages, 0 failures. `go vet ./...` passed; golangci-lint 2.11.4: zero issues.
- New regressions cover missing/incorrect authentication, live token rotation on global and server routes, and debug-log level/redaction behavior. Existing console overflow, timeout, token, containment, and race checks passed.
- 28,800 measured API requests across both passes, zero errors.
- 19,200/19,200 expected WebSocket deliveries received; HTTP/SFTP/archive integrity checks passed.
- All 16 optimized Manta live hardening/compatibility checks passed.
- For this recorded pass, the optimized image was deployed as `manta:local`, version `1.13.3-manta-local-20261015-perf`. The previous image was retained as `manta:before-perf-20261015` for comparison. See the subsequent report linked above for the latest deployment; all four benchmark games remain available through the existing Panel.

The previous shutdown-reporting delay remains a separate tuning target; this patch changes no power/lifecycle code and makes no shutdown improvement claim. More managed servers, authenticated player bots, sustained pressure, and longer runs are still needed to measure production capacity. Lower allocation per request or message does not imply the same percentage reduction in whole-daemon RAM.

## Reproduction

Raw data: [results/optimization](results/optimization/). Metadata in each live file records the actual image IDs, toolchains, caps, and server identifiers. Rebuild this summary with `python benchmarks/summarize_optimization.py`.

The original and optimized isolated runs used `go test ./router -run '^$' -bench BenchmarkServerStatus -benchmem -count=5 -benchtime=1s` and `go test ./system -run '^$' -bench BenchmarkSinkPoolReady -benchmem -count=5 -benchtime=500ms`, with identical CPU/toolchain settings and profiling enabled on both router runs. Run live phases with `benchmarks/Run-Benchmark.ps1 -Phases performance,resources,security -OutputDirectory benchmarks/results/optimization/after`; save results to another directory to preserve the recorded comparison.
