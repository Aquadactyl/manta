"""Summarize the measured Manta optimization without replacing the Wings report."""
from pathlib import Path
import json
import re
import statistics

root = Path(__file__).parent
results = root / "results" / "optimization"


def micro(name, stage):
    values = {}
    for line in (results / f"{name}-{stage}.txt").read_text().splitlines():
        match = re.match(r"^(Benchmark\S+)\s+\d+\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op", line)
        if match:
            key = re.sub(r"-\d+$", "", match[1])
            values.setdefault(key, []).append(tuple(float(match[i]) for i in (2, 3, 4)))
    if not values or any(len(samples) != 5 for samples in values.values()):
        raise RuntimeError(f"Expected five samples for {name}-{stage}")
    return {key: [statistics.median(s[i] for s in samples) for i in range(3)]
            for key, samples in values.items()}


micro_rows = []
for name in ("router", "fanout"):
    before, after = (micro(name, stage) for stage in ("before", "after"))
    for key in before:
        micro_rows.append({"benchmark": key, "before_ns": before[key][0], "after_ns": after[key][0],
                           "before_bytes": before[key][1], "after_bytes": after[key][1],
                           "before_allocs": before[key][2], "after_allocs": after[key][2],
                           "time_change_percent": (after[key][0] / before[key][0] - 1) * 100})

phases = {stage: {phase: json.loads((results / stage / f"{phase}.json").read_text())
                  for phase in ("performance", "resources")}
          for stage in ("before", "after")}
security = json.loads((results / "after" / "security.json").read_text())
validation = json.loads((results / "validation.json").read_text())
live_rows = []


def live(label, test, metric="p50_ms", concurrency=None):
    row = {"metric": label}
    for stage in ("before", "after"):
        for variant in ("manta", "wings"):
            samples = [r[metric] for r in phases[stage]["performance"]
                       if r.get("test") == test and r["variant"] == variant
                       and (concurrency is None or r["concurrency"] == concurrency)]
            if len(samples) != 3:
                raise RuntimeError(f"Expected three runs for {label}, {stage}, {variant}")
            row[f"{variant}_{stage}_ms"] = statistics.median(samples)
    row["manta_change_percent"] = (row["manta_after_ms"] / row["manta_before_ms"] - 1) * 100
    live_rows.append(row)


for test, label in (("api_status", "Status API"), ("directory_1000", "List 1,000 files")):
    for concurrency in (1, 16, 32):
        live(f"{label}, concurrency {concurrency}", test, concurrency=concurrency)
        live(f"{label}, concurrency {concurrency}, p95", test, "p95_ms", concurrency)
for test, label in (("http_write_16m", "HTTP write 16 MiB"), ("http_read_16m", "HTTP read 16 MiB"),
                    ("sftp_write_16m", "SFTP write 16 MiB"), ("sftp_read_16m", "SFTP read 16 MiB"),
                    ("websocket_16_subscribers_100_lines", "Log delivery, 16 subscribers")):
    live(label, test)
for test, label in (("chmod_1000", "Chmod 1,000 files"), ("rename_1000", "Rename 1,000 files")):
    live(label, test, "duration_ms")
live("Compress 16 MiB + 1,000 files", "archive_16m_1000_files", "compress_ms")
live("Extract 16 MiB + 1,000 files", "archive_16m_1000_files", "extract_ms")

micro_table = ["| Local benchmark | Before | After | Time change | Bytes/op before → after | Allocations/op before → after |",
               "| --- | ---: | ---: | ---: | ---: | ---: |"]
for r in micro_rows:
    micro_table.append(f"| {r['benchmark']} | {r['before_ns']:.2f} ns | {r['after_ns']:.2f} ns | "
                       f"{r['time_change_percent']:+.1f}% | {r['before_bytes']:g} → {r['after_bytes']:g} | "
                       f"{r['before_allocs']:g} → {r['after_allocs']:g} |")

live_table = ["| Live metric | Manta before | Manta after | Manta change | Stock before | Stock after |",
              "| --- | ---: | ---: | ---: | ---: | ---: |"]
for r in live_rows:
    live_table.append(f"| {r['metric']} | {r['manta_before_ms']:.3f} ms | {r['manta_after_ms']:.3f} ms | "
                      f"{r['manta_change_percent']:+.1f}% | {r['wings_before_ms']:.3f} ms | {r['wings_after_ms']:.3f} ms |")

resource_table = ["| Window | Variant | Daemon CPU (% of one core) | Daemon mean working memory |",
                  "| --- | --- | ---: | ---: |"]
for stage in ("before", "after"):
    for r in phases[stage]["resources"]:
        if r.get("role") == "daemon":
            resource_table.append(f"| {stage}, {r['test'].removeprefix('resources_')} | {r['variant']} | "
                                  f"{r['cpu_core_percent']:.3f}% | {r['mean_working_mib']:.3f} MiB |")

api_rows = [r for p in phases.values() for r in p["performance"]
            if r.get("test") in ("api_status", "directory_1000")]
socket_rows = [r for p in phases.values() for r in p["performance"]
               if r.get("test") == "websocket_16_subscribers_100_lines"]
checks = [r for r in security if r.get("type") == "check" and r["variant"] == "manta"]
status_tail = max(r["p95_ms"] for r in phases["after"]["performance"]
                  if r.get("test") == "api_status" and r["variant"] == "manta" and r["concurrency"] == 32)
if any(r["errors"] for r in api_rows) or any(r["received_messages"] != r["expected_messages"] for r in socket_rows):
    raise RuntimeError("Live workload has errors; review raw data")
if len(checks) != 16 or not all(r["pass"] for r in checks):
    raise RuntimeError("Live Manta hardening checks did not all pass")

report = f"""# Manta performance follow-up

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

{'\n'.join(micro_table)}

## Live Docker measurements

The previous and optimized daemons both use Go 1.27.2, two CPU quota cores, 1 GiB memory, and `GOMAXPROCS=2`. Each manages the same Paper and Bedrock server. Game containers, worlds, binaries, limits, and stock Wings image remain the same. Each pass uses three alternating-order repetitions of the original workload. This is sequential before/after observation on a shared development host; it is not a randomized crossover or a production load test. Additional Panel benchmark containers appeared on the host between passes. The unchanged stock results are included to expose variation; live timing changes cannot all be attributed to this patch.

{'\n'.join(live_table)}

Manta's status medians improved at concurrency 16 and 32, while the single-request median increased and file-operation timings remained mixed. One optimized concurrency-32 status run had a p95 of {status_tail:.3f} ms; the table shows the median of the three per-run p95 values, so that tail spike remains visible in the raw data. These observations support reducing internal overhead, not a uniform end-to-end speed-up.

The optimized Manta daemon was recreated between passes and reattached to its running games. It completed the same file/control workload before resource sampling. The earlier daemon had a longer prior workload history, and resource windows are only about 30 seconds each. Consequently, these working-set/CPU observations cannot establish a retained-memory reduction. The raw resource files also contain game CPU/memory, native queries, and Paper tick reports; no game-engine configuration was tuned for this change.

{'\n'.join(resource_table)}

## Validation and availability

- Go race suite: {validation['passed_cases']} cases in {validation['passed_packages']} packages, {validation['failed']} failures. `go vet ./...` passed; golangci-lint 2.11.4: zero issues.
- New regressions cover missing/incorrect authentication, live token rotation on global and server routes, and debug-log level/redaction behavior. Existing console overflow, timeout, token, containment, and race checks passed.
- {sum(r['n'] for r in api_rows):,} measured API requests across both passes, zero errors.
- {sum(r['received_messages'] for r in socket_rows):,}/{sum(r['expected_messages'] for r in socket_rows):,} expected WebSocket deliveries received; HTTP/SFTP/archive integrity checks passed.
- All {len(checks)} optimized Manta live hardening/compatibility checks passed.
- For this recorded pass, the optimized image was deployed as `manta:local`, version `1.13.3-manta-local-20261015-perf`. The previous image was retained as `manta:before-perf-20261015` for comparison. See the subsequent report linked above for the latest deployment; all four benchmark games remain available through the existing Panel.

The previous shutdown-reporting delay remains a separate tuning target; this patch changes no power/lifecycle code and makes no shutdown improvement claim. More managed servers, authenticated player bots, sustained pressure, and longer runs are still needed to measure production capacity. Lower allocation per request or message does not imply the same percentage reduction in whole-daemon RAM.

## Reproduction

Raw data: [results/optimization](results/optimization/). Metadata in each live file records the actual image IDs, toolchains, caps, and server identifiers. Rebuild this summary with `python benchmarks/summarize_optimization.py`.

The original and optimized isolated runs used `go test ./router -run '^$' -bench BenchmarkServerStatus -benchmem -count=5 -benchtime=1s` and `go test ./system -run '^$' -bench BenchmarkSinkPoolReady -benchmem -count=5 -benchtime=500ms`, with identical CPU/toolchain settings and profiling enabled on both router runs. Run live phases with `benchmarks/Run-Benchmark.ps1 -Phases performance,resources,security -OutputDirectory benchmarks/results/optimization/after`; save results to another directory to preserve the recorded comparison.
"""
(root / "OPTIMIZATION.md").write_text(report, encoding="utf-8")
(results / "summary.json").write_text(json.dumps({"micro": micro_rows, "live": live_rows}, indent=2) + "\n")
print("\n".join(micro_table).replace("\u2192", "->"))
