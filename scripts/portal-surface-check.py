#!/usr/bin/env python3
"""portal-surface-check.py -- warn when a diff adds an MCP tool or a top-level
config section and changes nothing a person sees in the portal (#2560).

A feature can register a tool, pass every gate and reach a release with
nothing in the portal. manage_site shipped in v1.155.0 with no Sites page
(#2517, #2518), and the API gateway's first five pull requests (#377-#383)
shipped with no way to add an api connection in the portal. No other step asks
what a person sees.

The check reads the diff against the merge base with main, tracked and
untracked, and reports:

  * each tool name the diff registers, read the way acceptance-check reads it
    (scripts/acceptance-evidence.py tools_added: a tool name constant or a
    Name field added in a package that registers tools);
  * each top-level section added to the platform config: a yaml tag added to
    the Config struct in pkg/platform/config.go;

when the diff changes nothing under ui/src. A tool or section that has no
portal surface by design is listed in scripts/portal-surface-exempt.txt with
the reason, and is not reported; an entry naming a tool or section that no
longer exists fails.

It warns rather than fails for a human; for an AI agent the warning blocks, the
rule state-readers-check and builtin-pages-check carry. A base branch that
cannot be resolved fails.
"""

from __future__ import annotations

import importlib.util
import os
import re
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
EXEMPT = REPO_ROOT / "scripts" / "portal-surface-exempt.txt"
CONFIG = "pkg/platform/config.go"
PORTAL = "ui/src/"
YAML_TAG = re.compile(r'`[^`]*\byaml:"([A-Za-z_][\w]*)[,"]')


def evidence():
    """scripts/acceptance-evidence.py, which reads the tools a diff registers."""
    spec = importlib.util.spec_from_file_location("acceptance_evidence", REPO_ROOT / "scripts" / "acceptance-evidence.py")
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def git(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(["git", *args], cwd=REPO_ROOT, check=check, capture_output=True, text=True)


def merge_base(branch: str) -> str:
    for ref in (f"origin/{branch}", branch):
        if git("rev-parse", "--verify", "--quiet", f"{ref}^{{commit}}", check=False).returncode == 0:
            return git("merge-base", "HEAD", ref).stdout.strip()
    raise SystemExit(
        f"FAIL portal-surface-check: cannot resolve base branch '{branch}' "
        f"(tried origin/{branch} and {branch}); fetch it or set BASE_BRANCH."
    )


def untracked() -> list[str]:
    return [p for p in git("ls-files", "--others", "--exclude-standard").stdout.splitlines() if p]


def changed(base: str) -> list[str]:
    names = set(git("diff", "--name-only", base).stdout.splitlines()) | set(untracked())
    return sorted(n for n in names if n)


def tools_added(ev, base: str) -> dict[str, str]:
    """The tools the diff registers, the untracked files included: git diff
    shows a new file only once it is added, and verify runs before that."""
    added = ev.tools_added(base)
    for rel in untracked():
        if not rel.endswith(".go") or rel.endswith("_test.go") or not rel.startswith(("pkg/", "internal/")):
            continue
        for line in (REPO_ROOT / rel).read_text(encoding="utf-8", errors="replace").splitlines():
            m = ev.TOOL_CONST.match("+" + line) or ev.TOOL_NAME_FIELD.match("+" + line)
            if m and ("_" in m.group(1) or m.group(1) in ("search", "fetch")) and ev.registers_tools(rel):
                added.setdefault(m.group(1), rel)
    return added


def table_tools(ev, text: str) -> set[str]:
    """The tools a test/toolsurface/toolsurface.go text names, as
    acceptance-evidence's native_tools reads the working tree's."""
    start = text.find("Annotations = map[string]Expectation{")
    if start < 0:
        return set()
    consts = dict(ev.STRING_CONST.findall(text))
    block = ev.balanced(text, text.index("{", start), "{", "}")
    names = set()
    for m in re.finditer(r'^\s*(?:"([a-z][a-z0-9_]*)"|([A-Za-z_]\w*))\s*:', block, re.MULTILINE):
        name = m.group(1) or consts.get(m.group(2) or "")
        if name:
            names.add(name)
    return names


def new_tools(ev, base: str) -> dict[str, str]:
    """The tools the platform did not have at the base: a name the diff
    registers that the base's tool table lacks, and a name the tool table
    gained. A tool constant moved or re-declared is not a new tool."""
    shown = git("show", f"{base}:{ev.TOOL_TABLE.as_posix()}", check=False)
    before = table_tools(ev, shown.stdout) if shown.returncode == 0 else set()
    found = {name: where for name, where in tools_added(ev, base).items() if name not in before}
    for name in sorted(ev.native_tools() - before):
        found.setdefault(name, ev.TOOL_TABLE.as_posix())
    return found


def config_sections(text: str) -> set[str]:
    """The yaml names of the Config struct's fields: the top-level sections."""
    start = text.find("type Config struct {")
    if start < 0:
        return set()
    end = text.find("\n}", start)
    return {m.group(1) for m in YAML_TAG.finditer(text[start:end])}


def sections_added(base: str) -> set[str]:
    path = REPO_ROOT / CONFIG
    now = config_sections(path.read_text(encoding="utf-8")) if path.exists() else set()
    shown = git("show", f"{base}:{CONFIG}", check=False)
    before = config_sections(shown.stdout) if shown.returncode == 0 else set()
    return now - before


def exemptions() -> tuple[dict[tuple[str, str], str], list[str]]:
    """Each exempt (kind, name) and its reason, and the malformed lines."""
    entries: dict[tuple[str, str], str] = {}
    bad: list[str] = []
    if not EXEMPT.exists():
        return entries, bad
    for n, line in enumerate(EXEMPT.read_text(encoding="utf-8").splitlines(), 1):
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        parts = line.split(None, 2)
        if len(parts) < 3 or parts[0] not in ("tool", "config"):
            bad.append(f"line {n}: {line!r} is not `tool|config <name> <reason>`")
            continue
        entries[(parts[0], parts[1])] = parts[2]
    return entries, bad


def main() -> int:
    ev = evidence()
    entries, problems = exemptions()
    tools = ev.native_tools()
    path = REPO_ROOT / CONFIG
    sections = config_sections(path.read_text(encoding="utf-8")) if path.exists() else set()
    for kind, name in sorted(entries):
        if kind == "tool" and name not in tools:
            problems.append(f"tool {name} is exempt, and the platform registers no tool of that name")
        if kind == "config" and name not in sections:
            problems.append(f"config {name} is exempt, and the platform config has no section of that name")
    if problems:
        for p in problems:
            print(f"FAIL portal-surface-check: {p} (scripts/portal-surface-exempt.txt)", file=sys.stderr)
        return 1

    base = merge_base(os.environ.get("BASE_BRANCH", "main"))
    if any(p.startswith(PORTAL) for p in changed(base)):
        print("OK portal-surface-check: the change reaches the portal (ui/src changed).")
        return 0
    found = [f"tool {name} (registered in {where})" for name, where in sorted(new_tools(ev, base).items())
             if ("tool", name) not in entries]
    found += [f"config section {name}: (pkg/platform/config.go)" for name in sorted(sections_added(base))
              if ("config", name) not in entries]
    if not found:
        print("OK portal-surface-check: the change adds no tool or config section without a portal surface.")
        return 0
    print("WARNING portal-surface-check: this change adds what a person cannot see in the portal, and changes nothing under ui/src:")
    for f in found:
        print(f"  - {f}")
    print("  Build its portal surface (a page, a card, a setting), or list it in scripts/portal-surface-exempt.txt")
    print("  with the reason it has none. The criteria say what a person sees and does there (CLAUDE.md, rule 6).")
    print("  This warning is blocking for an AI agent.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
