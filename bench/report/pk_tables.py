#!/usr/bin/env python3
"""Recompute every table in benchmark report 2 from the committed run data.

Reads bench/results/pk-*/ and bench/results/s5-supersede-probe/ and prints
the report's tables with Wilson 95% intervals. No network, no API key: the
same contract report 1's notebook honors. Run from the repository root:

    python3 bench/report/pk_tables.py
"""
import glob
import json
import math
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
RESULTS = os.path.join(ROOT, "results")


def wilson(k, n, z=1.96):
    if n == 0:
        return (0.0, 0.0)
    p = k / n
    d = 1 + z * z / n
    c = (p + z * z / (2 * n)) / d
    h = z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n)) / d
    return (max(0.0, c - h), min(1.0, c + h))


def fmt(k, n):
    lo, hi = wilson(k, n)
    return f"{k}/{n} ({100*k/n:.0f}%, CI [{100*lo:.0f}, {100*hi:.0f}])" if n else "-"


def load(pattern):
    out = []
    for f in sorted(glob.glob(os.path.join(RESULTS, pattern, "results.json"))):
        r = json.load(open(f))
        r["_dir"] = os.path.basename(os.path.dirname(f))
        out.append(r)
    return out


def clean(attempts):
    return [a for a in attempts if not a.get("error")]


def tally(attempts, pred=lambda a: True):
    sel = [a for a in clean(attempts) if pred(a)]
    n = len(sel)
    ver = sum(1 for a in sel if a["outcome"]["observation"]["verified"])
    tru = sum(1 for a in sel if a.get("trusted"))
    cor = sum(1 for a in sel if a["outcome"].get("correct") is True)
    return n, ver, tru, cor


def final_line(a):
    m = re.search(r"FINAL ANSWER:\s*(.+)$", a.get("final_answer", ""), re.M)
    return (m.group(1).strip() if m else "").split()[0] if m else ""


def section(title):
    print(f"\n=== {title} ===")


def by_driver(runs):
    for r in runs:
        model = r["manifest"]["model"]
        yield r["_dir"], model, r["attempts"]


def main():
    section("T1: strong-model insensitivity (sonnet, claude-cli)")
    for r in load("pk-prerun/pk-prerun-20260725-002128"):
        for world in ("monitors-0", "monitors-3"):
            n, v, t, c = tally(r["attempts"], lambda a, w=world: a["query_world"] == w)
            print(f"  prerun {world}: verified {fmt(v,n)}  correct {fmt(c,n)}")
    for r in load("pk-costsweep/*"):
        for world in ("monitors-0", "monitors-0-scoped", "monitors-0-scoped-5", "monitors-0-scoped-10"):
            sel = [a for a in clean(r["attempts"]) if a["query_world"] == world]
            calls = sorted(a["outcome"]["observation"]["calls"] for a in sel)
            n, v, t, c = tally(r["attempts"], lambda a, w=world: a["query_world"] == w)
            med = calls[len(calls)//2] if calls else 0
            print(f"  costsweep {world}: verified {fmt(v,n)}  median calls {med}")

    section("T2: answer sweep, belief vs control, by model and driver")
    for run_dir, model, attempts in by_driver(load("pk-answersweep/*")):
        for cond, pred in (("belief", lambda a: a.get("seed_id")), ("none", lambda a: not a.get("seed_id"))):
            n, v, t, c = tally(attempts, pred)
            print(f"  {run_dir} [{model}] {cond}: n={n} verified {fmt(v,n)} trusted {fmt(t,n)} correct {fmt(c,n)}")

    section("T3: derivability bridge, by model and driver")
    for run_dir, model, attempts in by_driver(load("pk-bridge/*")):
        note = [a for a in clean(attempts) if a.get("seed_id")]
        ctrl = [a for a in clean(attempts) if not a.get("seed_id")]
        used = sum(1 for a in note if a["outcome"].get("correct") is True)
        fab = sum(1 for a in ctrl if not a["outcome"]["refused"])
        leak = sum(1 for a in ctrl if final_line(a) == "11")
        failed = len(attempts) - len(clean(attempts))
        print(f"  {run_dir} [{model}]: convention used {fmt(used,len(note))}  control fabricated {fmt(fab,len(ctrl))}  leaked {leak}  failures {failed}")

    section("T4: stale answer-bearing note, by model")
    for run_dir, model, attempts in by_driver(load("pk-staleanswer/*")):
        for cond, pred in (("stale note", lambda a: a.get("seed_id")), ("no note", lambda a: not a.get("seed_id"))):
            sel = [a for a in clean(attempts) if pred(a)]
            n, v, t, c = tally(attempts, pred)
            wrong3 = sum(1 for a in sel if final_line(a) == "3")
            print(f"  {run_dir} [{model}] {cond}: n={n} verified {fmt(v,n)} trusted {fmt(t,n)} correct {fmt(c,n)} answered-stale-3 {wrong3}")

    section("T5: haiku prerun (stale unavailability note)")
    for r in load("pk-prerun/pk-prerun-haiku-*"):
        for world in ("monitors-0", "monitors-3"):
            n, v, t, c = tally(r["attempts"], lambda a, w=world: a["query_world"] == w)
            print(f"  {world}: verified {fmt(v,n)} trusted {fmt(t,n)} correct {fmt(c,n)}")

    section("T6: S5 capture probe decomposition")
    f = glob.glob(os.path.join(RESULTS, "s5-supersede-probe", "*", "supersede-a3.json"))
    if f:
        r = json.load(open(f[0]))
        m = r.get("metrics", {})
        print("  metrics keys:", {k: v for k, v in m.items() if isinstance(v, (int, float))})
    print("  (capture decomposition: see the run README; derived from per-run records + the run database)")


if __name__ == "__main__":
    sys.exit(main())
