#!/usr/bin/env python3
"""Allocations per call on the hot paths, held to a checked-in baseline (#2716).

The code every tools/call and HTTP request crosses had no benchmark, so a change
that added an allocation to it was seen only by a load test or production.
scripts/bench-allocs.tsv names each benchmark and the allocs/op it may take.

  check     run every benchmark the baseline names, a fixed number of
            iterations, three times, and fail when the lowest allocs/op of any
            exceeds its baseline, naming it; a benchmark the baseline names
            that no longer exists fails too, and so does a benchmark in a
            listed package the baseline does not name. A benchmark under its
            baseline is reported, so the baseline can be lowered.
  update    run them and write what they measure as the new baseline. A change
            that adds allocations updates the baseline in the same diff.
  trend     run every listed package's benchmarks -count=10 and write the go
            test output to the path given, for benchstat (the weekly
            Benchmark trend workflow). ns/op is tracked there, never gated:
            shared runners vary too much for it, and allocation counts do not.

Allocation counts are deterministic for a fixed input and iteration count, which
is why they are what a pull request is held to.
"""

from __future__ import annotations

import re
import subprocess
import sys
from collections import defaultdict
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
BASELINE = REPO_ROOT / "scripts" / "bench-allocs.tsv"
ITERATIONS = "200x"
COUNT = "3"
LINE_RE = re.compile(r"^(Benchmark\S+?)(?:-\d+)?\s+\d+\s+.*?(\d+) allocs/op")
DECL_RE = re.compile(r"^func (Benchmark\w+)\(b \*testing\.B\)", re.M)


def read_baseline() -> dict[str, dict[str, int]]:
    """package -> benchmark -> allowed allocs/op."""
    out: dict[str, dict[str, int]] = defaultdict(dict)
    for line in BASELINE.read_text().splitlines():
        if not line.strip() or line.startswith("#"):
            continue
        pkg, name, allocs = line.split("\t")
        out[pkg][name] = int(allocs)
    return out


def declared(pkg: str) -> set[str]:
    """The benchmarks a package's test files declare."""
    names: set[str] = set()
    for f in (REPO_ROOT / pkg.removeprefix("./")).glob("*_test.go"):
        names.update(DECL_RE.findall(f.read_text()))
    return names


def run(pkg: str, names: list[str], count: str, benchtime: str) -> str:
    pattern = "^(" + "|".join(sorted(names)) + ")$"
    proc = subprocess.run(
        ["go", "test", "-run", "^$", "-bench", pattern, "-benchmem", "-benchtime", benchtime, "-count", count, pkg],
        cwd=REPO_ROOT, capture_output=True, text=True,
    )
    if proc.returncode != 0:
        sys.stdout.write(proc.stdout + proc.stderr)
        raise SystemExit(f"FAIL bench-allocs: the benchmarks of {pkg} did not run")
    return proc.stdout


def measure(pkg: str, names: list[str]) -> dict[str, int]:
    """The lowest allocs/op each benchmark reported over COUNT runs."""
    best: dict[str, int] = {}
    for line in run(pkg, names, COUNT, ITERATIONS).splitlines():
        m = LINE_RE.match(line)
        if m:
            name, allocs = m.group(1), int(m.group(2))
            best[name] = min(best.get(name, allocs), allocs)
    return best


def check() -> int:
    failed: list[str] = []
    lower: list[str] = []
    for pkg, want in sorted(read_baseline().items()):
        extra = declared(pkg) - set(want)
        for name in sorted(extra):
            failed.append(f"[FAIL] {name}: {pkg} declares it and scripts/bench-allocs.tsv has no baseline for it; "
                          "run `make bench-allocs-update`")
        got = measure(pkg, list(want))
        for name, allowed in sorted(want.items()):
            if name not in got:
                failed.append(f"[FAIL] {name}: named in the baseline and not run in {pkg}; remove or rename its row")
                continue
            n = got[name]
            print(f"{pkg}\t{name}\t{n} allocs/op (baseline {allowed})")
            if n > allowed:
                failed.append(f"[FAIL] {name}: {n} allocs/op, over its baseline of {allowed} ({pkg})")
            elif n < allowed:
                lower.append(f"{name}: {n} allocs/op, under its baseline of {allowed}; lower it with `make bench-allocs-update`")
    for line in lower:
        print("note: " + line)
    if failed:
        print("\n".join(failed))
        print("\nFAIL bench-allocs: a hot path allocates more than its baseline. Remove the allocation, or, when it "
              "is intended, update scripts/bench-allocs.tsv in the same change (`make bench-allocs-update`).")
        return 1
    print("bench-allocs: every benchmark is within its baseline")
    return 0


def update() -> int:
    current = read_baseline()
    rows = []
    for pkg in sorted(current):
        names = sorted(declared(pkg))
        got = measure(pkg, names)
        rows += [f"{pkg}\t{name}\t{got[name]}" for name in names if name in got]
    head = [ln for ln in BASELINE.read_text().splitlines() if ln.startswith("#")]
    BASELINE.write_text("\n".join(head + rows) + "\n")
    print(f"bench-allocs: wrote {len(rows)} baselines to {BASELINE.relative_to(REPO_ROOT)}")
    return 0


def trend(out: Path) -> int:
    chunks = []
    for pkg in sorted(read_baseline()):
        chunks.append(run(pkg, sorted(declared(pkg)), "10", "1s"))
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text("".join(chunks))
    print(f"bench-allocs: wrote the trend run to {out}")
    return 0


def main() -> int:
    if len(sys.argv) < 2 or sys.argv[1] not in {"check", "update", "trend"}:
        print("usage: bench-allocs.py check|update|trend <out>", file=sys.stderr)
        return 2
    if sys.argv[1] == "check":
        return check()
    if sys.argv[1] == "update":
        return update()
    if len(sys.argv) < 3:
        print("usage: bench-allocs.py trend <out>", file=sys.stderr)
        return 2
    return trend(Path(sys.argv[2]))


if __name__ == "__main__":
    sys.exit(main())
