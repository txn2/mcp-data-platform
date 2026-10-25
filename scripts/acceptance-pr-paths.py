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
one ticket (test/acceptance/issue_<n>_test.go) is not the harness: it runs in
the nightly like any other.
"""

from __future__ import annotations

import re
import sys

# The paths that decide how the stack starts and the suite is launched.
STACK_PATHS = (
    re.compile(r"^dev/"),
    re.compile(r"^Makefile$"),
    re.compile(r"^scripts/acceptance[^/]*$"),
    re.compile(r"^scripts/release-acceptance\.sh$"),
    re.compile(r"^test/acceptance/(?!issue_\d+_test\.go$)[^/]+$"),
    re.compile(r"^\.github/workflows/(e2e-nightly|acceptance-run)\.yml$"),
)


def touches_stack(paths: list[str]) -> bool:
    """Whether any of paths is one STACK_PATHS names."""
    return any(p.search(path) for path in paths for p in STACK_PATHS)


def main() -> int:
    paths = [line.strip() for line in sys.stdin if line.strip()]
    print("true" if touches_stack(paths) else "false")
    return 0


if __name__ == "__main__":
    sys.exit(main())
