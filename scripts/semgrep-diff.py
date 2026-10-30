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

`--whole-files` scans every changed Go file whole and reports every finding in
it. That is the scope for .semgrep/, whose rules are defects wherever they sit:
`make semgrep` already holds the whole tree to them, so a finding in a file the
branch touched is the branch's, and reporting it here answers in seconds what
verify's static lane answered fifteen minutes in (#2145). `make semgrep` also
runs the p/golang registry ruleset, so `make semgrep-changed` names it beside
.semgrep/: an open-redirect finding on a new http.Redirect passed every
cheap gate and failed verify's static lane (#2339).

Usage: semgrep-diff.py [--whole-files] <config>...
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


def untracked_go_files() -> list[str]:
    """New Go files git does not track yet, which `git diff` does not show.

    `make verify` runs before the commit, often before `git add`, so a gate
    that read only the diff would pass a new file it never looked at.
    """
    out = run("git", "ls-files", "--others", "--exclude-standard", "--", "*.go")
    return [p for p in out.splitlines() if p]


def findings(configs: list[str], files: list[str]) -> list[dict]:
    """Scan the changed files and return Semgrep's results."""
    flags = [f for c in configs for f in ("--config", c)]
    out = subprocess.run(
        ["semgrep", "scan", *flags, "--json", "--quiet", "--"] + files,
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
    args = sys.argv[1:]
    whole = "--whole-files" in args
    args = [a for a in args if a != "--whole-files"]
    if not args:
        print("Usage: semgrep-diff.py [--whole-files] <config>...", file=sys.stderr)
        sys.exit(1)
    config = " ".join(args)

    base = merge_base()
    changed = changed_lines(base)
    if whole:
        for path in untracked_go_files():
            changed.setdefault(path, set())
    # A path the diff added and the working tree has since removed is in the
    # map and not on disk; semgrep errors on a path it cannot open.
    files = sorted(p for p in changed if os.path.exists(p))
    if not files:
        print(f"No changed Go files vs merge-base {base[:12]}; nothing to scan.")
        return

    hits = []
    for r in findings(args, files):
        path = r["path"]
        span = range(r["start"]["line"], r["end"]["line"] + 1)
        if whole or any(line in changed.get(path, ()) for line in span):
            hits.append(r)

    scope = "in changed files" if whole else "on changed lines"
    if not hits:
        print(f"Scanned {len(files)} changed Go file(s) against {config}: no findings {scope}.")
        return

    for r in hits:
        print(
            f"{r['path']}:{r['start']['line']}: [{r['check_id'].split('.')[-1]}] "
            f"{' '.join(r['extra']['message'].split())}"
        )
    print(f"\n{len(hits)} finding(s) {scope}. See {config}.", file=sys.stderr)
    sys.exit(1)


if __name__ == "__main__":
    main()
