"""Summarize the resource-event handler pass from retained evidence."""
import json
import re
import statistics
from pathlib import Path

root = Path(__file__).parent
results = root / "results" / "resource-events"


def benchmarks(stage):
    samples = {}
    for line in (results / f"handler-{stage}.txt").read_text().splitlines():
        match = re.match(r"^(Benchmark\S+)\s+\d+\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op", line)
        if match:
            key = re.sub(r"-\d+$", "", match[1])
            samples.setdefault(key, []).append([float(match[i]) for i in (2, 3, 4)])
    if len(samples) != 4 or any(len(values) != 5 for values in samples.values()):
        raise RuntimeError("Expected five runs of four benchmark cases")
    return {key: [statistics.median(row[i] for row in values) for i in range(3)]
            for key, values in samples.items()}


before, after = benchmarks("before"), benchmarks("after")
table = ["| Handler path | Before | After | Time change | Bytes/op before / after | Allocations/op before / after |",
         "| --- | ---: | ---: | ---: | ---: | ---: |"]
rows = []
for key, old in before.items():
    new = after[key]
    count = key.rsplit("_", 1)[-1]
    label = "State event (control)" if key == "BenchmarkEnvironmentStateHandling" else f"Resource event, {count} " + ("observer" if count == "1" else "observers")
    change = (new[0]/old[0]-1)*100
    table.append(f"| {label} | {old[0]/1000:.3f} us | {new[0]/1000:.3f} us | {change:+.1f}% | "
                 f"{old[1]:g} / {new[1]:g} | {old[2]:g} / {new[2]:g} |")
    rows.append({"benchmark": key, "before_ns": old[0], "after_ns": new[0],
                 "time_change_percent": change, "before_bytes": old[1], "after_bytes": new[1],
                 "bytes_change_percent": (new[1]/old[1]-1)*100,
                 "before_allocs": old[2], "after_allocs": new[2]})


def stream_checks(directory):
    data = json.loads((directory / "events.json").read_text())
    meta = next(row for row in data if row.get("type") == "metadata" and row["variant"] == "manta")
    checks = [row for row in data if row.get("type") == "check"]
    if len(checks) != 64 or not all(row["pass"] and row["samples"] == 3 for row in checks):
        raise RuntimeError("Automatic stats delivery failed")
    for variant in ("manta", "wings"):
        for game in ("paper", "bedrock"):
            if {row["viewer"] for row in checks if row["variant"] == variant and row["game"] == game} != set(range(16)):
                raise RuntimeError("Incomplete game/viewer coverage")
    if meta["cpu_limit"] != 2 or meta["memory_limit"] != 1 << 30 or meta["gomaxprocs"] != "2":
        raise RuntimeError("Unexpected Manta resource caps")
    return meta


old_meta = stream_checks(results / "live-before")
meta = stream_checks(results / "live")
if meta["system"]["version"] != "1.13.3-manta-local-20261016-resource-events":
    raise RuntimeError("Unexpected optimized build")
api = [json.loads((results / f"api-{stage}.json").read_text()) for stage in ("before", "after")]
if not all(stage["passed"] and stage["servers_checked"] == 4 and len(stage["samples"]) == 12 for stage in api):
    raise RuntimeError("API telemetry checks failed")
security = json.loads((results / "live" / "security.json").read_text())
checks = [row for row in security if row.get("type") == "check" and row["variant"] == "manta"]
if len(checks) != 16 or not all(row["pass"] for row in checks):
    raise RuntimeError("Manta hardening/compatibility checks failed")
validation = json.loads((results / "validation.json").read_text())
if validation["failed"] or validation["skipped_cases"]:
    raise RuntimeError("Go suite failed or skipped cases")
validation["live"] = {"before_stats_samples": 192, "after_stats_samples": 192,
                      "viewers_per_game": 16, "games_checked": 4,
                      "before_api_samples": 12, "after_api_samples": 12,
                      "manta_checks_passed": 16, "image_id": meta["image_id"],
                      "version": meta["system"]["version"]}
(results / "validation.json").write_text(json.dumps(validation, indent=2) + "\n", encoding="utf-8")
(results / "summary.json").write_text(json.dumps(rows, indent=2) + "\n", encoding="utf-8")

report = f"""# Manta resource-event overhead

16 October 2026. This pass reduces periodic daemon work while retaining Aquadactyl's API and stats format. Isolated measurements were recorded on 15 October; full validation and deployment completed on 16 October after the interruption. Earlier [file operations](FILES.md), [WebSocket conversion](EVENTS.md), and [Docker telemetry](TELEMETRY.md) results remain separate.

## Changes

Resource events previously constructed a generic object/float tree, discarded it, then decoded the entire envelope again into typed counters. The handler now reads a raw payload envelope and decodes just that payload into fresh `environment.Stats`. This retains integer precision and missing/null-field behavior while avoiding the intermediate tree and repeated full-envelope decoding. Docker's CPU/memory calculations and collection cadence are unchanged.

Periodic stats broadcasts now skip snapshot construction and JSON encoding when the server bus has no registered observers. Resource updates and disk-limit enforcement always run. API requests continue to take a current snapshot, and any registered bus observer still enables the broadcast. The listener check is a synchronized snapshot: an observer arriving after a zero-listener check receives subsequent publications. Existing initial/requested stats replies remain available through the WebSocket API.

State-event handling retains plain/escaped starting-state detection, throttle/disk-limiter resets and state-change processing. Pull-progress payloads retain their JSON values when forwarded. The shared decoder now passes valid pointer destinations directly to the JSON decoder, avoiding an extra interface box. Invalid nil/non-pointer destinations now return the wrapped standard JSON destination error rather than decoding into an unused local interface.

Event goroutine scheduling, sink backpressure/drop behavior, WebSocket authentication/permissions, and write synchronization are unchanged.

## Isolated results

Five 500 ms runs per case, Go 1.26.9 Linux amd64 with four CPU quota cores. Both versions use the same synthetic resource frame and handler benchmark. The baseline handler was extracted from the preceding listener without changing its work; the goroutine dispatch remains in the listener. The measured function performs envelope/payload decoding, resource update, a cached disk check, and optional stats publication. Disk scans are disabled in the benchmark; enforcement is covered separately by regression tests. Buffered/drained channels model ready observers. Results exclude Docker HTTP transport, goroutine scheduling, WebSocket conversion/compression, and network writes. Isolated runs did not overlap our live workloads, builds, or regression jobs.

{chr(10).join(table)}

These savings apply to each internal resource event and reduce background allocation work. They do not establish equal whole-daemon CPU/RAM savings, faster game ticks, or additional player capacity. No whole-daemon resource comparison or player-load benchmark was run in this pass. Separate optimization percentages should not be added together.

## Correctness and live checks

- {validation['passed_cases']} Go test cases in {validation['passed_packages']} packages passed with race detection, zero failures/skips, plus vet and zero golangci-lint 2.11.4 issues.
- Handler regressions verify counter updates with and without observers, delivery of exact uint64 values to observers, fresh/missing/null fields, payload/envelope field order, and malformed counter rejection without replacing the last good stats.
- Disk-limit regression tests exceed an actual test filesystem's quota with no observers, confirm one stop per start, then verify plain and escaped starting events reset disk enforcement and console throttling. Observer skipping does not bypass enforcement.
- Subscription tests cover registration/removal/destruction and concurrent listener changes. Decoder tests cover struct/raw, pointer-to-pointer, interface and slice destinations, plus wrapped syntax/destination errors. Existing real WebSocket permission and expiry tests passed.
- Before and after deployment, 16 viewers per benchmark game each received three automatic stats events: 192 samples per stage across Paper and Bedrock on both daemons. Field schemas, typed values, resource validity, advancing uptime and non-regressing network counters passed.
- Resource API checks opened no console sockets and sampled all four games three times before and after deployment. All 12 samples per stage passed, including advancing uptime. The after-stage API checks followed closure of the automatic-stats sockets and security checks.
- All 16 Manta live hardening/compatibility checks passed, including token handling, raw file streaming, SFTP compatibility, and containment. These live checks verify behavior; they are not an overall performance score. Docker Desktop restarted across the interruption, so numeric values/uptimes from the two dates are not compared for equality.

## Deployment and reproduction

The optimized local build is `{meta['system']['version']}`, Go `{meta['go_version']}`, image `{meta['image_id']}`. It retains two CPU quota cores, 1 GiB RAM and `GOMAXPROCS=2`. The preceding image is retained as `manta:before-resource-events-20261015` ({old_meta['image_id']}). The production base uses the distinct tag `manta:resource-events-build-20261016` so other local builds cannot replace its input accidentally.

Raw evidence: [results/resource-events](results/resource-events/). Rebuild this report with `python benchmarks/summarize_resource_events.py`.

```sh
go test ./server -run '^$' -bench 'BenchmarkEnvironment(Resource|State)Handling' -benchmem -count=5 -benchtime=500ms
```

Use identical benchmark code/toolchain settings against both source versions. With the existing local test stack, run:

```powershell
.\\benchmarks\\Run-Benchmark.ps1 -Phases events,security -OutputDirectory benchmarks/results/my-resource-events-run
```

API-only sampling uses `benchmarks/check-telemetry.php` inside the existing local Panel. It refuses non-local environments and selects only the four benchmark servers.
"""
(root / "RESOURCE_EVENTS.md").write_text(report, encoding="utf-8")
print(f"Wrote RESOURCE_EVENTS.md: {len(rows)} benchmark cases; all live checks passed.")
