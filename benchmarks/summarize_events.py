"""Rebuild the event-conversion report from retained measurements and checks."""
import json
import re
import statistics
from pathlib import Path

root = Path(__file__).parent
results = root / "results" / "events"


def benchmarks(stage):
    samples = {}
    for line in (results / f"event-{stage}.txt").read_text().splitlines():
        match = re.match(r"^(Benchmark\S+)\s+\d+\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op", line)
        if match:
            key = re.sub(r"-\d+$", "", match[1])
            samples.setdefault(key, []).append([float(match[i]) for i in (2, 3, 4)])
    if len(samples) != 4 or any(len(values) != 5 for values in samples.values()):
        raise RuntimeError("Expected five samples for each of four benchmark cases")
    return {key: [statistics.median(v[i] for v in values) for i in range(3)]
            for key, values in samples.items()}


before, after = benchmarks("before"), benchmarks("after")
rows = []
table = ["| Conversion and outer JSON encoding | Before | After | Time change | Bytes/op before / after | Allocations/op before / after |",
         "| --- | ---: | ---: | ---: | ---: | ---: |"]
for key, old in before.items():
    new = after[key]
    change = (new[0] / old[0] - 1) * 100
    viewers = key.rsplit("_", 1)[-1]
    label = "Status string" if key.endswith("/status") else f"Stats, {viewers} " + ("viewer" if viewers == "1" else "viewers")
    table.append(f"| {label} | {old[0]/1000:.3f} us | {new[0]/1000:.3f} us | {change:+.1f}% | "
                 f"{old[1]:g} / {new[1]:g} | {old[2]:g} / {new[2]:g} |")
    rows.append({"benchmark": key, "before_ns": old[0], "after_ns": new[0],
                 "time_change_percent": change, "before_bytes": old[1], "after_bytes": new[1],
                 "before_allocs": old[2], "after_allocs": new[2]})


def stream_checks(directory, expected_version):
    data = json.loads((directory / "events.json").read_text())
    meta = next(row for row in data if row.get("type") == "metadata" and row["variant"] == "manta")
    if meta["system"]["version"] != expected_version:
        raise RuntimeError("Unexpected Manta build in live stats check")
    checks = [row for row in data if row.get("type") == "check"]
    if len(checks) != 64 or not all(row["pass"] and row["samples"] == 3 for row in checks):
        raise RuntimeError("Expected 64 successful viewer checks with three samples each")
    groups = {(row["variant"], row["game"]) for row in checks}
    if groups != {("manta", "paper"), ("manta", "bedrock"), ("wings", "paper"), ("wings", "bedrock")}:
        raise RuntimeError("Unexpected live game coverage")
    for group in groups:
        if {row["viewer"] for row in checks if (row["variant"], row["game"]) == group} != set(range(16)):
            raise RuntimeError("Incomplete viewer coverage")
    return meta, sum(row["samples"] for row in checks)


old_meta, before_samples = stream_checks(results / "live-before", "1.13.3-manta-local-20261015-telemetry")
meta, after_samples = stream_checks(results / "live", "1.13.3-manta-local-20261015-events")
validation = json.loads((results / "validation.json").read_text())
security = json.loads((results / "live" / "security.json").read_text())
checks = [row for row in security if row.get("type") == "check" and row["variant"] == "manta"]
if len(checks) != 16 or not all(row["pass"] for row in checks):
    raise RuntimeError("Manta hardening/compatibility checks failed")
performance = json.loads((results / "live" / "performance.json").read_text())
api_rows = [row for row in performance if row.get("test") in {"api_status", "directory_1000"}]
console_rows = [row for row in performance if row.get("test") == "websocket_16_subscribers_100_lines"]
if len(api_rows) != 36 or any(row["errors"] for row in api_rows):
    raise RuntimeError("Live API regression workload failed")
api_requests = sum(row["n"] for row in api_rows)
if api_requests != 14400:
    raise RuntimeError("Unexpected API workload size")
file_rows = [row for row in performance if row.get("test") in {
    "http_write_16m", "http_read_16m", "sftp_write_16m", "sftp_read_16m", "archive_16m_1000_files"}]
if len(file_rows) != 30 or not all(row["verified"] for row in file_rows):
    raise RuntimeError("File integrity checks failed")
if len(console_rows) != 6 or any(row["received_messages"] != row["expected_messages"] for row in console_rows):
    raise RuntimeError("Live console delivery regression failed")
if any(row.get("type") == "fatal" for row in performance + security):
    raise RuntimeError("Fatal live workload error")
console_messages = sum(row["received_messages"] for row in console_rows)
status = next(row for row in rows if row["benchmark"].endswith("/status"))
status_extra_ns = status["after_ns"] - status["before_ns"]

validation["live"] = {"before_automatic_stats_samples": before_samples,
                      "after_automatic_stats_samples": after_samples,
                      "viewers_per_game": 16, "games": 4,
                      "api_requests": api_requests, "api_errors": 0,
                      "console_messages": console_messages,
                      "manta_security_checks": len(checks), "all_manta_checks_passed": True,
                      "image_id": meta["image_id"], "version": meta["system"]["version"]}
(results / "validation.json").write_text(json.dumps(validation, indent=2) + "\n", encoding="utf-8")
(results / "summary.json").write_text(json.dumps(rows, indent=2) + "\n", encoding="utf-8")

report = f"""# Manta WebSocket event conversion

15 October 2026. This pass compares event conversion against the preceding Manta stats/console build. Earlier [telemetry](TELEMETRY.md), [lookup](SCALABILITY.md), [request/event](OPTIMIZATION.md), and [Wings](REPORT.md) measurements remain separate.

## Change

Each console viewer previously decoded bus payloads into generic maps and floating-point numbers, then serialized the payload again. Manta now decodes the event envelope directly into the string argument expected by Aquadactyl. Object/array/number payloads retain their existing JSON bytes; plain strings are unquoted, and escaped or invalid UTF-8 strings retain Go JSON's decoding/replacement behavior. Missing/null data still produces the string `null`.

This removes the intermediate object tree and preserves integer counters above 2^53 through Manta's WebSocket serialization. JavaScript clients still need an appropriate parser to represent integers above their safe range. Nested JSON member order can now follow the original producer rather than sorted map order; parsed values and the outer `event`/`args` protocol remain compatible. Authentication, permissions, write serialization, deadlines, and sink behavior still pass through the existing `SendJson` gate.

## Measurements

Five runs per case, 500 ms each, Go 1.26.9 Linux amd64, four CPU quota cores, on the shared development host. The identical benchmark calls the actual conversion helper and outer message JSON encoder. The baseline helper was extracted from the previous listener without changing its map/string conversion. A representative synthetic stats envelope uses the `Server.Proc` payload shape. The 16/64-viewer cases repeat that conversion sequentially for each viewer; they measure aggregate serialization work, excluding network writes, JWT checks, compression, scheduling, and bus waits.

{chr(10).join(table)}

The status-string control costs about {status_extra_ns:.0f} ns more ({status['time_change_percent']:+.1f}%) while allocating fewer bytes and objects. The change is retained for the larger periodic stats improvement and integer preservation. These isolated results do not establish equal whole-daemon CPU/RAM savings, higher game TPS, or additional player capacity. Results from separate optimization passes must not be added arithmetically.

## Validation

- {validation['passed_cases']} Go test cases in {validation['passed_packages']} packages passed with race detection; {validation['failed']} failures and {validation['skipped_cases']} skipped cases. Vet passed and golangci-lint 2.11.4 reported zero issues.
- Payload regressions cover strings, escaping, Unicode, invalid UTF-8, surrogate replacement, objects, arrays, booleans, null/missing data, byte-slice base64 behavior, malformed envelopes, input ownership, and exact uint64 counters. Real WebSocket listener tests exercise allowed/denied install, backup, and transfer events, plus expired-token rejection.
- Before and after deployment, 16 authenticated viewers on each of four Paper/Bedrock benchmark servers each received three **automatic** stats events: {before_samples} samples before and {after_samples} after. These tests send no `send stats` requests, whose replies take a different path. They check the API field schema, typed counters, valid resource values, non-regressing network counters, and advancing uptime. API and streamed samples are taken at different times, so equal numeric values are not required.
- The post-update workload completed 14,400 API requests with zero errors, verified 16 MiB HTTP/SFTP/archive transfers, and delivered all {console_messages:,} expected console messages across the repeated 16-viewer checks. These are compatibility/regression observations; this pass does not claim faster file operations.
- All 16 Manta live hardening/compatibility checks passed. The dedicated benchmark games stayed running through the daemon update. No player-load, lifecycle-timing, or whole-daemon resource comparison was repeated in this pass.

## Deployment and reproduction

The healthy local `manta:local` deployment is version `{meta['system']['version']}`, Go `{meta['go_version']}`, image `{meta['image_id']}`. It retains two CPU quota cores, 1 GiB memory and `GOMAXPROCS=2`. The previous image is retained as `manta:before-events-20261015` ({old_meta['image_id']}).

Raw evidence is under [results/events](results/events/). Rebuild this report with `python benchmarks/summarize_events.py`.

Run the isolated comparison against each source version with identical toolchain/CPU settings:

```sh
go test ./router/websocket -run '^$' -bench BenchmarkEventMessage -benchmem -count=5 -benchtime=500ms
```

The existing local test stack is required for live checks:

```powershell
.\\benchmarks\\Run-Benchmark.ps1 -Phases events,performance,security -OutputDirectory benchmarks/results/my-events-run
```

The `events` phase validates automatic resource broadcasts independently of the console-line workload. All phases select only the four dedicated benchmark servers.
"""
(root / "EVENTS.md").write_text(report, encoding="utf-8")
print(f"Wrote EVENTS.md: {len(rows)} benchmark cases; {after_samples} live stats samples; all Manta checks passed.")
