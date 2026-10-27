# Manta lookup and request-allocation improvements

15 October 2026. This pass compares the immediately preceding Manta build with indexed server lookup and deferred request-logger construction. The [first optimization report](OPTIMIZATION.md) and [original Wings comparison](REPORT.md) remain intact. All values below are newly measured for this pass; improvements should not be added arithmetically to earlier percentages.

The subsequent [stats and console pass](TELEMETRY.md) records the latest runtime. This document retains its measured comparison.

The full local status-route median changed from 4.832 to 4.039 microseconds with one registration. At 1,000 in-memory registrations, the last-entry route changed from 30.882 to 3.806 microseconds (8.11x speed-up). Allocated bytes per one-registration request changed from 4,491 to 3,529. These measurements describe daemon routing overhead, not game TPS or player capacity.

## Changes

Server lookup now uses a UUID index guarded by the manager's existing lock. Request routing, SFTP, state recovery, and duplicate-registration checks can retrieve a server without scanning and locking every registered server configuration. The ordered collection remains available for iteration. Adds, removals, and bulk replacement keep the index synchronized; duplicate IDs retain the previous first-match behavior. UUIDs remain immutable through configuration sync.

Collection inputs and returned snapshots own their slice storage, preventing callers from replacing elements outside the manager lock and desynchronizing the index. This adds snapshot allocation to collection reads. The index itself consumes additional memory proportional to server count, and bulk replacement/removal still takes linear work. Point lookups allocate no memory in either version; the improvement there is CPU time.

Successful requests no longer construct unused request log entries. The first caller of `ExtractLogger` creates and caches an independent log entry with request/server fields. Request IDs are still generated and returned for every request. Existing custom logger fields are preserved, and background tasks receive an entry that remains valid after Gin reuses its context. Error logging and URL redaction remain covered by regression/live checks.

## Method and measured results

Linux amd64, Go 1.26.9, four CPU quota cores, five runs per case with 500 ms benchmark duration; tables show medians. Both versions use identical benchmark code and settings. This shared development host still has other workloads; allocation counts are stronger evidence than small timing differences. No confidence intervals are claimed.

The routing benchmark exercises the full Gin route, authorization, resource/configuration serialization, and response writing into an in-memory HTTP recorder. It excludes network latency and Docker. The target server is registered last to expose the previous scan's worst case. At scale, the other registrations are in-memory server objects with real configuration locks, not live game containers. The target server retains a real temporary filesystem and a stub environment, matching the original one-server routing benchmark. `BenchmarkServerStatus` is the one-registration case.

| Benchmark                         |      Before |      After | Speed-up | Bytes/op before / after | Allocations/op before / after |
| --------------------------------- | ----------: | ---------: | -------: | ----------------------: | ----------------------------: |
| Status route, 1 registration      |  4832.00 ns | 4039.00 ns |    1.20x |             4491 / 3529 |                       38 / 27 |
| Status route, 10 registrations    |  5156.00 ns | 4136.00 ns |    1.25x |             4491 / 3529 |                       38 / 27 |
| Status route, 100 registrations   |  7990.00 ns | 4118.00 ns |    1.94x |             4491 / 3529 |                       38 / 27 |
| Status route, 1,000 registrations | 30882.00 ns | 3806.00 ns |    8.11x |             4485 / 3523 |                       38 / 27 |

The point-lookup benchmark measures the last registered UUID and a missing UUID. Earlier hits in the old scan would be cheaper, so these are worst-case scan comparisons rather than average randomized-client latency. The new index avoids growth in lookup time as registrations increase.

| Benchmark                         |      Before |    After | Speed-up | Bytes/op before / after | Allocations/op before / after |
| --------------------------------- | ----------: | -------: | -------: | ----------------------: | ----------------------------: |
| 1 registration, last UUID         |    33.12 ns | 14.07 ns |    2.35x |                   0 / 0 |                         0 / 0 |
| 1 registration, missing UUID      |    34.60 ns | 14.08 ns |    2.46x |                   0 / 0 |                         0 / 0 |
| 100 registrations, last UUID      |  2204.00 ns | 13.97 ns |  157.77x |                   0 / 0 |                         0 / 0 |
| 100 registrations, missing UUID   |  2229.00 ns | 13.20 ns |  168.86x |                   0 / 0 |                         0 / 0 |
| 1,000 registrations, last UUID    | 21208.00 ns | 13.86 ns | 1530.16x |                   0 / 0 |                         0 / 0 |
| 1,000 registrations, missing UUID | 21384.00 ns | 13.05 ns | 1638.62x |                   0 / 0 |                         0 / 0 |

## Live validation and deployment

- 388 Go test cases in 22 packages passed with race detection; zero failures or skipped cases. `go vet ./...` passed; golangci-lint 2.11.4 reported zero issues.
- New regressions cover index updates, duplicate ordering/promotion, input/output slice ownership, concurrent manager access, lazy logger fields, context reuse, and custom logger preservation.
- For this pass, the optimized Go 1.27.2 image was deployed locally as `manta:local`, version `1.13.3-manta-local-20261015-scale`; the previous image was retained as `manta:before-scale-20261015`. See the subsequent report linked above for the latest runtime.
- The existing local Paper/Bedrock workload was repeated: 14,400 API requests with zero errors, 9,600/9,600 WebSocket messages delivered, and HTTP/SFTP/archive integrity checks passed.
- All 16 Manta live hardening/compatibility checks passed. Both benchmark daemons are healthy, and all four benchmark games remain running in Aquadactyl.

The live nodes still manage only two games each. Those checks establish compatibility of the deployed changes; they do not validate 1,000 live games, a higher player count, overall daemon RAM savings, or a game TPS improvement. Raw live timing data include the unchanged Wings control and are retained for inspection, but no new universal Manta-versus-Wings speed-up is claimed. Power/lifecycle code was unchanged; the previous shutdown-reporting delay remains a separate target.

## Reproduction and artifacts

Run `go test ./server -run '^$' -bench BenchmarkManagerLookup -benchmem -count=5 -benchtime=500ms` and `go test ./router -run '^$' -bench BenchmarkServerStatus -benchmem -count=5 -benchtime=500ms` with the same Linux Go 1.26.9 toolchain and CPU limits on both source versions.

Live check: `benchmarks/Run-Benchmark.ps1 -Phases performance,security -OutputDirectory benchmarks/results/my-scale-run`. The existing provisioned local test stack is required. The script removes its temporary credential manifest and probe listeners after completion.

Raw data and validation are in [results/scalability](results/scalability/). Regenerate this document with `python benchmarks/summarize_scalability.py`. The runtime JSON records actual image IDs, toolchains, caps, and server identities.
