#!/usr/bin/env python3
"""Offline analyzer for the graph-completion probe (#1241).

Reads only the run archives committed beside it (results.json per run
directory), recomputes the per-arm coverage and cost tables, and prints the
contrasts the pre-stated kill conditions are read from. Stdlib only; no
network, no API key.
"""

import json
import sys
from pathlib import Path


def load_runs(root: Path):
    runs = []
    for results in sorted(root.glob("gc-*/results.json")):
        with results.open() as f:
            data = json.load(f)
        m = data["manifest"]
        if m.get("probe") != "graph-completion":
            continue
        runs.append((results.parent.name, data))
    return runs


def condition(manifest):
    search = "search" if manifest["search_enabled"] else "nosearch"
    return manifest["arm"], search, manifest["model"]


def summarize(runs):
    # (arm, search, model) -> aggregate
    agg = {}
    for name, data in runs:
        key = condition(data["manifest"])
        a = agg.setdefault(key, {
            "runs": [], "n": 0, "failed": 0,
            "off_cov": 0, "off_grnd": 0, "off_total": 0, "unread": 0,
            "entry_cov": 0, "entry_total": 0,
            "searches": 0, "fetches": 0, "trav": 0,
        })
        a["runs"].append(name)
        for att in data["attempts"]:
            if att.get("error"):
                a["failed"] += 1
                continue
            a["n"] += 1
            cov = att["coverage"]
            a["off_cov"] += cov["off_entry_covered"]
            a["off_grnd"] += cov["off_entry_grounded"]
            a["off_total"] += cov["off_entry_total"]
            a["unread"] += cov["unread_covered"]
            a["entry_cov"] += cov["entry_covered"]
            a["entry_total"] += cov["entry_total"]
            r = att["reading"]
            a["searches"] += len(r.get("searches") or [])
            a["fetches"] += len(r.get("fetches") or [])
            if r.get("max_traversal_depth", -1) > 0:
                a["trav"] += 1
        return_check(data)
    return agg


def return_check(data):
    # Instrument kill: any off-entry grounded coverage in stripped/no-search.
    m = data["manifest"]
    if m["arm"] == "stripped" and not m["search_enabled"]:
        for att in data["attempts"]:
            if not att.get("error") and att["coverage"]["off_entry_grounded"] > 0:
                print(f"INSTRUMENT LEAK: stripped/no-search attempt "
                      f"{att['cell_id']} r{att['replicate']} grounded off-entry coverage "
                      f"{att['coverage']['off_entry_grounded']}", file=sys.stderr)


def ratio(n, d):
    return n / d if d else 0.0


def print_table(agg):
    hdr = (f"{'arm':<9} {'search':<9} {'model':<7} {'n':>3} {'fail':>4} "
           f"{'off-cov':>8} {'off-grnd':>9} {'unread':>7} {'entry':>6} "
           f"{'srch/ep':>8} {'ftch/ep':>8} {'srch/grnd':>10} {'trav-ep':>8}")
    print(hdr)
    for key in sorted(agg):
        a = agg[key]
        arm, search, model = key
        print(f"{arm:<9} {search:<9} {model:<7} {a['n']:>3} {a['failed']:>4} "
              f"{ratio(a['off_cov'], a['off_total']):>8.2f} "
              f"{ratio(a['off_grnd'], a['off_total']):>9.2f} "
              f"{a['unread']:>7} "
              f"{ratio(a['entry_cov'], a['entry_total']):>6.2f} "
              f"{ratio(a['searches'], a['n']):>8.1f} "
              f"{ratio(a['fetches'], a['n']):>8.1f} "
              f"{ratio(a['searches'], a['off_grnd']) if a['off_grnd'] else float('inf'):>10.2f} "
              f"{a['trav']:>8}")


def print_contrasts(agg):
    models = sorted({k[2] for k in agg})
    print("\nKill-condition contrasts (off-entry grounded coverage):")
    for model in models:
        gn = agg.get(("graph", "nosearch", model))
        gs = agg.get(("graph", "search", model))
        ss = agg.get(("stripped", "search", model))
        if gn:
            cov = ratio(gn["off_grnd"], gn["off_total"])
            print(f"  {model}: graph/no-search coverage = {cov:.2f} "
                  f"(kill 1 floor at 0.25, condition 3 needs 0.50)")
        if gs and ss:
            d = ratio(gs["off_grnd"], gs["off_total"]) - ratio(ss["off_grnd"], ss["off_total"])
            sg = ratio(gs["searches"], gs["off_grnd"]) if gs["off_grnd"] else float("inf")
            sst = ratio(ss["searches"], ss["off_grnd"]) if ss["off_grnd"] else float("inf")
            print(f"  {model}: search-test coverage delta (graph - stripped) = {d:+.2f}; "
                  f"searches per grounded constraint {sg:.2f} vs {sst:.2f}")


def per_cell(runs):
    print("\nPer-cell off-entry grounded coverage:")
    cells = {}
    for _, data in runs:
        key = condition(data["manifest"])
        for att in data["attempts"]:
            if att.get("error"):
                continue
            c = cells.setdefault((att["cell_id"], *key), [0, 0])
            c[0] += att["coverage"]["off_entry_grounded"]
            c[1] += att["coverage"]["off_entry_total"]
    for k in sorted(cells):
        g, t = cells[k]
        cell, arm, search, model = k
        print(f"  {cell:<22} {arm:<9} {search:<9} {model:<7} {ratio(g, t):.2f}")


def main():
    root = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(__file__).parent
    runs = load_runs(root)
    if not runs:
        print(f"no graph-completion archives under {root}", file=sys.stderr)
        return 1
    print(f"{len(runs)} runs")
    agg = summarize(runs)
    print_table(agg)
    print_contrasts(agg)
    per_cell(runs)
    return 0


if __name__ == "__main__":
    sys.exit(main())
