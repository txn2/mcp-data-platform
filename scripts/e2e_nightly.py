#!/usr/bin/env python3
"""The E2E Nightly's verdict, where a release and a red night need it (#2036).

The nightly (.github/workflows/e2e-nightly.yml) failed every night for three
weeks while releases were tagged over it: nothing read its result before a
release, and a red night notified no one. This module is both halves.

  report <conclusion> <test-log>   run by the nightly on main after its tests:
                  a failed night opens the one tracking issue, or comments on it
                  when it is already open, naming the failing tests and the run;
                  a passing night comments on that issue and closes it.

scripts/release-tag.py imports latest_run and run_failures to refuse a final
tag while the most recent nightly on main failed.

Every GitHub read and write goes through the gh CLI, which reads the repository
from the checkout's origin and the token from GH_TOKEN.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

WORKFLOW = "e2e-nightly.yml"
TRACKING_TITLE = "E2E Nightly is failing"
# A run that was cancelled or skipped says nothing about the suite; the verdict
# is the most recent run that finished one way or the other.
DECISIVE = {"success", "failure", "timed_out"}
FAIL_RE = re.compile(r"--- FAIL: (\S+)")


def gh(*args: str) -> str:
    return subprocess.run(["gh", *args], capture_output=True, text=True, check=True).stdout


def failing_tests(text: str) -> list[str]:
    """The tests a go test log reports failed, in the order it reports them."""
    seen: dict[str, None] = {}
    for name in FAIL_RE.findall(text):
        seen.setdefault(name, None)
    return list(seen)


@dataclass
class Run:
    id: int
    conclusion: str
    url: str
    created: str

    @property
    def failed(self) -> bool:
        return self.conclusion != "success"


def latest_run() -> Run | None:
    """The most recent nightly on main that passed or failed, or None."""
    out = gh("run", "list", "--workflow", WORKFLOW, "--branch", "main", "--status", "completed",
             "--limit", "20", "--json", "databaseId,conclusion,url,createdAt")
    for r in json.loads(out or "[]"):
        if r.get("conclusion") in DECISIVE:
            return Run(id=int(r["databaseId"]), conclusion=r["conclusion"], url=r.get("url", ""),
                       created=r.get("createdAt", ""))
    return None


def run_failures(run: Run) -> list[str]:
    """The tests a failed run reports, read from its failed steps' log."""
    return failing_tests(gh("run", "view", str(run.id), "--log-failed"))


def tracking_issue() -> int | None:
    out = gh("issue", "list", "--state", "open", "--search", f'in:title "{TRACKING_TITLE}"',
             "--json", "number,title", "--limit", "20")
    for issue in json.loads(out or "[]"):
        if issue.get("title") == TRACKING_TITLE:
            return int(issue["number"])
    return None


def run_url() -> str:
    server = os.environ.get("GITHUB_SERVER_URL", "https://github.com")
    repo = os.environ.get("GITHUB_REPOSITORY", "")
    run_id = os.environ.get("GITHUB_RUN_ID", "")
    return f"{server}/{repo}/actions/runs/{run_id}" if repo and run_id else "(run URL unavailable)"


def failure_body(url: str, tests: list[str]) -> str:
    lines = [f"The E2E Nightly failed: {url}", ""]
    if tests:
        lines.append("Failing tests:")
        lines.extend(f"- `{t}`" for t in tests)
    else:
        lines.append("No test reported a failure; a step before or after the tests failed. The run's log names it.")
    lines += ["", "A final release tag is refused while this is red (`make release-tag-check`); "
              "`release-without-nightly: <reason>` in the tag annotation releases over it."]
    return "\n".join(lines)


def report(conclusion: str, log: Path) -> int:
    issue = tracking_issue()
    url = run_url()
    if conclusion == "success":
        if issue is not None:
            gh("issue", "comment", str(issue), "--body", f"The E2E Nightly passed: {url}")
            gh("issue", "close", str(issue))
            print(f"e2e nightly: passed; closed #{issue}.")
        else:
            print("e2e nightly: passed.")
        return 0
    text = log.read_text(errors="replace") if log.is_file() else ""
    body = failure_body(url, failing_tests(text))
    if issue is not None:
        gh("issue", "comment", str(issue), "--body", body)
        print(f"e2e nightly: failed; commented on #{issue}.")
    else:
        gh("issue", "create", "--title", TRACKING_TITLE, "--body", body)
        print("e2e nightly: failed; opened the tracking issue.")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    p = sub.add_parser("report")
    p.add_argument("conclusion", help="the test step's outcome: success or failure")
    p.add_argument("log", type=Path, help="the go test output the step wrote")
    args = parser.parse_args()
    return report(args.conclusion, args.log)


if __name__ == "__main__":
    sys.exit(main())
