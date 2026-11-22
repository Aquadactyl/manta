"""Summarize the matched directory-listing component benchmark (stdlib only)."""

import json
import re
import statistics
import sys
from pathlib import Path


def summarize(path: Path) -> str:
    report = json.loads(path.read_text(encoding="utf-8"))
    if not report.get("completedAt"):
        raise ValueError("Benchmark report is incomplete")
    pattern = re.compile(
        r"^(Benchmark\S+)-\d+\s+\d+\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op$"
    )
    samples: dict[str, dict[str, list[tuple[float, int, int]]]] = {}
    for run in report["runs"]:
        if run["warmup"]:
            continue
        for line in run["output"].splitlines():
            match = pattern.fullmatch(line.strip())
            if match:
                name, ns, size, allocs = match.groups()
                samples.setdefault(name, {"baseline": [], "optimized": []})[
                    run["variant"]
                ].append((float(ns), int(size), int(allocs)))
    if len(samples) != 8:
        raise ValueError(f"Expected eight benchmark cases, found {len(samples)}")
    lines = [
        "| Case | Before (ms) | After (ms) | Time reduction | Before B/op | After B/op | Before allocs/op | After allocs/op |",
        "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for name, variants in samples.items():
        medians = {}
        for variant, values in variants.items():
            if len(values) != 5:
                raise ValueError(f"Expected five samples: {name}/{variant}")
            medians[variant] = [statistics.median(v[i] for v in values) for i in range(3)]
        before, after = medians["baseline"], medians["optimized"]
        lines.append(
            f"| {name.removeprefix('BenchmarkDirectoryListing')} | {before[0] / 1e6:.3f} | "
            f"{after[0] / 1e6:.3f} | {100 * (1 - after[0] / before[0]):.1f}% | "
            f"{before[1]:,.0f} | {after[1]:,.0f} | {before[2]:,.0f} | {after[2]:,.0f} |"
        )
    return "\n".join(lines)


if __name__ == "__main__":
    source = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(__file__).parent / "results/listing-20261122.json"
    print(summarize(source))
