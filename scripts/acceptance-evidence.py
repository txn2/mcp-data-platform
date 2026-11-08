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
  transcript -- write build/<n>/acceptance.md from a ticket's acceptance files
              and its stream: their `// Wire forms:` comments, then each
              criterion's result (#2541)
  affected -- the acceptance files that call a tool whose registering package
              a diff changes (#2543), read from the changed paths on stdin

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


def elapsed(events: list[dict]) -> dict[str, float]:
    """The seconds each test name's terminal event reports."""
    return {ev["Test"]: float(ev.get("Elapsed") or 0)
            for ev in events
            if ev.get("Test") and ev.get("Action") in ("pass", "fail", "skip")}


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
            sys.stdout.flush()
            continue
        if ev.get("Action") == "output":
            # Flushed as it arrives: a run written to a file or a CI log is
            # read while it runs, and a block-buffered one lags by minutes.
            sys.stdout.write(ev.get("Output", ""))
            sys.stdout.flush()
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


# ---------------------------------------------------------------- transcript


WIRE_FORMS = re.compile(r"^\s*// Wire forms:\s*(.*)$")


def ticket_files(issue: str) -> list[str]:
    """Every acceptance file of a ticket, a suffixed one included, in order."""
    directory = REPO_ROOT / "test" / "acceptance"
    found = []
    for path in sorted(directory.glob(f"issue_{issue}*_test.go")):
        rel = path.relative_to(REPO_ROOT).as_posix()
        if issue_of(rel) == issue:
            found.append(rel)
    return found


def wire_forms_in(path: Path) -> str | None:
    """The text of a file's `// Wire forms:` comment, its continuation lines
    joined, or None when the file has no such comment."""
    lines = path.read_text(encoding="utf-8").splitlines()
    for i, line in enumerate(lines):
        m = WIRE_FORMS.match(line)
        if not m:
            continue
        parts = [m.group(1).strip()]
        for following in lines[i + 1:]:
            text = following.strip()
            if not text.startswith("//") or text == "//":
                break
            parts.append(text[2:].strip())
        return " ".join(p for p in parts if p)
    return None


def cmd_transcript(args: argparse.Namespace) -> int:
    """Write build/<n>/acceptance.md for one ticket's recorded run.

    The transcript used to be written by hand, so a recorded, passing run
    still failed verify when nobody had written it (#2541). It says what the
    acceptance files state and what the stream holds, and nothing else: a
    criterion with no terminal event reads absent.
    """
    issue = args.issue
    files = ticket_files(issue)
    if not files:
        print(f"FAIL acceptance transcript: #{issue} has no test/acceptance/issue_{issue}_test.go", file=sys.stderr)
        return 1
    forms: list[str] = []
    missing: list[str] = []
    for f in files:
        text = wire_forms_in(REPO_ROOT / f)
        if text:
            forms.append(text)
        else:
            missing.append(f)
    if missing:
        print("FAIL acceptance transcript: no `// Wire forms:` comment naming the JSON forms sent in: "
              + ", ".join(missing), file=sys.stderr)
        return 1

    path = stream_path(issue)
    events = read_events(path) if path.is_file() else []
    final = outcomes(events)
    seconds = elapsed(events)
    rows = [f"| {name} | {final.get(name, 'absent')} | {seconds.get(name, 0):.2f} |"
            for f in files for name in criteria_in(REPO_ROOT / f, issue)]

    transcript = transcript_path(issue)
    transcript.parent.mkdir(parents=True, exist_ok=True)
    transcript.write_text("Wire forms: " + " ".join(forms) + "\n\n"
                          f"# Acceptance #{issue}\n\n"
                          f"Recorded against the dev stack; the run is {path.relative_to(REPO_ROOT)}.\n\n"
                          "| Criterion | Result | Seconds |\n|---|---|---|\n"
                          + "\n".join(rows) + "\n", encoding="utf-8")
    print(f"acceptance: wrote {transcript.relative_to(REPO_ROOT)} ({len(rows)} criteria)")
    return 0


# ---------------------------------------------------------------- affected

# A production change that alters what a tool does breaks the acceptance files
# calling that tool, which are often not in the diff (#2543): on #2542 six
# unchanged files failed the nightly after the merge.

TOOL_TABLE = Path("test") / "toolsurface" / "toolsurface.go"
ENVIRONMENTS = Path("test") / "acceptance" / "environments.txt"

# The composition root holds the whole platform and registers a few tools of
# its own, and every session calls one of them, so a change anywhere in it is
# not a change to those tools: only the file registering a tool is.
COMPOSITION_ROOTS = ("pkg/platform",)

ADD_TOOL = re.compile(r"\bAddTool\(")
STRING_CONST = re.compile(r'^[ \t]*(?:const[ \t]+)?([A-Za-z_]\w*)(?:[ \t]+string)?[ \t]*=[ \t]*"([a-z][a-z0-9_]*)"', re.MULTILINE)
LITERAL = re.compile(r'"([a-z][a-z0-9_]*)"')
IDENT = re.compile(r"\b[A-Za-z_]\w*\b")
CALL = re.compile(r"\b([A-Za-z_]\w*)\(")
NAME_FIELD = re.compile(r'\bName:\s*(?:"([a-z][a-z0-9_]*)"|([A-Za-z_]\w*))')
BUILT_BY = re.compile(r"(?:\w+\.)?(\w+)\(\s*(\w*)")
FUNC_DECL = re.compile(r"^func (?:\([^)]*\) )?([A-Za-z_]\w*)\(", re.MULTILINE)


def balanced(text: str, start: int, opening: str, closing: str) -> str:
    """The text from the bracket at start through its match, with strings,
    runes and comments skipped: an apostrophe in a comment opens nothing."""
    depth = 0
    i = start
    n = len(text)
    while i < n:
        c = text[i]
        if text.startswith("//", i):
            end = text.find("\n", i)
            i = n if end < 0 else end
        elif text.startswith("/*", i):
            end = text.find("*/", i)
            i = n if end < 0 else end + 1
        elif c == "`":
            end = text.find("`", i + 1)
            i = n if end < 0 else end
        elif c in "\"'":
            i += 1
            while i < n and text[i] != c and text[i] != "\n":
                i += 2 if text[i] == "\\" else 1
        elif c == opening:
            depth += 1
        elif c == closing:
            depth -= 1
            if depth == 0:
                return text[start:i + 1]
        i += 1
    return text[start:]


def native_tools() -> set[str]:
    """Every tool the platform registers: the keys of toolsurface.Annotations,
    which TestRealDB_AdvertisedSurfaceMatchesTheTables holds to the running
    server's tools/list."""
    text = (REPO_ROOT / TOOL_TABLE).read_text(encoding="utf-8")
    consts = dict(STRING_CONST.findall(text))
    start = text.index("Annotations = map[string]Expectation{")
    block = balanced(text, text.index("{", start), "{", "}")
    tools: set[str] = set()
    for m in re.finditer(r'^\s*(?:"([a-z][a-z0-9_]*)"|([A-Za-z_]\w*))\s*:', block, re.MULTILINE):
        name = m.group(1) or consts.get(m.group(2) or "")
        if name:
            tools.add(name)
    return tools


def package_functions(text: str) -> dict[str, str]:
    """Each top-level function of a Go source's name, mapped to its body."""
    bodies: dict[str, str] = {}
    for m in FUNC_DECL.finditer(text):
        brace = text.find("{", m.end())
        if brace >= 0:
            bodies[m.group(1)] = balanced(text, brace, "{", "}")
    return bodies


def tools_named(span: str, tools: set[str], consts: dict[str, str], funcs: dict[str, str], seen: set[str]) -> set[str]:
    """The tools a span of a registration names: a literal that is exactly a
    tool's name (a description is longer), a constant holding one, or either
    of those inside a function the span calls."""
    found = {m.group(1) for m in LITERAL.finditer(span) if m.group(1) in tools}
    found |= {consts[m.group(0)] for m in IDENT.finditer(span) if consts.get(m.group(0)) in tools}
    for m in CALL.finditer(span):
        name = m.group(1)
        if name in funcs and name not in seen:
            seen.add(name)
            found |= tools_named(funcs[name], tools, consts, funcs, seen)
    return found


def registered_in(span: str, tools: set[str], consts: dict[str, str], funcs: dict[str, str]) -> set[str]:
    """The tools one AddTool call registers: its Tool's Name field, or when a
    helper builds the Tool, that helper's Name field or the constant it is
    handed first. A tool name elsewhere in the call is an enum or a mention."""
    def named(text: str) -> set[str]:
        found = set()
        for m in NAME_FIELD.finditer(text):
            value = m.group(1) or consts.get(m.group(2) or "")
            if value in tools:
                found.add(value)
        return found

    found = named(span)
    if found:
        return found
    _, _, rest = span.partition(",")
    helper = BUILT_BY.match(rest.lstrip())
    if not helper:
        return set()
    value = consts.get(helper.group(2) or "")
    if value in tools:
        return {value}
    return named(funcs.get(helper.group(1), ""))


def production_sources() -> dict[str, list[Path]]:
    """Each package directory under pkg/ and internal/, with its non-test Go."""
    packages: dict[str, list[Path]] = {}
    for tree in ("pkg", "internal"):
        for go in sorted((REPO_ROOT / tree).rglob("*.go")):
            if not go.name.endswith("_test.go"):
                packages.setdefault(go.parent.relative_to(REPO_ROOT).as_posix(), []).append(go)
    return packages


def tool_sources() -> dict[str, set[str]]:
    """Each native tool, mapped to what must change for the tool to change:
    the package whose source registers it, or within a composition root the
    file. A tool a library registers (mcp-trino's) maps to its adapter, the
    toolkit named by its prefix."""
    tools = native_tools()
    owners: dict[str, set[str]] = {tool: set() for tool in tools}
    for package, files in production_sources().items():
        texts = {go: go.read_text(encoding="utf-8", errors="replace") for go in files}
        joined = "\n".join(texts.values())
        consts = dict(STRING_CONST.findall(joined))
        funcs = package_functions(joined)
        for go, text in texts.items():
            for m in ADD_TOOL.finditer(text):
                span = balanced(text, m.end() - 1, "(", ")")
                for tool in registered_in(span, tools, consts, funcs):
                    owners[tool].add(go.relative_to(REPO_ROOT).as_posix() if package in COMPOSITION_ROOTS else package)
    for tool, where in owners.items():
        adapter = Path("pkg") / "toolkits" / tool.split("_", 1)[0]
        if not where and (REPO_ROOT / adapter).is_dir():
            where.add(adapter.as_posix())
    return owners


def environment_criteria() -> list[re.Pattern[str]]:
    """The criteria that run in an environment of their own, which a run on
    the plain stack leaves out."""
    path = REPO_ROOT / ENVIRONMENTS
    if not path.is_file():
        return []
    patterns = []
    for line in path.read_text(encoding="utf-8").splitlines():
        tokens = line.split()
        if len(tokens) >= 2 and not line.lstrip().startswith("#"):
            patterns.append(re.compile(tokens[1]))
    return patterns


# The harness's MCP client, whose methods any acceptance file may call. A
# method of another type is scanned with the file declaring it, and a call
# elsewhere with the same name is some other type's method.
HARNESS_RECEIVER = "client"
METHOD_DECL = re.compile(r"^func \(\w+ \*?(\w+)\) (\w+)\(", re.MULTILINE)


def foreign_methods(text: str) -> set[str]:
    """The names of the methods declared on a type other than the harness's."""
    own = {name for receiver, name in METHOD_DECL.findall(text) if receiver == HARNESS_RECEIVER}
    return {name for receiver, name in METHOD_DECL.findall(text) if receiver != HARNESS_RECEIVER} - own


def tools_called() -> dict[str, set[str]]:
    """Each issue_<n> acceptance file, mapped to the tools it calls by name,
    directly or through a helper function any acceptance file declares."""
    tools = native_tools()
    directory = REPO_ROOT / "test" / "acceptance"
    sources = {p.relative_to(REPO_ROOT).as_posix(): p.read_text(encoding="utf-8") for p in sorted(directory.glob("*_test.go"))}
    joined = "\n".join(sources.values())
    consts = dict(STRING_CONST.findall(joined))
    # Opening a client calls platform_info and search for every session; that
    # is the harness connecting, not the criterion calling those tools.
    foreign = foreign_methods(joined)
    funcs = {name: body for name, body in package_functions(joined).items()
             if not name.startswith("connect") and name not in foreign}
    return {path: tools_named(text, tools, consts, funcs, set()) for path, text in sources.items() if issue_of(path)}


def changed_source_of(where: str, changed: list[str]) -> str | None:
    """The first changed production file that where (a package or a file)
    names, or None."""
    for path in changed:
        if path.endswith("_test.go") or not path.endswith(".go"):
            continue
        if path == where or Path(path).parent.as_posix() == where:
            return path
    return None


def affected(changed: list[str]) -> dict[str, dict[str, str]]:
    """The acceptance files calling a tool the changed paths change, each
    mapped to those tools and the changed file behind each. A file whose every
    criterion runs in its own environment is left out: a run on the plain
    stack cannot execute it, and the E2E Nightly runs that environment."""
    changed_tools: dict[str, str] = {}
    for tool, sources in tool_sources().items():
        for where in sorted(sources):
            hit = changed_source_of(where, changed)
            if hit:
                changed_tools.setdefault(tool, hit)
                break
    if not changed_tools:
        return {}
    elsewhere = environment_criteria()
    out: dict[str, dict[str, str]] = {}
    for path, called in sorted(tools_called().items()):
        hits = {tool: changed_tools[tool] for tool in sorted(called) if tool in changed_tools}
        if not hits:
            continue
        criteria = criteria_in(REPO_ROOT / path, issue_of(path) or "")
        if criteria and all(any(p.search(c) for p in elsewhere) for c in criteria):
            continue
        out[path] = hits
    return out


def cmd_affected(_args: argparse.Namespace) -> int:
    """Print the tickets of the acceptance files the changed paths on stdin
    reach, comma-separated, as acceptance-pr-paths.py --tickets does."""
    changed = [line.strip() for line in sys.stdin if line.strip()]
    print(",".join(str(n) for n in sorted({int(issue_of(p) or 0) for p in affected(changed)})))
    return 0


def stale_recordings(changed: list[str]) -> list[str]:
    """Each acceptance file calling a changed tool whose recorded run did not
    pass, or was recorded before the change."""
    problems: list[str] = []
    if not changed:
        return problems
    for path, hits in affected(changed).items():
        issue = issue_of(path) or ""
        named = ", ".join(sorted(hits))
        found = verdict(issue, [path])
        problems.extend(f"{p} (it calls {named}, which this diff changes)" for p in found)
        if found:
            continue
        recorded = stream_path(issue).stat().st_mtime
        for tool, source in sorted(hits.items()):
            if (REPO_ROOT / source).stat().st_mtime > recorded:
                problems.append(f"#{issue}: {stream_path(issue).relative_to(REPO_ROOT)} was recorded "
                                f"before {source} changed, and {path} calls {tool}")
                break
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


def changed_paths(args: argparse.Namespace) -> list[str]:
    """The paths --changed-stdin hands the check, one per line."""
    return [line.strip() for line in sys.stdin if line.strip()] if args.changed_stdin else []


def cmd_check(args: argparse.Namespace) -> int:
    by_issue: dict[str, list[str]] = {}
    for path in args.files:
        issue = issue_of(path)
        if issue:
            by_issue.setdefault(issue, []).append(path)
    stale = stale_recordings(changed_paths(args))
    if not by_issue and not stale:
        return 0

    problems: list[str] = list(stale)
    for issue, files in sorted(by_issue.items()):
        problems.extend(p for p in verdict(issue, files) if not any(p in s for s in stale))
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
    # The last RELEASE, not the last tag: a release candidate (vX.Y.Z-rcN) is
    # a build being judged, and measuring from one would check only what
    # changed since it (#2012).
    tag = subprocess.run(["git", "describe", "--tags", "--abbrev=0", "--exclude", "*-rc*"],
                         cwd=REPO_ROOT, capture_output=True, text=True)
    if tag.returncode != 0:
        print("FAIL release acceptance gate: no release tag to measure from "
              "(git describe --tags --abbrev=0 --exclude '*-rc*' found none).", file=sys.stderr)
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
    check.add_argument("--changed-stdin", action="store_true", help="read the diff's changed paths from stdin")
    check.add_argument("files", nargs="*", help="changed test/acceptance/*_test.go paths")

    sub.add_parser("release", help="pre-tag gate over every ticket closed since the last tag")

    transcript = sub.add_parser("transcript", help="write a ticket's acceptance.md from its run")
    transcript.add_argument("issue", help="the ticket number")

    sub.add_parser("affected", help="tickets calling a tool the changed paths on stdin change")

    args = parser.parse_args()
    commands = {"split": cmd_split, "check": cmd_check, "release": cmd_release,
                "transcript": cmd_transcript, "affected": cmd_affected}
    return commands[args.command](args)


if __name__ == "__main__":
    sys.exit(main())
