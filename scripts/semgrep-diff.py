#!/usr/bin/env python3
"""Run a Semgrep config against the lines this branch changed.

`make semgrep` runs .semgrep/ over the whole tree, because a finding there is a
defect wherever it sits. Some rules are not like that: they refuse a SHAPE that
a diff-scoped CI check rejects, using reasoning Semgrep cannot reproduce. The
allocation-size rule in .semgrep-diff/ is the case that motivated this file --
CodeQL's go/allocation-size-overflow flags `len(x) + 1` as an allocation size
only where the operand can be large, and the syntactic form it refuses appears
55 times in this tree without being a defect once (#1748).

Scoping such a rule to the diff is what CI already does with it: GitHub Code
Scanning reports against a pull request's changes. It is also what `make lint`
does for golangci-lint, with --new-from-patch and the same merge-base.

Semgrep's own --baseline-commit cannot be used: it aborts when the working tree
has unstaged changes, and `make verify` is a PRE-commit gate that exists to be
run on exactly that.

Usage: semgrep-diff.py <config-path>
"""

import json
import os
import re
import subprocess
import sys
from collections import defaultdict

# Only added lines count, matching golangci-lint --new-from-patch. A finding on
# a line the diff merely moved past is pre-existing.
HUNK = re.compile(r"^@@ -\S+ \+(\d+)(?:,(\d+))? @@")


def run(*args: str) -> str:
    """Run a command, failing loudly rather than returning a partial answer."""
    out = subprocess.run(args, capture_output=True, text=True, check=False)
    if out.returncode != 0:
        print(f"ERROR: {' '.join(args)} failed:\n{out.stderr}", file=sys.stderr)
        sys.exit(1)
    return out.stdout


def merge_base() -> str:
    """Resolve the base this branch is measured against.

    Mirrors `make lint`: origin/main when it is reachable, main otherwise, and
    a hard failure when neither is. A gate that silently skips because it could
    not find a base is how lint findings reached CI in #393.
    """
    subprocess.run(
        ["git", "fetch", "--quiet", "origin", "main"],
        capture_output=True, check=False,
    )
    for base in ("origin/main", "main"):
        rev = subprocess.run(
            ["git", "rev-parse", "--verify", "--quiet", base],
            capture_output=True, text=True, check=False,
        )
        if rev.returncode == 0:
            return run("git", "merge-base", base, "HEAD").strip()
    print(
        "ERROR: neither origin/main nor main is reachable.\n"
        "       Run `git fetch origin main` and retry.\n"
        "       (a diff-scoped gate MUST run against a base; silent-skip is a "
        "CI-parity hole.)",
        file=sys.stderr,
    )
    sys.exit(1)


def changed_lines(base: str) -> dict[str, set[int]]:
    """Map each changed Go file to the line numbers this branch added to it.

    The diff includes the working tree, so the gate answers before the commit
    exists rather than after it.
    """
    added: dict[str, set[int]] = defaultdict(set)
    path = ""
    for line in run("git", "diff", "--unified=0", base).splitlines():
        if line.startswith("+++ b/"):
            candidate = line[6:]
            path = candidate if candidate.endswith(".go") else ""
        elif path and (m := HUNK.match(line)):
            start, count = int(m.group(1)), int(m.group(2) or 1)
            added[path].update(range(start, start + count))
    return {p: lines for p, lines in added.items() if lines}


def findings(config: str, files: list[str]) -> list[dict]:
    """Scan the changed files and return Semgrep's results."""
    out = subprocess.run(
        ["semgrep", "scan", "--config", config, "--json", "--quiet", "--"] + files,
        capture_output=True, text=True, check=False,
    )
    if not out.stdout:
        print(f"ERROR: semgrep produced no output:\n{out.stderr}", file=sys.stderr)
        sys.exit(1)
    report = json.loads(out.stdout)
    for err in report.get("errors", []):
        print(f"semgrep: {err.get('message', err)}", file=sys.stderr)
    return report.get("results", [])


def main() -> None:
    if len(sys.argv) != 2:
        print("Usage: semgrep-diff.py <config-path>", file=sys.stderr)
        sys.exit(1)
    config = sys.argv[1]

    base = merge_base()
    changed = changed_lines(base)
    # A path the diff added and the working tree has since removed is in the
    # map and not on disk; semgrep errors on a path it cannot open.
    files = sorted(p for p in changed if os.path.exists(p))
    if not files:
        print(f"No changed Go lines vs merge-base {base[:12]}; nothing to scan.")
        return

    hits = []
    for r in findings(config, files):
        path = r["path"]
        span = range(r["start"]["line"], r["end"]["line"] + 1)
        if any(line in changed.get(path, ()) for line in span):
            hits.append(r)

    if not hits:
        print(f"Scanned {len(files)} changed Go file(s) against {config}: no findings.")
        return

    for r in hits:
        print(
            f"{r['path']}:{r['start']['line']}: [{r['check_id'].split('.')[-1]}] "
            f"{' '.join(r['extra']['message'].split())}"
        )
    print(f"\n{len(hits)} finding(s) on changed lines. See {config}.", file=sys.stderr)
    sys.exit(1)


if __name__ == "__main__":
    main()
