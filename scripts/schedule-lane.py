#!/usr/bin/env python3
"""schedule-lane.py -- run the tests a change touched under schedules the
developer machine does not choose on its own (#1711).

`make test` runs every Go test once, at the machine's CPU count, and the
frontend suite runs every vitest file once. A test whose assertion depends on
which goroutine the scheduler runs first, or on whether a React effect has
flushed, passes on an 18-core machine and fails on a loaded CI runner.
TestWithRevocations_WiredLate did that: 0 of 300 runs failed at -cpu=2 and
-cpu=4, 140 of 300 at -cpu=1, and it reached main behind two green `make
verify` runs.

  go  Every Go package in the root module with a changed file against the
      merge base runs `go test -race -count=5` once per CPU setting in
      CPU_SETTINGS. At a 47% failure rate five runs at one CPU miss the defect
      with probability under 5%. Each failure prints the command that
      reproduces it.

  ui  The full vitest suite runs, and beside it every changed *.test.ts(x)
      file runs five times, so the files under test run on a loaded machine
      the way the CI runner's do.

A base branch that cannot be resolved fails the lane: a check that silently
skips is a check that was not run.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
from collections import defaultdict
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
CPU_SETTINGS = (1, 2)
RUNS = 5
# A package runs RUNS times inside one test binary, so go test's default
# 10-minute binary timeout is shared by all of them. internal/pdftext took 368s
# for five runs here with the rest of the tree beside it; on a slower runner
# the default would kill a package no test in it is wrong about.
TIMEOUT = "30m"
# How many runs a reproduction command asks for. At the rates this lane exists
# for, 300 runs at the failing CPU setting reproduce the failure every time.
REPRO_COUNT = 300
# The output kept per failed test when the lane prints why it failed.
MAX_OUTPUT_LINES = 40
VITEST_FILE = re.compile(r"^ui/(?!e2e/|node_modules/).*\.test\.tsx?$")
VITEST_FAIL = re.compile(r"^\s*(?:\u276f\s+|×\s+)?FAIL\s+(\S+\.test\.tsx?)")
ANSI = re.compile(r"\x1b\[[0-9;]*m")


def git(*args: str) -> str:
    return subprocess.run(
        ["git", *args], cwd=REPO_ROOT, check=True, capture_output=True, text=True
    ).stdout


def merge_base(base_branch: str) -> str:
    for ref in (f"origin/{base_branch}", base_branch):
        probe = subprocess.run(
            ["git", "rev-parse", "--verify", "--quiet", f"{ref}^{{commit}}"],
            cwd=REPO_ROOT,
            capture_output=True,
            text=True,
        )
        if probe.returncode == 0:
            return git("merge-base", "HEAD", ref).strip()
    raise SystemExit(
        f"FAIL schedule-lane: cannot resolve base branch '{base_branch}' "
        f"(tried origin/{base_branch} and {base_branch}); fetch it or set BASE_BRANCH."
    )


def changed_files(base: str) -> list[str]:
    """Committed, staged, unstaged and untracked paths that still exist."""
    names = set(git("diff", "--name-only", base).splitlines())
    names.update(git("ls-files", "--others", "--exclude-standard").splitlines())
    return sorted(n for n in names if n and (REPO_ROOT / n).is_file())


def in_root_module(rel: str) -> bool:
    """True when the nearest go.mod above rel is the repository's own."""
    parent = (REPO_ROOT / rel).parent
    while parent != REPO_ROOT:
        if (parent / "go.mod").is_file():
            return False
        parent = parent.parent
    return True


def go_package_dirs(files: list[str]) -> list[str]:
    dirs = set()
    for rel in files:
        if not rel.endswith(".go") or not in_root_module(rel):
            continue
        parts = Path(rel).parts[:-1]
        # The go command ignores these directories, so there is no package.
        if any(p == "testdata" or p.startswith(("_", ".")) for p in parts):
            continue
        dirs.add("./" + "/".join(parts) if parts else ".")
    return sorted(dirs)


def packages_with_tests(dirs: list[str]) -> dict[str, str]:
    """Import path -> ./relative dir, for the dirs that hold a test file.

    A directory whose files are all behind a build tag (test/acceptance) or
    that holds no test has nothing for this lane to run.
    """
    if not dirs:
        return {}
    out = subprocess.run(
        ["go", "list", "-e", "-json=Dir,ImportPath,TestGoFiles,XTestGoFiles", *dirs],
        cwd=REPO_ROOT,
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    found = {}
    decoder, pos = json.JSONDecoder(), 0
    while pos < len(out.rstrip()):
        pkg, pos = decoder.raw_decode(out, pos)
        pos = len(out) - len(out[pos:].lstrip())
        if pkg.get("TestGoFiles") or pkg.get("XTestGoFiles"):
            rel = os.path.relpath(pkg["Dir"], REPO_ROOT)
            found[pkg["ImportPath"]] = "./" + rel + "/" if rel != "." else "./"
    return found


def top_level(test: str) -> str:
    return test.split("/", 1)[0]


def run_go_setting(packages: dict[str, str], cpu: int) -> list[str]:
    """Run every package at one CPU setting; return a report per failure."""
    cmd = ["go", "test", "-race", f"-cpu={cpu}", f"-count={RUNS}", f"-timeout={TIMEOUT}", "-json", *packages]
    print(f"schedule-lane: {' '.join(cmd[:5])} ({len(packages)} package(s))", flush=True)
    proc = subprocess.Popen(cmd, cwd=REPO_ROOT, stdout=subprocess.PIPE, text=True)
    fails: dict[tuple[str, str], int] = defaultdict(int)
    output: dict[tuple[str, str], list[str]] = defaultdict(list)
    package_failed: set[str] = set()
    assert proc.stdout is not None
    for line in proc.stdout:
        try:
            ev = json.loads(line)
        except json.JSONDecodeError:
            print(line, end="")
            continue
        pkg, test, action = ev.get("ImportPath") or ev.get("Package", ""), ev.get("Test"), ev.get("Action")
        if action in ("output", "build-output"):
            output[(pkg, top_level(test) if test else "")].append(ev.get("Output", ""))
        elif action == "fail" and test and "/" not in test:
            fails[(pkg, test)] += 1
        elif action in ("fail", "build-fail") and not test:
            package_failed.add(pkg)
    proc.wait()

    reports = []
    for (pkg, test), n in sorted(fails.items()):
        rel = packages.get(pkg, pkg)
        tail = "".join(output[(pkg, test)][-MAX_OUTPUT_LINES:])
        reports.append(
            f"FAIL {test} in {pkg}: {n} of {RUNS} runs failed at -cpu={cpu}\n"
            f"  reproduce: go test -race -cpu={cpu} -count={REPRO_COUNT} -run '^{test}$' {rel}\n"
            + indent(tail)
        )
    for pkg in sorted(package_failed - {p for p, _ in fails}):
        rel = packages.get(pkg, pkg)
        tail = "".join(output[(pkg, "")][-MAX_OUTPUT_LINES:])
        reports.append(
            f"FAIL {pkg} at -cpu={cpu} with no failing test (build failure, panic or timeout)\n"
            f"  reproduce: go test -race -cpu={cpu} -count={RUNS} {rel}\n" + indent(tail)
        )
    if proc.returncode != 0 and not reports:
        reports.append(f"FAIL go test exited {proc.returncode} at -cpu={cpu} and named no package")
    return reports


def indent(text: str) -> str:
    return "".join("    " + ln + "\n" for ln in text.rstrip("\n").splitlines()) if text.strip() else ""


def lane_go(base_branch: str) -> int:
    base = merge_base(base_branch)
    packages = packages_with_tests(go_package_dirs(changed_files(base)))
    if not packages:
        print(f"schedule-lane: no Go package with tests changed against {base_branch}; nothing to run.")
        return 0
    reports = []
    for cpu in CPU_SETTINGS:
        reports += run_go_setting(packages, cpu)
    if reports:
        print("\n=== schedule-lane: tests that fail under a schedule `make test` did not choose ===")
        print("\n".join(reports))
        return 1
    settings = ",".join(str(c) for c in CPU_SETTINGS)
    print(f"schedule-lane: {len(packages)} package(s) passed {RUNS} runs at -cpu={settings}.")
    return 0


def failing_vitest_files(output: str) -> list[str]:
    """The test files a vitest run's default reporter marked FAIL."""
    found = []
    for line in ANSI.sub("", output).splitlines():
        m = VITEST_FAIL.match(line)
        if m and m.group(1) not in found:
            found.append(m.group(1))
    return found


def lane_ui(base_branch: str) -> int:
    base = merge_base(base_branch)
    files = [f[len("ui/"):] for f in changed_files(base) if VITEST_FILE.match(f)]
    ui = REPO_ROOT / "ui"
    env = {**os.environ, "NO_COLOR": "1", "FORCE_COLOR": "0"}
    # The repeats start together with the suite so that every one of them
    # overlaps its load. Queued one after another they outlast a suite that
    # takes about 24 seconds here, and the last runs see an idle machine.
    suite = subprocess.Popen(["npm", "run", "test"], cwd=ui, env=env)
    repeats = []
    if files:
        print(f"schedule-lane-ui: {len(files)} changed test file(s), {RUNS} runs beside the full suite", flush=True)
        repeats = [
            subprocess.Popen(
                ["npx", "vitest", "run", *files], cwd=ui, env=env,
                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
            )
            for _ in range(RUNS)
        ]
    failures = []
    for run, proc in enumerate(repeats, start=1):
        out, _ = proc.communicate()
        if proc.returncode != 0:
            named = failing_vitest_files(out) or files
            failures.append(
                f"FAIL run {run} of {RUNS} beside the full suite: {', '.join(named)}\n"
                f"  reproduce: make schedule-lane-ui (the failure needs a loaded machine; "
                f"cd ui && npx vitest run {' '.join(named)} runs it alone)\n"
                + indent("\n".join(ANSI.sub("", out).splitlines()[-MAX_OUTPUT_LINES:]))
            )
    suite_code = suite.wait()
    if failures:
        print("\n=== schedule-lane-ui: test files that fail on a loaded machine ===")
        print("\n".join(failures))
    if suite_code != 0:
        print(f"FAIL schedule-lane-ui: the full vitest suite exited {suite_code}")
    if not files:
        print(f"schedule-lane-ui: no vitest file changed against {base_branch}; ran the suite once.")
    elif not failures:
        print(f"schedule-lane-ui: {len(files)} changed test file(s) passed {RUNS} runs beside the full suite.")
    return 1 if failures or suite_code != 0 else 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n", 1)[0])
    parser.add_argument("lane", choices=("go", "ui"))
    args = parser.parse_args()
    base_branch = os.environ.get("BASE_BRANCH", "main")
    return lane_go(base_branch) if args.lane == "go" else lane_ui(base_branch)


if __name__ == "__main__":
    sys.exit(main())
