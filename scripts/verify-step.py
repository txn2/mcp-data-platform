#!/usr/bin/env python3
"""Run one step of make verify, skip it when its inputs already passed, and
print what it cost (#2369, #2548).

    verify-step.py [--keep-going] [--key <name>] <make> <target>
    verify-step.py --begin
    verify-step.py --report

A step runs `<make> --no-print-directory <target>`, its output shown as it
comes and kept at build/verify-cache/<step>.log, and prints one line as it
ends:

    [step <target>: 12.3s ok]

or `failed (<code>)` in place of ok, and exits with the step's code.

Inputs. The fifth column of scripts/verify-steps.tsv names what a step reads.
Before the step runs, a digest is taken of:

  - those inputs, as the working tree holds them, tracked or not (an ignored
    file is not part of the tree and never counts);
  - what every step reads: the Makefile, which holds every recipe and tool
    pin, scripts/, and the Go and Node versions;
  - the base a diff-scoped step reads (the merge base with main, DELTA_BASE);
  - the variables make was given on its command line, and the few the
    Makefile takes from the environment.

A step that passed on the same digest is not run again:

    [step lint: skipped, its inputs passed at 14:03:12 (387.8s)]

so a re-run costs the steps that failed and the steps whose inputs the fix
touched. VERIFY_FULL=1 runs every step. A step whose column is "-" always
runs: it reads something outside the tree (a running stack, a database fetched
over the network, recorded acceptance runs), prints what a reader needs when
it passes, or costs less than taking the digest.

Column tokens, separated by spaces:

    <path>        a file or directory of the working tree; "." is all of it
    @go           go.mod, go.sum and every top-level directory holding Go
    tool:semgrep  semgrep's version (the only tool not pinned in the Makefile)
    out:<path>    a file the step writes for another step to read; a skip
                  requires it unchanged since the pass
    replay        a failure on the same digest is reported without running
                  again, when its output carries the linter's issue count

--keep-going records a failed step and exits 0, so the next step runs;
--report prints every recorded failure with its output and exits 1 when there
is one. --begin clears the record. make verify runs its serial preamble that
way (the steps that rewrite the tree still stop it), then --report, so one run
reports every failing step and the lanes do not start after one.

--key <name> keeps the digest and the record under name rather than target:
`make lint` runs lint-run under the key lint, so verify and a lint run by
hand share one record. When the target is already running under that key
(VERIFY_STEP in the environment, which this sets for the step it runs), it
runs as it is, with no second digest and no second line.
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
from datetime import datetime
from pathlib import Path

CACHE = Path("build/verify-cache")
RUN = CACHE / "run-failed.tsv"
TABLE = Path("scripts/verify-steps.tsv")
EVERY_STEP = ("Makefile", "scripts")
# Variables the Makefile takes from the environment (?=) that change what a
# step checks. A command-line assignment is read from MAKEFLAGS instead.
ENV_INPUTS = ("DELTA_BASE", "REALDB_PKGS", "REALDB_RUN", "COVERAGE_MIN", "PATCH_COVERAGE_MIN")
# golangci-lint's summary when it reports findings: "3 issues:". A failure
# without one (a crash, a timeout, a base it could not reach) is not replayed.
ISSUES = re.compile(rb"^\d+ issues?:", re.M)
REPORT_LINES = 200


def table() -> dict[str, list[str]]:
    """Each step's input tokens, from scripts/verify-steps.tsv."""
    steps: dict[str, list[str]] = {}
    if not TABLE.exists():
        return steps
    for line in TABLE.read_text().splitlines():
        if not line or line.startswith("#"):
            continue
        cols = line.split("\t")
        if len(cols) >= 5:
            steps[cols[1]] = cols[4].split()
    return steps


def git(*args: str, env: dict[str, str] | None = None) -> str:
    out = subprocess.run(["git", *args], capture_output=True, text=True, check=False, env=env)
    return out.stdout if out.returncode == 0 else ""


def tree() -> list[tuple[str, str]]:
    """Every file of the working tree as (path, "mode blob"), tracked or not.

    Built in a copy of the index, so git rehashes only what changed since the
    index last saw it and the real index is left alone.
    """
    index = git("rev-parse", "--git-path", "index").strip()
    with tempfile.TemporaryDirectory() as tmp:
        copy = os.path.join(tmp, "index")
        if index and os.path.exists(index):
            shutil.copyfile(index, copy)
        env = dict(os.environ, GIT_INDEX_FILE=copy)
        subprocess.run(["git", "add", "-A"], capture_output=True, check=False, env=env)
        listing = git("ls-files", "-s", "-z", env=env)
    entries = []
    for rec in listing.split("\0"):
        if "\t" in rec:
            meta, path = rec.split("\t", 1)
            mode, blob, _stage = meta.split(" ")
            entries.append((path, f"{mode} {blob}"))
    return entries


def go_roots(entries: list[tuple[str, str]]) -> set[str]:
    """go.mod, go.sum, the root's Go files and each top-level directory holding Go."""
    roots = {"go.mod", "go.sum"}
    for path, _ in entries:
        if path.endswith(".go"):
            roots.add(path.split("/", 1)[0])
    return roots


def selected(entries: list[tuple[str, str]], paths: set[str]) -> list[str]:
    if "." in paths:
        return [f"{p} {m}" for p, m in entries]
    return [f"{p} {m}" for p, m in entries if p in paths or any(p.startswith(d + "/") for d in paths)]


def version(*cmd: str) -> str:
    try:
        out = subprocess.run(list(cmd), capture_output=True, text=True, timeout=30, check=False)
    except (OSError, subprocess.TimeoutExpired):
        return "absent"
    return out.stdout.strip() or "absent"


def merge_base() -> str:
    for base in ("origin/main", "main"):
        mb = git("merge-base", base, "HEAD").strip()
        if mb:
            return mb
    return ""


def command_line_vars() -> str:
    """The variables make was given on its command line: MAKEFLAGS after " -- "."""
    flags = os.environ.get("MAKEFLAGS", "")
    return flags.split(" -- ", 1)[1] if " -- " in flags else ""


def digest(key: str, tokens: list[str]) -> str:
    entries = tree()
    paths = set(EVERY_STEP)
    h = hashlib.sha256(f"step {key}\n".encode())
    for tok in tokens:
        if tok == "@go":
            paths |= go_roots(entries)
        elif tok == "tool:semgrep":
            h.update(f"semgrep {version('semgrep', '--version')}\n".encode())
        elif tok == "replay" or tok.startswith("out:"):
            continue
        else:
            paths.add(tok.rstrip("/") or ".")
    for line in selected(entries, paths):
        h.update(line.encode() + b"\n")
    h.update(f"go {version('go', 'env', 'GOVERSION')}\nnode {version('node', '--version')}\n".encode())
    h.update(f"base {merge_base()}\nvars {command_line_vars()}\n".encode())
    for name in ENV_INPUTS:
        h.update(f"env {name}={os.environ.get(name, '')}\n".encode())
    return h.hexdigest()


def file_hash(path: str) -> str:
    try:
        return hashlib.sha256(Path(path).read_bytes()).hexdigest()
    except OSError:
        return ""


def outputs(tokens: list[str]) -> dict[str, str]:
    return {t[4:]: file_hash(t[4:]) for t in tokens if t.startswith("out:")}


def record_path(key: str) -> Path:
    return CACHE / f"{key}.json"


def log_path(key: str) -> Path:
    return CACHE / f"{key}.log"


def load(key: str) -> dict | None:
    try:
        return json.loads(record_path(key).read_text())
    except (OSError, ValueError):
        return None


def save(key: str, rec: dict | None) -> None:
    if rec is None:
        record_path(key).unlink(missing_ok=True)
        return
    record_path(key).write_text(json.dumps(rec, indent=1) + "\n")


def full_run() -> bool:
    return os.environ.get("VERIFY_FULL", "") not in ("", "0")


def stream(make: str, target: str, key: str) -> tuple[int, bytes]:
    """Run the target, showing its output as it comes and keeping a copy."""
    env = dict(os.environ, VERIFY_STEP=key)
    proc = subprocess.Popen(
        [make, "--no-print-directory", target], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, env=env,
    )
    kept = bytearray()
    assert proc.stdout is not None
    for chunk in iter(lambda: proc.stdout.read1(65536), b""):  # type: ignore[union-attr]
        sys.stdout.buffer.write(chunk)
        sys.stdout.buffer.flush()
        kept += chunk
    return proc.wait(), bytes(kept)


def failed(key: str, seconds: float, code: int, keep_going: bool) -> int:
    if not keep_going:
        return code
    with RUN.open("a") as f:
        f.write(f"{key}\t{seconds:.1f}\t{code}\n")
    return 0


def cached(key: str, tokens: list[str], d: str, keep_going: bool) -> int | None:
    """The answer a record on this digest gives, or None when the step must run."""
    rec = load(key)
    if full_run() or not rec or rec.get("digest") != d:
        return None
    if rec.get("result") == "pass" and rec.get("outputs", {}) == outputs(tokens):
        print(f"[step {key}: skipped, its inputs passed at {rec['at']} ({rec['seconds']:.1f}s)]", flush=True)
        return 0
    if rec.get("result") == "fail" and "replay" in tokens and log_path(key).exists():
        print(f"[step {key}: its inputs failed at {rec['at']}; that run's output follows, not run again]", flush=True)
        sys.stdout.buffer.write(log_path(key).read_bytes())
        print(f"[step {key}: 0.0s failed ({rec['code']}), replayed]", flush=True)
        return failed(key, 0.0, rec["code"], keep_going)
    return None


def run_step(make: str, target: str, key: str, keep_going: bool) -> int:
    if os.environ.get("VERIFY_STEP") == key:
        return subprocess.call([make, "--no-print-directory", target])
    CACHE.mkdir(parents=True, exist_ok=True)
    tokens = table().get(key, ["-"])
    d = "" if tokens == ["-"] else digest(key, tokens)
    if d and (answer := cached(key, tokens, d, keep_going)) is not None:
        return answer
    start = time.monotonic()
    code, output = stream(make, target, key)
    seconds = time.monotonic() - start
    log_path(key).write_bytes(output)
    at = datetime.now().strftime("%H:%M:%S")
    print(f"[step {key}: {seconds:.1f}s {'ok' if code == 0 else f'failed ({code})'}]", flush=True)
    if code == 0:
        save(key, {"digest": d, "result": "pass", "at": at, "seconds": seconds, "outputs": outputs(tokens)} if d else None)
        return 0
    replay = d and "replay" in tokens and ISSUES.search(output)
    save(key, {"digest": d, "result": "fail", "at": at, "seconds": seconds, "code": code} if replay else None)
    return failed(key, seconds, code, keep_going)


def report() -> int:
    """Every step --keep-going recorded as failed, with its output; 1 when any."""
    if not RUN.exists() or not RUN.read_text().strip():
        return 0
    rows = [line.split("\t") for line in RUN.read_text().splitlines() if line]
    print(f"\n=== {len(rows)} step(s) failed: {', '.join(r[0] for r in rows)} ===", flush=True)
    for key, seconds, code in rows:
        lines = log_path(key).read_text(errors="replace").splitlines() if log_path(key).exists() else []
        shown = lines[-REPORT_LINES:]
        print(f"\n--- {key}: failed ({code}) after {seconds}s; full output in {log_path(key)}", flush=True)
        if len(lines) > len(shown):
            print(f"    (the last {len(shown)} of {len(lines)} lines)")
        print("\n".join(shown), flush=True)
    print("\nThe lanes do not start after a failed step. Fix these and run make verify again:", flush=True)
    print("a step whose inputs did not change since it passed is skipped.", flush=True)
    return 1


def main(argv: list[str]) -> int:
    args = argv[1:]
    if args == ["--begin"]:
        CACHE.mkdir(parents=True, exist_ok=True)
        RUN.unlink(missing_ok=True)
        return 0
    if args == ["--report"]:
        return report()
    keep_going = False
    key = ""
    while args and args[0].startswith("--"):
        if args[0] == "--keep-going":
            keep_going, args = True, args[1:]
        elif args[0] == "--key" and len(args) > 1:
            key, args = args[1], args[2:]
        else:
            break
    if len(args) != 2:
        print(f"usage: {argv[0]} [--keep-going] [--key <name>] <make> <target> | --begin | --report", file=sys.stderr)
        return 2
    make, target = args
    return run_step(make, target, key or target, keep_going)


if __name__ == "__main__":
    sys.exit(main(sys.argv))
