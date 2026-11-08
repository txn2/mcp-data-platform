#!/usr/bin/env python3
"""The endpoint latency job's percentiles as a job summary (#2544).

Reads a loadgen report and prints markdown. A run that wrote no report says so,
so a job that failed before the scenario still explains itself.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

OPERATION = "endpoint_request"


def summary(path: Path) -> str:
    if not path.is_file():
        return f"### Endpoint latency\n\nNo report at `{path}`: the scenario did not finish.\n"
    report = json.loads(path.read_text(encoding="utf-8"))
    lines = ["### Endpoint latency", "", "| p50 ms | p99 ms | requests | errors | verdict |", "|---|---|---|---|---|"]
    verdict = "pass" if report.get("passed") else "FAIL"
    found = [o for o in report.get("operations") or [] if o.get("operation") == OPERATION]
    if not found:
        return "\n".join(lines[:2]) + f"\nThe report holds no `{OPERATION}` samples. Verdict: {verdict}.\n"
    op = found[0]
    lines.append(f"| {op['p50_ms']:.1f} | {op['p99_ms']:.1f} | {op['count']} | {op['error_rate'] * 100:.2f}% | {verdict} |")
    lines.append("")
    lines.extend(f"- {a['name']}: {a['message']}" for a in report.get("assertions") or [])
    return "\n".join(lines) + "\n"


if __name__ == "__main__":
    sys.stdout.write(summary(Path(sys.argv[1])))
