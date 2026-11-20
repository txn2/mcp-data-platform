#!/usr/bin/env python3
"""Every scheduled workflow's verdict, filed where people look (#2730, #2731).

Only the E2E Nightly used to report a failure anywhere: the weekly Load run
failed every week from August to November and nobody noticed (#2729). Every
workflow with a schedule now ends in a report job that runs this, on main, on
success and on failure.

  report <conclusion> [LOG ...] [--workflow NAME] [--note TEXT]
        A failed run opens the issue "<workflow> is failing", or comments on it
        while it is open, with the run's URL, its commit, the jobs and steps
        that failed, and what failed in them: the failing tests, fuzz targets
        and benchmarks (`--- FAIL: <name>`), load assertions (`[FAIL] <name>:`),
        fuzz crashers (`Failing input written to <path>`) and surviving mutants
        (`LIVED <mutation> at <file:line:col>`), read from the LOG files given
        and from the log of every job of the run that failed. A passing run
        comments on that issue and closes it. NAME defaults to the running
        workflow's name.

Flaky criteria (#2731). A criterion that failed on a tree and then passed on
the same tree is flaky; closing the tracking issue on the green run used to
lose that, and it failed again on a later night. So:

  - a passing run looks for failed runs of the same workflow on the same tree
    (as tree_of reads it) among the tracking issues of the last 14 days, names
    the criteria that failed there as flaky on the tracking issue before
    closing it, and lists them in the "Flaky criteria" issue;
  - a failing run lists in that issue each of its criteria that also failed in
    another tracking issue of the same workflow within 14 days, with both runs.

The "Flaky criteria" issue is opened when it is needed and never closed here:
it stays open until each criterion it lists is fixed. A criterion it already
names is not added again.

Every GitHub read and write goes through the gh CLI, which reads the repository
from the checkout's origin and the token from GH_TOKEN.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import re
import subprocess
import sys
from dataclasses import dataclass, field
from pathlib import Path

TITLE_SUFFIX = " is failing"
FLAKY_TITLE = "Flaky criteria"
WINDOW_DAYS = 14
FAILED = {"failure", "timed_out"}

# What a failed run's output names as failing, one pattern per kind of output.
CRITERION_RES = [
    re.compile(r"--- FAIL: (\S+)"),  # go test, fuzz targets, benchmarks
    re.compile(r"\[FAIL\] (\S+):(?=\s|$)"),  # loadgen and alloc-gate assertions
    re.compile(r"Failing input written to (\S+)"),  # a fuzz crasher
    re.compile(r"\bLIVED (\S+ at \S+)"),  # a surviving mutant
]
ANSI_RE = re.compile(r"\x1b\[[0-9;]*m")
HEADER_RE = re.compile(r"^The (.+?) failed: (\S+)", re.M)
COMMIT_RE = re.compile(r"^Commit: `([0-9a-f]{7,40})`", re.M)
ITEM_RE = re.compile(r"^- `([^`]+)`", re.M)
RUN_ID_RE = re.compile(r"/runs/(\d+)")


def gh(*args: str) -> str:
    return subprocess.run(["gh", *args], capture_output=True, text=True, check=True).stdout


def gh_json(*args: str, default):
    """A gh read decoded as JSON, or default when gh fails or prints nothing."""
    try:
        out = gh(*args)
    except (OSError, subprocess.CalledProcessError):
        return default
    try:
        return json.loads(out) if out.strip() else default
    except json.JSONDecodeError:
        return default


def tracking_title(workflow: str) -> str:
    return workflow + TITLE_SUFFIX


def criteria(text: str) -> list[str]:
    """What a run's output names as failing, in the order it names them."""
    text = ANSI_RE.sub("", text)
    seen: dict[str, None] = {}
    for line in text.splitlines():
        for pattern in CRITERION_RES:
            for name in pattern.findall(line):
                seen.setdefault(name, None)
    return list(seen)


def tree_of(sha: str) -> str:
    """The tree a commit records: read from this clone, else from GitHub, since a
    merged pull request's branch is often deleted and absent from the clone."""
    try:
        out = subprocess.run(["git", "rev-parse", f"{sha}^{{tree}}"], capture_output=True, text=True, check=True)
        return out.stdout.strip()
    except (OSError, subprocess.CalledProcessError):
        return gh("api", f"repos/{{owner}}/{{repo}}/git/commits/{sha}", "--jq", ".tree.sha").strip()


def safe_tree(sha: str) -> str:
    try:
        return tree_of(sha)
    except (OSError, subprocess.CalledProcessError):
        return ""


def open_issue(title: str) -> int | None:
    found = gh_json("issue", "list", "--state", "open", "--search", f'in:title "{title}"',
                    "--json", "number,title", "--limit", "20", default=[])
    for issue in found:
        if issue.get("title") == title:
            return int(issue["number"])
    return None


def run_url() -> str:
    server = os.environ.get("GITHUB_SERVER_URL", "https://github.com")
    repo = os.environ.get("GITHUB_REPOSITORY", "")
    run_id = os.environ.get("GITHUB_RUN_ID", "")
    return f"{server}/{repo}/actions/runs/{run_id}" if repo and run_id else "(run URL unavailable)"


@dataclass
class FailedJob:
    name: str
    steps: list[str]
    log: str = ""


def failed_jobs(run_id: str) -> list[FailedJob]:
    """The run's jobs that failed, with their failed steps and their logs. The
    report job runs before its own run completes, so each finished job's log is
    read through the jobs API rather than gh run view --log-failed."""
    if not run_id:
        return []
    data = gh_json("run", "view", run_id, "--json", "jobs", default={})
    out = []
    for job in data.get("jobs", []) if isinstance(data, dict) else []:
        if job.get("conclusion") not in FAILED:
            continue
        steps = [s.get("name", "") for s in job.get("steps", []) if s.get("conclusion") in FAILED]
        log = ""
        if job.get("databaseId"):
            try:
                log = gh("api", f"repos/{{owner}}/{{repo}}/actions/jobs/{job['databaseId']}/logs")
            except (OSError, subprocess.CalledProcessError):
                log = ""
        out.append(FailedJob(name=job.get("name", ""), steps=steps, log=log))
    return out


def failure_body(workflow: str, url: str, sha: str, jobs: list[FailedJob], names: list[str],
                 notes: list[str]) -> str:
    lines = [f"The {workflow} failed: {url}"]
    if sha:
        lines.append(f"Commit: `{sha}`")
    lines.append("")
    if jobs:
        lines.append("Failed jobs:")
        for j in jobs:
            where = f" (step: {', '.join(j.steps)})" if j.steps else ""
            lines.append(f"- {j.name}{where}")
        lines.append("")
    if names:
        lines.append("Failing:")
        lines.extend(f"- `{n}`" for n in names)
    else:
        lines.append("Nothing the run printed names a failing test, scenario, assertion, fuzz target, benchmark "
                     "or mutant; the run's log says where it stopped.")
    for note in notes:
        lines += ["", note]
    return "\n".join(lines)


@dataclass
class Record:
    """One failed run as a tracking issue recorded it."""
    issue: int
    url: str
    sha: str
    created: str
    names: list[str] = field(default_factory=list)


def parse_records(issue: int, text: str, created: str) -> list[Record]:
    """The failed runs one issue body or comment records (one, or none)."""
    m = HEADER_RE.search(text)
    if not m:
        return []
    sha = COMMIT_RE.search(text)
    return [Record(issue=issue, url=m.group(2), sha=sha.group(1) if sha else "", created=created,
                   names=ITEM_RE.findall(text))]


def recent_records(workflow: str, now: dt.datetime) -> list[Record]:
    """Every failed run the workflow's tracking issues recorded in the window."""
    since = now - dt.timedelta(days=WINDOW_DAYS)
    title = tracking_title(workflow)
    found = gh_json("issue", "list", "--state", "all", "--search",
                    f'in:title "{title}" updated:>={since.date().isoformat()}',
                    "--json", "number,title", "--limit", "50", default=[])
    out: list[Record] = []
    for issue in found:
        if issue.get("title") != title:
            continue
        n = int(issue["number"])
        view = gh_json("issue", "view", str(n), "--json", "body,createdAt,comments", default={})
        if not isinstance(view, dict):
            continue
        entries = [(view.get("body", ""), view.get("createdAt", ""))]
        entries += [(c.get("body", ""), c.get("createdAt", "")) for c in view.get("comments", [])]
        for body, created in entries:
            if created and parse_time(created) < since:
                continue
            out.extend(parse_records(n, body or "", created))
    return out


def parse_time(stamp: str) -> dt.datetime:
    return dt.datetime.fromisoformat(stamp.replace("Z", "+00:00"))


def record_sha(r: Record) -> str:
    """The commit a record's run tested: written in the record, else read from
    the run (records written before #2731 name only the run)."""
    if r.sha:
        return r.sha
    m = RUN_ID_RE.search(r.url)
    if not m:
        return ""
    data = gh_json("run", "view", m.group(1), "--json", "headSha", default={})
    return data.get("headSha", "") if isinstance(data, dict) else ""


@dataclass
class Flaky:
    name: str
    reason: str


def flaky_on_tree(workflow: str, records: list[Record], sha: str, url: str) -> list[Flaky]:
    """The criteria that failed on the tree this passing run passed on."""
    tree = safe_tree(sha)
    if not tree:
        return []
    out: dict[str, Flaky] = {}
    for r in records:
        rsha = record_sha(r)
        if not rsha or safe_tree(rsha) != tree:
            continue
        for n in r.names:
            out.setdefault(n, Flaky(n, f"{workflow}: failed in {r.url} and passed in {url}, both on tree `{tree}`"))
    return list(out.values())


def flaky_recurring(workflow: str, records: list[Record], names: list[str], issue: int | None,
                    url: str) -> list[Flaky]:
    """This run's criteria that also failed in another tracking issue in the window."""
    out = []
    for n in names:
        other = next((r for r in records if r.issue != issue and n in r.names), None)
        if other is not None:
            out.append(Flaky(n, f"{workflow}: failed in #{other.issue} ({other.url}) and again in {url}, "
                                f"within {WINDOW_DAYS} days"))
    return out


FLAKY_INTRO = (
    "Criteria that failed and passed on one tree, or failed in two tracking issues within "
    f"{WINDOW_DAYS} days (#2731). Each is flaky: a criterion asserting background or cross-replica "
    "work once is made to poll up to a bound, and one fixed already is recorded with the commit "
    "that fixed it. Close this issue when every criterion listed is fixed."
)


def file_flaky(found: list[Flaky]) -> None:
    """Lists in the Flaky criteria issue each criterion it does not name yet."""
    if not found:
        return
    issue = open_issue(FLAKY_TITLE)
    named = ""
    if issue is not None:
        view = gh_json("issue", "view", str(issue), "--json", "body,comments", default={})
        if isinstance(view, dict):
            named = (view.get("body") or "") + "".join(c.get("body", "") for c in view.get("comments", []))
    new = [f for f in found if f"`{f.name}`" not in named]
    if not new:
        return
    items = "\n".join(f"- `{f.name}`: {f.reason}" for f in new)
    if issue is None:
        gh("issue", "create", "--title", FLAKY_TITLE, "--body", f"{FLAKY_INTRO}\n\n{items}")
    else:
        gh("issue", "comment", str(issue), "--body", items)


def report(workflow: str, conclusion: str, logs: list[Path], notes: list[str] | None = None,
           now: dt.datetime | None = None) -> int:
    notes = notes or []
    now = now or dt.datetime.now(dt.timezone.utc)
    title = tracking_title(workflow)
    issue = open_issue(title)
    url = run_url()
    sha = os.environ.get("GITHUB_SHA", "")
    records = recent_records(workflow, now)
    if conclusion == "success":
        flaky = flaky_on_tree(workflow, records, sha, url) if sha else []
        if issue is not None:
            body = f"The {workflow} passed: {url}"
            if flaky:
                body += ("\n\nIt passed on a tree a failed run also ran, so these criteria are flaky "
                         f"and are listed in \"{FLAKY_TITLE}\":\n" + "\n".join(f"- `{f.name}`" for f in flaky))
            gh("issue", "comment", str(issue), "--body", body)
            gh("issue", "close", str(issue))
            print(f"{workflow}: passed; closed #{issue}.")
        else:
            print(f"{workflow}: passed.")
        file_flaky(flaky)
        return 0
    jobs = failed_jobs(os.environ.get("GITHUB_RUN_ID", ""))
    text = "".join(p.read_text(errors="replace") for p in logs if p.is_file())
    text += "".join(j.log for j in jobs)
    names = criteria(text)
    body = failure_body(workflow, url, sha, jobs, names, notes)
    if issue is not None:
        gh("issue", "comment", str(issue), "--body", body)
        print(f"{workflow}: failed; commented on #{issue}.")
    else:
        gh("issue", "create", "--title", title, "--body", body)
        print(f"{workflow}: failed; opened \"{title}\".")
    file_flaky(flaky_recurring(workflow, records, names, issue, url))
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    p = sub.add_parser("report")
    p.add_argument("conclusion", help="the run's outcome: success or failure")
    p.add_argument("logs", nargs="*", type=Path, help="output files the run wrote, read for what failed")
    p.add_argument("--workflow", default=os.environ.get("GITHUB_WORKFLOW", ""),
                   help="the workflow's name (default: the running workflow's)")
    p.add_argument("--note", action="append", default=[], help="a paragraph added to a failure's body")
    args = parser.parse_args()
    if not args.workflow:
        parser.error("--workflow is required outside a workflow run")
    return report(args.workflow, args.conclusion, args.logs, args.note)


if __name__ == "__main__":
    sys.exit(main())
