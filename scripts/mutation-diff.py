#!/usr/bin/env python3
"""Mutation testing on the lines a change touches (#2732).

The weekly mutation run (make mutate) read only ./pkg/..., so no mutant was ever
made in internal/, where most of the code the security-design rules are about
lives, and a mutant that survived turned up days after its change merged. This
runs gremlins on the packages under pkg/ and internal/ whose production Go the
change touches, mutating only the lines it changed:

  - the base is DELTA_BASE when verify or CI set it (the last green tree), the
    merge base with main otherwise, as every diff-scoped step reads it;
  - with no production Go changed, it says so and passes;
  - a surviving mutant in a package scripts/mutation-security-packages.txt
    lists fails the step, naming the file, the line and the mutation; in any
    other package it is printed and the step passes.

gremlins v0.6.0 reads --diff as `git diff --merge-base <ref>` and matches each
mutant by its path relative to the package it was given, while git names files
from the repository's root, so a run on one package matched nothing. Each
package is therefore run from its own directory with git's diff.relative set,
which makes git name the files the way gremlins does. A base that is not an
ancestor of HEAD (a recorded tree) widens that diff to its merge base with
HEAD: more lines are mutated, none is missed.

Each package has MUTATION_PACKAGE_TIMEOUT seconds (default 900). A package that
does not finish is printed; in a security package that fails the step, since
nothing was shown about its changed lines. Each package runs with a temporary
directory of its own, removed after it: gremlins copies the module there per
worker.

It runs in the pull request job only, not in make verify: on #2733's diff, 14
of its packages took 413 seconds (internal/hmacsig alone 150) before the
module copies filled the disk, far past the local gate's budget.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
SECURITY_LIST = REPO_ROOT / "scripts" / "mutation-security-packages.txt"
ROOTS = ("pkg/", "internal/")
# Generated code no test is written against.
SKIP_PREFIXES = ("internal/apidocs/",)


def git(*args: str) -> str:
    return subprocess.run(["git", *args], cwd=REPO_ROOT, capture_output=True, text=True, check=True).stdout


def diff_base() -> str:
    """DELTA_BASE when set, else the merge base with main (#2535, #2660)."""
    delta = os.environ.get("DELTA_BASE", "").strip()
    if delta:
        return delta
    for ref in ("origin/main", "main"):
        try:
            return git("merge-base", ref, "HEAD").strip()
        except subprocess.CalledProcessError:
            continue
    raise SystemExit("FAIL mutation-diff: no merge base with main")


def security_packages() -> set[str]:
    out = set()
    for line in SECURITY_LIST.read_text().splitlines():
        line = line.split("#", 1)[0].strip()
        if line:
            out.add(line.rstrip("/"))
    return out


def changed_packages(base: str) -> list[str]:
    """The package directories under pkg/ and internal/ with a changed,
    existing, non-test Go file, in path order."""
    names = git("diff", "--name-only", base, "--", *ROOTS).split()
    names += git("ls-files", "--others", "--exclude-standard", "--", *ROOTS).split()
    dirs = set()
    for name in names:
        if not name.endswith(".go") or name.endswith("_test.go") or name.startswith(SKIP_PREFIXES):
            continue
        if (REPO_ROOT / name).is_file():
            dirs.add(str(Path(name).parent))
    return sorted(dirs)


@dataclass
class Mutant:
    file: str
    line: int
    column: int
    kind: str

    def __str__(self) -> str:
        return f"LIVED {self.kind} at {self.file}:{self.line}:{self.column}"


def survivors(pkg: str, report: dict) -> list[Mutant]:
    out = []
    for f in report.get("files") or []:
        for m in f.get("mutations") or []:
            if m.get("status") == "LIVED":
                out.append(Mutant(f"{pkg}/{f.get('file_name', '')}", int(m.get("line", 0)),
                                  int(m.get("column", 0)), m.get("type", "")))
    return out


def run_package(pkg: str, base: str, timeout: int) -> tuple[list[Mutant] | None, str]:
    """The package's surviving mutants on changed lines, or None when gremlins
    did not finish, with what it printed."""
    pkg_dir = REPO_ROOT / pkg
    with tempfile.TemporaryDirectory() as tmp:
        # gremlins copies the module into a work directory per worker under
        # TMPDIR and leaves it behind when a worker panics; one directory per
        # package, removed when the package is done, keeps a run from filling
        # the disk (it did, measuring this step on #2733's diff).
        work = Path(tmp) / "work"
        work.mkdir()
        env = dict(os.environ, TMPDIR=str(work), GIT_CONFIG_COUNT="1", GIT_CONFIG_KEY_0="diff.relative",
                   GIT_CONFIG_VALUE_0="true")
        out_json = Path(tmp) / "gremlins.json"
        cmd = ["gremlins", "unleash", "--diff", base, "--timeout-coefficient", "3",
               "-o", str(out_json), str(pkg_dir)]
        try:
            proc = subprocess.run(cmd, cwd=pkg_dir, env=env, capture_output=True, text=True, timeout=timeout)
        except subprocess.TimeoutExpired:
            return None, f"did not finish in {timeout}s"
        if proc.returncode != 0 or not out_json.is_file():
            return None, (proc.stdout + proc.stderr)[-2000:]
        return survivors(pkg, json.loads(out_json.read_text() or "{}")), ""


def main() -> int:
    base = diff_base()
    pkgs = changed_packages(base)
    if not pkgs:
        print(f"mutation-diff: no production Go under pkg/ or internal/ changed since {base[:12]}; skipped")
        return 0
    secure = security_packages()
    timeout = int(os.environ.get("MUTATION_PACKAGE_TIMEOUT", "900"))
    failed = []
    for pkg in pkgs:
        critical = pkg in secure
        label = " (security-critical)" if critical else ""
        print(f"mutation-diff: {pkg}{label}", flush=True)
        lived, why = run_package(pkg, base, timeout)
        if lived is None:
            print(f"  gremlins did not finish: {why}")
            if critical:
                failed.append(f"{pkg}: gremlins did not finish, so its changed lines are unproven")
            continue
        for m in lived:
            print(f"  {m}")
            if critical:
                failed.append(str(m))
        if not lived:
            print("  every mutant on a changed line was killed or not viable")
    if failed:
        print("\nFAIL mutation-diff: mutants survived on changed lines of a security-critical package "
              f"({SECURITY_LIST.relative_to(REPO_ROOT)}); add a test that kills each:")
        for f in failed:
            print(f"  {f}")
        return 1
    print(f"mutation-diff: {len(pkgs)} package(s) checked since {base[:12]}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
