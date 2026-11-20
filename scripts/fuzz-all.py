#!/usr/bin/env python3
"""Runs every fuzz target in the module for a fixed time each (#2713).

Fuzz targets used to be replayed from their seed corpus by `go test` and
never fuzzed. This finds every `func Fuzz<Name>(f *testing.F)` in the module's
test files and runs each with `go test -fuzz` for FUZZTIME (default 30s), one
at a time, since each fuzzes on every core.

A target that fails (a crasher, a broken property, a hang) leaves its
minimized input under the package's testdata/fuzz/<Target>/, which `go test`
replays from then on once it is committed; the run prints go's own
`--- FAIL: <Target>` and `Failing input written to ...` lines, which the
scheduled workflow's report names, and exits 1 after running every target.

  fuzz-all.py [--list] [--only REGEX]
"""

from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
DECL_RE = re.compile(r"^func (Fuzz\w+)\(f \*testing\.F\)", re.M)
SKIP_DIRS = {"node_modules", "ui", "bench", "test", "build", "dist", "_dist", ".git", "vendor"}


def targets() -> list[tuple[str, str]]:
    """(package directory, target) for every fuzz target, in path order."""
    out: list[tuple[str, str]] = []
    for root in ("pkg", "internal", "cmd"):
        for f in sorted((REPO_ROOT / root).rglob("*_test.go")):
            if any(part in SKIP_DIRS for part in f.relative_to(REPO_ROOT).parts):
                continue
            for name in DECL_RE.findall(f.read_text(errors="replace")):
                out.append(("./" + str(f.parent.relative_to(REPO_ROOT)), name))
    return sorted(set(out))


def run(pkg: str, target: str, fuzztime: str, logs: Path) -> bool:
    log = logs / f"{pkg.strip('./').replace('/', '_')}-{target}.log"
    with log.open("w") as out:
        proc = subprocess.run(
            ["go", "test", pkg, "-run", "^$", "-fuzz", f"^{target}$", "-fuzztime", fuzztime],
            cwd=REPO_ROOT, stdout=out, stderr=subprocess.STDOUT,
        )
    if proc.returncode == 0:
        print(f"ok    {pkg} {target}")
        return True
    text = log.read_text(errors="replace")
    print(f"FAIL  {pkg} {target}")
    for line in text.splitlines():
        if line.startswith("--- FAIL") or "Failing input written to" in line or ".go:" in line and "fuzz_test" in line:
            print("      " + line.strip())
    if "--- FAIL" not in text:
        print(f"--- FAIL: {target} ({pkg}: the fuzzer stopped; {log.relative_to(REPO_ROOT)} says why)")
    return False


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--list", action="store_true", help="print the targets and stop")
    parser.add_argument("--only", default="", help="run the targets whose name matches this regular expression")
    args = parser.parse_args()
    found = [t for t in targets() if not args.only or re.search(args.only, t[1])]
    if args.list:
        for pkg, name in found:
            print(f"{pkg}\t{name}")
        return 0
    if not found:
        print("FAIL fuzz-all: no fuzz target found")
        return 1
    fuzztime = os.environ.get("FUZZTIME", "30s")
    logs = REPO_ROOT / "build" / "fuzz"
    logs.mkdir(parents=True, exist_ok=True)
    failed = [f"{pkg} {name}" for pkg, name in found if not run(pkg, name, fuzztime, logs)]
    print(f"\nfuzz-all: {len(found)} targets, {fuzztime} each, {len(failed)} failed")
    if failed:
        print("A failing input is under its package's testdata/fuzz/<Target>/. Commit it with the fix, "
              "so go test replays it.")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
