# Manta resource-event overhead

16 October 2026. This pass reduces periodic daemon work while retaining Aquadactyl's API and stats format. Isolated measurements were recorded on 15 October; full validation and deployment completed on 16 October after the interruption. Earlier [file operations](FILES.md), [WebSocket conversion](EVENTS.md), and [Docker telemetry](TELEMETRY.md) results remain separate.

## Changes

Resource events previously constructed a generic object/float tree, discarded it, then decoded the entire envelope again into typed counters. The handler now reads a raw payload envelope and decodes just that payload into fresh `environment.Stats`. This retains integer precision and missing/null-field behavior while avoiding the intermediate tree and repeated full-envelope decoding. Docker's CPU/memory calculations and collection cadence are unchanged.

Periodic stats broadcasts now skip snapshot construction and JSON encoding when the server bus has no registered observers. Resource updates and disk-limit enforcement always run. API requests continue to take a current snapshot, and any registered bus observer still enables the broadcast. The listener check is a synchronized snapshot: an observer arriving after a zero-listener check receives subsequent publications. Existing initial/requested stats replies remain available through the WebSocket API.

State-event handling retains plain/escaped starting-state detection, throttle/disk-limiter resets and state-change processing. Pull-progress payloads retain their JSON values when forwarded. The shared decoder now passes valid pointer destinations directly to the JSON decoder, avoiding an extra interface box. Invalid nil/non-pointer destinations now return the wrapped standard JSON destination error rather than decoding into an unused local interface.

Event goroutine scheduling, sink backpressure/drop behavior, WebSocket authentication/permissions, and write synchronization are unchanged.

## Isolated results

Five 500 ms runs per case, Go 1.26.9 Linux amd64 with four CPU quota cores. Both versions use the same synthetic resource frame and handler benchmark. The baseline handler was extracted from the preceding listener without changing its work; the goroutine dispatch remains in the listener. The measured function performs envelope/payload decoding, resource update, a cached disk check, and optional stats publication. Disk scans are disabled in the benchmark; enforcement is covered separately by regression tests. Buffered/drained channels model ready observers. Results exclude Docker HTTP transport, goroutine scheduling, WebSocket conversion/compression, and network writes. Isolated runs did not overlap our live workloads, builds, or regression jobs.

| Handler path                 |   Before |    After | Time change | Bytes/op before / after | Allocations/op before / after |
| ---------------------------- | -------: | -------: | ----------: | ----------------------: | ----------------------------: |
| Resource event, 0 observers  | 4.863 us | 2.629 us |      -45.9% |              2025 / 800 |                       48 / 16 |
| Resource event, 1 observer   | 4.865 us | 3.623 us |      -25.5% |             2025 / 1168 |                       48 / 21 |
| Resource event, 16 observers | 5.390 us | 4.183 us |      -22.4% |             2025 / 1168 |                       48 / 21 |
| State event (control)        | 0.634 us | 0.580 us |       -8.5% |               304 / 296 |                         9 / 7 |

These savings apply to each internal resource event and reduce background allocation work. They do not establish equal whole-daemon CPU/RAM savings, faster game ticks, or additional player capacity. No whole-daemon resource comparison or player-load benchmark was run in this pass. Separate optimization percentages should not be added together.

## Correctness and live checks

- 479 Go test cases in 22 packages passed with race detection, zero failures/skips, plus vet and zero golangci-lint 2.11.4 issues.
- Handler regressions verify counter updates with and without observers, delivery of exact uint64 values to observers, fresh/missing/null fields, payload/envelope field order, and malformed counter rejection without replacing the last good stats.
- Disk-limit regression tests exceed an actual test filesystem's quota with no observers, confirm one stop per start, then verify plain and escaped starting events reset disk enforcement and console throttling. Observer skipping does not bypass enforcement.
- Subscription tests cover registration/removal/destruction and concurrent listener changes. Decoder tests cover struct/raw, pointer-to-pointer, interface and slice destinations, plus wrapped syntax/destination errors. Existing real WebSocket permission and expiry tests passed.
- Before and after deployment, 16 viewers per benchmark game each received three automatic stats events: 192 samples per stage across Paper and Bedrock on both daemons. Field schemas, typed values, resource validity, advancing uptime and non-regressing network counters passed.
- Resource API checks opened no console sockets and sampled all four games three times before and after deployment. All 12 samples per stage passed, including advancing uptime. The after-stage API checks followed closure of the automatic-stats sockets and security checks.
- All 16 Manta live hardening/compatibility checks passed, including token handling, raw file streaming, SFTP compatibility, and containment. These live checks verify behavior; they are not an overall performance score. Docker Desktop restarted across the interruption, so numeric values/uptimes from the two dates are not compared for equality.

## Deployment and reproduction

The optimized local build is `1.13.3-manta-local-20261016-resource-events`, Go `go1.27.2`, image `sha256:71958ace106703f82909d3e843cace32c2aae1c44a98fbc9308f83efcf8ced2d`. It retains two CPU quota cores, 1 GiB RAM and `GOMAXPROCS=2`. The preceding image is retained as `manta:before-resource-events-20261015` (sha256:c05d956c4bf5b8b0cbac8730893eb5986ae46a88010e9e17d061e7882788db46). The production base uses the distinct tag `manta:resource-events-build-20261016` so other local builds cannot replace its input accidentally.

Raw evidence: [results/resource-events](results/resource-events/). Rebuild this report with `python benchmarks/summarize_resource_events.py`.

```sh
go test ./server -run '^$' -bench 'BenchmarkEnvironment(Resource|State)Handling' -benchmem -count=5 -benchtime=500ms
```

Use identical benchmark code/toolchain settings against both source versions. With the existing local test stack, run:

```powershell
.\benchmarks\Run-Benchmark.ps1 -Phases events,security -OutputDirectory benchmarks/results/my-resource-events-run
```

API-only sampling uses `benchmarks/check-telemetry.php` inside the existing local Panel. It refuses non-local environments and selects only the four benchmark servers.
