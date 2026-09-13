#!/usr/bin/env python3
"""state-readers-check.py -- warn when a diff changes what an API answers with
and no reader of the answer changed (#1709).

Three tickets in two patch releases were one defect. A fix redefined a state
an API answers with, and a reader of that state was left behind: #1689 (the
GraphQL Schema card) and #1703 (the other replica) after #1676, and #1706 (the
admin tool listing) after #1692. Each fix was reviewed on its own diff.

The check reads the diff against the merge base with main. It warns when
either of these changed and no file under ui/src did:

  * a field carrying a json:"..." tag on a struct under one of CONTRACT_DIRS
    was added, removed, or had its tag or type changed;
  * internal/apidocs/swagger.json changed: a route or a definition, including
    its description. #1676 changed no tag; what it changed was the refresh
    route's description, to say a refused re-read is reported beside the
    schema, and the Schema card was the reader that did not learn it.

ui/src/api/generated/admin-api.d.ts is not read: it is gitignored and
generated from swagger.json, so it never appears in a diff, and the spec it is
generated from carries the same definition names.

The warning names each changed field and the swagger definition it lands in. A
reader this script cannot see, such as a card that reads a field it already
had whose meaning changed, is what the PR template's "Readers of this state"
section is for.

It warns rather than fails for a human; for an AI agent the warning blocks, the
same rule doc-check carries. A base branch that cannot be resolved fails.
"""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
from collections import defaultdict
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
CONTRACT_DIRS = ("internal/admin/", "internal/httpserver/", "pkg/admin/", "pkg/portal/", "internal/portal/")
SWAGGER = "internal/apidocs/swagger.json"
READER_DIR = "ui/src/"

# A line that opens a struct body: `type X struct {`, a field `Items []struct {`,
# `ByName map[string]struct {`, or `resp := struct {` inside a function.
STRUCT_OPEN = re.compile(r"\bstruct\s*\{\s*$")
STRUCT_NAME = re.compile(r"^\s*(?:type\s+|var\s+)?(\w+)")
FIELD = re.compile(r"^\s*(\w+(?:\s*,\s*\w+)*)?\s*([^`]*?)\s*`[^`]*\bjson:\"([^\"]*)\"")
JSON_TAG = re.compile(r"`[^`]*\bjson:\"([^\"]*)\"")
PACKAGE = re.compile(r"^package\s+(\w+)", re.MULTILINE)


def git(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(["git", *args], cwd=REPO_ROOT, check=check, capture_output=True, text=True)


def merge_base(base_branch: str) -> str:
    for ref in (f"origin/{base_branch}", base_branch):
        if git("rev-parse", "--verify", "--quiet", f"{ref}^{{commit}}", check=False).returncode == 0:
            return git("merge-base", "HEAD", ref).stdout.strip()
    raise SystemExit(
        f"FAIL state-readers-check: cannot resolve base branch '{base_branch}' "
        f"(tried origin/{base_branch} and {base_branch}); fetch it or set BASE_BRANCH."
    )


def changed_files(base: str) -> list[str]:
    """Committed, staged, unstaged, untracked and deleted paths."""
    names = set(git("diff", "--name-only", base).stdout.splitlines())
    names.update(git("ls-files", "--others", "--exclude-standard").stdout.splitlines())
    return sorted(n for n in names if n)


def base_text(base: str, rel: str) -> str:
    shown = git("show", f"{base}:{rel}", check=False)
    return shown.stdout if shown.returncode == 0 else ""


def work_text(rel: str) -> str:
    path = REPO_ROOT / rel
    return path.read_text(encoding="utf-8") if path.is_file() else ""


def tagged_fields(src: str) -> dict[tuple[str, str], tuple[str, str]]:
    """(struct, field) -> (type, json tag) for every tagged field in src.

    A struct nested in another is named Outer.Field, and its closing line
    (`} `json:"field"``) records Field on Outer.
    """
    fields: dict[tuple[str, str], tuple[str, str]] = {}
    stack: list[tuple[str, str]] = []  # (qualified struct name, its field name in the parent)
    for line in src.splitlines():
        code = line if "`" in line else line.split("//", 1)[0]
        if STRUCT_OPEN.search(code):
            ident = STRUCT_NAME.match(code)
            name = ident.group(1) if ident else "struct"
            nested = bool(stack) and not code.lstrip().startswith("type ")
            stack.append((f"{stack[-1][0]}.{name}" if nested else name, name if nested else ""))
            continue
        if not stack:
            continue
        if re.match(r"^\s*}", code):
            _, field_name = stack.pop()
            tag = JSON_TAG.search(code)
            if stack and field_name and tag:
                fields[(stack[-1][0], field_name)] = ("struct", tag.group(1))
            continue
        field = FIELD.match(code)
        if field:
            names = field.group(1) or field.group(2)
            for name in re.split(r"\s*,\s*", names.strip()):
                fields[(stack[-1][0], name)] = (field.group(2).strip(), field.group(3))
    return fields


def go_changes(base: str, files: list[str]) -> list[str]:
    """One line per changed tagged field, compared per package so a field that
    moved between two files of one package is not a change."""
    before: dict[str, dict] = defaultdict(dict)
    after: dict[str, dict] = defaultdict(dict)
    pkg_name: dict[str, str] = {}
    for rel in files:
        if not rel.endswith(".go") or rel.endswith("_test.go") or not rel.startswith(CONTRACT_DIRS):
            continue
        pkg_dir = str(Path(rel).parent)
        old, new = base_text(base, rel), work_text(rel)
        before[pkg_dir].update(tagged_fields(old))
        after[pkg_dir].update(tagged_fields(new))
        declared = PACKAGE.search(new) or PACKAGE.search(old)
        if declared:
            pkg_name[pkg_dir] = declared.group(1)

    definitions = load_swagger(work_text(SWAGGER)).get("definitions", {})
    lines = []
    for pkg_dir in sorted(set(before) | set(after)):
        old, new = before[pkg_dir], after[pkg_dir]
        for key in sorted(set(old) | set(new)):
            if old.get(key) == new.get(key):
                continue
            struct, field = key
            if key not in old:
                what = f'added (json:"{new[key][1]}")'
            elif key not in new:
                what = f'removed (json:"{old[key][1]}")'
            elif old[key][1] != new[key][1]:
                what = f'tag json:"{old[key][1]}" -> json:"{new[key][1]}"'
            else:
                what = f"type {old[key][0]} -> {new[key][0]}"
            schema = f"{pkg_name.get(pkg_dir, Path(pkg_dir).name)}.{struct.split('.', 1)[0]}"
            where = (
                f"swagger definition {schema}"
                if schema in definitions
                else f"no swagger definition {schema}, so no generated type in admin-api.d.ts"
            )
            lines.append(f"  {pkg_dir}: {struct}.{field} {what}; {where}")
    return lines


def load_swagger(text: str) -> dict:
    try:
        return json.loads(text) if text.strip() else {}
    except json.JSONDecodeError:
        return {}


def keyed_diff(old: dict, new: dict) -> list[tuple[str, str]]:
    """(key, added|removed|changed) for each key whose value differs."""
    return [
        (k, "added" if k not in old else "removed" if k not in new else "changed")
        for k in sorted(set(old) | set(new))
        if old.get(k) != new.get(k)
    ]


def swagger_changes(base: str) -> list[str]:
    old, new = load_swagger(base_text(base, SWAGGER)), load_swagger(work_text(SWAGGER))
    lines = []
    old_paths, new_paths = old.get("paths", {}), new.get("paths", {})
    for path in sorted(set(old_paths) | set(new_paths)):
        for method, state in keyed_diff(old_paths.get(path, {}), new_paths.get(path, {})):
            detail = ""
            before, after = old_paths.get(path, {}).get(method), new_paths.get(path, {}).get(method)
            # A path-level "parameters" list sits beside the methods.
            if state == "changed" and isinstance(before, dict) and isinstance(after, dict):
                detail = ": " + ", ".join(k for k, _ in keyed_diff(before, after))
            lines.append(f"  {SWAGGER}: {method.upper()} {path} {state}{detail}")
    old_defs, new_defs = old.get("definitions", {}), new.get("definitions", {})
    for name, state in keyed_diff(old_defs, new_defs):
        detail = ""
        if state == "changed":
            props = keyed_diff(old_defs[name].get("properties", {}), new_defs[name].get("properties", {}))
            named = [f"{k} {st}" for k, st in props]
            if old_defs[name].get("description") != new_defs[name].get("description"):
                named.append("description changed")
            detail = ": " + ", ".join(named) if named else ""
        lines.append(f"  {SWAGGER}: definition {name} {state}{detail}")
    return lines


def main() -> int:
    base_branch = os.environ.get("BASE_BRANCH", "main")
    base = merge_base(base_branch)
    files = changed_files(base)

    changes = go_changes(base, files)
    if SWAGGER in files:
        changes += swagger_changes(base)
    if not changes:
        print(f"state-readers-check: no API contract field changed against {base_branch}; nothing to check.")
        return 0

    readers = [f for f in files if f.startswith(READER_DIR)]
    if readers:
        print(f"state-readers-check: {len(changes)} API contract change(s) against {base_branch}, and {len(readers)} file(s) under {READER_DIR} changed with them:")
        print("\n".join(changes))
        print('  Name the readers checked for each under "Readers of this state" in the PR.')
        return 0

    print(f"WARNING state-readers-check: {len(changes)} API contract change(s) against {base_branch} and no file under {READER_DIR} changed:")
    print("\n".join(changes))
    print(
        "  A fix that redefines a state an API answers with leaves its other readers behind (#1689, #1703, #1706).\n"
        "  Check each reader of the changed state: the API route, the portal card or page, the admin listing,\n"
        "  the second replica, a restart, and tools/list, and name them under \"Readers of this state\" in the PR."
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
