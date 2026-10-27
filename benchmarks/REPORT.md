# Local Manta versus stock Wings benchmark

15 October 2026. Results are mixed: Manta improves directory listing and several write/batch operations, while the small status API is slower, reported shutdown takes longer, and daemon working memory is higher. Game tick performance is similar. These results do not establish a universal speed-up.

The later [performance follow-up](OPTIMIZATION.md) measures targeted request/event optimizations against this Manta build. The original comparison below is retained as measured.

The subsequent [lookup and allocation comparison](SCALABILITY.md) measures the next Manta pass with up to 1,000 in-memory registrations.

The [stats and console comparison](TELEMETRY.md) measures the periodic decoder and console-inspection paths.

The [WebSocket event comparison](EVENTS.md) measures per-viewer event serialization and checks automatic resource broadcasts.

The [file browsing and streaming comparison](FILES.md) profiles file-manager overhead and measures directory responses and buffered file reads, with a matched Manta build comparison and unchanged Wings control.

The latest [resource-event comparison](RESOURCE_EVENTS.md) measures internal stats decoding and skips periodic broadcast encoding when no observer is registered, with live API and console checks.

## Setup

- Dedicated **Local Manta** node 2 and **Benchmark Wings** node 3, each managing one Paper 26.3 server and one Bedrock 1.26.52 server. The existing Local Wings node 1 and its user servers were excluded from measured requests.
- Both daemons: two CPU quota cores, 1 GiB memory, `GOMAXPROCS=2`. Both Paper servers: 1,536 MiB configured memory, 200% CPU. Both Bedrock servers: 512 MiB, 200% CPU. Actual Docker limits match: Paper 1,852,204,646 bytes; Bedrock 617,401,549 bytes; CPU period 100,000 and quota 200,000; PID limit 512; read-only container roots.
- Docker Desktop 29.8.1, WSL2 Linux 6.18.54.1, 28 logical CPUs and approximately 31.2 GiB available RAM, on the same host and Docker-backed storage.
- Stock baseline: official `ghcr.io/pterodactyl/wings:v1.13.3`, with only a BusyBox probe added. Stock binary Go version: 1.24.11. Manta: current local checkout, Go 1.27.2. Compiler and dependency differences are part of this distribution comparison; the experiment cannot attribute every difference solely to the latest source changes.
- Game binaries were copied from the existing cached installations through a controlled local installer. Both variants ran identical binaries and startup settings, seed 746574, four-chunk view/simulation distances, and fresh benchmark worlds. Installers completed normally on both daemons. No user worlds were copied or benchmarked.
- Paper jar SHA-256: `35a6c650e2f7f540e8f62e947ff00614242c25ec68befbc54a5f668b2fbecbdb`.
- Bedrock binary SHA-256: `0a490c711d4a2ce075debcd979e8862b7eaba008373f9970f0edbb67f98f9207`.

## Method

Lifecycle: one initial world-creation start and three warm start/stop repetitions, alternating which daemon runs first. Startup/shutdown times are measured from the HTTP power request until the daemon reports running/offline, polled every 100 ms. The shutdown measurement is the Panel-visible state transition, not a separately timed OS process exit.

API and file operations: three alternating-order repetitions. Each API combination uses 400 requests at concurrency 1, 16, and 32. Transfers use five samples per run and a shared random 16 MiB payload. Read integrity is checked outside the transfer timer. SFTP connection authentication is outside the transfer timer and runs against the same local Panel. Archive round trips also verify the restored payload. Tables show the median across the three per-run medians; p95 is the median of the three per-run p95 values. Negative changes mean a lower measured value, not a single overall performance score.

Resources: one approximately 30-second idle window and one approximately 30-second command-load window, with 15 working-set samples per container. CPU is accumulated container CPU time divided by elapsed wall time, expressed as a percentage of one core. Memory is Docker working set (usage minus inactive file cache), measured after the preceding workloads without forcing garbage collection. These are short observations, not a capacity or endurance test.

The command workload alternates 4,096 blocks between stone and air at five commands per second per game server, with matching forced/ticking areas. Server-list queries run while that workload remains active. Java values include TCP connection and status response; Bedrock values are RakNet unconnected UDP ping responses. They are not authenticated player latency. No simulated or real players logged in; this is a console/file/control workload, not a multiplayer capacity benchmark.

## Lifecycle and control/file results

| Metric                                 | Stock Wings |       Manta | Manta change |
| -------------------------------------- | ----------: | ----------: | -----------: |
| Paper warm startup                     | 9588.991 ms | 8883.835 ms |        -7.4% |
| Paper reported offline                 |  505.592 ms |  909.183 ms |       +79.8% |
| Bedrock warm startup                   | 2726.164 ms | 2423.463 ms |       -11.1% |
| Bedrock reported offline               |  505.251 ms | 1111.231 ms |      +119.9% |
| Server API, concurrency 1              |    0.187 ms |    0.229 ms |       +22.5% |
| Server API, concurrency 1, p95         |    0.324 ms |    0.354 ms |        +9.3% |
| Server API, concurrency 16             |    0.202 ms |    0.259 ms |       +28.2% |
| Server API, concurrency 16, p95        |    0.820 ms |    1.201 ms |       +46.5% |
| Server API, concurrency 32             |    0.472 ms |    0.521 ms |       +10.4% |
| Server API, concurrency 32, p95        |    1.683 ms |    2.507 ms |       +49.0% |
| List 1,000 files, concurrency 1        |   11.723 ms |    9.706 ms |       -17.2% |
| List 1,000 files, concurrency 1, p95   |   13.487 ms |   11.270 ms |       -16.4% |
| List 1,000 files, concurrency 16       |   95.972 ms |   82.549 ms |       -14.0% |
| List 1,000 files, concurrency 16, p95  |  227.550 ms |  187.225 ms |       -17.7% |
| List 1,000 files, concurrency 32       |  187.871 ms |  157.725 ms |       -16.0% |
| List 1,000 files, concurrency 32, p95  |  477.314 ms |  390.451 ms |       -18.2% |
| HTTP write 16 MiB                      |   15.248 ms |   13.056 ms |       -14.4% |
| HTTP read 16 MiB                       |   29.726 ms |   29.818 ms |        +0.3% |
| SFTP write 16 MiB                      |  116.941 ms |  105.011 ms |       -10.2% |
| SFTP read 16 MiB                       |   59.000 ms |   62.443 ms |        +5.8% |
| WebSocket log delivery, 16 subscribers |    9.709 ms |   10.228 ms |        +5.3% |
| Chmod 1,000 files                      |    5.423 ms |    4.195 ms |       -22.6% |
| Rename 1,000 files                     |   16.827 ms |   16.933 ms |        +0.6% |
| Compress 16 MiB + 1,000 files          |   18.080 ms |   19.211 ms |        +6.3% |
| Extract 16 MiB + 1,000 files           |   53.666 ms |   57.276 ms |        +6.7% |

## Game and resource observations

| Metric                            |          Stock Wings |                Manta | Manta change |
| --------------------------------- | -------------------: | -------------------: | -----------: |
| Daemon idle mean memory           |           50.641 MiB |           59.222 MiB |       +16.9% |
| Daemon idle CPU                   |  0.454 % of one core |  0.418 % of one core |        -7.9% |
| Paper idle mean memory            |          744.133 MiB |          740.720 MiB |        -0.5% |
| Paper idle CPU                    |  1.764 % of one core |  1.719 % of one core |        -2.6% |
| Bedrock idle mean memory          |          185.704 MiB |          185.672 MiB |        -0.0% |
| Bedrock idle CPU                  |  2.467 % of one core |  2.565 % of one core |        +4.0% |
| Paper query during idle           |             1.260 ms |             1.273 ms |        +1.0% |
| Bedrock query during idle         |             0.332 ms |             0.339 ms |        +2.1% |
| Daemon command_load mean memory   |           50.118 MiB |           52.677 MiB |        +5.1% |
| Daemon command_load CPU           |  1.559 % of one core |  1.625 % of one core |        +4.3% |
| Paper command_load mean memory    |          767.473 MiB |          763.166 MiB |        -0.6% |
| Paper command_load CPU            | 17.307 % of one core | 18.093 % of one core |        +4.5% |
| Bedrock command_load mean memory  |          251.121 MiB |          250.720 MiB |        -0.2% |
| Bedrock command_load CPU          | 11.301 % of one core | 11.328 % of one core |        +0.2% |
| Paper query during command_load   |             1.370 ms |             1.434 ms |        +4.7% |
| Bedrock query during command_load |             0.463 ms |             0.388 ms |       -16.2% |

| Phase        | Daemon | TPS, 1 minute | Tick time average, last 5 seconds |
| ------------ | ------ | ------------: | --------------------------------: |
| idle         | manta  |          20.0 |                            0.2 ms |
| idle         | wings  |          20.0 |                            0.2 ms |
| command_load | manta  |          19.7 |                            1.0 ms |
| command_load | wings  |          19.7 |                            1.1 ms |

Both Paper servers held 20 TPS at idle and reported 19.7 TPS over the one-minute window containing forced-chunk initialization and command load. The final five-second tick averages were about 1 ms for both. The first forced-chunk load produced a roughly 1.2-second maximum tick on both; the short test does not show a meaningful multiplayer tick advantage.

## Live hardening and integration checks

Manta passed 16 of 16 checks. Stock differences below identify behavior/policy differences and missing identifier support; they are not a count of independently exploitable vulnerabilities.

| Live check                         | Manta               | Stock Wings                       |
| ---------------------------------- | ------------------- | --------------------------------- |
| expired_token                      | Pass (jwt error)    | Pass (jwt error)                  |
| full_uuid_sftp                     | Pass                | Different behavior                |
| future_issued_token                | Pass (jwt error)    | Different behavior (auth success) |
| future_not_before_token            | Pass (jwt error)    | Different behavior (auth success) |
| invalid_upload_token_log_redaction | Pass (HTTP 500)     | Different behavior (HTTP 500)     |
| missing_expiry_token               | Pass (jwt error)    | Pass (jwt error)                  |
| modern_identifier_sftp             | Pass                | Different behavior                |
| multiple_json_documents            | Pass (HTTP 400)     | Different behavior (HTTP 204)     |
| one_time_file_token                | Pass                | Pass                              |
| oversized_json_chunked             | Pass (HTTP 413)     | Different behavior (HTTP 204)     |
| oversized_json_known               | Pass (HTTP 413)     | Different behavior (HTTP 204)     |
| raw_stream_above_json_limit        | Pass                | Pass                              |
| sftp_symlink_containment           | Pass                | Pass                              |
| ssrf_loopback_blocked              | Pass (HTTP 400)     | Pass (HTTP 400)                   |
| unauthenticated_api                | Pass (HTTP 401)     | Pass (HTTP 401)                   |
| valid_websocket_token              | Pass (auth success) | Pass (auth success)               |

The loopback download check used a verified, temporary HTTP listener bound inside each daemon to `127.0.0.1:19090`, serving only a harmless sentinel. Both refused retrieval. An earlier closed-port probe returned 500 on stock Wings and was inconclusive; it was replaced with the live sentinel test. No SSRF bypass was demonstrated. Both reject symlink reads outside their server root.

The log-redaction check used a synthetic invalid token marker. Manta omitted it from its logs; stock Wings logged it. File token replay was tested immediately after first use with a three-hour signed expiry; retention through the entire expiry is covered by the unit test, not a three-hour live wait.

## Validation and integration fix

- 14,400 measured API requests, 0 errors.
- 9,600/9,600 expected WebSocket log deliveries received.
- HTTP, SFTP, and archive payload checks passed.
- Final Go race suite: 381 cases in 22 packages passed, 0 failures. `go vet ./...` passed; golangci-lint 2.11.4 reported zero issues. Tests used Go 1.26.9; the deployed Manta image used Go 1.27.2. Details are in [validation.json](results/validation.json).
- Aquadactyl SFTP integration suite: 18 tests, 74 assertions, all passed in its isolated test database.
- Live testing discovered the Panel rejected legacy eight-character UUID prefixes, while the daemon rejected the Panel's modern `serv_...` identifiers. Manta now accepts short IDs, full UUIDs, and properly shaped modern IDs. Aquadactyl now resolves legacy short IDs within the authenticated node and rejects ambiguous matches. Changes are in both repositories and were applied to the running local test Panel.

## Limits and next targets

This is one shared development host, with background user servers left running. Three alternating runs reduce ordering bias but do not supply confidence intervals or prove production capacity. The Go versions differ. Loopback Docker networking and Linux volume caches are much faster than many real deployments. Only two managed servers per daemon were tested; startup pagination limits were covered by regression tests, not a many-node live test. CPU burst could not be applied from these containerized daemons because the host cgroup path is unavailable; no burst-performance claim is made.

The prior 99.8% allocation reduction applies to the isolated serialized-request microbenchmark. It is not an overall daemon memory reduction: this run observed higher Manta daemon working memory after equivalent activity. The main remaining tuning targets from this run are retained daemon memory, small status API latency, and the Panel-visible shutdown delay. A later comparison with the same compiler, authenticated player bots, more managed servers, longer runs, and explicit CPU/I/O pressure would be needed for a production performance claim.

## Test servers left available

| Server                  | Panel                                         | Local game address |
| ----------------------- | --------------------------------------------- | ------------------ |
| Benchmark Manta Paper   | <http://localhost:8081/admin/servers/view/7>  | localhost:27015    |
| Benchmark Manta Bedrock | <http://localhost:8081/admin/servers/view/8>  | localhost:27016    |
| Benchmark Wings Paper   | <http://localhost:8081/admin/servers/view/9>  | localhost:27115    |
| Benchmark Wings Bedrock | <http://localhost:8081/admin/servers/view/10> | localhost:27116    |

All game ports bind to host loopback. The test worlds use offline authentication for local testing. Benchmark forced/ticking areas were removed after measurement. Raw JSON files in `results/` and the runner source are retained for review and reproduction; private credentials are excluded.
