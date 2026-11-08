#!/usr/bin/env python3
"""Say whether a pull request changes how the dev stack starts or how the
acceptance suite is launched (#2274).

The acceptance suite runs on Linux only in the E2E Nightly, so a pull request
that changed the stack's start or the suite's launch was merged without that
path running on the runner's OS: #2264's TLS key mode and #2266's pipefail
under dash passed every local run and failed every nightly group. CI runs one
acceptance group on a pull request this answers "true" for.

Reads the changed paths, one per line, on stdin. Prints "true" when one of
them is the stack or the suite's harness, "false" otherwise. A criterion of
one ticket (test/acceptance/issue_<n>_test.go) is not the harness.

With --tickets it prints instead the tickets whose criteria file the pull
request changed and still holds, comma-separated (empty for none), and CI
runs those criteria on a fresh stack (#2341). They were otherwise run only on
the author's machine before merge, against whatever its .env set: #2179's
criteria passed there with a field-encryption key the runners never had, and
failed the first nightly after the merge. The list also takes the acceptance
files that call, by name, a tool whose registering package the paths change
(#2543): those files are often not in the diff, and the first run to see them
used to be the E2E Nightly after the merge. With --ticket-groups it prints the
same tickets as a JSON array of comma-separated groups, one job per group, so a
change to a widely called tool fits the jobs' time limit.
"""

from __future__ import annotations

import json
import re
import subprocess
import sys
from pathlib import Path

# The paths that decide how the stack starts and the suite is launched.
STACK_PATHS = (
    re.compile(r"^dev/"),
    re.compile(r"^Makefile$"),
    re.compile(r"^scripts/acceptance[^/]*$"),
    re.compile(r"^scripts/release-acceptance\.sh$"),
    re.compile(r"^test/acceptance/(?!issue_\d+_test\.go$)[^/]+$"),
    re.compile(r"^\.github/workflows/(e2e-nightly|acceptance-run)\.yml$"),
)


# A ticket's criteria file.
CRITERIA = re.compile(r"^test/acceptance/issue_(\d+)_test\.go$")
ROOT = Path(__file__).resolve().parent.parent


def changed_tickets(paths: list[str], root: Path = ROOT) -> list[int]:
    """The tickets whose criteria file is among paths and still exists."""
    found = {int(m.group(1)) for path in paths if (m := CRITERIA.match(path)) and (root / path).is_file()}
    return sorted(found)


def tickets_calling_changed_tools(paths: list[str], root: Path = ROOT) -> list[int]:
    """The tickets whose acceptance files call a tool paths change, read from
    the evidence script, which derives the map from the tools the server
    registers."""
    out = subprocess.run(
        [sys.executable, str(root / "scripts" / "acceptance-evidence.py"), "affected"],
        input="\n".join(paths) + "\n", capture_output=True, text=True, check=True,
    ).stdout.strip()
    return [int(n) for n in out.split(",") if n]


# The tickets one job runs, and the most jobs a pull request starts.
TICKETS_PER_GROUP = 25
MAX_GROUPS = 8


def ticket_groups(tickets: list[int]) -> list[str]:
    """Tickets dealt round robin into groups, as comma-separated strings."""
    if not tickets:
        return []
    count = min(MAX_GROUPS, (len(tickets) + TICKETS_PER_GROUP - 1) // TICKETS_PER_GROUP)
    return [",".join(str(n) for n in tickets[i::count]) for i in range(count)]


def touches_stack(paths: list[str]) -> bool:
    """Whether any of paths is one STACK_PATHS names."""
    return any(p.search(path) for path in paths for p in STACK_PATHS)


def main() -> int:
    paths = [line.strip() for line in sys.stdin if line.strip()]
    if sys.argv[1:] in (["--tickets"], ["--ticket-groups"]):
        tickets = sorted(set(changed_tickets(paths)) | set(tickets_calling_changed_tools(paths)))
        if sys.argv[1] == "--tickets":
            print(",".join(str(n) for n in tickets))
        else:
            print(json.dumps(ticket_groups(tickets)))
    else:
        print("true" if touches_stack(paths) else "false")
    return 0


if __name__ == "__main__":
    sys.exit(main())
