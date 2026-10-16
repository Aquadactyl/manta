"""Summarize the separately retained stats/console optimization measurements."""
from pathlib import Path
import hashlib
import json
import re
import statistics

root = Path(__file__).parent
results = root / "results" / "telemetry"


def read_benchmarks(name, stage):
    samples = {}
    for line in (results / f"{name}-{stage}.txt").read_text().splitlines():
        match = re.match(r"^(Benchmark\S+)\s+\d+\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op", line)
        if match:
            key = re.sub(r"-\d+$", "", match[1])
            samples.setdefault(key, []).append([float(match[i]) for i in (2, 3, 4)])
    if not samples or any(len(s) != 5 for s in samples.values()):
        raise RuntimeError("Expected five samples for every benchmark")
    return {key: [statistics.median(s[i] for s in values) for i in range(3)]
            for key, values in samples.items()}


rows = []
for name in ("stats", "console"):
    before, after = (read_benchmarks(name, stage) for stage in ("before", "after"))
    for key in before:
        rows.append({"benchmark": key, "before_ns": before[key][0], "after_ns": after[key][0],
                     "before_bytes": before[key][1], "after_bytes": after[key][1],
                     "before_allocs": before[key][2], "after_allocs": after[key][2],
                     "time_change_percent": (after[key][0] / before[key][0] - 1) * 100,
                     "bytes_change_percent": (after[key][1] / before[key][1] - 1) * 100})

table = ["| Isolated benchmark | Before | After | Time change | Bytes/op before / after | Allocations/op before / after |",
         "| --- | ---: | ---: | ---: | ---: | ---: |"]
for r in rows:
    stats = r["benchmark"] == "BenchmarkDockerStatsStream"
    divisor, unit = (1000, "us") if stats else (1, "ns")
    label = "Docker stats decode + conversion" if stats else "Console inspection, " + r["benchmark"].split("/")[1]
    table.append(f"| {label} | {r['before_ns']/divisor:.3f} {unit} | {r['after_ns']/divisor:.3f} {unit} | "
                 f"{r['time_change_percent']:+.1f}% | {r['before_bytes']:g} / {r['after_bytes']:g} | "
                 f"{r['before_allocs']:g} / {r['after_allocs']:g} |")

validation = json.loads((results / "validation.json").read_text())
live_before = json.loads((results / "live-before.json").read_text())
live_after = json.loads((results / "live-after.json").read_text())
resources = json.loads((results / "live" / "resources.json").read_text())
security = json.loads((results / "live" / "security.json").read_text())
checks = [r for r in security if r.get("type") == "check" and r["variant"] == "manta"]
if not (live_before["passed"] and live_after["passed"] and live_after["servers_checked"] == 4):
    raise RuntimeError("Live telemetry validation failed")
if len(checks) != 16 or not all(r["pass"] for r in checks):
    raise RuntimeError("Optimized Manta live checks did not all pass")
meta = next(r for r in resources if r.get("type") == "metadata" and r["variant"] == "manta")
if meta["system"]["version"] != "1.13.3-manta-local-20261015-telemetry":
    raise RuntimeError("Live results came from an unexpected Manta version")

resource_table = ["| Post-update window | Daemon | CPU (% of one core) | Mean working memory |",
                  "| --- | --- | ---: | ---: |"]
ticks = ["| Post-update window | Paper daemon | TPS, one minute |",
         "| --- | --- | ---: |"]
for r in resources:
    if r.get("role") == "daemon":
        resource_table.append(f"| {r['test'].removeprefix('resources_')} | {r['variant']} | "
                              f"{r['cpu_core_percent']:.3f}% | {r['mean_working_mib']:.3f} MiB |")
    if r.get("type") == "game_console":
        text = "\n".join(json.loads(r["logs"])["data"])
        text = re.sub(r"\x1b\[[0-9;]*m", "", text)
        tps = re.findall(r"TPS from last.*?:\s*([\d.]+)", text)[-1]
        ticks.append(f"| {r['test'].removeprefix('paper_ticks_')} | {r['variant']} | {tps} |")

fixture = root.parent / "environment" / "docker" / "testdata" / "stats-v2.json"
fixture_hash = hashlib.sha256(fixture.read_bytes()).hexdigest()
report = f"""# Manta stats and console overhead

15 October 2026. This pass measures the stats decoder and console inspection against the immediately preceding Manta build. Earlier [lookup](SCALABILITY.md), [request/event](OPTIMIZATION.md), and [Wings](REPORT.md) results are retained separately. Improvements from separate passes should not be added arithmetically.

## Changes

Docker resource sampling now decodes only the fields Manta reports. It avoids constructing the unused memory-counter map, block-I/O arrays, and unused network counters on each frame. The CPU conversion remains the same. Memory handling retains cgroup-v1 precedence, including present zero/null counters; resource samples start with fresh fields so absent counters/interfaces cannot retain values from a previous frame.

Console inspection now exits once the environment is outside the starting state. The rest of console delivery and throttling still runs. Startup inspection keeps its private line copy, ANSI handling, matchers, and second state check; normal running lines avoid the copy and process-configuration lock.

## Isolated measurements

Five runs per case, 500 ms each, Go 1.26.9 on Linux amd64 with four CPU quota cores. The stats benchmark uses a persistent `json.Decoder` over the same captured Docker wire frame, followed by resource conversion; it excludes Docker HTTP transport, event fanout, and Panel work. Baseline sampling used the full SDK type behind the same extracted stream helper. Only the container ID/name were replaced in the fixture; numeric tokens were preserved without reserialization. The console benchmark measures inspection of a 156-byte line, excluding throttling, log fanout, and WebSocket transport. Both versions use identical benchmark code/settings.

{'\n'.join(table)}

The starting-state row is a control: its retained copy and checks show essentially unchanged cost. The large allocation reductions apply to the measured sample/inspection paths. They do not establish equal whole-daemon CPU/RAM reductions, higher game TPS, or additional player capacity. Only two games per daemon run on this shared development host.

Fixture SHA-256: `{fixture_hash}`. Source: [captured cgroup-v2 frame](../environment/docker/testdata/stats-v2.json).

## Correctness and live validation

- {validation['passed_cases']} Go test cases in {validation['passed_packages']} packages passed with race detection; {validation['failed']} failures and {validation['skipped_cases']} skipped cases. Vet passed; golangci-lint 2.11.4 reported zero issues.
- Regression comparisons use the full SDK response and previous memory calculation as a reference. Cases cover captured v2 counters, v1 cache precedence, zero/null caches, caches above usage, missing fields, multiple interfaces, integer values above 2^53, CPU core fallback/reset, read intervals, stream resets/EOF, and invalid used counters.
- Console regressions cover plain/ANSI startup markers, unchanged input buffers, and running output proceeding while the process-configuration lock is held. Existing state, console-ordering, cancellation, and power tests also passed.
- Before and after deployment, authenticated Panel requests sampled all four games three times. All had positive memory/uptime, valid memory limits/CPU/network values, non-decreasing network counters, and advancing uptime. The checks compare valid behavior over time, not identical values from different moments.
- The short idle/console-block workload completed with native Paper/Bedrock query responses. The daemon had just been recreated, and this pass omitted the repeated 16 MiB API/SFTP transfers used before earlier resource samples. These windows also preceded the large-stream security checks. Its heap history therefore differs from earlier passes. No matching daemon-resource baseline was collected here; the memory values below cannot establish a whole-daemon saving.
- All 16 optimized Manta live hardening/compatibility checks passed. The existing games remained running during the daemon update. Game binaries, limits, and startup settings were unchanged.

{'\n'.join(resource_table)}

{'\n'.join(ticks)}

## Deployment and reproduction

The deployed local image is `manta:local`, version `{meta['system']['version']}`, Go `{meta['go_version']}`, image `{meta['image_id']}`. It retains two CPU quota cores, 1 GiB memory, and `GOMAXPROCS=2`. The previous image is retained as `manta:before-telemetry-20261015`.

Raw files and validation are in [results/telemetry](results/telemetry/). Rebuild this document using `python benchmarks/summarize_telemetry.py`.

Use `go test ./environment/docker -run '^$' -bench BenchmarkDockerStatsStream -benchmem -count=5 -benchtime=500ms` and `go test ./server -run '^$' -bench BenchmarkConsoleInspection -benchmem -count=5 -benchtime=500ms` with identical Linux toolchain/CPU settings on each source version.

Repeat live sampling by copying `benchmarks/check-telemetry.php` into the existing local Panel and running it from `/app`; it refuses non-local environments and selects only the four existing benchmark servers. Run `benchmarks/Run-Benchmark.ps1 -Phases resources,security -OutputDirectory benchmarks/results/my-telemetry-run` for workload/security checks. The existing provisioned test stack is required. This run did not repeat lifecycle timings; startup behavior is covered by the startup regression cases, and no shutdown improvement is claimed.
"""
(root / "TELEMETRY.md").write_text(report, encoding="utf-8")
(results / "summary.json").write_text(json.dumps(rows, indent=2) + "\n")
print("\n".join(table))
