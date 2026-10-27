# Manta WebSocket event conversion

15 October 2026. This pass compares event conversion against the preceding Manta stats/console build. Earlier [telemetry](TELEMETRY.md), [lookup](SCALABILITY.md), [request/event](OPTIMIZATION.md), and [Wings](REPORT.md) measurements remain separate.

## Change

Each console viewer previously decoded bus payloads into generic maps and floating-point numbers, then serialized the payload again. Manta now decodes the event envelope directly into the string argument expected by Aquadactyl. Object/array/number payloads retain their existing JSON bytes; plain strings are unquoted, and escaped or invalid UTF-8 strings retain Go JSON's decoding/replacement behavior. Missing/null data still produces the string `null`.

This removes the intermediate object tree and preserves integer counters above 2^53 through Manta's WebSocket serialization. JavaScript clients still need an appropriate parser to represent integers above their safe range. Nested JSON member order can now follow the original producer rather than sorted map order; parsed values and the outer `event`/`args` protocol remain compatible. Authentication, permissions, write serialization, deadlines, and sink behavior still pass through the existing `SendJson` gate.

## Measurements

Five runs per case, 500 ms each, Go 1.26.9 Linux amd64, four CPU quota cores, on the shared development host. The identical benchmark calls the actual conversion helper and outer message JSON encoder. The baseline helper was extracted from the previous listener without changing its map/string conversion. A representative synthetic stats envelope uses the `Server.Proc` payload shape. The 16/64-viewer cases repeat that conversion sequentially for each viewer; they measure aggregate serialization work, excluding network writes, JWT checks, compression, scheduling, and bus waits.

| Conversion and outer JSON encoding |     Before |      After | Time change | Bytes/op before / after | Allocations/op before / after |
| ---------------------------------- | ---------: | ---------: | ----------: | ----------------------: | ----------------------------: |
| Stats, 1 viewer                    |   4.453 us |   1.832 us |      -58.9% |              2600 / 816 |                       63 / 13 |
| Stats, 16 viewers                  |  71.560 us |  29.612 us |      -58.6% |           41616 / 13063 |                    1008 / 208 |
| Stats, 64 viewers                  | 280.421 us | 118.967 us |      -57.6% |          166463 / 52251 |                    4032 / 832 |
| Status string                      |   0.774 us |   0.832 us |       +7.5% |               408 / 392 |                       12 / 11 |

The status-string control costs about 58 ns more (+7.5%) while allocating fewer bytes and objects. The change is retained for the larger periodic stats improvement and integer preservation. These isolated results do not establish equal whole-daemon CPU/RAM savings, higher game TPS, or additional player capacity. Results from separate optimization passes must not be added arithmetically.

## Validation

- 438 Go test cases in 22 packages passed with race detection; 0 failures and 0 skipped cases. Vet passed and golangci-lint 2.11.4 reported zero issues.
- Payload regressions cover strings, escaping, Unicode, invalid UTF-8, surrogate replacement, objects, arrays, booleans, null/missing data, byte-slice base64 behavior, malformed envelopes, input ownership, and exact uint64 counters. Real WebSocket listener tests exercise allowed/denied install, backup, and transfer events, plus expired-token rejection.
- Before and after deployment, 16 authenticated viewers on each of four Paper/Bedrock benchmark servers each received three **automatic** stats events: 192 samples before and 192 after. These tests send no `send stats` requests, whose replies take a different path. They check the API field schema, typed counters, valid resource values, non-regressing network counters, and advancing uptime. API and streamed samples are taken at different times, so equal numeric values are not required.
- The post-update workload completed 14,400 API requests with zero errors, verified 16 MiB HTTP/SFTP/archive transfers, and delivered all 9,600 expected console messages across the repeated 16-viewer checks. These are compatibility/regression observations; this pass does not claim faster file operations.
- All 16 Manta live hardening/compatibility checks passed. The dedicated benchmark games stayed running through the daemon update. No player-load, lifecycle-timing, or whole-daemon resource comparison was repeated in this pass.

## Deployment and reproduction

The healthy local `manta:local` deployment is version `1.13.3-manta-local-20261015-events`, Go `go1.27.2`, image `sha256:2528243f3ca11a3d4092220844eb1fea732116be5aea630231029c50c6c00f4c`. It retains two CPU quota cores, 1 GiB memory and `GOMAXPROCS=2`. The previous image is retained as `manta:before-events-20261015` (sha256:ec82bc587a01876a1454d393884ee0e1122659595e797d3bc39d6d3c1f881c74).

Raw evidence is under [results/events](results/events/). Rebuild this report with `python benchmarks/summarize_events.py`.

Run the isolated comparison against each source version with identical toolchain/CPU settings:

```sh
go test ./router/websocket -run '^$' -bench BenchmarkEventMessage -benchmem -count=5 -benchtime=500ms
```

The existing local test stack is required for live checks:

```powershell
.\benchmarks\Run-Benchmark.ps1 -Phases events,performance,security -OutputDirectory benchmarks/results/my-events-run
```

The `events` phase validates automatic resource broadcasts independently of the console-line workload. All phases select only the four dedicated benchmark servers.
