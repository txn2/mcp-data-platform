#!/usr/bin/env python3
"""Judge an osv-scanner JSON report (#2690).

`make osv` runs osv-scanner over go.mod, ui/package-lock.json and
docs/requirements.txt and hands its report here. Whether a finding fails the
run depends on what the run is checking:

  full (OSV_SCOPE=full, the push to main and the scheduled runs)
      Every advisory fails. Main goes red on an advisory published against a
      dependency it already pins, and someone reads it.

  diff (OSV_SCOPE=diff, a pull request and a local verify)
      An advisory fails only when it is against a package version the change
      adds or moves to: one present in a manifest at the tree under test and
      absent at OSV_BASE. An advisory against a dependency the change left
      alone is about main, not the change, and is printed as a warning.

Either way an osv-scanner.toml entry that no longer matches a finding fails:
an accepted advisory cannot outlive its reason.

Usage: osv-gate.py REPORT.json SCANNER.log
"""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent

# The manifests `make osv` scans, by the ecosystem osv-scanner reports for each.
GO_MOD = "go.mod"
NPM_LOCK = "ui/package-lock.json"
PIP_REQUIREMENTS = "docs/requirements.txt"


def show(rev: str, path: str) -> str:
    """The file at rev, or at the working tree when rev is empty; "" when absent."""
    if not rev:
        try:
            return (REPO_ROOT / path).read_text(encoding="utf-8")
        except OSError:
            return ""
    out = subprocess.run(["git", "show", f"{rev}:{path}"], cwd=REPO_ROOT, capture_output=True, text=True)
    return out.stdout if out.returncode == 0 else ""


GO_REQUIRE = re.compile(r"^\s*(?:require\s+)?([^\s()]+)\s+(v[^\s]+)")
GO_TOOLCHAIN = re.compile(r"^\s*(?:go|toolchain)\s+(?:go)?([0-9][^\s]*)\s*$")


def go_versions(text: str) -> set[tuple[str, str]]:
    """The (module, version) pairs go.mod requires, and the Go version as stdlib."""
    pairs = set()
    for line in text.splitlines():
        if m := GO_TOOLCHAIN.match(line):
            pairs.add(("stdlib", m.group(1)))
        elif m := GO_REQUIRE.match(line.split("//")[0]):
            if m.group(1) not in ("module", "go", "toolchain", "replace", "exclude", "retract"):
                pairs.add((m.group(1), m.group(2).removeprefix("v")))
    return pairs


def npm_versions(text: str) -> set[tuple[str, str]]:
    """The (package, version) pairs an npm lockfile installs."""
    try:
        lock = json.loads(text) if text else {}
    except ValueError:
        return set()
    pairs = set()
    for key, entry in (lock.get("packages") or {}).items():
        if not key or "node_modules/" not in key or not isinstance(entry, dict) or "version" not in entry:
            continue
        pairs.add((key.rsplit("node_modules/", 1)[1], str(entry["version"])))
    return pairs


def pip_name(name: str) -> str:
    return re.sub(r"[-_.]+", "-", name).lower()


PIP_PIN = re.compile(r"^([A-Za-z0-9][A-Za-z0-9._-]*)(?:\[[^\]]*\])?==([^\s;\\]+)")


def pip_versions(text: str) -> set[tuple[str, str]]:
    """The (package, version) pairs a requirements file pins."""
    return {(pip_name(m.group(1)), m.group(2)) for line in text.splitlines() if (m := PIP_PIN.match(line.strip()))}


READERS = {"Go": (GO_MOD, go_versions), "npm": (NPM_LOCK, npm_versions), "PyPI": (PIP_REQUIREMENTS, pip_versions)}


def introduced(base: str) -> dict[str, set[tuple[str, str]]]:
    """Per ecosystem, the (package, version) pairs the tree has and base did not."""
    out = {}
    for eco, (path, read) in READERS.items():
        out[eco] = read(show("", path)) - read(show(base, path))
    return out


def key(eco: str, name: str, version: str) -> tuple[str, str]:
    name = pip_name(name) if eco == "PyPI" else name
    version = version.removeprefix("v") if eco == "Go" else version
    return name, version


def findings(report: dict) -> list[dict]:
    """Each vulnerable package in the report with its advisory ids."""
    out = []
    for result in report.get("results") or []:
        source = (result.get("source") or {}).get("path", "")
        for pkg in result.get("packages") or []:
            ids = [v.get("id", "") for v in pkg.get("vulnerabilities") or [] if v.get("id")]
            if not ids:
                continue
            p = pkg.get("package") or {}
            out.append({"ecosystem": p.get("ecosystem", ""), "name": p.get("name", ""),
                        "version": p.get("version", ""), "ids": sorted(ids), "source": source})
    return out


def describe(f: dict) -> str:
    return f"{f['ecosystem']} {f['name']}@{f['version']}: {', '.join(f['ids'])}"


def main(argv: list[str]) -> int:
    if len(argv) != 3:
        print(__doc__, file=sys.stderr)
        return 2
    scope = os.environ.get("OSV_SCOPE", "full").strip() or "full"
    base = os.environ.get("OSV_BASE", "").strip()
    log = Path(argv[2]).read_text(encoding="utf-8") if Path(argv[2]).exists() else ""
    try:
        report = json.loads(Path(argv[1]).read_text(encoding="utf-8") or "{}")
    except (OSError, ValueError) as err:
        print(f"osv-gate: the osv-scanner report could not be read: {err}", file=sys.stderr)
        return 1

    status = 0
    if "has unused ignores" in log:
        print("ERROR: osv-scanner.toml accepts an advisory no longer reported; delete that entry.")
        status = 1

    found = findings(report)
    if scope == "diff" and not base:
        print("osv-gate: OSV_SCOPE=diff needs OSV_BASE; judging every advisory", file=sys.stderr)
        scope = "full"
    new = introduced(base) if scope == "diff" else {}
    failing, warning = [], []
    for f in found:
        mine = scope == "full" or key(f["ecosystem"], f["name"], f["version"]) in new.get(f["ecosystem"], set())
        (failing if mine else warning).append(f)

    for f in warning:
        print(f"::warning title=osv advisory against an unchanged dependency::{describe(f)} "
              "(this change does not add or move it; the push to main fails on it)")
    for f in failing:
        print(f"::error title=osv advisory::{describe(f)}")
    if failing:
        what = "a dependency" if scope == "full" else "a dependency version this change adds or moves to"
        print(f"osv-scanner: {len(failing)} advisory finding(s) against {what}. Upgrade it, or accept the advisory "
              "in osv-scanner.toml with a reason and an ignoreUntil date.")
        status = 1
    elif not warning:
        print("osv-scanner: no advisory against a scanned manifest.")
    return status


if __name__ == "__main__":
    sys.exit(main(sys.argv))
