#!/usr/bin/env python3
"""Run one step of make verify and print how long it took (#2369).

    verify-step.py <make> <target>

runs `<make> --no-print-directory <target>`, then prints one line,

    [step <target>: 12.3s ok]

or `failed (<code>)` in place of ok, and exits with the step's code. make
verify runs every step through this, so a run reports each step's cost and the
order in scripts/verify-steps.tsv can be checked against real runs.
"""

from __future__ import annotations

import subprocess
import sys
import time


def main(argv: list[str]) -> int:
    if len(argv) != 3:
        print(f"usage: {argv[0]} <make> <target>", file=sys.stderr)
        return 2
    make, target = argv[1], argv[2]
    start = time.monotonic()
    code = subprocess.call([make, "--no-print-directory", target])
    verdict = "ok" if code == 0 else f"failed ({code})"
    print(f"[step {target}: {time.monotonic() - start:.1f}s {verdict}]", flush=True)
    return code


if __name__ == "__main__":
    sys.exit(main(sys.argv))
