#!/usr/bin/env python3
"""Acceptance evidence: the record that a ticket's criteria actually ran.

`make acceptance` runs the suite as `go test -json` and hands the stream to
`split`, which files each ticket's events at build/<n>/acceptance.jsonl. The
gates then read results rather than prose:

  split    -- file a go test -json stream per issue (reads stdin)
  check    -- per-commit gate: every criterion in a changed acceptance file
              has a terminal pass in that ticket's stream, and every tool the
              diff registers is named by one of those files
  release  -- pre-tag gate: every acceptance file changed since the last tag
              has a stream in which every criterion passed

Before this existed the gate read build/<n>/acceptance.md and checked only
that it carried a "Wire forms:" line. The transcript for #1663 carried a
section saying one of its criteria had not been executed, and the gate passed;
1.131.0 then shipped graphql_export unreachable on every deployment
(#1675/#1680). A transcript is prose. A test either produced a pass event or
it did not.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
BUILD = REPO_ROOT / "build"

# test/acceptance/issue_<n>_test.go, optionally with a suffix
# (issue_1606_script_test.go), which is how one ticket splits its criteria
# across files.
ACCEPTANCE_FILE = re.compile(r"^test/acceptance/issue_(\d+)(?:_[a-z0-9]+)*_test\.go$")
TEST_FUNC = re.compile(r"^func (TestIssue(\d+)_\w+)\s*\(", re.MULTILINE)


def issue_of(path: str) -> str | None:
    m = ACCEPTANCE_FILE.match(path)
    return m.group(1) if m else None


def stream_path(issue: str) -> Path:
    return BUILD / issue / "acceptance.jsonl"


def transcript_path(issue: str) -> Path:
    return BUILD / issue / "acceptance.md"


def read_events(path: Path) -> list[dict]:
    events = []
    with path.open(encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            try:
                events.append(json.loads(line))
            except json.JSONDecodeError:
                # `go test -json` interleaves build errors as raw text.
                continue
    return events


def outcomes(events: list[dict]) -> dict[str, str]:
    """The terminal action recorded for each test name in a stream."""
    final: dict[str, str] = {}
    for ev in events:
        name = ev.get("Test")
        action = ev.get("Action")
        if name and action in ("pass", "fail", "skip"):
            final[name] = action
    return final


def criteria_in(path: Path, issue: str) -> list[str]:
    """The TestIssue<issue>_ functions a file declares, in source order.

    Scoped to the file's own ticket because that is how `split` files the
    events: a TestIssue9999_ function living in issue_1234_test.go is recorded
    under #9999, and looking for it under #1234 would fail a run that happened.
    """
    return [m.group(1) for m in TEST_FUNC.finditer(path.read_text(encoding="utf-8"))
            if m.group(2) == issue]


# ---------------------------------------------------------------- split


def cmd_split(_args: argparse.Namespace) -> int:
    """Read a go test -json stream on stdin, echo the human-readable output,
    and file each issue's events under build/<n>/acceptance.jsonl.

    Only the issues this run produced events for are rewritten, so running one
    ticket's criteria does not erase another's evidence.
    """
    per_issue: dict[str, list[str]] = {}
    raw: list[str] = []
    for line in sys.stdin:
        raw.append(line)
        try:
            ev = json.loads(line)
        except json.JSONDecodeError:
            sys.stdout.write(line)
            continue
        if ev.get("Action") == "output":
            sys.stdout.write(ev.get("Output", ""))
        name = ev.get("Test") or ""
        m = re.match(r"^TestIssue(\d+)_", name)
        if m:
            per_issue.setdefault(m.group(1), []).append(line)
    sys.stdout.flush()

    BUILD.mkdir(parents=True, exist_ok=True)
    (BUILD / "acceptance.jsonl").write_text("".join(raw), encoding="utf-8")
    for issue, lines in per_issue.items():
        path = stream_path(issue)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("".join(lines), encoding="utf-8")
        print(f"acceptance: recorded {len(lines)} events for #{issue} at {path.relative_to(REPO_ROOT)}")
    if not per_issue:
        print("acceptance: the run produced no TestIssue<n>_ events to record.")
    return 0


# ---------------------------------------------------------------- shared verdict


def verdict(issue: str, files: list[str]) -> list[str]:
    """Problems with one ticket's evidence, empty when it is complete."""
    problems: list[str] = []
    declared: list[str] = []
    for f in files:
        declared.extend(criteria_in(REPO_ROOT / f, issue))
    if not declared:
        return [f"#{issue}: {', '.join(files)} declares no func TestIssue{issue}_... criterion"]

    path = stream_path(issue)
    if not path.is_file() or path.stat().st_size == 0:
        return [
            f"#{issue}: no run stream at {path.relative_to(REPO_ROOT)} "
            f"({len(declared)} criteria declared). Run `make acceptance ISSUE={issue}` "
            "with the stack up."
        ]
    final = outcomes(read_events(path))
    for name in declared:
        got = final.get(name)
        if got != "pass":
            problems.append(f"#{issue}: {name} {got or 'never ran'}")
    return problems


# ---------------------------------------------------------------- check


# A tool name reaching the server through a constant the toolkit declares
# (ToolExport = "graphql_export", SaveToolName = "save_asset", toolList =
# "s3_list") or written into the mcp.Tool literal directly.
TOOL_CONST = re.compile(r'^\+\s*[A-Za-z_]*[Tt]ool[A-Za-z_]*\s*=\s*"([a-z][a-z0-9_]*)"')
TOOL_NAME_FIELD = re.compile(r'^\+\s*Name:\s*"([a-z][a-z0-9_]*)"')


def registers_tools(path: str) -> bool:
    """Whether the package holding path registers MCP tools at all, which is
    what separates a toolkit's own name constant from the copies other
    packages keep to refer to a tool they do not own."""
    directory = REPO_ROOT / Path(path).parent
    if not directory.is_dir():
        return False
    for go in directory.glob("*.go"):
        if go.name.endswith("_test.go"):
            continue
        if "AddTool(" in go.read_text(encoding="utf-8", errors="replace"):
            return True
    return False


def tools_added(merge_base: str) -> dict[str, str]:
    """Tool names this diff newly registers, mapped to the file adding them."""
    diff = subprocess.run(
        ["git", "diff", "-U0", merge_base, "--", "pkg", "internal"],
        cwd=REPO_ROOT, capture_output=True, text=True, check=True,
    ).stdout
    added: dict[str, str] = {}
    current = ""
    for line in diff.splitlines():
        if line.startswith("+++ b/"):
            current = line[len("+++ b/"):]
            continue
        if current.endswith("_test.go") or not current.endswith(".go"):
            continue
        m = TOOL_CONST.match(line) or TOOL_NAME_FIELD.match(line)
        if not m:
            continue
        name = m.group(1)
        # A tool name carries its surface in it. The one-word exceptions are
        # named rather than guessed at, so "tool" or "name" from an unrelated
        # constant is not read as a tool this ticket registered.
        if "_" not in name and name not in ("search", "fetch"):
            continue
        if registers_tools(current):
            added.setdefault(name, current)
    return added


def cmd_check(args: argparse.Namespace) -> int:
    by_issue: dict[str, list[str]] = {}
    for path in args.files:
        issue = issue_of(path)
        if issue:
            by_issue.setdefault(issue, []).append(path)
    if not by_issue:
        return 0

    problems: list[str] = []
    for issue, files in sorted(by_issue.items()):
        problems.extend(verdict(issue, files))
        transcript = transcript_path(issue)
        if not transcript.is_file() or transcript.stat().st_size == 0:
            problems.append(f"#{issue}: no run transcript at {transcript.relative_to(REPO_ROOT)}")
        elif not any(line.startswith("Wire forms:")
                     for line in transcript.read_text(encoding="utf-8").splitlines()):
            problems.append(
                f"#{issue}: {transcript.relative_to(REPO_ROOT)} carries no "
                '"Wire forms:" line naming the JSON forms each touched parameter was sent as'
            )

    if args.merge_base:
        named = ""
        for files in by_issue.values():
            for f in files:
                named += (REPO_ROOT / f).read_text(encoding="utf-8")
        for tool, where in sorted(tools_added(args.merge_base).items()):
            # Word-bounded, so graphql_query in the file does not stand in for
            # a criterion on graphql_query_v2.
            if not re.search(rf"\b{re.escape(tool)}\b", named):
                problems.append(
                    f"{where} registers {tool} and no changed acceptance file calls it"
                )

    if problems:
        print("FAIL acceptance-check: a criterion that did not run is a blocker, not a transcript paragraph:",
              file=sys.stderr)
        for p in problems:
            print(f"  {p}", file=sys.stderr)
        print("  Bring the ticket's upstream up and run `make acceptance` (ISSUE=<n> for one ticket).",
              file=sys.stderr)
        return 1

    total = sum(len(f) for f in by_issue.values())
    print(f"acceptance-check: {total} acceptance file(s) changed; every criterion in "
          f"{', '.join('#' + i for i in sorted(by_issue))} passed in its recorded run.")
    return 0


# ---------------------------------------------------------------- release


def cmd_release(_args: argparse.Namespace) -> int:
    """Refuse a release whose tickets have no passing acceptance run on record.

    The set is every test/acceptance/issue_<n>_test.go changed since the last
    tag, not the `Closes #n` references in the log. Both name the same tickets
    when the convention holds, and the file list holds whether or not it does:
    this repository squashes with `(#ticket) (#pr)` in the subject and no
    closing keyword in the body, so a gate reading keywords would have found
    nothing to check on any release to date. The closing references that are
    present are reported, because a ticket closed with an acceptance file that
    never changed is worth seeing.
    """
    tag = subprocess.run(["git", "describe", "--tags", "--abbrev=0"],
                         cwd=REPO_ROOT, capture_output=True, text=True)
    if tag.returncode != 0:
        print("FAIL release acceptance gate: no tag to measure from "
              "(git describe --tags --abbrev=0 found none).", file=sys.stderr)
        return 1
    since = tag.stdout.strip()

    changed = subprocess.run(["git", "diff", "--name-only", f"{since}..HEAD"],
                             cwd=REPO_ROOT, capture_output=True, text=True, check=True).stdout.split()
    acceptance: dict[str, list[str]] = {}
    for path in changed:
        issue = issue_of(path)
        if issue:
            acceptance.setdefault(issue, []).append(path)

    if not acceptance:
        print(f"release acceptance gate: no acceptance file changed since {since}; nothing to check.")
        return 0

    problems: list[str] = []
    for issue, files in sorted(acceptance.items()):
        problems.extend(verdict(issue, files))

    if problems:
        print(f"FAIL release acceptance gate: acceptance files changed since {since} whose "
              "criteria have no passing run on record:", file=sys.stderr)
        for p in problems:
            print(f"  {p}", file=sys.stderr)
        print("  Bring each ticket's upstream up and run `make acceptance ISSUE=<n>`.", file=sys.stderr)
        return 1
    print(f"release acceptance gate: every criterion of "
          f"{', '.join('#' + i for i in sorted(acceptance))} passed in its recorded run since {since}.")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)

    sub.add_parser("split", help="file a go test -json stream (stdin) per issue")

    check = sub.add_parser("check", help="per-commit gate over the changed acceptance files")
    check.add_argument("--merge-base", default="", help="base commit the diff is read against")
    check.add_argument("files", nargs="*", help="changed test/acceptance/*_test.go paths")

    sub.add_parser("release", help="pre-tag gate over every ticket closed since the last tag")

    args = parser.parse_args()
    return {"split": cmd_split, "check": cmd_check, "release": cmd_release}[args.command](args)


if __name__ == "__main__":
    sys.exit(main())
