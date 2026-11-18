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
  affected -- the acceptance files that call a tool a diff changes (#2543),
              read from the changed paths on stdin: within the package
              registering the tool, only a change the tool's handler reaches
              through a command the file names, or through the code every
              command runs, counts (#2693),
              and the unchanged ones that call, directly or through another
              helper, a helper whose declaration the diff changes in another
              acceptance file (#2627), read from the diff ACCEPTANCE_DIFF_RANGE
              names

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
import os
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
    for name in required_of(declared):
        got = final.get(name)
        if got != "pass":
            problems.append(f"#{issue}: {name} {got or 'never ran'}")
    for name in declared:
        if final.get(name) == "fail" and name not in required_of(declared):
            problems.append(f"#{issue}: {name} fail")
    return problems


def required_of(declared: list[str]) -> list[str]:
    """The criteria a ticket's recording must show passed (#2637). In a file
    mixing plain criteria with criteria an environment holds, a run on the
    plain stack records the plain ones, and each environment's run
    (ACCEPTANCE_ENV, the E2E Nightly) proves its own, as affected leaves out a
    file every criterion of which runs elsewhere. A file of one environment's
    criteria is that environment's run, and every one is read from it."""
    elsewhere = environment_criteria()
    plain = [n for n in declared if not any(p.search(n) for p in elsewhere)]
    return plain or declared


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


def changed_sources_of(where: str, changed: list[str]) -> list[str]:
    """The changed production files that where (a package or a file) names."""
    return [path for path in changed
            if path.endswith(".go") and not path.endswith("_test.go")
            and (path == where or Path(path).parent.as_posix() == where)]


def tool_hit(tool: str, where: str, files: list[str], lines: dict[str, set[int]],
             ops: set[str] | None) -> tuple[str, str] | None:
    """The changed file and the change in it that reaches tool's handler for a
    criterion naming ops, or None when no change does (#2693). A file changed
    in a composition root, a diff with no lines to read, or a change the reading
    cannot place is the package rule: the first changed file."""
    if where in COMPOSITION_ROOTS or where.endswith(".go") or not all(f in lines for f in files):
        return files[0], "changed"
    reach = tool_reach(where, tool)
    if not reach.full:
        return files[0], "changed, and the handler it registers was not read"
    reached = reach.reached(ops)
    by_rel = {f.rel: f for f in reach.pkg.files}
    for path in files:
        gofile = by_rel.get(path)
        if gofile is None:
            return path, "changed"
        for kind, name, what in placed(gofile, lines[path], reach):
            if kind == "unplaced":
                return path, f"changed at {what}"
            if kind == "func" and name in reached:
                return path, f"changed {what}, which {tool} reaches"
            if kind == "op" and (ops is None or name in ops or not (ops & set(reach.ops))):
                return path, f"changed {what}, which {tool} runs"
            if kind in ("type", "const", "var", "field") and mentioned(kind, name or "", reach.text(ops), reach.aliases):
                return path, f"changed {what}, which {tool} reaches"
    return None


def mentioned(kind: str, name: str, ran: tuple[str, set[str]], aliases: frozenset[str] = frozenset()) -> bool:
    """Whether the source a criterion runs names a changed type, constant,
    variable or struct field. A field is read as x.Field or written Field: in
    a literal; pkg.Field, an imported package's name, is not the field."""
    text, types = ran
    if kind == "type":
        return name in types
    if kind == "field":
        owner, _, field = name.partition(".")
        if owner not in types:
            return False
        for m in re.finditer(rf"(\w*)\.{field}\b|\b{field}\s*:", text):
            if m.group(1) is None or m.group(1) not in aliases:
                return True
        return False
    return re.search(rf"\b{re.escape(name)}\b", text) is not None


def affected(changed: list[str]) -> dict[str, dict[str, tuple[str, str]]]:
    """The acceptance files calling a tool the changed paths change, each
    mapped to those tools and the changed file behind each with what in it
    reached the tool. A file whose every criterion runs in its own environment
    is left out: a run on the plain stack cannot execute it, and the E2E Nightly
    runs that environment."""
    changed_tools: dict[str, tuple[str, list[str]]] = {}
    for tool, sources in tool_sources().items():
        for where in sorted(sources):
            hits = changed_sources_of(where, changed)
            if hits:
                changed_tools.setdefault(tool, (where, hits))
                break
    if not changed_tools:
        return {}
    files = sorted({f for _, hits in changed_tools.values() for f in hits})
    lines = production_changed_lines(diff_range(), files)
    elsewhere = environment_criteria()
    directory = REPO_ROOT / "test" / "acceptance"
    sources = {p.relative_to(REPO_ROOT).as_posix(): p.read_text(encoding="utf-8") for p in sorted(directory.glob("*_test.go"))}
    joined = "\n".join(sources.values())
    foreign = foreign_methods(joined)
    funcs = {name: body for name, body in package_functions(joined).items()
             if not name.startswith("connect") and name not in foreign}
    out: dict[str, dict[str, tuple[str, str]]] = {}
    for path, called in sorted(tools_called().items()):
        wanted = [tool for tool in sorted(called) if tool in changed_tools]
        if not wanted:
            continue
        ops = ops_called(sources[path], funcs, set())
        hits = {}
        for tool in wanted:
            hit = tool_hit(tool, changed_tools[tool][0], changed_tools[tool][1], lines, ops)
            if hit:
                hits[tool] = hit
        if not hits:
            continue
        criteria = criteria_in(REPO_ROOT / path, issue_of(path) or "")
        if criteria and all(any(p.search(c) for p in elsewhere) for c in criteria):
            continue
        out[path] = hits
    return out


# The git range or base a diff is read from for the helpers it changes:
# acceptance-check.sh sets it to the merge base, and the pull request job to
# the range it diffs. Unset, no helper is read as changed.
DIFF_RANGE_ENV = "ACCEPTANCE_DIFF_RANGE"
GROUP_EDGE = re.compile(r"^(?:(?:const|var|type)\s*\(|\))$")
HUNK = re.compile(r"^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@")


def diff_range() -> str:
    return os.environ.get(DIFF_RANGE_ENV, "").strip()


def changed_lines(range_: str, paths: tuple[str, ...] = ("test/acceptance",)) -> dict[str, set[int]]:
    """Each file under paths the diff changes, mapped to its changed lines on
    the new side; a deletion counts as a change of the line after it."""
    if not range_:
        return {}
    diff = subprocess.run(
        ["git", "diff", "-U0", range_, "--", *paths],
        cwd=REPO_ROOT, capture_output=True, text=True, check=True,
    ).stdout
    out: dict[str, set[int]] = {}
    current = ""
    for line in diff.splitlines():
        if line.startswith("+++ "):
            current = line[len("+++ b/"):] if line.startswith("+++ b/") else ""
            continue
        m = HUNK.match(line)
        if current and m:
            start, count = int(m.group(1)), int(m.group(2) if m.group(2) is not None else "1")
            out.setdefault(current, set()).update(range(start, start + count) if count else {start})
    return out


def production_changed_lines(range_: str, files: list[str]) -> dict[str, set[int]]:
    """The changed lines of each named production file, from the diff range;
    a file the range does not change, as when the change is not committed and
    no range is set, is absent, and the package rule reads it."""
    if not range_ or not files:
        return {}
    diff = subprocess.run(
        ["git", "diff", "-U0", "-w", range_, "--", *files],
        cwd=REPO_ROOT, capture_output=True, text=True, check=True,
    ).stdout
    # Whitespace is ignored (gofmt realigning a struct changes no field), and
    # so is a blank or comment line: an added one is not counted, and a hunk
    # removing only such lines is not either.
    out: dict[str, set[int]] = {f: set() for f in files}
    current, at, removed = "", 0, False

    def code(text: str) -> bool:
        # A group's own "const (" or closing ")" says nothing its specs do not.
        t = text.strip()
        return bool(t) and not t.startswith("//") and not GROUP_EDGE.match(t)

    for line in diff.splitlines():
        if line.startswith("+++ "):
            current = line[len("+++ b/"):] if line.startswith("+++ b/") else ""
            continue
        if line.startswith(("--- ", "diff ", "index ")):
            continue
        m = HUNK.match(line)
        if m:
            at, removed = int(m.group(1)), False
            if m.group(2) == "0":
                at += 1
            continue
        if not current:
            continue
        if line.startswith("-") and code(line[1:]) and not removed:
            removed = True
            out.setdefault(current, set()).add(at)
        elif line.startswith("+"):
            if code(line[1:]):
                out.setdefault(current, set()).add(at)
            at += 1
    return out


def declarations(text: str) -> list[tuple[str, int, int]]:
    """Each top-level function or method a Go source declares: its name and
    the first and last line of its declaration."""
    found = []
    for m in FUNC_DECL.finditer(text):
        brace = text.find("{", m.end())
        if brace < 0:
            continue
        body = balanced(text, brace, "{", "}")
        first = text.count("\n", 0, m.start()) + 1
        found.append((m.group(1), first, first + text.count("\n", m.start(), brace + len(body))))
    return found


def changed_helpers(range_: str) -> dict[str, str]:
    """Each helper an acceptance file declares whose declaration the diff
    changes, mapped to the file changing it. A criterion (TestIssue...) is not
    a helper: its own file is selected because it changed."""
    out: dict[str, str] = {}
    for path, lines in sorted(changed_lines(range_).items()):
        source = REPO_ROOT / path
        if not path.endswith("_test.go") or not source.is_file():
            continue
        for name, first, last in declarations(source.read_text(encoding="utf-8")):
            if name.startswith("Test") or not any(first <= n <= last for n in lines):
                continue
            out.setdefault(name, path)
    return out


def helpers_reached(text: str, wanted: set[str], funcs: dict[str, str], seen: set[str]) -> set[str]:
    """The wanted helpers a span calls, directly or through a helper it calls."""
    found: set[str] = set()
    for m in CALL.finditer(text):
        name = m.group(1)
        if name in wanted:
            found.add(name)
        if name in funcs and name not in seen:
            seen.add(name)
            found |= helpers_reached(funcs[name], wanted, funcs, seen)
    return found


def helper_affected(changed: list[str], range_: str) -> dict[str, dict[str, str]]:
    """The acceptance files the diff leaves alone that call a helper it
    changes, each mapped to those helpers and the file changing each (#2627).
    A file whose every criterion runs in its own environment is left out, as
    affected leaves one out."""
    helpers = changed_helpers(range_)
    if not helpers:
        return {}
    directory = REPO_ROOT / "test" / "acceptance"
    sources = {p.relative_to(REPO_ROOT).as_posix(): p.read_text(encoding="utf-8") for p in sorted(directory.glob("*_test.go"))}
    joined = "\n".join(sources.values())
    foreign = foreign_methods(joined)
    funcs = {name: body for name, body in package_functions(joined).items() if name not in foreign}
    elsewhere = environment_criteria()
    touched = set(changed)
    out: dict[str, dict[str, str]] = {}
    for path, text in sources.items():
        issue = issue_of(path)
        if not issue or path in touched:
            continue
        reached = helpers_reached(text, set(helpers), funcs, set())
        if not reached:
            continue
        criteria = criteria_in(REPO_ROOT / path, issue)
        if criteria and all(any(p.search(c) for p in elsewhere) for c in criteria):
            continue
        out[path] = {name: helpers[name] for name in sorted(reached)}
    return out


def cmd_affected(_args: argparse.Namespace) -> int:
    """Print the tickets of the acceptance files the changed paths on stdin
    reach, comma-separated, as acceptance-pr-paths.py --tickets does: the
    files calling a changed tool, and the unchanged files calling a changed
    helper."""
    changed = [line.strip() for line in sys.stdin if line.strip()]
    paths = set(affected(changed)) | set(helper_affected(changed, diff_range()))
    print(",".join(str(n) for n in sorted({int(issue_of(p) or 0) for p in paths})))
    return 0


def stale_helper_recordings(changed: list[str]) -> list[str]:
    """Each unchanged acceptance file calling a changed helper whose recorded
    run did not pass, or was recorded before the helper changed (#2627)."""
    problems: list[str] = []
    for path, hits in helper_affected(changed, diff_range()).items():
        issue = issue_of(path) or ""
        named = ", ".join(f"{h} ({where})" for h, where in sorted(hits.items()))
        found = verdict(issue, [path])
        problems.extend(f"{p} (it calls {named}, which this diff changes)" for p in found)
        if found:
            continue
        recorded = stream_path(issue).stat().st_mtime
        for helper, source in sorted(hits.items()):
            if (REPO_ROOT / source).stat().st_mtime > recorded:
                problems.append(f"#{issue}: {stream_path(issue).relative_to(REPO_ROOT)} was recorded "
                                f"before {source} changed {helper}, which {path} calls")
                break
    return problems


def stale_recordings(changed: list[str]) -> list[str]:
    """Each acceptance file calling a changed tool or helper whose recorded run
    did not pass, or was recorded before the change."""
    problems: list[str] = []
    if not changed:
        return problems
    problems.extend(stale_helper_recordings(changed))
    for path, hits in affected(changed).items():
        issue = issue_of(path) or ""
        named = "; ".join(f"{tool}: {source} {what}" for tool, (source, what) in sorted(hits.items()))
        found = verdict(issue, [path])
        problems.extend(f"{p} (it calls {named})" for p in found)
        if found:
            continue
        recorded = stream_path(issue).stat().st_mtime
        for tool, (source, what) in sorted(hits.items()):
            if (REPO_ROOT / source).stat().st_mtime > recorded:
                problems.append(f"#{issue}: {stream_path(issue).relative_to(REPO_ROOT)} was recorded "
                                f"before {source} changed, and {path} calls {tool} ({source} {what})")
                break
    return problems


# ---------------------------------------------------------------- reach

# A recorded run is stale only when the diff changes what its criteria
# exercise (#2693). On #2692 a change to manage_site's set_domain and to the
# api gateway's config type marked 139 tickets stale under the package rule,
# though almost none of them called set_domain or read the field. Inside the
# package registering a tool, a changed function counts against a criterion
# only when the tool's handler reaches it: through the handler of a command the
# acceptance file names, or through the code every command runs. Reach is read
# lexically, by name, so a method shared by name with another type is counted
# as reached (more stale, never fewer). A change the reading cannot place (a
# type, a constant, a variable, an init, the registration, a deleted function)
# keeps the package rule for that tool.

# The fields a tool with several operations takes the operation in.
DISCRIMINATORS = ("command", "action", "operation")
DISCRIMINATOR_ARG = re.compile(r'"(?:' + "|".join(DISCRIMINATORS) + r')"\s*:\s*(?:"([a-z][a-z0-9_]*)")?')
# A dispatch table: map[string]<func type>{...}, keyed by operation.
DISPATCH_MAP = re.compile(r"\bmap\[string\](func\b|[A-Za-z_]\w*)")
FUNC_TYPE = re.compile(r"^type[ \t]+([A-Za-z_]\w*)[ \t]+func\b", re.MULTILINE)
# A dispatch switch: switch in.Command {, switch action {.
DISPATCH_SWITCH = re.compile(r"\bswitch\s+(?:[A-Za-z_][\w.]*\.)?(?:" +
                             "|".join(d.capitalize() + "|" + d for d in DISCRIMINATORS) + r"|cmd)\s*\{")
CASE_KEY = re.compile(r'"([a-z][a-z0-9_]*)"|([A-Za-z_][\w.]*)')
IMPORT_LINE = re.compile(r'^\s*(?:import\s+)?(?:([A-Za-z_]\w*)\s+)?"([^"]+)"\s*$')
ENTRY_KEY = re.compile(r'\s*(?:"([a-z][a-z0-9_]*)"|([A-Za-z_][\w.]*))\s*:')
TOP_DECL = re.compile(r"^(type|const|var)\b", re.MULTILINE)
SPEC_NAME = re.compile(r"\s*([A-Za-z_]\w*)")
FIELD_LINE = re.compile(r"^\s*([A-Za-z_]\w*)\s+[\[\]*A-Za-z_(]")


def skip_literal(text: str, i: int) -> int:
    """The index of the last character of the string, rune or comment starting
    at i, or i when none starts there."""
    n = len(text)
    if text.startswith("//", i):
        end = text.find("\n", i)
        return n - 1 if end < 0 else end
    if text.startswith("/*", i):
        end = text.find("*/", i)
        return n - 1 if end < 0 else end + 1
    c = text[i]
    if c == "`":
        end = text.find("`", i + 1)
        return n - 1 if end < 0 else end
    if c in "\"'":
        j = i + 1
        while j < n and text[j] != c and text[j] != "\n":
            j += 2 if text[j] == "\\" else 1
        return j
    return i


def top_level(text: str, start: int, end: int, marks: str) -> list[int]:
    """The indexes in text[start:end] of the characters in marks at bracket
    depth zero, strings and comments skipped."""
    found: list[int] = []
    depth = 0
    i = start
    while i < end:
        j = skip_literal(text, i)
        if j != i:
            i = j + 1
            continue
        c = text[i]
        if c in "([{":
            depth += 1
        elif c in ")]}":
            depth -= 1
        elif depth == 0 and c in marks:
            found.append(i)
        i += 1
    return found


def module_path() -> str:
    gomod = REPO_ROOT / "go.mod"
    if not gomod.is_file():
        return ""
    for line in gomod.read_text(encoding="utf-8").splitlines():
        if line.startswith("module "):
            return line.split()[1]
    return ""


class GoFile:
    """One Go source: its text, its declarations' extents, its imports."""

    def __init__(self, path: Path):
        self.rel = path.relative_to(REPO_ROOT).as_posix()
        self.text = path.read_text(encoding="utf-8", errors="replace")
        self.starts = [0] + [m.end() for m in re.finditer("\n", self.text)]
        # name, doc line, last line, body start, body end, declaration start
        self.funcs: list[tuple[str, int, int, int, int, int]] = []
        for m in FUNC_DECL.finditer(self.text):
            brace = self.text.find("{", m.end())
            if brace < 0:
                continue
            body = balanced(self.text, brace, "{", "}")
            self.funcs.append((m.group(1), self.doc_line(self.line_of(m.start())),
                               self.line_of(brace + len(body) - 1), brace, brace + len(body), m.start()))
        # kind (type, const, var), name, first line, last line, text
        self.specs: list[tuple[str, str, int, int, str]] = []
        for m in TOP_DECL.finditer(self.text):
            after = m.end()
            while after < len(self.text) and self.text[after] in " \t":
                after += 1
            if self.text.startswith("(", after):
                close = after + len(balanced(self.text, after, "(", ")"))
                cuts = [after] + top_level(self.text, after + 1, close - 1, "\n") + [close - 1]
                for a, b in zip(cuts, cuts[1:]):
                    a += 1
                    # A spec's doc comment ends at a newline the cut skipped.
                    while self.text[a:b].lstrip().startswith("//"):
                        a = self.text.find("\n", a + len(self.text[a:b]) - len(self.text[a:b].lstrip()), b) + 1
                        if a <= 0:
                            a = b
                    spec = self.text[a:b]
                    name = SPEC_NAME.match(spec)
                    if name:
                        first = self.line_of(a + name.start(1))
                        self.specs.append((m.group(1), name.group(1), first, self.line_of(b), spec))
                continue
            # The declaration ends at the first newline outside brackets that
            # does not follow an operator: "a" +\n"b" continues.
            end = len(self.text)
            for i in top_level(self.text, after, len(self.text), "\n"):
                if not self.text[after:i].rstrip().endswith(("+", "-", "*", "/", ",", "||", "&&", "=", ".")):
                    end = i
                    break
            name = SPEC_NAME.match(self.text, after, end)
            if name:
                self.specs.append((m.group(1), name.group(1), self.doc_line(self.line_of(m.start())),
                                   self.line_of(end), self.text[after:end]))
        self.imports: dict[str, str] = {}
        self.import_lines: set[int] = set()
        for m in re.finditer(r"^import\s*\(.*?^\)|^import\s+[^\n]+", self.text, re.MULTILINE | re.DOTALL):
            first, last = self.line_of(m.start()), self.line_of(m.end() - 1)
            self.import_lines.update(range(first, last + 1))
            for raw in m.group(0).replace("import", "", 1).strip("()\n ").splitlines():
                im = IMPORT_LINE.match(raw)
                if im:
                    self.imports[im.group(1) or im.group(2).rsplit("/", 1)[-1]] = im.group(2)
        pkg = re.search(r"^package\s+\w+", self.text, re.MULTILINE)
        if pkg:
            self.import_lines.add(self.line_of(pkg.start()))

    def doc_line(self, first: int) -> int:
        """The first line of the doc comment above line first, or first."""
        while first > 1 and self.line(first - 1).lstrip().startswith("//"):
            first -= 1
        return first

    def line_of(self, index: int) -> int:
        lo, hi = 0, len(self.starts) - 1
        while lo < hi:
            mid = (lo + hi + 1) // 2
            if self.starts[mid] <= index:
                lo = mid
            else:
                hi = mid - 1
        return lo + 1

    def line(self, number: int) -> str:
        start = self.starts[number - 1]
        end = self.starts[number] if number < len(self.starts) else len(self.text)
        return self.text[start:end]


class Entry:
    """One operation's handler in a dispatch table or switch."""

    def __init__(self, gofile: GoFile, holder: str, op: str | None, start: int, end: int):
        self.file, self.holder, self.op = gofile, holder, op
        self.start, self.end = start, end
        self.first, self.last = gofile.line_of(start), gofile.line_of(max(start, end - 1))


class Package:
    """A package's functions by name and its dispatch entries, read once."""

    def __init__(self, directory: str):
        self.files = [GoFile(p) for p in sorted((REPO_ROOT / directory).glob("*.go")) if not p.name.endswith("_test.go")]
        joined = "\n".join(f.text for f in self.files)
        self.consts = dict(STRING_CONST.findall(joined))
        self.func_types = set(FUNC_TYPE.findall(joined))
        self.entries: list[Entry] = []
        self.bodies: dict[str, list[str]] = {}
        self.full: dict[str, list[str]] = {}
        self.types: dict[str, list[str]] = {}
        for f in self.files:
            for name, _doc, _last, start, end, decl in f.funcs:
                found = self._dispatch(f, name, start, end)
                self.entries.extend(found)
                self.full.setdefault(name, []).append(f.text[decl:end])
                self.bodies.setdefault(name, []).append(blank(f.text, decl, end, found))
            for kind, name, _first, _last, text in f.specs:
                if kind == "type":
                    self.types.setdefault(name, []).append(text)

    def key(self, gofile: GoFile, raw: str | None, literal: str | None) -> str | None:
        """The operation a table key or case names, or None when unresolved."""
        if literal:
            return literal
        if not raw:
            return None
        alias, _, name = raw.rpartition(".")
        if not alias:
            return self.consts.get(name)
        return imported_consts(gofile.imports.get(alias, "")).get(name)

    def _dispatch(self, f: GoFile, holder: str, start: int, end: int) -> list[Entry]:
        found: list[Entry] = []
        text = f.text
        for m in DISPATCH_MAP.finditer(text, start, end):
            if m.group(1) == "func":
                brace = text.find("{", m.end())
                if brace < 0 or "\n" in text[m.end():brace]:
                    continue
            elif m.group(1) in self.func_types and text.startswith("{", m.end()):
                brace = m.end()
            else:
                continue
            block_end = brace + len(balanced(text, brace, "{", "}"))
            cuts = [brace] + top_level(text, brace + 1, block_end - 1, ",") + [block_end - 1]
            for a, b in zip(cuts, cuts[1:]):
                km = ENTRY_KEY.match(text, a + 1, b)
                if not km:
                    continue
                # The entry holds its key: a constant naming one command is
                # that command's, not the code every command runs.
                lead = text[a + 1:b]
                found.append(Entry(f, holder, self.key(f, km.group(2), km.group(1)), b - len(lead.lstrip()), b))
        for m in DISPATCH_SWITCH.finditer(text, start, end):
            brace = m.end() - 1
            block_end = brace + len(balanced(text, brace, "{", "}"))
            heads = [i for i in top_level(text, brace + 1, block_end - 1, "cd")
                     if re.match(r"(?:case\b|default\s*:)", text[i:i + 8]) and not re.match(r"\w", text[i - 1])]
            for a, b in zip(heads, heads[1:] + [block_end - 1]):
                colon = top_level(text, a, b, ":")
                if not colon:
                    continue
                if text.startswith("default", a):
                    found.append(Entry(f, holder, None, colon[0] + 1, b))
                    continue
                for km in CASE_KEY.finditer(text, a + 4, colon[0]):
                    found.append(Entry(f, holder, self.key(f, km.group(2), km.group(1)), a, b))
        return found

    def reach(self, texts: list[str], bodies: dict[str, list[str]]) -> set[str]:
        """The package functions the texts name, directly or through a
        function they name."""
        seen: set[str] = set()
        work = list(texts)
        while work:
            for ident in IDENT.findall(work.pop()):
                if ident in bodies and ident not in seen:
                    seen.add(ident)
                    work.extend(bodies[ident])
        return seen


TOOL_LITERAL = re.compile(r"\bTool\{")


def without_definition(span: str) -> str:
    """A registration with its mcp.Tool literal cut out: the name,
    description and input schema tools/list advertises, which no handler runs
    (the advertised surface is held to the tool table by toolsurface's
    tests)."""
    m = TOOL_LITERAL.search(span)
    if not m:
        return span
    literal = balanced(span, m.end() - 1, "{", "}")
    return span[:m.start()] + span[m.end() - 1 + len(literal):]


def blank(text: str, start: int, end: int, entries: list[Entry]) -> str:
    """text[start:end] with each resolved entry's handler cut out: what the
    function runs for every operation."""
    out, at = [], start
    for e in sorted((e for e in entries if e.op), key=lambda e: e.start):
        if e.start >= at:
            out.append(text[at:e.start])
            at = e.end
    out.append(text[at:end])
    return "".join(out)


_IMPORTED: dict[str, dict[str, str]] = {}


def imported_consts(import_path: str) -> dict[str, str]:
    """The string constants of a package of this module, by import path."""
    if import_path not in _IMPORTED:
        consts: dict[str, str] = {}
        prefix = module_path() + "/"
        if import_path.startswith(prefix):
            directory = REPO_ROOT / import_path[len(prefix):]
            for go in sorted(directory.glob("*.go")) if directory.is_dir() else []:
                if not go.name.endswith("_test.go"):
                    consts.update(STRING_CONST.findall(go.read_text(encoding="utf-8", errors="replace")))
        _IMPORTED[import_path] = consts
    return _IMPORTED[import_path]


_PACKAGES: dict[str, Package] = {}


def package(directory: str) -> Package:
    if directory not in _PACKAGES:
        _PACKAGES[directory] = Package(directory)
    return _PACKAGES[directory]


class ToolReach:
    """What one tool's handler runs: the functions every operation reaches,
    and per operation the functions its handler reaches."""

    def __init__(self, pkg: Package, tool: str):
        self.pkg = pkg
        spans: list[str] = []
        self.registration: set[str] = set()
        tools = {tool}
        for f in pkg.files:
            for m in ADD_TOOL.finditer(f.text):
                span = balanced(f.text, m.end() - 1, "(", ")")
                if registered_in(span, tools, pkg.consts, {n: b[0] for n, b in pkg.full.items()}):
                    spans.append(without_definition(span))
                    self.registration |= {d[0] for d in f.funcs if d[5] <= m.start() < d[4]}
        self.spans = spans
        # The package names the package's files import, which a field read
        # (x.Field) is never qualified by.
        self.aliases = frozenset(a for f in pkg.files for a in f.imports)
        self.full = pkg.reach(spans, pkg.full)
        self._texts: dict[frozenset[str] | None, tuple[str, set[str]]] = {}
        owned = [e for e in pkg.entries if e.holder in self.full or e.holder in self.registration]
        self.entries = owned
        unresolved = [e.file.text[e.start:e.end] for e in owned if not e.op]
        self.common = pkg.reach(spans + unresolved, pkg.bodies)
        self.ops: dict[str, set[str]] = {}
        for e in owned:
            if e.op:
                self.ops.setdefault(e.op, set()).update(pkg.reach([e.file.text[e.start:e.end]], pkg.bodies))

    def text(self, ops: set[str] | None) -> tuple[str, set[str]]:
        """The source a criterion naming ops runs, and the package types it
        names, directly or through another type."""
        key = frozenset(ops) if ops is not None else None
        if key not in self._texts:
            funcs = self.reached(ops)
            texts = list(self.spans)
            for name in funcs:
                texts.extend(self.pkg.bodies[name] if funcs is not self.full else self.pkg.full[name])
            named = ops & set(self.ops) if ops is not None else set(self.ops)
            texts.extend(e.file.text[e.start:e.end] for e in self.entries
                         if not e.op or e.op in named or funcs is self.full)
            text = "\n".join(texts)
            self._texts[key] = (text, self.pkg.reach([text], self.pkg.types))
        return self._texts[key]

    def reached(self, ops: set[str] | None) -> set[str]:
        """The functions a criterion naming ops reaches; None, or no op this
        tool dispatches, is every function the handler reaches."""
        named = ops & set(self.ops) if ops is not None else set()
        if not named:
            return self.full
        out = set(self.common)
        for op in named:
            out |= self.ops[op]
        return out


_TOOLS: dict[tuple[str, str], ToolReach] = {}


def tool_reach(directory: str, tool: str) -> ToolReach:
    if (directory, tool) not in _TOOLS:
        _TOOLS[(directory, tool)] = ToolReach(package(directory), tool)
    return _TOOLS[(directory, tool)]


def placed(gofile: GoFile, lines: set[int], reach: ToolReach) -> list[tuple[str, str | None, str]]:
    """Each changed line of a file in a tool's package, placed as ("func",
    name, what), ("op", operation, what), ("type"|"const"|"var", name, what),
    ("field", "Type.Field", what) or ("unplaced", None, what)."""
    out = []
    for n in sorted(lines):
        if n in gofile.import_lines:
            continue
        entry = next((e for e in reach.entries if e.file is gofile and e.op and e.first <= n <= e.last), None)
        if entry:
            out.append(("op", entry.op, f"{gofile.rel}:{n} ({entry.op} in {entry.holder})"))
            continue
        decl = next((d for d in gofile.funcs if d[1] <= n <= d[2]), None)
        spec = next((d for d in gofile.specs if d[2] <= n <= d[3]), None) if decl is None else None
        if spec is not None:
            kind, name = spec[0], spec[1]
            field = FIELD_LINE.match(gofile.line(n)) if kind == "type" and n > spec[2] else None
            if field and field.group(1) not in ("struct", "interface", "func", "type"):
                out.append(("field", f"{name}.{field.group(1)}", f"{gofile.rel}:{n} (field {field.group(1)} of {name})"))
            else:
                out.append((kind, name, f"{gofile.rel}:{n} ({kind} {name})"))
        elif decl is None:
            out.append(("unplaced", None, f"{gofile.rel}:{n}, outside any declaration"))
        elif decl[0] == "init" or decl[0] in reach.registration:
            out.append(("unplaced", None, f"{gofile.rel}:{n}, in {decl[0]}, which registers or initializes the tool"))
        else:
            out.append(("func", decl[0], f"{gofile.rel}:{n} ({decl[0]})"))
    return out


def ops_called(text: str, funcs: dict[str, str], seen: set[str]) -> set[str] | None:
    """The operations an acceptance file names in literal command, action or
    operation arguments, directly or through a helper it calls; None when one
    is not a literal, so every operation counts."""
    ops: set[str] = set()
    for m in DISCRIMINATOR_ARG.finditer(text):
        if not m.group(1):
            return None
        ops.add(m.group(1))
    for m in CALL.finditer(text):
        name = m.group(1)
        if name in funcs and name not in seen:
            seen.add(name)
            more = ops_called(funcs[name], funcs, seen)
            if more is None:
                return None
            ops |= more
    return ops


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
