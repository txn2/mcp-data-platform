#!/usr/bin/env python3
"""Print the packages whose real-database tests a branch can break (#1947).

make preverify runs the RealDB tests of these packages against one Postgres
(make test-realdb REALDB_PKGS=...), so a change that breaks one fails there
rather than in make verify's Docker lane, which then has to run in full a
second time. On #1913 the save gates refused scripts that three RealDB tests in
pkg/platform created; pkg/platform imports internal/platform/scriptlayer, which
the branch changed, and preverify never compiled a test behind the integration
build tag.

A package is printed when it holds a RealDB test and it has a changed Go file
against the base branch or imports -- directly, transitively, or from a test --
a package that has one. Changed files are computed exactly as the schedule lane
computes them (scripts/schedule-lane.py): committed, staged, unstaged and
untracked. Nothing is printed when there is nothing to run.
"""

from __future__ import annotations

import importlib.util
import json
import os
import re
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
REALDB_TEST = re.compile(r"^func Test\w*RealDB", re.MULTILINE)


def schedule_lane():
    """The schedule lane's diff helpers, loaded from their one copy."""
    spec = importlib.util.spec_from_file_location("schedule_lane", REPO_ROOT / "scripts" / "schedule-lane.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def go_packages() -> list[dict]:
    """Every package in the module, with the integration-tagged test files."""
    out = subprocess.run(
        ["go", "list", "-e", "-tags=integration",
         "-json=Dir,ImportPath,Deps,TestImports,XTestImports,TestGoFiles,XTestGoFiles", "./..."],
        cwd=REPO_ROOT, check=True, capture_output=True, text=True,
    ).stdout
    pkgs, decoder, pos = [], json.JSONDecoder(), 0
    while pos < len(out.rstrip()):
        pkg, pos = decoder.raw_decode(out, pos)
        pos = len(out) - len(out[pos:].lstrip())
        pkgs.append(pkg)
    return pkgs


def has_realdb_test(pkg: dict) -> bool:
    for name in pkg.get("TestGoFiles", []) + pkg.get("XTestGoFiles", []):
        if REALDB_TEST.search((Path(pkg["Dir"]) / name).read_text(encoding="utf-8")):
            return True
    return False


def main() -> int:
    lane = schedule_lane()
    base = lane.merge_base(os.environ.get("BASE_BRANCH", "main"))
    changed_dirs = {os.path.normpath(REPO_ROOT / d) for d in lane.go_package_dirs(lane.changed_files(base))}
    pkgs = go_packages()
    changed = {p["ImportPath"] for p in pkgs if os.path.normpath(p["Dir"]) in changed_dirs}
    if not changed:
        return 0
    selected = []
    for p in pkgs:
        reach = {p["ImportPath"], *p.get("Deps", []), *p.get("TestImports", []), *p.get("XTestImports", [])}
        if reach & changed and has_realdb_test(p):
            selected.append(p["ImportPath"])
    print(" ".join(sorted(selected)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
