# Manta stats and console overhead

15 October 2026. This pass measures the stats decoder and console inspection against the immediately preceding Manta build. Earlier [lookup](SCALABILITY.md), [request/event](OPTIMIZATION.md), and [Wings](REPORT.md) results are retained separately. Improvements from separate passes should not be added arithmetically.

## Changes

Docker resource sampling now decodes only the fields Manta reports. It avoids constructing the unused memory-counter map, block-I/O arrays, and unused network counters on each frame. The CPU conversion remains the same. Memory handling retains cgroup-v1 precedence, including present zero/null counters; resource samples start with fresh fields so absent counters/interfaces cannot retain values from a previous frame.

Console inspection now exits once the environment is outside the starting state. The rest of console delivery and throttling still runs. Startup inspection keeps its private line copy, ANSI handling, matchers, and second state check; normal running lines avoid the copy and process-configuration lock.

## Isolated measurements

Five runs per case, 500 ms each, Go 1.26.9 on Linux amd64 with four CPU quota cores. The stats benchmark uses a persistent `json.Decoder` over the same captured Docker wire frame, followed by resource conversion; it excludes Docker HTTP transport, event fanout, and Panel work. Baseline sampling used the full SDK type behind the same extracted stream helper. Only the container ID/name were replaced in the fixture; numeric tokens were preserved without reserialization. The console benchmark measures inspection of a 156-byte line, excluding throttling, log fanout, and WebSocket transport. Both versions use identical benchmark code/settings.

| Isolated benchmark               |    Before |     After | Time change | Bytes/op before / after | Allocations/op before / after |
| -------------------------------- | --------: | --------: | ----------: | ----------------------: | ----------------------------: |
| Docker stats decode + conversion | 17.926 us | 11.255 us |      -37.2% |              6472 / 660 |                        84 / 6 |
| Console inspection, running      | 73.600 ns | 10.110 ns |      -86.3% |                 160 / 0 |                         1 / 0 |
| Console inspection, starting     | 62.390 ns | 62.860 ns |       +0.8% |               160 / 160 |                         1 / 1 |

The starting-state row is a control: its retained copy and checks show essentially unchanged cost. The large allocation reductions apply to the measured sample/inspection paths. They do not establish equal whole-daemon CPU/RAM reductions, higher game TPS, or additional player capacity. Only two games per daemon run on this shared development host.

Fixture SHA-256: `2ccf7851c5639088a6c4ec192df34be3ef8b10710e3b83c8a39691f88d557852`. Source: [captured cgroup-v2 frame](../environment/docker/testdata/stats-v2.json).

## Correctness and live validation

- 409 Go test cases in 22 packages passed with race detection; 0 failures and 0 skipped cases. Vet passed; golangci-lint 2.11.4 reported zero issues.
- Regression comparisons use the full SDK response and previous memory calculation as a reference. Cases cover captured v2 counters, v1 cache precedence, zero/null caches, caches above usage, missing fields, multiple interfaces, integer values above 2^53, CPU core fallback/reset, read intervals, stream resets/EOF, and invalid used counters.
- Console regressions cover plain/ANSI startup markers, unchanged input buffers, and running output proceeding while the process-configuration lock is held. Existing state, console-ordering, cancellation, and power tests also passed.
- Before and after deployment, authenticated Panel requests sampled all four games three times. All had positive memory/uptime, valid memory limits/CPU/network values, non-decreasing network counters, and advancing uptime. The checks compare valid behavior over time, not identical values from different moments.
- The short idle/console-block workload completed with native Paper/Bedrock query responses. The daemon had just been recreated, and this pass omitted the repeated 16 MiB API/SFTP transfers used before earlier resource samples. These windows also preceded the large-stream security checks. Its heap history therefore differs from earlier passes. No matching daemon-resource baseline was collected here; the memory values below cannot establish a whole-daemon saving.
- All 16 optimized Manta live hardening/compatibility checks passed. The existing games remained running during the daemon update. Game binaries, limits, and startup settings were unchanged.

| Post-update window | Daemon | CPU (% of one core) | Mean working memory |
| ------------------ | ------ | ------------------: | ------------------: |
| idle               | manta  |              0.365% |          10.976 MiB |
| idle               | wings  |              0.372% |          51.556 MiB |
| command_load       | manta  |              1.612% |          11.721 MiB |
| command_load       | wings  |              1.617% |          52.462 MiB |

| Post-update window | Paper daemon | TPS, one minute |
| ------------------ | ------------ | --------------: |
| idle               | wings        |            20.0 |
| idle               | manta        |            20.0 |
| command_load       | manta        |            20.0 |
| command_load       | wings        |            20.0 |

## Deployment and reproduction

The deployed local image is `manta:local`, version `1.13.3-manta-local-20261015-telemetry`, Go `go1.27.2`, image `sha256:ec82bc587a01876a1454d393884ee0e1122659595e797d3bc39d6d3c1f881c74`. It retains two CPU quota cores, 1 GiB memory, and `GOMAXPROCS=2`. The previous image is retained as `manta:before-telemetry-20261015`.

Raw files and validation are in [results/telemetry](results/telemetry/). Rebuild this document using `python benchmarks/summarize_telemetry.py`.

Use `go test ./environment/docker -run '^$' -bench BenchmarkDockerStatsStream -benchmem -count=5 -benchtime=500ms` and `go test ./server -run '^$' -bench BenchmarkConsoleInspection -benchmem -count=5 -benchtime=500ms` with identical Linux toolchain/CPU settings on each source version.

Repeat live sampling by copying `benchmarks/check-telemetry.php` into the existing local Panel and running it from `/app`; it refuses non-local environments and selects only the four existing benchmark servers. Run `benchmarks/Run-Benchmark.ps1 -Phases resources,security -OutputDirectory benchmarks/results/my-telemetry-run` for workload/security checks. The existing provisioned test stack is required. This run did not repeat lifecycle timings; startup behavior is covered by the startup regression cases, and no shutdown improvement is claimed.
