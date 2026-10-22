#!/usr/bin/env python3
"""Read the branch's diff for the preverify gates that verify runs in full (#2186).

Two of verify's gates had no cheap counterpart, and each failed a verify on
the #2171-#2174 branch after make preverify passed:

  gosec-packages  print the packages with a changed non-test Go file, for the
                  standalone gosec. golangci-lint's gosec honours //nolint:gosec
                  and the standalone one reads only #nosec, so a finding the
                  patch lint passes still fails verify-static.
  routes-changed  exit 0 when an added or removed line of a non-test Go file
                  registers a mux pattern (Handle or HandleFunc), so the
                  route-template gate (TestEveryRouteReportsItsTemplate_RealDB)
                  runs; exit 1 when none does.

Changed files are computed as the schedule lane computes them
(scripts/schedule-lane.py): committed, staged, unstaged and untracked.
"""

from __future__ import annotations

import importlib.util
import os
import re
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
REGISTRATION = re.compile(r"\.Handle(Func)?\(")


def schedule_lane():
    """The schedule lane's diff helpers, loaded from their one copy."""
    spec = importlib.util.spec_from_file_location("schedule_lane", REPO_ROOT / "scripts" / "schedule-lane.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def production_go(files: list[str]) -> list[str]:
    return [f for f in files if f.endswith(".go") and not f.endswith("_test.go")]


def gosec_packages(lane, base: str) -> int:
    dirs = lane.go_package_dirs(production_go(lane.changed_files(base)))
    if dirs:
        print(" ".join(dirs))
    return 0


def changed_lines(base: str, rel: str, untracked: set[str]) -> list[str]:
    """The added and removed lines of one file against base."""
    if rel in untracked:
        return (REPO_ROOT / rel).read_text(encoding="utf-8").splitlines()
    diff = subprocess.run(
        ["git", "diff", "-U0", base, "--", rel], cwd=REPO_ROOT, check=True, capture_output=True, text=True,
    ).stdout
    return [
        line[1:] for line in diff.splitlines()
        if line[:1] in "+-" and not line.startswith(("+++", "---"))
    ]


def routes_changed(lane, base: str) -> int:
    untracked = set(lane.git("ls-files", "--others", "--exclude-standard").splitlines())
    for rel in production_go(lane.changed_files(base)):
        for line in changed_lines(base, rel, untracked):
            if REGISTRATION.search(line):
                print(f"routes-changed: {rel} changes a route registration: {line.strip()}")
                return 0
    return 1


def main(argv: list[str]) -> int:
    modes = {"gosec-packages": gosec_packages, "routes-changed": routes_changed}
    if len(argv) != 2 or argv[1] not in modes:
        print(f"usage: {argv[0]} {{{'|'.join(modes)}}}", file=sys.stderr)
        return 2
    lane = schedule_lane()
    return modes[argv[1]](lane, lane.merge_base(os.environ.get("BASE_BRANCH", "main")))


if __name__ == "__main__":
    sys.exit(main(sys.argv))
