"""Create a reproducible summary from the runner's credential-free JSON files."""
from pathlib import Path
import csv
import json
import re
import statistics

root = Path(__file__).parent
results = root / "results"
phases = {name: json.loads((results / f"{name}.json").read_text())
          for name in ("lifecycle", "performance", "resources", "security")}
validation = json.loads((results / "validation.json").read_text())
rows = []

def comparison(label, subset, metric, unit="ms"):
    values = {variant: [float(r[metric]) for r in subset if r["variant"] == variant]
              for variant in ("wings", "manta")}
    if not all(values.values()):
        raise RuntimeError(f"Missing measurements for {label}")
    wings, manta = (statistics.median(values[v]) for v in ("wings", "manta"))
    rows.append({"metric": label, "unit": unit, "wings": wings, "manta": manta,
                 "manta_change_percent": (manta / wings - 1) * 100,
                 "wings_runs": len(values["wings"]), "manta_runs": len(values["manta"])})

for game in ("paper", "bedrock"):
    subset = [r for r in phases["lifecycle"] if r.get("test") == "lifecycle"
              and r["game"] == game and not r["cold"]]
    comparison(game.title() + " warm startup", subset, "start_ms")
    comparison(game.title() + " reported offline", subset, "stop_ms")
for test, label in (("api_status", "Server API"), ("directory_1000", "List 1,000 files")):
    for concurrency in (1, 16, 32):
        subset = [r for r in phases["performance"] if r.get("test") == test
                  and r["concurrency"] == concurrency]
        comparison(f"{label}, concurrency {concurrency}", subset, "p50_ms")
        comparison(f"{label}, concurrency {concurrency}, p95", subset, "p95_ms")
for test, label in (("http_write_16m", "HTTP write 16 MiB"), ("http_read_16m", "HTTP read 16 MiB"),
                    ("sftp_write_16m", "SFTP write 16 MiB"), ("sftp_read_16m", "SFTP read 16 MiB"),
                    ("websocket_16_subscribers_100_lines", "WebSocket log delivery, 16 subscribers")):
    comparison(label, [r for r in phases["performance"] if r.get("test") == test], "p50_ms")
for test, label in (("chmod_1000", "Chmod 1,000 files"), ("rename_1000", "Rename 1,000 files")):
    comparison(label, [r for r in phases["performance"] if r.get("test") == test], "duration_ms")
archives = [r for r in phases["performance"] if r.get("test") == "archive_16m_1000_files"]
comparison("Compress 16 MiB + 1,000 files", archives, "compress_ms")
comparison("Extract 16 MiB + 1,000 files", archives, "extract_ms")
for phase in ("idle", "command_load"):
    for role in ("daemon", "paper", "bedrock"):
        subset = [r for r in phases["resources"] if r.get("test") == "resources_" + phase and r["role"] == role]
        comparison(f"{role.title()} {phase} mean memory", subset, "mean_working_mib", "MiB")
        comparison(f"{role.title()} {phase} CPU", subset, "cpu_core_percent", "% of one core")
    for game in ("paper", "bedrock"):
        subset = [r for r in phases["resources"] if r.get("test") == "game_query_during_" + phase and r["game"] == game]
        comparison(f"{game.title()} query during {phase}", subset, "p50_ms")

with (results / "summary.csv").open("w", newline="") as f:
    writer = csv.DictWriter(f, fieldnames=list(rows[0]))
    writer.writeheader()
    writer.writerows(rows)
(results / "summary.json").write_text(json.dumps(rows, indent=2) + "\n")

def table(subset):
    out = ["| Metric | Stock Wings | Manta | Manta change |",
           "| --- | ---: | ---: | ---: |"]
    for r in subset:
        out.append(f'| {r["metric"]} | {r["wings"]:.3f} {r["unit"]} | {r["manta"]:.3f} {r["unit"]} | {r["manta_change_percent"]:+.1f}% |')
    return "\n".join(out)

checks = [r for r in phases["security"] if r.get("type") == "check"]
tests = sorted({r["test"] for r in checks})
check_table = ["| Live check | Manta | Stock Wings |", "| --- | --- | --- |"]
for name in tests:
    by_variant = {r["variant"]: r for r in checks if r["test"] == name}
    def desc(r):
        result = "Pass" if r["pass"] else "Different behavior"
        if "status" in r:
            result += f' (HTTP {r["status"]})'
        if "event" in r:
            result += f' ({r["event"]})'
        return result
    check_table.append(f'| {name} | {desc(by_variant["manta"])} | {desc(by_variant["wings"])} |')

tick_table = ["| Phase | Daemon | TPS, 1 minute | Tick time average, last 5 seconds |",
              "| --- | --- | ---: | ---: |"]
for r in phases["resources"]:
    if r.get("type") != "game_console":
        continue
    lines = [re.sub(r"\x1b\[[0-9;]*m", "", line) for line in json.loads(r["logs"])["data"]]
    tps = next(line for line in lines if "TPS from last" in line)
    tick = next(line for line in reversed(lines) if re.search(r"\d+\.\d+/\d+\.\d+/\d+\.\d+", line))
    tps_value = re.search(r"TPS from last.*?:\s*([\d.]+)", tps).group(1)
    tick_value = re.search(r"([\d.]+)/[\d.]+/[\d.]+", tick).group(1)
    tick_table.append(f'| {r["test"].removeprefix("paper_ticks_")} | {r["variant"]} | {tps_value} | {tick_value} ms |')

request_rows = [r for r in phases["performance"] if r.get("test") in ("api_status", "directory_1000")]
sockets = [r for r in phases["performance"] if r.get("test") == "websocket_16_subscribers_100_lines"]
report = f"""# Local Manta versus stock Wings benchmark

15 October 2026. Results are mixed: Manta improves directory listing and several write/batch operations, while the small status API is slower, reported shutdown takes longer, and daemon working memory is higher. Game tick performance is similar. These results do not establish a universal speed-up.

The later [performance follow-up](OPTIMIZATION.md) measures targeted request/event optimizations against this Manta build. The original comparison below is retained as measured.

The subsequent [lookup and allocation comparison](SCALABILITY.md) measures the next Manta pass with up to 1,000 in-memory registrations.

The latest [stats and console comparison](TELEMETRY.md) measures the periodic decoder and console-inspection paths.

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

{table([r for r in rows if r['unit'] == 'ms' and 'query during' not in r['metric']])}

## Game and resource observations

{table([r for r in rows if r['unit'] != 'ms' or 'query during' in r['metric']])}

{'\n'.join(tick_table)}

Both Paper servers held 20 TPS at idle and reported 19.7 TPS over the one-minute window containing forced-chunk initialization and command load. The final five-second tick averages were about 1 ms for both. The first forced-chunk load produced a roughly 1.2-second maximum tick on both; the short test does not show a meaningful multiplayer tick advantage.

## Live hardening and integration checks

Manta passed {sum(r['pass'] for r in checks if r['variant']=='manta')} of {sum(r['variant']=='manta' for r in checks)} checks. Stock differences below identify behavior/policy differences and missing identifier support; they are not a count of independently exploitable vulnerabilities.

{'\n'.join(check_table)}

The loopback download check used a verified, temporary HTTP listener bound inside each daemon to `127.0.0.1:19090`, serving only a harmless sentinel. Both refused retrieval. An earlier closed-port probe returned 500 on stock Wings and was inconclusive; it was replaced with the live sentinel test. No SSRF bypass was demonstrated. Both reject symlink reads outside their server root.

The log-redaction check used a synthetic invalid token marker. Manta omitted it from its logs; stock Wings logged it. File token replay was tested immediately after first use with a three-hour signed expiry; retention through the entire expiry is covered by the unit test, not a three-hour live wait.

## Validation and integration fix

- {sum(r['n'] for r in request_rows):,} measured API requests, {sum(r['errors'] for r in request_rows)} errors.
- {sum(r['received_messages'] for r in sockets):,}/{sum(r['expected_messages'] for r in sockets):,} expected WebSocket log deliveries received.
- HTTP, SFTP, and archive payload checks passed.
- Final Go race suite: {validation['go']['passed_cases']} cases in {validation['go']['passed_packages']} packages passed, {validation['go']['failed']} failures. `go vet ./...` passed; golangci-lint 2.11.4 reported zero issues. Tests used Go 1.26.9; the deployed Manta image used Go 1.27.2. Details are in [validation.json](results/validation.json).
- Aquadactyl SFTP integration suite: 18 tests, 74 assertions, all passed in its isolated test database.
- Live testing discovered the Panel rejected legacy eight-character UUID prefixes, while the daemon rejected the Panel's modern `serv_...` identifiers. Manta now accepts short IDs, full UUIDs, and properly shaped modern IDs. Aquadactyl now resolves legacy short IDs within the authenticated node and rejects ambiguous matches. Changes are in both repositories and were applied to the running local test Panel.

## Limits and next targets

This is one shared development host, with background user servers left running. Three alternating runs reduce ordering bias but do not supply confidence intervals or prove production capacity. The Go versions differ. Loopback Docker networking and Linux volume caches are much faster than many real deployments. Only two managed servers per daemon were tested; startup pagination limits were covered by regression tests, not a many-node live test. CPU burst could not be applied from these containerized daemons because the host cgroup path is unavailable; no burst-performance claim is made.

The prior 99.8% allocation reduction applies to the isolated serialized-request microbenchmark. It is not an overall daemon memory reduction: this run observed higher Manta daemon working memory after equivalent activity. The main remaining tuning targets from this run are retained daemon memory, small status API latency, and the Panel-visible shutdown delay. A later comparison with the same compiler, authenticated player bots, more managed servers, longer runs, and explicit CPU/I/O pressure would be needed for a production performance claim.

## Test servers left available

| Server | Panel | Local game address |
| --- | --- | --- |
| Benchmark Manta Paper | http://localhost:8081/admin/servers/view/7 | localhost:27015 |
| Benchmark Manta Bedrock | http://localhost:8081/admin/servers/view/8 | localhost:27016 |
| Benchmark Wings Paper | http://localhost:8081/admin/servers/view/9 | localhost:27115 |
| Benchmark Wings Bedrock | http://localhost:8081/admin/servers/view/10 | localhost:27116 |

All game ports bind to host loopback. The test worlds use offline authentication for local testing. Benchmark forced/ticking areas were removed after measurement. Raw JSON files in `results/` and the runner source are retained for review and reproduction; private credentials are excluded.
"""
(root / "REPORT.md").write_text(report, encoding="utf-8")
print(table([r for r in rows if r["metric"] in {
    "Paper warm startup", "Bedrock warm startup", "List 1,000 files, concurrency 1", "HTTP write 16 MiB",
    "Server API, concurrency 1", "Paper reported offline", "Daemon idle mean memory"}]))
