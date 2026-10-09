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
                  - a final tag while the most recent E2E Nightly on main
                  failed (#2036), naming the run and its failing tests;
                  `release-without-nightly: <reason>` in the tag's annotation
                  releases over it. A candidate is not held to this: it may be
                  the fix the red night is waiting for.
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

sys.path.insert(0, str(Path(__file__).resolve().parent))
import e2e_nightly  # noqa: E402

REPO_ROOT = Path(__file__).resolve().parent.parent
TAG_RE = re.compile(r"^v(\d+)\.(\d+)\.(\d+)(?:-rc(\d+))?$")
NIGHTLY_OVERRIDE_RE = re.compile(r"^release-without-nightly:\s*\S", re.MULTILINE)


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


def annotation(tag: str) -> str:
    """The message of an annotated tag; empty for a lightweight or absent one."""
    try:
        if git("cat-file", "-t", f"refs/tags/{tag}") != "tag":
            return ""
        return git("tag", "-l", "--format=%(contents)", tag)
    except subprocess.CalledProcessError:
        return ""


def check_nightly(tag: str) -> int:
    """Refuse a final tag while the most recent E2E Nightly on main failed."""
    if NIGHTLY_OVERRIDE_RE.search(annotation(tag)):
        print(f"release tag: {tag} is released over the E2E Nightly (release-without-nightly).")
        return 0
    override = "or annotate the tag with `release-without-nightly: <reason>`"
    try:
        run = e2e_nightly.latest_run()
    except (OSError, subprocess.CalledProcessError) as err:
        detail = getattr(err, "stderr", "") or str(err)
        print(f"FAIL release tag: {tag}: the E2E Nightly's result could not be read ({detail.strip()}); "
              f"fix gh access {override}.", file=sys.stderr)
        return 1
    if run is None:
        print(f"FAIL release tag: {tag}: no E2E Nightly on main has finished; dispatch one {override}.",
              file=sys.stderr)
        return 1
    if not run.failed:
        print(f"release tag: the most recent E2E Nightly on main passed ({run.url}).")
        return 0
    try:
        tests = e2e_nightly.run_failures(run)
    except (OSError, subprocess.CalledProcessError):
        tests = []
    named = ", ".join(tests) if tests else "no test named in its log"
    print(f"FAIL release tag: {tag}: the most recent E2E Nightly on main {run.conclusion} ({run.url}, "
          f"{run.created}): {named}. Fix it and dispatch the nightly again, {override}.", file=sys.stderr)
    return 1


def check(tag: str) -> int:
    if (code := check_candidates(tag)) != 0:
        return code
    if parse(tag)[3] is not None:  # type: ignore[index]
        return 0
    return check_nightly(tag)


def check_candidates(tag: str) -> int:
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
