#!/usr/bin/env python3
"""The delta a follow-up run checks: what changed since the last green run (#2535).

A follow-up push to a pull request reran the whole CI pipeline against code the
previous green run had already proved, and a commit after a green `make verify`
reran the whole of verify. Both now read the change from the last tree that was
proved green to the tree under test, and run only what that change can affect.

The base is a commit, DELTA_BASE, and every diff-reading script takes it in place
of the merge base with main (scripts/schedule-lane.py merge_base, which
realdb-lane.py and verify-diff.py share, and scripts/patch-coverage.sh):

  CI       Each pull-request run uploads the two commits it tested (the PR head
           and the main tip GitHub merged it with) as the ci-tested artifact.
           On a synchronize push, `ci-base` finds the newest successful run of
           the pull request whose head is an ancestor of the new head, reads
           its artifact, and rebuilds the merge it tested as a local commit with
           fixed dates (`rebuild`), so every job derives the same sha.
  verify   A green `make verify` records a commit of the tree it passed
           (`record-local`, .claude/.last-verify-commit); the next verify reads
           it (`local-base`).

A change to a file that decides how the gates run (FULL_RUN_FILES) is never a
delta: the run is full. So is a pull request's first run, every push to main,
and any run whose base cannot be read.

Subcommands:
  ci-base            write mode/old_head/old_main/reason to $GITHUB_OUTPUT
  record-tested      print the commits a pull_request run tests, as JSON
  summary MODE WHY   print the run summary (markdown)
  rebuild HEAD MAIN  print the commit of the merge of HEAD and MAIN
  record-local       record the working tree as the last green verify
  local-base         print the recorded base when it is usable, else nothing
  packages           the Go packages the delta affects (default build), or ./...
  tagged-affected P  exit 0 when import path P (integration build) is affected
  ui                 exit 0 when the delta touches ui/
  files              the paths the delta changes
"""

from __future__ import annotations

import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import urllib.parse
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
MODULE = "github.com/txn2/mcp-data-platform"
LOCAL_RECORD = REPO_ROOT / ".claude" / ".last-verify-commit"
ARTIFACT = "ci-tested"

# A change to any of these decides how the gates themselves run, so a delta
# computed across it proves nothing: the run is full.
FULL_RUN_FILES = {
    "go.mod",
    "go.sum",
    "Makefile",
    ".golangci.yml",
    ".github/workflows/ci.yml",
    "scripts/ci-delta.py",
    "scripts/schedule-lane.py",
    "scripts/patch-coverage.sh",
    "scripts/realdb-lane.py",
    "scripts/verify-diff.py",
}

# The fixed identity and date a rebuilt commit is written with, so the same two
# parents give the same sha in every job and on every machine.
FIXED_ENV = {
    "GIT_AUTHOR_NAME": "ci-delta",
    "GIT_AUTHOR_EMAIL": "ci-delta@localhost",
    "GIT_AUTHOR_DATE": "2000-01-01T00:00:00Z",
    "GIT_COMMITTER_NAME": "ci-delta",
    "GIT_COMMITTER_EMAIL": "ci-delta@localhost",
    "GIT_COMMITTER_DATE": "2000-01-01T00:00:00Z",
}


def git(*args: str, env: dict | None = None, check: bool = True) -> str:
    full_env = {**os.environ, **(env or {})}
    return subprocess.run(
        ["git", *args], cwd=REPO_ROOT, check=check, capture_output=True, text=True, env=full_env
    ).stdout.strip()


def is_commit(sha: str) -> bool:
    if not sha:
        return False
    probe = subprocess.run(
        ["git", "rev-parse", "--verify", "--quiet", f"{sha}^{{commit}}"],
        cwd=REPO_ROOT, capture_output=True, text=True,
    )
    return probe.returncode == 0


def schedule_lane():
    """The schedule lane's diff helpers, loaded from their one copy."""
    spec = importlib.util.spec_from_file_location("schedule_lane", REPO_ROOT / "scripts" / "schedule-lane.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def delta_base() -> str:
    return os.environ.get("DELTA_BASE", "").strip()


def changed_files(base: str) -> list[str]:
    """Every path that differs between base and the working tree, deletions included."""
    names = set(git("diff", "--name-only", base).splitlines())
    names.update(git("ls-files", "--others", "--exclude-standard").splitlines())
    return sorted(n for n in names if n)


def full_run_reason(files: list[str]) -> str:
    hit = sorted(set(files) & FULL_RUN_FILES)
    if hit:
        return "the change touches " + ", ".join(hit) + ", which decide how the gates run"
    return ""


# --- the base -----------------------------------------------------------------


def rebuild(head: str, main: str) -> str:
    """The commit of the merge of head and main, written with fixed dates.

    It is the tree a pull_request run tested (GitHub's merge ref), rebuilt from
    its two parents, since the merge ref itself has moved on. A conflicting
    merge has no tree GitHub could have tested, and is refused.
    """
    for sha in (head, main):
        if not is_commit(sha):
            raise SystemExit(f"ci-delta: {sha} is not a commit in this clone")
    probe = subprocess.run(
        ["git", "merge-tree", "--write-tree", head, main], cwd=REPO_ROOT, capture_output=True, text=True,
    )
    if probe.returncode != 0:
        raise SystemExit(f"ci-delta: {head} and {main} do not merge cleanly")
    tree = probe.stdout.splitlines()[0].strip()
    return git("commit-tree", tree, "-p", head, "-p", main, "-m", "ci-delta: tested merge", env=FIXED_ENV)


def write_outputs(values: dict[str, str]) -> None:
    out = os.environ.get("GITHUB_OUTPUT")
    # One line per value: a newline in a value (an error's text) would start
    # an output of its own.
    lines = [f"{k}=" + " ".join(str(v).split()) for k, v in values.items()]
    if out:
        with open(out, "a", encoding="utf-8") as fh:
            fh.write("\n".join(lines) + "\n")
    print("\n".join(lines))


def gh_json(path: str) -> dict:
    out = subprocess.run(["gh", "api", path], cwd=REPO_ROOT, check=True, capture_output=True, text=True).stdout
    return json.loads(out)


def tested_commits(run_id: int) -> dict | None:
    """The head, main and tree a run recorded in its ci-tested artifact, or None."""
    with tempfile.TemporaryDirectory() as tmp:
        got = subprocess.run(
            ["gh", "run", "download", str(run_id), "-n", ARTIFACT, "-D", tmp],
            cwd=REPO_ROOT, capture_output=True, text=True,
        )
        if got.returncode != 0:
            return None
        try:
            data = json.loads((Path(tmp) / "tested.json").read_text(encoding="utf-8"))
        except (OSError, ValueError):
            return None
    tested = {k: str(data.get(k, "")) for k in ("head", "main", "tree")}
    return tested if all(tested.values()) else None


def find_green_base(repo: str, branch: str, head: str) -> dict:
    """The last green run's rebuilt base and its parents, or a reason there is none."""
    runs = gh_json(
        f"repos/{repo}/actions/workflows/ci.yml/runs?event=pull_request&status=success"
        f"&branch={urllib.parse.quote(branch, safe='')}&per_page=30"
    ).get("workflow_runs", [])
    for run in runs:
        sha = run.get("head_sha", "")
        if not sha or sha == head or not is_commit(sha):
            continue
        if subprocess.run(["git", "merge-base", "--is-ancestor", sha, head], cwd=REPO_ROOT).returncode != 0:
            continue
        tested = tested_commits(run["id"])
        if tested is None:
            return {"reason": f"the last green run ({run['id']}) recorded no tested commits"}
        if not is_commit(tested["main"]):
            subprocess.run(["git", "fetch", "--quiet", "origin", tested["main"]], cwd=REPO_ROOT, capture_output=True)
        try:
            base = rebuild(tested["head"], tested["main"])
        except SystemExit as err:
            return {"reason": str(err)}
        # The rebuilt merge must be the tree that run tested. GitHub merges
        # the same way as git's ort strategy for a clean merge; when it did
        # not, a diff from the rebuilt tree would not be the change, so the
        # run is full.
        if git("rev-parse", base + "^{tree}") != tested["tree"]:
            return {"reason": f"the merge rebuilt from run {run['id']} is not the tree it tested"}
        return {"base": base, "old_head": tested["head"], "old_main": tested["main"],
                "why": f"run {run['id']} at {sha[:10]}"}
    return {"reason": "the pull request has no green run on an ancestor of this head"}


def ci_base() -> int:
    event = os.environ.get("GITHUB_EVENT_NAME", "")
    action = os.environ.get("EVENT_ACTION", "")
    full = {"mode": "full", "old_head": "", "old_main": ""}
    if event != "pull_request":
        write_outputs({**full, "reason": f"a {event or 'non-PR'} event runs everything"})
        return 0
    if action != "synchronize":
        write_outputs({**full, "reason": f"a pull request's {action or 'first'} run runs everything"})
        return 0
    head = os.environ.get("PR_HEAD_SHA", "")
    try:
        found = find_green_base(os.environ["GITHUB_REPOSITORY"], os.environ.get("PR_HEAD_REF", ""), head)
    except (KeyError, subprocess.CalledProcessError, ValueError) as err:
        found = {"reason": f"the last green run could not be read ({err})"}
    if "base" not in found:
        write_outputs({**full, "reason": found["reason"]})
        return 0
    os.environ["DELTA_BASE"] = found["base"]
    if reason := full_run_reason(changed_files(found["base"])):
        write_outputs({**full, "reason": reason})
        return 0
    write_outputs({
        "mode": "delta", "old_head": found["old_head"], "old_main": found["old_main"],
        "reason": f"the change since the last green run ({found['why']})",
    })
    return 0


def record_tested() -> int:
    """The commits a pull_request run tests: the PR head, the main tip GitHub
    merged it with, and the merge's tree. The checkout is the merge ref, whose
    parents are those two commits."""
    head = os.environ.get("PR_HEAD_SHA", "")
    parents = git("rev-list", "--parents", "-n", "1", "HEAD").split()[1:]
    if len(parents) != 2 or head not in parents:
        main = ""
    else:
        main = parents[0] if parents[1] == head else parents[1]
    print(json.dumps({"head": head, "main": main, "tree": git("rev-parse", "HEAD^{tree}")}))
    return 0


# The jobs a delta run narrows, in the words the run summary uses.
SUMMARY_JOBS = [
    ("Lint", "always; on a delta run, the issues in the change"),
    ("Test", "always; on a delta run, the affected packages and their importers, the changed tests' schedule lane, "
             "the affected RealDB tests, and the change's patch coverage"),
    ("Build", "always"),
    ("Frontend Build / Frontend E2E", "on a delta run, only when the change touches ui/"),
    ("Security Scan", "always; on a delta run, gosec over the packages the change touches"),
    ("Acceptance", "the criteria of the tickets the change touches"),
]


def summary(mode: str, reason: str) -> int:
    """The run summary: whether the run is full, why, and what each job runs."""
    print(f"## CI scope: {mode}\n\n{reason}.\n")
    if mode != "delta":
        print("Every job runs in full.")
        return 0
    touched = changed_files(delta_base())
    ui_touched = any(f.startswith("ui/") for f in touched)
    print(f"{len(touched)} path(s) changed since the last green run.\n")
    print("| Job | What runs |\n| --- | --- |")
    for job, what in SUMMARY_JOBS:
        if job.startswith("Frontend"):
            what = "runs: the change touches ui/" if ui_touched else "skipped: the change touches nothing under ui/"
        print(f"| {job} | {what} |")
    return 0


def tree_of_working_copy() -> str:
    """The tree `make verify` writes its sentinel from: HEAD plus `git add -A`."""
    with tempfile.TemporaryDirectory() as tmp:
        env = {"GIT_INDEX_FILE": str(Path(tmp) / "index")}
        git("read-tree", "HEAD", env=env)
        git("add", "-A", env=env)
        return git("write-tree", env=env)


def record_local() -> int:
    commit = git("commit-tree", tree_of_working_copy(), "-p", "HEAD", "-m", "verify: green tree", env=FIXED_ENV)
    LOCAL_RECORD.parent.mkdir(parents=True, exist_ok=True)
    LOCAL_RECORD.write_text(commit + "\n", encoding="utf-8")
    print(f"Wrote {LOCAL_RECORD.relative_to(REPO_ROOT)} ({commit[:12]}): the next verify checks the change since it")
    return 0


def local_base() -> int:
    """The recorded green tree, when the next verify can narrow to the change since it."""
    if os.environ.get("VERIFY_FULL", "") not in ("", "0"):
        print("verify: VERIFY_FULL is set; running everything", file=sys.stderr)
        return 0
    try:
        base = LOCAL_RECORD.read_text(encoding="utf-8").strip()
    except OSError:
        return 0
    if not is_commit(base):
        print("verify: the last green tree is no longer in this clone; running everything", file=sys.stderr)
        return 0
    files = changed_files(base)
    if reason := full_run_reason(files):
        print(f"verify: {reason}; running everything", file=sys.stderr)
        return 0
    print(f"verify: checking the change since the last green verify ({base[:12]}, {len(files)} path(s))", file=sys.stderr)
    print(base)
    return 0


# --- what the delta affects ---------------------------------------------------


def go_packages(tags: str) -> list[dict]:
    args = ["go", "list", "-e", "-json=Dir,ImportPath,Deps,TestImports,XTestImports,GoFiles,TestGoFiles,XTestGoFiles"]
    if tags:
        args.append("-tags=" + tags)
    out = subprocess.run(args + ["./..."], cwd=REPO_ROOT, check=True, capture_output=True, text=True).stdout
    pkgs, decoder, pos = [], json.JSONDecoder(), 0
    text = out.rstrip()
    while pos < len(text):
        pkg, pos = decoder.raw_decode(text, pos)
        while pos < len(text) and text[pos].isspace():
            pos += 1
        pkgs.append(pkg)
    return pkgs


def owning_dirs(files: list[str], pkg_dirs: set[str]) -> tuple[set[str], bool]:
    """The package directory each path sits in or under, and whether any path
    sits in none: a file outside every package (the Makefile, docs, scripts,
    ui/) is read by the repository-wide tests under test/."""
    root = str(REPO_ROOT)
    owned, outside = set(), False
    for rel in files:
        d = os.path.normpath(str((REPO_ROOT / rel).parent))
        while True:
            # The root is a package too, but only its own Go files belong to
            # it: the Makefile, docs/ and scripts/ are read by the tests under
            # test/, not by the root package.
            if len(d) <= len(root):
                if d == root and rel.endswith(".go") and root in pkg_dirs:
                    owned.add(root)
                else:
                    outside = True
                break
            if d in pkg_dirs:
                owned.add(d)
                break
            d = os.path.dirname(d)
    return owned, outside


def affected(pkgs: list[dict], files: list[str]) -> list[str]:
    """Import paths whose package changed, or that import one (from a test too)."""
    dirs = {os.path.normpath(p["Dir"]): p["ImportPath"] for p in pkgs}
    owned, outside = owning_dirs(files, set(dirs))
    changed = {dirs[d] for d in owned}
    if outside:
        changed.update(p["ImportPath"] for p in pkgs if p["ImportPath"].startswith(MODULE + "/test/"))
    selected = []
    for p in pkgs:
        reach = {p["ImportPath"], *p.get("Deps", []), *p.get("TestImports", []), *p.get("XTestImports", [])}
        if reach & changed:
            selected.append(p["ImportPath"])
    return sorted(selected)


def runnable(p: dict) -> bool:
    """A package `go test` can be named on in the default build."""
    return bool(p.get("GoFiles") or p.get("TestGoFiles") or p.get("XTestGoFiles"))


def packages() -> int:
    base = delta_base()
    if not base:
        print("./...")
        return 0
    pkgs = [p for p in go_packages("") if runnable(p)]
    print(" ".join(affected(pkgs, changed_files(base))))
    return 0


def tagged_affected(path: str) -> int:
    base = delta_base()
    if not base:
        return 0
    want = path if path.startswith(MODULE) else MODULE + "/" + path.lstrip("./").rstrip("/")
    return 0 if want in affected(go_packages("integration"), changed_files(base)) else 1


def ui() -> int:
    base = delta_base()
    if not base:
        return 0
    return 0 if any(f.startswith("ui/") for f in changed_files(base)) else 1


def files() -> int:
    base = delta_base()
    if base:
        print("\n".join(changed_files(base)))
    return 0


def main(argv: list[str]) -> int:
    commands = {
        "ci-base": lambda: ci_base(),
        "record-tested": lambda: record_tested(),
        "record-local": lambda: record_local(),
        "local-base": lambda: local_base(),
        "packages": lambda: packages(),
        "ui": lambda: ui(),
        "files": lambda: files(),
    }
    if len(argv) == 4 and argv[1] == "rebuild":
        print(rebuild(argv[2], argv[3]))
        return 0
    if len(argv) == 4 and argv[1] == "summary":
        return summary(argv[2], argv[3])
    if len(argv) == 3 and argv[1] == "tagged-affected":
        return tagged_affected(argv[2])
    if len(argv) != 2 or argv[1] not in commands:
        print(__doc__, file=sys.stderr)
        return 2
    return commands[argv[1]]()


if __name__ == "__main__":
    sys.exit(main(sys.argv))
