"""Audit an unfiltered Go coverprofile, including repeated cross-package blocks."""

import argparse
import json
from collections import defaultdict
from decimal import Decimal
from pathlib import Path


def summarize(profile, minimum=Decimal(100)):
    lines = profile.splitlines()
    if not lines or lines[0] != "mode: atomic":
        raise ValueError("expected an atomic Go coverage profile")
    blocks = {}
    for line in lines[1:]:
        location, statements, hits = line.rsplit(" ", 2)
        statements, hits = int(statements), int(hits)
        if statements < 0 or hits < 0:
            raise ValueError("negative statement or hit count")
        previous = blocks.get(location)
        if previous and previous[0] != statements:
            raise ValueError("inconsistent duplicate coverage block: " + location)
        blocks[location] = (statements, hits + (previous[1] if previous else 0))
    files = defaultdict(lambda: {"statements": 0, "covered": 0, "uncoveredBlocks": []})
    for location, (statements, hits) in sorted(blocks.items()):
        file, span = location.rsplit(":", 1)
        row = files[file]
        row["statements"] += statements
        if hits:
            row["covered"] += statements
        elif statements:
            row["uncoveredBlocks"].append({"span": span, "statements": statements})
    total = sum(row["statements"] for row in files.values())
    covered = sum(row["covered"] for row in files.values())
    if not total:
        raise ValueError("profile has no executable statements")
    return {
        "schemaVersion": 1,
        "metric": "Go statement coverage",
        "scope": "All packages returned by go list ./..., including CLI and benchmark runner; no source exclusions",
        "mode": "atomic",
        "minimumPercent": float(minimum),
        "targetMet": Decimal(covered) * 100 >= minimum * total,
        "statements": total,
        "covered": covered,
        "uncovered": total - covered,
        "percent": round(covered * 100 / total, 6),
        "files": [{"file": file, **row} for file, row in sorted(files.items())],
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("profile", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--minimum", type=Decimal, default=Decimal(100))
    args = parser.parse_args()
    if not Decimal(0) <= args.minimum <= Decimal(100):
        parser.error("--minimum must be between 0 and 100")
    report = summarize(args.profile.read_text(encoding="utf-8"), args.minimum)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    print(f"{report['covered']}/{report['statements']} statements covered ({report['percent']:.6f}%).")
    print(f"{args.minimum}% target {'met' if report['targetMet'] else 'NOT met'}.")
    return 0 if report["targetMet"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
