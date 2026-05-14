#!/usr/bin/env python3
"""Count blocking CodeQL alerts in a SARIF file.

A blocker is a result that is BOTH:
  - level == "error" OR rule security-severity >= 7.0, AND
  - not present in the baseline file.

The security-severity gate catches what GitHub Code Scanning treats
as a blocking alert in CI (low-confidence taint findings like
go/request-forgery emit `level=note` but `security-severity=9.1`).
Without it, local make verify reports clean while CI rejects the
same PR — exactly the parity gap that bit PR #406.

The baseline file (default scripts/codeql-baseline.txt) lists known
pre-existing alerts that the gate ignores so this script can be
introduced without retroactively breaking unrelated PRs. Format:
one `<rule_id> <path>:<line>` per line; lines starting with `#` are
comments.

Usage: codeql-gate.py PATH_TO_SARIF [PATH_TO_BASELINE]
Exits 1 with details when NEW blockers are found, 0 otherwise.
"""

import json
import os
import sys

# Minimum security-severity that counts as a blocker. 7.0 is the
# CVSS-style cutoff for "high" — CRITICAL is anything >= 9.0 and
# also caught.
MIN_BLOCKING_SEVERITY = 7.0

DEFAULT_BASELINE = os.path.join(
    os.path.dirname(os.path.abspath(__file__)), "codeql-baseline.txt"
)


def load_baseline(path: str) -> set[str]:
    """Return the set of "rule_id path:line" tokens in the baseline.

    Missing file is fine — returns empty set so the gate runs in
    "no baseline" mode.
    """
    accepted: set[str] = set()
    if not os.path.exists(path):
        return accepted
    with open(path, encoding="utf-8") as f:
        for raw in f:
            line = raw.strip()
            if not line or line.startswith("#"):
                continue
            accepted.add(line)
    return accepted


def alert_key(rule_id: str, loc: str, line: int | str) -> str:
    """Format the (rule, location) tuple the baseline matches on."""
    return f"{rule_id} {loc}:{line}"


def count_blockers(sarif: dict, baseline: set[str]) -> list[str]:
    """Return descriptions of every NEW blocker (not in baseline)."""
    blockers: list[str] = []
    for run in sarif.get("runs", []):
        rules = {}
        for rule in run.get("tool", {}).get("driver", {}).get("rules", []):
            sev_raw = rule.get("properties", {}).get("security-severity", "0") or "0"
            try:
                rules[rule.get("id")] = float(sev_raw)
            except (TypeError, ValueError):
                rules[rule.get("id")] = 0.0
        for result in run.get("results", []):
            rule_id = result.get("ruleId", "<unknown>")
            level = result.get("level", "note")
            sev = rules.get(rule_id, 0.0)
            if not (level == "error" or sev >= MIN_BLOCKING_SEVERITY):
                continue
            location = (
                result.get("locations", [{}])[0]
                .get("physicalLocation", {})
            )
            loc = location.get("artifactLocation", {}).get("uri", "?")
            line = location.get("region", {}).get("startLine", "?")
            key = alert_key(rule_id, loc, line)
            if key in baseline:
                continue
            blockers.append(f"{rule_id} (level={level}, sev={sev}) at {loc}:{line}")
    return blockers


def main(argv: list[str]) -> int:
    if len(argv) not in (2, 3):
        print("usage: codeql-gate.py PATH_TO_SARIF [PATH_TO_BASELINE]", file=sys.stderr)
        return 2
    sarif_path = argv[1]
    baseline_path = argv[2] if len(argv) == 3 else DEFAULT_BASELINE
    try:
        with open(sarif_path, encoding="utf-8") as f:
            sarif = json.load(f)
    except OSError as e:
        print(f"FAIL: cannot read SARIF: {e}", file=sys.stderr)
        return 2
    baseline = load_baseline(baseline_path)
    blockers = count_blockers(sarif, baseline)
    if not blockers:
        print(
            f"CodeQL: no new blocking issues ({len(baseline)} baselined)."
        )
        return 0
    print(f"FAIL: CodeQL found {len(blockers)} NEW blocking issue(s):")
    for b in blockers:
        print(f"  - {b}")
    print(
        "\nIf a finding is a true false positive mitigated at runtime, "
        "add it to scripts/codeql-baseline.txt with rationale; "
        "otherwise fix the code."
    )
    return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
