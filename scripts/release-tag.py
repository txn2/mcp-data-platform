#!/usr/bin/env python3
"""Release-candidate rules for a release tag (#2012).

A release is cut as one or more release candidates, vX.Y.Z-rcN, each run on a
staging deployment, and then the final tag vX.Y.Z on the exact commit the last
candidate was built from. Nothing merges between the accepted candidate and the
final tag: a fix is another candidate.

  check <tag>     refuse a tag that breaks the rules:
                  - a tag that is neither vX.Y.Z nor vX.Y.Z-rcN;
                  - a final tag on a commit other than its latest candidate's.
                  A final tag with no candidate is a release cut straight from
                  main and passes: a candidate is how a release is judged when
                  one is cut, not a precondition of every release.
  previous <tag>  print the last FINAL release before <tag>: what the changelog
                  and the release gates measure from, so neither a final tag's
                  notes nor its checks shrink to what changed since a candidate.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
TAG_RE = re.compile(r"^v(\d+)\.(\d+)\.(\d+)(?:-rc(\d+))?$")


def git(*args: str) -> str:
    return subprocess.run(["git", *args], cwd=REPO_ROOT, capture_output=True, text=True, check=True).stdout.strip()


def parse(tag: str) -> tuple[int, int, int, int | None] | None:
    m = TAG_RE.match(tag)
    if not m:
        return None
    rc = int(m.group(4)) if m.group(4) is not None else None
    return int(m.group(1)), int(m.group(2)), int(m.group(3)), rc


def tags() -> list[str]:
    return [t for t in git("tag", "--list", "v*").split() if parse(t)]


def commit_of(tag: str) -> str:
    return git("rev-list", "-n", "1", tag)


def candidates(final: str) -> list[str]:
    """The release candidates of a final tag, oldest first."""
    found = [t for t in tags() if t.startswith(final + "-rc")]
    return sorted(found, key=lambda t: parse(t)[3])  # type: ignore[index]


def check(tag: str) -> int:
    parsed = parse(tag)
    if parsed is None:
        print(f"FAIL release tag: {tag!r} is neither vX.Y.Z nor vX.Y.Z-rcN.", file=sys.stderr)
        return 1
    if parsed[3] is not None:
        if parsed[3] < 1:
            print(f"FAIL release tag: {tag}: candidates are numbered from rc1.", file=sys.stderr)
            return 1
        print(f"release tag: {tag} is a release candidate.")
        return 0
    rcs = candidates(tag)
    if not rcs:
        print(f"release tag: {tag} has no candidate; it is released from its own commit.")
        return 0
    latest = rcs[-1]
    if commit_of(latest) != commit_of(tag):
        print(f"FAIL release tag: {tag} is on {commit_of(tag)[:12]}, but its latest candidate {latest} is on "
              f"{commit_of(latest)[:12]}. A final release is the commit its last candidate was built from; "
              f"tag another candidate for the new commit.", file=sys.stderr)
        return 1
    print(f"release tag: {tag} is on the commit {latest} was built from.")
    return 0


def previous(tag: str) -> int:
    parsed = parse(tag)
    if parsed is None:
        print(f"FAIL release tag: {tag!r} is neither vX.Y.Z nor vX.Y.Z-rcN.", file=sys.stderr)
        return 1
    version = parsed[:3]
    finals = [t for t in tags() if parse(t)[3] is None and parse(t)[:3] < version]  # type: ignore[index]
    if not finals:
        return 0
    print(max(finals, key=lambda t: parse(t)[:3]))  # type: ignore[index]
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    for name in ("check", "previous"):
        p = sub.add_parser(name)
        p.add_argument("tag")
    args = parser.parse_args()
    return check(args.tag) if args.command == "check" else previous(args.tag)


if __name__ == "__main__":
    sys.exit(main())
