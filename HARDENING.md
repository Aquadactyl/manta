# Manta hardening review

Reviewed on 14 October 2026. This change retains the Wings API and configuration format for Aquadactyl integration.

## Changes

| Area                   | Result                                                                                                                                           |
| ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| JSON control requests  | At most 4 MiB, including chunked bodies and trailing whitespace; rejects multiple JSON documents and preserves field validation.                 |
| Batch filesystem work  | At most eight workers per rename, delete, or chmod request; stops scheduling after an error or request cancellation.                             |
| Startup pagination     | At most four concurrent Panel requests; stops scheduling after a failure.                                                                        |
| Panel response parsing | At most 16 MiB after decompression; closes the original body and propagates read failures.                                                       |
| HTTP retries           | Reuses serialized bytes while giving each attempt a fresh reader.                                                                                |
| Panel redirects        | Refuses a change of scheme, host, or port before credentials or authentication bodies can be forwarded.                                          |
| Temporary credentials  | Strict expiry boundary; validates issuance and not-before with 30 seconds of clock tolerance.                                                    |
| One-time tokens        | Rejects missing identifiers and expired tokens; retains consumed identifiers until their signed expiry.                                          |
| Remote downloads       | Synchronizes cancellation and path access, honors cancellation before execution, and removes empty tracking entries.                             |
| URL log fields         | Removes user information, query strings, and fragments from access logs, request error logs, remote download start logs, and Panel request logs. |

File uploads, raw file writes, backup streams, and transfers keep their existing streaming paths. The new JSON and Panel response limits do not apply to those streams. Concurrency limits are per request, not global admission limits.

## Allocation benchmark

`BenchmarkRequestBody` exercises the real Panel request method with an already serialized 1 MiB body and an in-memory HTTP transport. Three runs compared the original `remote/http.go` at commit `5d9e2c3` with this implementation, using Go 1.26.9 on Linux amd64 with four CPUs available.

| Metric                      | Original, median | Updated, median |
| --------------------------- | ---------------: | --------------: |
| Allocated bytes per request |        1,050,809 |           2,080 |
| Allocations per request     |               28 |              26 |
| Time per request            |       214,558 ns |        1,736 ns |

The request method allocates about 99.8% fewer bytes in this benchmark. It isolates request handling and body-copy cost; it excludes JSON serialization, network latency, Panel processing, and game server workloads. It is not an end-to-end throughput measurement.

Reproduce the updated benchmark with:

```sh
go test ./remote -run '^$' -bench BenchmarkRequestBody -benchmem -count=3
```

## Dependency scan assessment

`govulncheck` v1.9.0 reports two symbol-level findings against `github.com/docker/docker@v28.5.2+incompatible`: [GO-2026-4887](https://pkg.go.dev/vuln/GO-2026-4887) and [GO-2026-4883](https://pkg.go.dev/vuln/GO-2026-4883). Both database entries are unreviewed and apply broadly to the old module; neither lists a fixed version for that module path. The scan therefore exits nonzero and must not be reported as clean.

The primary advisories describe [Docker Engine authorization plugin handling](https://github.com/moby/moby/security/advisories/GHSA-x744-4wpc-v9h2) and [Docker Engine plugin installation privilege checks](https://github.com/moby/moby/security/advisories/GHSA-pxq6-2prw-chj9). Both are patched in Engine 29.3.1. Manta's dependency graph excludes `github.com/docker/docker/daemon`, `github.com/docker/docker/pkg/authorization`, and `github.com/docker/docker/plugin`. Based on the advisory descriptions and this import graph, these are assessed as Engine deployment concerns rather than demonstrated vulnerabilities in Manta's embedded Docker client. Keep the host Engine patched independently of the Go SDK.

The scan also reports four module-only findings whose vulnerable packages are not imported: GO-2026-5932 (OpenPGP), GO-2026-5746, GO-2026-5668, and GO-2026-5617 (Docker daemon archive/copy handling). This review does not suppress any findings or migrate the Docker SDK.

## Deployment limits

`go test ./...`, `go test -race ./...`, `go vet ./...`, and a Linux build passed. The final file batch cancellation changes also passed the router race tests. `golangci-lint` v2.11.4 reported zero issues in a temporary Linux copy after normalizing the Windows checkout's CRLF line endings with `gofmt`; the original checkout's unaffected files were not reformatted.

The initial validation above did not exercise a live deployment. The follow-up local benchmark below covers Aquadactyl integration and Docker game workloads; production load remains untested. Panel nodes must use synchronized clocks; control payloads above 4 MiB or Panel responses above 16 MiB now fail explicitly.

## Local Panel connection, 15 October 2026

Built the current checkout as a Docker image and started the healthy `manta-test` container alongside the existing Wings daemon. Aquadactyl at `http://localhost:8081` now has a separate **Local Manta** node (ID 2), with API `http://manta.localhost:8083`, SFTP `localhost:2023`, and game allocations 27015-27019.

Verified Manta-to-Panel startup requests, authenticated Panel-to-Manta system information, unauthenticated API rejection (401), and the SFTP SSH banner. The existing Wings node and its running game containers remained available. Setup and restart instructions are in [the local Docker guide](docker/README.md).

The subsequent [Paper/Bedrock comparison](benchmarks/REPORT.md) exercised game installation, power actions, authenticated SFTP, API/file operations, and short command workloads against a dedicated stock Wings node. All 16 Manta live checks passed. It found and fixed SFTP identifier compatibility in Manta and Aquadactyl, with node-scoped legacy lookup and ambiguity rejection in the Panel. Full race, vet, lint, and Panel regression results are recorded in [validation.json](benchmarks/results/validation.json). Performance was mixed, with higher retained daemon memory and slower small status requests despite faster directory listings and several write operations. This is not a production capacity or player-load benchmark.

## Performance follow-up, 15 October 2026

The [follow-up measurements](benchmarks/OPTIMIZATION.md) target avoidable request and console/event overhead. Authentication now reads only resolved credentials under the configuration lock and runs once per protected route, while retaining constant-time comparison and live rotation. Disabled debug logging bypasses its preparation; debug logging retains request IDs and URL redaction. Ready event receivers avoid goroutines/timers; blocked receivers retain the concurrent 10 ms wait and oldest-message eviction under the same pool lifetime lock.

The updated suite passed 383 cases with race detection, plus vet and zero lint issues. New regressions exercise authentication and rotation on global/server routes and log-level/query-redaction behavior. Follow-up raw measurements and validation are kept separately under `benchmarks/results/optimization` so the earlier Wings comparison remains reviewable. Request/message allocation reductions do not establish an equal whole-daemon memory reduction.

## UUID lookup and deferred logging, 15 October 2026

The [next performance pass](benchmarks/SCALABILITY.md) replaces manager point-lookup scans with a UUID index under the existing collection lock. Adds, duplicate checks, removals, and bulk replacement maintain the index and preserve first-match/order semantics. Collection inputs and snapshots own their slice storage. The index assumes the existing invariant that a server's UUID cannot change after initialization; configuration synchronization already rejects UUID changes.

Request log entries are constructed only when a handler needs them. Every request still receives its unique ID, and emitted entries retain request/server fields independently of context reuse. Existing custom logger fields and error URL redaction remain intact. The new regressions include concurrent manager mutation/read access, slice ownership, duplicate removal/promotion, and retained log entry fields. All 388 cases passed with race detection, plus vet and zero lint issues. The 1,000-registration benchmarks use in-memory records rather than 1,000 running games; measured gains do not establish player capacity or total RAM savings.

## Stats decoding and console inspection, 15 October 2026

The [latest measurements](benchmarks/TELEMETRY.md) reduce allocations in each Docker resource sample by decoding only reported counters, with fresh fields for each frame. Regression comparisons against the full SDK calculation cover cgroup-v1/v2 cache handling, zero/null cache precedence, CPU fallback/reset, multiple interfaces, integer precision above 2^53, uptime intervals, EOF/errors, and absent fields after previous samples. The existing CPU formula and resource reporting format remain intact.

Console startup inspection now returns immediately outside the starting state. Startup line copies, ANSI processing, matchers, and the later state check remain. Normal console delivery/throttling still runs. Regressions verify startup markers, input ownership, and running inspection while the process-configuration lock is held. All 409 test cases passed with race detection, vet passed, and lint reported zero issues.

Authenticated resource samples before and after deployment passed for all four local games. The short game workload and 16 Manta live hardening checks passed. The per-sample and per-line allocation reductions do not establish equal whole-daemon RAM/CPU reductions; the post-update daemon had a different heap history from earlier file-heavy runs. This pass did not repeat lifecycle timing or player-load benchmarks.

## WebSocket payload conversion, 15 October 2026

The [event-conversion pass](benchmarks/EVENTS.md) avoids decoding stats payloads into generic maps and reserializing them for each console viewer. It retains validated JSON for structured arguments and exact integer counters through the daemon. Strings keep JSON escape/Unicode replacement handling, and all outgoing messages still use the existing token, permission, deadline, and write-lock checks.

All 438 test cases passed with race detection, plus vet and zero lint issues. New wire tests check install/backup/transfer permission filtering and expired-token rejection. Automatic stats broadcasts passed before and after deployment with 16 authenticated viewers per benchmark game. The post-update workload passed 14,400 API requests without errors, delivered all 9,600 expected console messages, verified file transfers, and passed all 16 Manta live hardening checks. The report includes the small status-string timing tradeoff; no whole-daemon or player-capacity saving is inferred.

## File browsing and streaming, 15 October 2026

The [file-operation pass](benchmarks/FILES.md) reuses fixed-size MIME scratch buffers without caching file metadata or content-type results. Current file content is still probed with the inherited 4 KiB limit, and all existing filesystem containment checks remain. Directory responses encode their existing ten metadata fields in one JSON pass and sort folders/names with one comparator.

HTTP file contents stream through the existing Gin writer with pooled 64 KiB buffers. Content-length limits, MIME rewind, headers, byte accounting, flushing, and error handling remain. Buffer sizes do not grow with file size; lower per-operation allocations do not establish equal retained-memory savings.

All 464 Go test cases passed with race detection, plus vet and zero lint issues. Regressions include previous-detector MIME comparisons, short/error reads, concurrent buffer reuse, live file changes, directory/symlink/FIFO metadata, JSON escaping, streaming boundaries, and growth during a file read. The matched local stages each passed 14,400 API requests without errors, verified file/archive transfers, and delivered 9,600 console messages. All 16 Manta live checks and automatic stats checks passed. The language and Aquadactyl API format remain Go/Wings-compatible; the report does not infer game TPS or production player capacity from file-operation gains.

## Resource-event processing, 16 October 2026

The [resource-event pass](benchmarks/RESOURCE_EVENTS.md) removes the discarded generic counter tree and repeated full-envelope decoding. Stats payloads decode into fresh typed counters, including exact uint64 values. Periodic stats snapshot/JSON work is skipped when the server bus has no observers. Resource updates and disk-limit enforcement still run on every valid sample; API/requested stats snapshots remain available. Listener checks use the existing sink-pool lifetime lock.

State changes retain plain and escaped starting-state resets for the disk limiter and console throttler. Pull-status values retain their JSON representation. The shared event decoder passes valid pointer destinations directly to JSON decoding and preserves error wrapping; invalid nil/non-pointer destinations now return standard destination errors.

All 479 Go cases passed with race detection, plus vet and zero lint issues. New regressions cover observed/unobserved updates, malformed and missing fields, exact counters, subscriptions changing concurrently, and disk-limit stops/reset without observers. Live automatic-stat delivery, API-only resource sampling, and all 16 Manta hardening/compatibility checks passed. The measured savings are per event, not an inferred whole-daemon memory, CPU, TPS, or player-capacity improvement.
