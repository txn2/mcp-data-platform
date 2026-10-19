#!/usr/bin/env python3
"""builtin-pages-check.py -- warn when a diff changes the source a built-in
knowledge page describes and leaves the page alone (#2200).

The platform ships knowledge pages about itself
(internal/platform/knowledgebuiltin/pages) and reconciles them into every
deployment on its next start. Nothing compiles their prose, so a change to
what a page describes can leave the page describing what the code used to do,
and every deployment serves the stale text as the platform's own word.

internal/platform/knowledgebuiltin/covers.txt lists, for each page, the
source it describes: a path ending in / covers everything under it. The check
reads the diff against the merge base with main and warns, per page, when a
covered path changed and the page did not. A test file never counts as a
change. The parts of a page generated from code (the dialect contract, the
content-type tables, the map regions, the index) are already current.

It warns rather than fails for a human, since a refactor that changes no
behavior owes the page nothing; for an AI agent the warning blocks until the
page has been read against the change, the same rule doc-check carries. A base
branch that cannot be resolved fails.
"""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
PAGES_DIR = "internal/platform/knowledgebuiltin/pages/"
COVERS = "internal/platform/knowledgebuiltin/covers.txt"
TEST_SUFFIXES = ("_test.go", ".test.ts", ".test.tsx")


def git(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(["git", *args], cwd=REPO_ROOT, check=check, capture_output=True, text=True)


def merge_base(base_branch: str) -> str:
    for ref in (f"origin/{base_branch}", base_branch):
        if git("rev-parse", "--verify", "--quiet", f"{ref}^{{commit}}", check=False).returncode == 0:
            return git("merge-base", "HEAD", ref).stdout.strip()
    raise SystemExit(
        f"FAIL builtin-pages-check: cannot resolve base branch '{base_branch}' "
        f"(tried origin/{base_branch} and {base_branch}); fetch it or set BASE_BRANCH."
    )


def changed_files(base: str) -> list[str]:
    """Committed, staged, unstaged, untracked and deleted paths."""
    names = set(git("diff", "--name-only", base).stdout.splitlines())
    names.update(git("ls-files", "--others", "--exclude-standard").stdout.splitlines())
    return sorted(n for n in names if n)


def read_covers() -> dict[str, list[str]]:
    path = REPO_ROOT / COVERS
    if not path.exists():
        raise SystemExit(f"FAIL builtin-pages-check: {COVERS} is missing.")
    covers: dict[str, list[str]] = {}
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        page, *paths = line.split()
        covers[page] = paths
    return covers


def covered(path: str, prefix: str) -> bool:
    return path.startswith(prefix) if prefix.endswith("/") else path == prefix


def main() -> int:
    base = merge_base(os.environ.get("BASE_BRANCH", "main"))
    changed = [f for f in changed_files(base) if not f.endswith(TEST_SUFFIXES)]
    covers = read_covers()

    stale: dict[str, list[str]] = {}
    for page, prefixes in sorted(covers.items()):
        if PAGES_DIR + page in changed:
            continue
        hits = [f for f in changed if any(covered(f, p) for p in prefixes)]
        if hits:
            stale[page] = hits

    if not stale:
        print("OK builtin-pages-check: no built-in knowledge page describes source this diff changed without the page changing too.")
        return 0

    print("WARNING builtin-pages-check: this diff changes source a built-in knowledge page describes, and not the page.")
    print("Every deployment serves these pages as the platform's own word on its next start. Read each page")
    print(f"against the change and update it, or leave it when the change alters nothing it says ({COVERS}).")
    for page, hits in stale.items():
        print(f"  {PAGES_DIR}{page}")
        for f in hits:
            print(f"    {f}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
