"""Generate the separately retained UUID-index/lazy-logger comparison."""
from pathlib import Path
import json
import re
import statistics

root = Path(__file__).parent
results = root / "results" / "scalability"


def measurements(name, stage):
    samples = {}
    for line in (results / f"{name}-{stage}.txt").read_text().splitlines():
        match = re.match(r"^(Benchmark\S+)\s+\d+\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op", line)
        if match:
            key = re.sub(r"-\d+$", "", match[1])
            samples.setdefault(key, []).append([float(match[i]) for i in (2, 3, 4)])
    if not samples or any(len(s) != 5 for s in samples.values()):
        raise RuntimeError("Expected five runs for every benchmark")
    return {key: [statistics.median(s[i] for s in values) for i in range(3)]
            for key, values in samples.items()}


rows = []
for name in ("router", "lookup"):
    before, after = (measurements(name, stage) for stage in ("before", "after"))
    for key in before:
        rows.append({"kind": name, "benchmark": key, "before_ns": before[key][0], "after_ns": after[key][0],
                     "before_bytes": before[key][1], "after_bytes": after[key][1],
                     "before_allocs": before[key][2], "after_allocs": after[key][2],
                     "speedup": before[key][0] / after[key][0],
                     "time_change_percent": (after[key][0] / before[key][0] - 1) * 100})


def table(kind):
    lines = ["| Benchmark | Before | After | Speed-up | Bytes/op before / after | Allocations/op before / after |",
             "| --- | ---: | ---: | ---: | ---: | ---: |"]
    for r in rows:
        if r["kind"] == kind:
            parts = r["benchmark"].split("/")
            if kind == "router":
                count = 1 if len(parts) == 1 else int(parts[1])
                label = f"Status route, {count:,} registration" + ("s" if count != 1 else "")
            else:
                count = int(parts[1])
                label = f"{count:,} registration" + ("s" if count != 1 else "")
                label += ", " + ("missing UUID" if parts[2] == "missing=true" else "last UUID")
            lines.append(f"| {label} | {r['before_ns']:.2f} ns | {r['after_ns']:.2f} ns | "
                         f"{r['speedup']:.2f}x | {r['before_bytes']:g} / {r['after_bytes']:g} | "
                         f"{r['before_allocs']:g} / {r['after_allocs']:g} |")
    return "\n".join(lines)


validation = json.loads((results / "validation.json").read_text())
performance = json.loads((results / "live" / "performance.json").read_text())
security = json.loads((results / "live" / "security.json").read_text())
api = [r for r in performance if r.get("test") in ("api_status", "directory_1000")]
sockets = [r for r in performance if r.get("test") == "websocket_16_subscribers_100_lines"]
checks = [r for r in security if r.get("type") == "check" and r["variant"] == "manta"]
if len(checks) != 16 or not all(r["pass"] for r in checks):
    raise RuntimeError("Live Manta checks did not all pass")
if any(r["errors"] for r in api) or any(r["expected_messages"] != r["received_messages"] for r in sockets):
    raise RuntimeError("Live performance workload reported errors")
single = next(r for r in rows if r["benchmark"] == "BenchmarkServerStatus")
large = next(r for r in rows if r["benchmark"] == "BenchmarkServerStatusScale/1000")

report = f"""# Manta lookup and request-allocation improvements

15 October 2026. This pass compares the immediately preceding Manta build with indexed server lookup and deferred request-logger construction. The [first optimization report](OPTIMIZATION.md) and [original Wings comparison](REPORT.md) remain intact. All values below are newly measured for this pass; improvements should not be added arithmetically to earlier percentages.

The subsequent [stats and console pass](TELEMETRY.md) records the latest runtime. This document retains its measured comparison.

The full local status-route median changed from {single['before_ns']/1000:.3f} to {single['after_ns']/1000:.3f} microseconds with one registration. At 1,000 in-memory registrations, the last-entry route changed from {large['before_ns']/1000:.3f} to {large['after_ns']/1000:.3f} microseconds ({large['speedup']:.2f}x speed-up). Allocated bytes per one-registration request changed from {single['before_bytes']:,.0f} to {single['after_bytes']:,.0f}. These measurements describe daemon routing overhead, not game TPS or player capacity.

## Changes

Server lookup now uses a UUID index guarded by the manager's existing lock. Request routing, SFTP, state recovery, and duplicate-registration checks can retrieve a server without scanning and locking every registered server configuration. The ordered collection remains available for iteration. Adds, removals, and bulk replacement keep the index synchronized; duplicate IDs retain the previous first-match behavior. UUIDs remain immutable through configuration sync.

Collection inputs and returned snapshots own their slice storage, preventing callers from replacing elements outside the manager lock and desynchronizing the index. This adds snapshot allocation to collection reads. The index itself consumes additional memory proportional to server count, and bulk replacement/removal still takes linear work. Point lookups allocate no memory in either version; the improvement there is CPU time.

Successful requests no longer construct unused request log entries. The first caller of `ExtractLogger` creates and caches an independent log entry with request/server fields. Request IDs are still generated and returned for every request. Existing custom logger fields are preserved, and background tasks receive an entry that remains valid after Gin reuses its context. Error logging and URL redaction remain covered by regression/live checks.

## Method and measured results

Linux amd64, Go 1.26.9, four CPU quota cores, five runs per case with 500 ms benchmark duration; tables show medians. Both versions use identical benchmark code and settings. This shared development host still has other workloads; allocation counts are stronger evidence than small timing differences. No confidence intervals are claimed.

The routing benchmark exercises the full Gin route, authorization, resource/configuration serialization, and response writing into an in-memory HTTP recorder. It excludes network latency and Docker. The target server is registered last to expose the previous scan's worst case. At scale, the other registrations are in-memory server objects with real configuration locks, not live game containers. The target server retains a real temporary filesystem and a stub environment, matching the original one-server routing benchmark. `BenchmarkServerStatus` is the one-registration case.

{table('router')}

The point-lookup benchmark measures the last registered UUID and a missing UUID. Earlier hits in the old scan would be cheaper, so these are worst-case scan comparisons rather than average randomized-client latency. The new index avoids growth in lookup time as registrations increase.

{table('lookup')}

## Live validation and deployment

- {validation['passed_cases']} Go test cases in {validation['passed_packages']} packages passed with race detection; zero failures or skipped cases. `go vet ./...` passed; golangci-lint 2.11.4 reported zero issues.
- New regressions cover index updates, duplicate ordering/promotion, input/output slice ownership, concurrent manager access, lazy logger fields, context reuse, and custom logger preservation.
- For this pass, the optimized Go 1.27.2 image was deployed locally as `manta:local`, version `1.13.3-manta-local-20261015-scale`; the previous image was retained as `manta:before-scale-20261015`. See the subsequent report linked above for the latest runtime.
- The existing local Paper/Bedrock workload was repeated: {sum(r['n'] for r in api):,} API requests with zero errors, {sum(r['received_messages'] for r in sockets):,}/{sum(r['expected_messages'] for r in sockets):,} WebSocket messages delivered, and HTTP/SFTP/archive integrity checks passed.
- All 16 Manta live hardening/compatibility checks passed. Both benchmark daemons are healthy, and all four benchmark games remain running in Aquadactyl.

The live nodes still manage only two games each. Those checks establish compatibility of the deployed changes; they do not validate 1,000 live games, a higher player count, overall daemon RAM savings, or a game TPS improvement. Raw live timing data include the unchanged Wings control and are retained for inspection, but no new universal Manta-versus-Wings speed-up is claimed. Power/lifecycle code was unchanged; the previous shutdown-reporting delay remains a separate target.

## Reproduction and artifacts

Run `go test ./server -run '^$' -bench BenchmarkManagerLookup -benchmem -count=5 -benchtime=500ms` and `go test ./router -run '^$' -bench BenchmarkServerStatus -benchmem -count=5 -benchtime=500ms` with the same Linux Go 1.26.9 toolchain and CPU limits on both source versions.

Live check: `benchmarks/Run-Benchmark.ps1 -Phases performance,security -OutputDirectory benchmarks/results/my-scale-run`. The existing provisioned local test stack is required. The script removes its temporary credential manifest and probe listeners after completion.

Raw data and validation are in [results/scalability](results/scalability/). Regenerate this document with `python benchmarks/summarize_scalability.py`. The runtime JSON records actual image IDs, toolchains, caps, and server identities.
"""
(root / "SCALABILITY.md").write_text(report, encoding="utf-8")
(results / "summary.json").write_text(json.dumps(rows, indent=2) + "\n")
print(table("router"))
