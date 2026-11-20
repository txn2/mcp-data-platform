#!/usr/bin/env python3
"""The E2E Nightly's verdict, where a release and a red night need it (#2036).

The nightly (.github/workflows/e2e-nightly.yml) failed every night for three
weeks while releases were tagged over it: nothing read its result before a
release, and a red night notified no one. This module is both halves.

  report <conclusion> <test-log>   run by the nightly on main after its tests:
                  a failed night opens the one tracking issue, or comments on it
                  when it is already open, naming the failing tests and the run;
                  a passing night comments on that issue and closes it. Since
                  #2730 this is scripts/scheduled_report.py's report, which
                  every scheduled workflow reports through, and since #2731 it
                  names a criterion that failed and passed on one tree as flaky.

  rerun-on-fix <sha>   run on every push to main (.github/workflows/
                  e2e-nightly-on-fix.yml): when the pull request merged as <sha>
                  closes the tracking issue, dispatch the nightly on main, so
                  the fix is measured that day rather than at the next 07:00
                  UTC run, and a final tag is not refused over the red run from
                  before the fix (#2310).

scripts/release-tag.py imports latest_run, pending_runs and run_failures to
refuse a final tag while the most recent nightly on main failed, and to say
whether a run on a newer commit is already on its way; and green_run_on_tree to
accept a passing run on any branch whose commit has the tagged commit's tree
(#2487): a nightly-fix pull request runs the full nightly on its branch before
it merges, and the merge commit tests the same tree again for nothing.

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

sys.path.insert(0, str(Path(__file__).resolve().parent))
import scheduled_report  # noqa: E402
from scheduled_report import tree_of  # noqa: E402,F401  (release-tag.py reads it from here)

WORKFLOW = "e2e-nightly.yml"
WORKFLOW_NAME = "E2E Nightly"
TRACKING_TITLE = scheduled_report.tracking_title(WORKFLOW_NAME)
RELEASE_NOTE = ("A final release tag is refused while this is red (`make release-tag-check`); "
                "`release-without-nightly: <reason>` in the tag annotation releases over it.")
# A run that was cancelled or skipped says nothing about the suite; the verdict
# is the most recent run that finished one way or the other.
DECISIVE = {"success", "failure", "timed_out"}
# A run in any of these states has not finished and will report.
PENDING = {"queued", "in_progress", "waiting", "pending", "requested"}
RUN_FIELDS = "databaseId,status,conclusion,url,createdAt,headSha"
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
    head_sha: str = ""
    status: str = "completed"

    @property
    def failed(self) -> bool:
        return self.conclusion != "success"


def runs(branch: str | None = "main") -> list[Run]:
    """The nightly's most recent runs on branch (any branch for None), newest first, in any state."""
    scope = ["--branch", branch] if branch else []
    out = gh("run", "list", "--workflow", WORKFLOW, *scope, "--limit", "50" if branch is None else "30",
             "--json", RUN_FIELDS)
    return [Run(id=int(r["databaseId"]), conclusion=r.get("conclusion") or "", url=r.get("url", ""),
                created=r.get("createdAt", ""), head_sha=r.get("headSha", ""),
                status=r.get("status") or "completed")
            for r in json.loads(out or "[]")]


def latest_run() -> Run | None:
    """The most recent nightly on main that passed or failed, or None."""
    for r in runs():
        if r.status == "completed" and r.conclusion in DECISIVE:
            return r
    return None


def green_run_on_tree(tree: str) -> Run | None:
    """The newest passing nightly, on any branch, whose commit has this tree, or None."""
    for r in runs(branch=None):
        if r.status != "completed" or r.conclusion != "success" or not r.head_sha:
            continue
        try:
            if tree_of(r.head_sha) == tree:
                return r
        except (OSError, subprocess.CalledProcessError):
            continue
    return None


def pending_runs() -> list[Run]:
    """The nightly's runs on main that are queued or running, newest first."""
    return [r for r in runs() if r.status in PENDING]


def run_failures(run: Run) -> list[str]:
    """The tests a failed run reports, read from its failed steps' log."""
    return failing_tests(gh("run", "view", str(run.id), "--log-failed"))


def report(conclusion: str, log: Path) -> int:
    return scheduled_report.report(WORKFLOW_NAME, conclusion, [log], [RELEASE_NOTE])


# The pull requests a commit was merged from, and the issues each one closes.
CLOSES_QUERY = """query($owner: String!, $name: String!, $sha: String!) {
  repository(owner: $owner, name: $name) {
    object(expression: $sha) {
      ... on Commit {
        associatedPullRequests(first: 5) {
          nodes { number merged closingIssuesReferences(first: 50) { nodes { number title } } }
        }
      }
    }
  }
}"""


def closed_tracking_issue(sha: str) -> tuple[int, int] | None:
    """(pull request, issue) when the pull request merged as sha closes the tracking issue."""
    owner, _, name = os.environ.get("GITHUB_REPOSITORY", "").partition("/")
    out = gh("api", "graphql", "-f", f"query={CLOSES_QUERY}", "-f", f"owner={owner}", "-f", f"name={name}",
             "-f", f"sha={sha}")
    commit = (json.loads(out or "{}").get("data") or {}).get("repository", {}).get("object") or {}
    for pr in commit.get("associatedPullRequests", {}).get("nodes", []):
        if not pr.get("merged"):
            continue
        for issue in pr.get("closingIssuesReferences", {}).get("nodes", []):
            if issue.get("title") == TRACKING_TITLE:
                return int(pr["number"]), int(issue["number"])
    return None


def rerun_on_fix(sha: str) -> int:
    found = closed_tracking_issue(sha)
    if found is None:
        print(f"e2e nightly: {sha[:12]} closes no tracking issue; nothing to dispatch.")
        return 0
    pr, issue = found
    gh("workflow", "run", WORKFLOW, "--ref", "main")
    print(f"e2e nightly: #{pr} closed #{issue}; dispatched the nightly on main.")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    p = sub.add_parser("report")
    p.add_argument("conclusion", help="the test step's outcome: success or failure")
    p.add_argument("log", type=Path, help="the go test output the step wrote")
    f = sub.add_parser("rerun-on-fix")
    f.add_argument("sha", help="the commit pushed to main")
    args = parser.parse_args()
    if args.command == "rerun-on-fix":
        return rerun_on_fix(args.sha)
    return report(args.conclusion, args.log)


if __name__ == "__main__":
    sys.exit(main())
