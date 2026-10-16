"""Summarize the file-listing/streaming pass without merging earlier runs."""
import json
import re
import statistics
from pathlib import Path

root = Path(__file__).parent
results = root / "results" / "files"


def read_benchmarks(name, stage):
    samples = {}
    for line in (results / f"{name}-{stage}.txt").read_text().splitlines():
        match = re.match(r"^(Benchmark\S+)\s+\d+\s+([\d.]+) ns/op.*?\s+(\d+) B/op\s+(\d+) allocs/op", line)
        if match:
            key = re.sub(r"-\d+$", "", match[1])
            writes = re.search(r"([\d.]+) writes/op", line)
            samples.setdefault(key, []).append([float(match[i]) for i in (2, 3, 4)] + [float(writes[1]) if writes else 0])
    if not samples or any(len(values) != 5 for values in samples.values()):
        raise RuntimeError("Expected five samples per benchmark case")
    return {key: [statistics.median(row[i] for row in values) for i in range(4)]
            for key, values in samples.items()}


labels = {
    "BenchmarkServerStatus": "Status API (control)",
    "BenchmarkDirectoryAPI/10": "Directory API, 10 files",
    "BenchmarkDirectoryAPI/1000": "Directory API, 1,000 files",
    "BenchmarkFileReadAPI/1MiB": "File read API, 1 MiB",
    "BenchmarkFileReadAPI/16MiB": "File read API, 16 MiB",
    "BenchmarkDirectoryListing/entries_10/read": "List only, 10 files",
    "BenchmarkDirectoryListing/entries_10/read_and_json": "List + legacy Stat JSON, 10 files",
    "BenchmarkDirectoryListing/entries_1000/read": "List only, 1,000 files",
    "BenchmarkDirectoryListing/entries_1000/read_and_json": "List + legacy Stat JSON, 1,000 files",
}
rows = []
for name in ("api", "read", "list"):
    before, after = (read_benchmarks(name, stage) for stage in ("before", "after"))
    if before.keys() != after.keys():
        raise RuntimeError("Benchmark cases differ")
    for key, old in before.items():
        new = after[key]
        rows.append({"benchmark": key, "label": labels[key], "before_ns": old[0], "after_ns": new[0],
                     "time_change_percent": (new[0]/old[0]-1)*100,
                     "before_bytes": old[1], "after_bytes": new[1],
                     "bytes_change_percent": (new[1]/old[1]-1)*100,
                     "before_allocs": old[2], "after_allocs": new[2],
                     "before_writes": old[3], "after_writes": new[3]})
if len(rows) != 9:
    raise RuntimeError("Unexpected number of isolated cases")
table = ["| Isolated path | Before | After | Time change | Bytes/op before / after | Allocations/op before / after |",
         "| --- | ---: | ---: | ---: | ---: | ---: |"]
for row in rows:
    table.append(f"| {row['label']} | {row['before_ns']/1e6:.4f} ms | {row['after_ns']/1e6:.4f} ms | "
                 f"{row['time_change_percent']:+.1f}% | {row['before_bytes']:,.0f} / {row['after_bytes']:,.0f} | "
                 f"{row['before_allocs']:g} / {row['after_allocs']:g} |")


def performance(directory, expected_version):
    data = json.loads((directory / "performance.json").read_text())
    meta = next(row for row in data if row.get("type") == "metadata" and row["variant"] == "manta")
    if meta["system"]["version"] != expected_version or meta["cpu_limit"] != 2 or meta["memory_limit"] != 1 << 30 or meta["gomaxprocs"] != "2":
        raise RuntimeError("Unexpected live build or resource caps")
    api = [row for row in data if row.get("test") in {"api_status", "directory_1000"}]
    files = [row for row in data if row.get("test") in {"http_write_16m", "http_read_16m", "sftp_write_16m", "sftp_read_16m", "archive_16m_1000_files"}]
    console = [row for row in data if row.get("test") == "websocket_16_subscribers_100_lines"]
    if len(api) != 36 or sum(row["n"] for row in api) != 14400 or any(row["errors"] for row in api):
        raise RuntimeError("API regression checks failed")
    if len(files) != 30 or not all(row["verified"] for row in files):
        raise RuntimeError("File integrity checks failed")
    if len(console) != 6 or sum(row["received_messages"] for row in console) != 9600 or any(row["received_messages"] != row["expected_messages"] for row in console):
        raise RuntimeError("Console delivery checks failed")
    if any(row.get("type") == "fatal" for row in data):
        raise RuntimeError("Fatal workload error")
    return data, meta


before, old_meta = performance(results / "live-before", "1.13.3-manta-local-20261015-events")
after, meta = performance(results / "live", "1.13.3-manta-local-20261015-files")
stock_before = next(row for row in before if row.get("type") == "metadata" and row["variant"] == "wings")
stock_after = next(row for row in after if row.get("type") == "metadata" and row["variant"] == "wings")
if stock_before["image_id"] != stock_after["image_id"] or stock_before["go_version"] != stock_after["go_version"]:
    raise RuntimeError("The measured stock control changed between stages")


def median_live(data, test, variant, concurrency):
    values = [row for row in data if row.get("test") == test and row["variant"] == variant and row.get("concurrency") == concurrency]
    if len(values) != 3:
        raise RuntimeError("Expected three alternating-order live rounds")
    return [statistics.median(row[key] for row in values) for key in ("p50_ms", "p95_ms")]


live_rows = []
live_table = ["| Live operation | Daemon | Concurrency | Before p50 | After p50 | p50 change | Before / after p95 |",
              "| --- | --- | ---: | ---: | ---: | ---: | ---: |"]
for test, label, concurrencies in [("directory_1000", "List 1,000 files", [1, 16, 32]),
                                    ("http_read_16m", "Read 16 MiB", [None]),
                                    ("api_status", "Status control", [1])]:
    for concurrency in concurrencies:
        for variant in ("manta", "wings"):
            old, new = (median_live(data, test, variant, concurrency) for data in (before, after))
            change = (new[0]/old[0]-1)*100
            live_rows.append({"test": test, "variant": variant, "concurrency": concurrency,
                              "before_p50_ms": old[0], "after_p50_ms": new[0], "p50_change_percent": change,
                              "before_p95_ms": old[1], "after_p95_ms": new[1]})
            live_table.append(f"| {label} | {variant} | {concurrency or 1} | {old[0]:.3f} ms | {new[0]:.3f} ms | "
                              f"{change:+.1f}% | {old[1]:.3f} / {new[1]:.3f} ms |")

events = json.loads((results / "live" / "events.json").read_text())
streams = [row for row in events if row.get("type") == "check"]
if len(streams) != 64 or not all(row["pass"] and row["samples"] == 3 for row in streams):
    raise RuntimeError("Automatic stats checks failed")
security = json.loads((results / "live" / "security.json").read_text())
checks = [row for row in security if row.get("type") == "check" and row["variant"] == "manta"]
if len(checks) != 16 or not all(row["pass"] for row in checks):
    raise RuntimeError("Manta hardening/compatibility checks failed")
validation = json.loads((results / "validation.json").read_text())
if validation["failed"] or validation["skipped_cases"]:
    raise RuntimeError("Go suite had failures or skips")
validation["live"] = {"api_requests_per_stage": 14400, "api_errors": 0,
                      "verified_file_cases_per_stage": 30, "console_messages_per_stage": 9600,
                      "automatic_stats_samples_after": 192, "viewers_per_game": 16,
                      "manta_security_checks_passed": 16, "image_id": meta["image_id"],
                      "version": meta["system"]["version"]}
runtime_note = ""
runtime_dir = results / "current-runtime"
if (runtime_dir / "events.json").exists():
    runtime_events = json.loads((runtime_dir / "events.json").read_text())
    runtime_security = json.loads((runtime_dir / "security.json").read_text())
    runtime_meta = next(row for row in runtime_events if row.get("type") == "metadata" and row["variant"] == "manta")
    runtime_security_meta = next(row for row in runtime_security if row.get("type") == "metadata" and row["variant"] == "manta")
    runtime_streams = [row for row in runtime_events if row.get("type") == "check"]
    runtime_checks = [row for row in runtime_security if row.get("type") == "check" and row["variant"] == "manta"]
    inspection = json.loads((results / "runtime-inspection.json").read_text())
    if runtime_meta["image_id"] != runtime_security_meta["image_id"] or runtime_meta["image_id"] != inspection["image_id"]:
        raise RuntimeError("Runtime changed during compatibility validation")
    if len(runtime_streams) != 64 or not all(row["pass"] and row["samples"] == 3 for row in runtime_streams):
        raise RuntimeError("Current runtime automatic stats checks failed")
    if len(runtime_checks) != 16 or not all(row["pass"] for row in runtime_checks):
        raise RuntimeError("Current runtime hardening checks failed")
    if len(inspection["helpers"]) != 4 or not all(inspection["helpers"].values()):
        raise RuntimeError("Expected file/event helpers missing from current binary")
    validation["current_runtime"] = {"image_id": runtime_meta["image_id"],
                                     "version": runtime_meta["system"]["version"],
                                     "binary_sha256": inspection["binary_sha256"],
                                     "automatic_stats_samples": 192, "manta_checks_passed": 16}
    validation["current_runtime"]["initial_attempt"] = "An initial additional runtime check aborted on invalid streamed resource values after concurrent rebuilds; the subsequent retry passed."
    runtime_note = f"Concurrent local rebuilds subsequently replaced both daemons. Current Manta is version `{runtime_meta['system']['version']}`, image `{runtime_meta['image_id']}`. That healthy binary contains the MIME/metadata helpers and a 64 KiB buffer type; separate current-runtime checks passed all 16 Manta hardening/compatibility cases and 192 automatic stats samples. Its binary SHA-256 is `{inspection['binary_sha256']}`. An initial additional runtime check aborted on invalid streamed resource values after the concurrent rebuilds; benchmark-game start times also changed during this later window. The subsequent retry passed. These later observations are separate from the completed file comparison. The performance tables belong to the versioned file build retained as `manta:files-candidate-20261015`; timings are not relabeled as measurements of this later rebuild."
(results / "validation.json").write_text(json.dumps(validation, indent=2) + "\n", encoding="utf-8")
(results / "summary.json").write_text(json.dumps({"isolated": rows, "live": live_rows}, indent=2) + "\n", encoding="utf-8")

profile_line = next(line for line in (results / "alloc-before.txt").read_text().splitlines() if line.rstrip().endswith("mimetype.DetectReader"))
mime_percent = float(re.search(r"([\d.]+)%", profile_line)[1])
report = f"""# Manta file browsing and streaming

15 October 2026. This pass targets file-manager responsiveness and HTTP file reads. It compares the preceding [event-conversion build](EVENTS.md) with this Manta build; earlier measurements remain separate.

## Changes and compatibility

The allocation profile attributed {mime_percent:.2f}% of sampled allocation bytes to MIME-probe buffers during the 1,000-file listing/JSON workload. The implementation keeps the existing 4 KiB content probe and reuses its scratch buffer. It still opens and checks each regular file and reads current content; it caches no MIME results or directory responses. Smaller/empty files are detected using only the bytes actually read, and read errors keep the existing caller behavior.

Directory responses now encode plain metadata records in one JSON pass, avoiding a separate encode and validation/compaction for every file. The inherited ten fields, types, timestamp formatting, permissions, MIME values, HTML/Unicode escaping, and empty-array behavior are retained. A single comparator puts directories first and sorts names within each group.

HTTP file reads now use pooled 64 KiB streaming buffers through Gin's existing response writer. For the synthetic 16 MiB read, application writes drop from 4,096 to 256. The original content-length reader, filesystem containment, MIME detection/rewind, response headers, byte accounting, flushing, and error path remain. Request-time storage remains bounded per active read: the buffer is not sized to the file. Pools can retain scratch capacity between requests; lower allocated bytes/op do not imply equal retained-RAM savings.

The implementation stays in Go. Profiles identified repeated allocations, encoding, and small I/O operations that can be removed while retaining the existing API implementation. This pass supplies no evidence that a language rewrite would improve these operations further.

## Isolated measurements

Five 500 ms runs per case, Go 1.26.9 Linux amd64 and four CPU quota cores. Both versions use identical benchmark code/settings, and final isolated runs do not overlap the live workload or build/test jobs. The host remains shared. Fixtures contain 10 or 1,000 256-byte text files. Full API cases include routing, authentication, containment, filesystem work and JSON encoding with an in-memory response writer. File-read cases stream cached file data to a counting/discarding response writer and exclude network transport; their reported MB/s is not network throughput. The retained legacy Stat-JSON cases isolate filesystem improvements independently of the directory handler's new encoding.

{chr(10).join(table)}

The status route is an unchanged control. CPU/allocation profile text is retained in `cpu-before/after.txt` and `alloc-before/after.txt`. Profiles are sampled and include fixture setup; their total allocated MB should not be compared as per-operation values because iteration counts differ. The table uses benchmark bytes/op instead.

## Live Docker comparison

Both Manta stages use Go 1.27.2, two CPU quota cores, 1 GiB RAM, and `GOMAXPROCS=2`, with the same benchmark Paper/Bedrock containers. Within the measured stages, stock Wings was unchanged and provided a host/workload control. Each stage runs three alternating Manta/Wings rounds. Each directory/status concurrency case contains 400 requests per round; 16 MiB reads contain five verified samples per round. Values below are medians of the three per-round p50/p95 values. Final live stages did not overlap our builds, profiles, isolated benchmarks, or regression tests. The optimized stage was measured first, followed by a fresh baseline-daemon run, then the optimized deployment was restored. An earlier exploratory baseline overlapped profiling and is retained as `exploratory-performance.json`; it is excluded from the table.

{chr(10).join(live_table)}

Shared-host scheduling, page cache, and daemon heap history can affect these timings. Stock-control changes must be considered when interpreting Manta changes. These results measure file operations rather than overall game speed; no player-capacity, TPS, startup-time, or whole-daemon CPU/RAM gain is established here.

## Verification and deployment

- {validation['passed_cases']} Go test cases in {validation['passed_packages']} packages passed with race detection, zero failures/skips, plus vet and zero golangci-lint 2.11.4 issues.
- MIME comparisons use the previous library reader as the reference and cover text, Unicode, JSON/HTML/XML, image/archive signatures, binary/empty data, short reads, read failures, probe limits and concurrent reuse. Filesystem tests cover fresh external writes, directory/name ordering, symlinks and FIFOs.
- HTTP tests cover concurrent readers, empty/small/buffer-boundary bodies, download headers, original-length enforcement when a file grows, and the directory's ten-field JSON contract and escaping.
- Each live stage completed 14,400 API requests with zero errors, 30 verified transfer/archive cases, and all 9,600 expected console messages. The after stage also received 192 automatic stats samples across 16 viewers on each of the four games, and all 16 Manta hardening/compatibility checks passed.
- The measured file build is version `{meta['system']['version']}`, Go `{meta['go_version']}`, image `{meta['image_id']}`. The previous image is retained as `manta:before-files-20261015` ({old_meta['image_id']}). During the measured stages, only the local daemon was recreated and the existing game containers stayed running.

{runtime_note}

Raw evidence is under [results/files](results/files/). Rebuild this report with `python benchmarks/summarize_files.py`.

```sh
go test ./server/filesystem -run '^$' -bench BenchmarkDirectoryListing -benchmem -count=5 -benchtime=500ms
go test ./router -run '^$' -bench 'BenchmarkDirectoryAPI|BenchmarkServerStatus$|BenchmarkFileReadAPI' -benchmem -count=5 -benchtime=500ms
```

With the existing dedicated local test stack:

```powershell
.\\benchmarks\\Run-Benchmark.ps1 -Phases performance,events,security -OutputDirectory benchmarks/results/my-files-run
```
"""
(root / "FILES.md").write_text(report, encoding="utf-8")
print(f"Wrote FILES.md: {len(rows)} isolated cases and {len(live_rows)} live comparisons; all Manta checks passed.")
