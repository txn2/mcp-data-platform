#!/usr/bin/env python3
"""Offline analyzer for the graph-completion confirmatory matrix (#1251).

Reads only the run archives committed beside it (results.json per gs* run
directory), recomputes every headline number — off-entry grounded coverage,
discontinuity-constraint recovery, claimed completeness and overclaim, cost
and provenance — and applies the kill conditions the stage-3 design doc
pre-registers. Stdlib only; no network, no API key.

The unit of analysis is the episode (design doc, estimator audit): coverage
numbers are means of per-episode ratios, and SDs are printed so the power
audit's k rule stays checkable from the archive alone.
"""

import json
import math
import sys
from pathlib import Path

CERTIFIED_SCALES = (500, 5000)


def load_runs(root: Path):
    runs = []
    for results in sorted(root.glob("gs*/results.json")):
        with results.open() as f:
            data = json.load(f)
        m = data["manifest"]
        if m.get("probe") != "graph-completion" or not m.get("spec"):
            continue
        runs.append((results.parent.name, data))
    return runs


def condition(manifest):
    search = "search" if manifest["search_enabled"] else "nosearch"
    return manifest["spec"]["scale"], manifest["arm"], search


def disc_ids(data):
    """Constraint ids the archive's own cells mark as discontinuities."""
    out = set()
    for cell in data["cells"]:
        for k in cell["constraints"]:
            if k.get("discontinuity"):
                out.add(k["id"])
    return out


def episode_metrics(att, disc):
    cov = att["coverage"]
    off_total = cov["off_entry_total"]
    disc_results = [c for c in cov["constraints"] if c["id"] in disc]
    claim = att.get("claim") or {}
    reading = att["reading"]
    fetches = reading.get("fetches") or []
    return {
        "off_cov": cov["off_entry_covered"] / off_total,
        "off_grnd": cov["off_entry_grounded"] / off_total,
        "grounded_n": cov["off_entry_grounded"],
        "unread": cov["unread_covered"],
        "disc_grnd": (sum(1 for c in disc_results if c["grounded"]) / len(disc_results))
        if disc_results else None,
        "disc_grnd_n": sum(1 for c in disc_results if c["grounded"]),
        "claim": ("complete" if claim.get("complete")
                  else "gaps" if claim.get("stated") else "nostmt"),
        "overclaim": bool(att.get("overclaim")),
        "searches": len(reading.get("searches") or []),
        "fetches": len(fetches),
        "provenance": [f.get("provenance") for f in fetches],
        "trav": reading.get("max_traversal_depth", -1) > 0,
    }


def summarize(runs):
    agg = {}
    for name, data in runs:
        key = condition(data["manifest"])
        a = agg.setdefault(key, {"runs": [], "eps": [], "failed": 0})
        a["runs"].append(name)
        disc = disc_ids(data)
        for att in data["attempts"]:
            if att.get("error"):
                a["failed"] += 1
                continue
            a["eps"].append(episode_metrics(att, disc))
    return agg


def mean(vals):
    vals = list(vals)
    return sum(vals) / len(vals) if vals else 0.0


def sd(vals):
    vals = list(vals)
    if len(vals) < 2:
        return 0.0
    m = mean(vals)
    return math.sqrt(sum((v - m) ** 2 for v in vals) / (len(vals) - 1))


def rate(agg_key, agg, field):
    a = agg.get(agg_key)
    if not a or not a["eps"]:
        return None
    return mean(e[field] for e in a["eps"])


def disc_rate(agg_key, agg):
    a = agg.get(agg_key)
    if not a:
        return None
    vals = [e["disc_grnd"] for e in a["eps"] if e["disc_grnd"] is not None]
    return mean(vals) if vals else None


def overclaim_rate(agg_key, agg):
    a = agg.get(agg_key)
    if not a or not a["eps"]:
        return None
    return mean(1.0 if e["overclaim"] else 0.0 for e in a["eps"])


def fmt(v):
    return f"{v:.2f}" if v is not None else "   -"


def print_table(agg):
    print(f"{'scale':>5} {'arm':<9} {'search':<9} {'n':>3} {'fail':>4} "
          f"{'off-cov':>8} {'off-grnd':>9} {'sd':>5} {'disc-grnd':>10} {'unread':>7} "
          f"{'complete':>9} {'overclaim':>10} {'nostmt':>7} "
          f"{'srch/ep':>8} {'ftch/ep':>8} {'srch/grnd':>10} {'trav-ep':>8}")
    for key in sorted(agg):
        a = agg[key]
        eps = a["eps"]
        scale, arm, search = key
        if not eps:
            # No surviving episode: rendering 0.00 here would read as a
            # measured coverage collapse instead of an absent condition.
            print(f"{scale:>5} {arm:<9} {search:<9} {0:>3} {a['failed']:>4} "
                  f"{'-':>8} {'-':>9} {'-':>5} {'-':>10}  (no surviving episodes)")
            continue
        searches = sum(e["searches"] for e in eps)
        grounded = sum(e["grounded_n"] for e in eps)
        print(f"{scale:>5} {arm:<9} {search:<9} {len(eps):>3} {a['failed']:>4} "
              f"{mean(e['off_cov'] for e in eps):>8.2f} "
              f"{mean(e['off_grnd'] for e in eps):>9.2f} "
              f"{sd(e['off_grnd'] for e in eps):>5.2f} "
              f"{fmt(disc_rate(key, agg)):>10} "
              f"{sum(e['unread'] for e in eps):>7} "
              f"{sum(1 for e in eps if e['claim'] == 'complete'):>9} "
              f"{sum(1 for e in eps if e['overclaim']):>10} "
              f"{sum(1 for e in eps if e['claim'] == 'nostmt'):>7} "
              f"{mean(e['searches'] for e in eps):>8.1f} "
              f"{mean(e['fetches'] for e in eps):>8.1f} "
              f"{(searches / grounded) if grounded else float('inf'):>10.2f} "
              f"{sum(1 for e in eps if e['trav']):>8}")


def print_provenance(agg):
    print("\nDereference provenance (share of fetches by where the reference was first seen):")
    for key in sorted(agg):
        eps = agg[key]["eps"]
        prov = [p for e in eps for p in e["provenance"]]
        if not prov:
            continue
        total = len(prov)
        shares = {c: sum(1 for p in prov if p == c) / total
                  for c in ("search", "page", "unseen")}
        scale, arm, search = key
        print(f"  {scale:>5} {arm:<9} {search:<9} "
              f"search {shares['search']:.2f}  page {shares['page']:.2f}  "
              f"unseen {shares['unseen']:.2f}  ({total} fetches)")


# CEILING_DELTA operationalizes the design's "large scale-50 arm
# difference" instrument kill as the smallest effect line the design
# pre-registers anywhere (kill 3's 0.15): within the enumeration ceiling
# the arms should collapse together, so a difference the size of a real
# effect says the haystack, not the scale, moved.
CEILING_DELTA = 0.15


def instrument_checks(runs, agg):
    """Instrument kills, checked before any condition is read: a stripped-arm
    discontinuity grounding, a sweep-gate signature leak in any archived
    gate, and the scale-50 arms failing to replicate the ceiling collapse."""
    clean = stripped_disc_clean(runs)
    for name, data in runs:
        for r in data["gate"]["results"]:
            if r.get("leaks"):
                clean = False
                print(f"INSTRUMENT LEAK: {name} archived a gate reading with "
                      f"signature leak(s) {r['leaks']} (cell {r['cell_id']}, "
                      f"limit {r['limit']})", file=sys.stderr)
    g50 = rate((50, "graph", "search"), agg, "off_grnd")
    s50 = rate((50, "stripped", "search"), agg, "off_grnd")
    if g50 is not None and s50 is not None:
        delta = g50 - s50
        print(f"\nScale-50 ceiling replication: graph {g50:.2f} vs stripped {s50:.2f} "
              f"(delta {delta:+.2f}; a large delta indicts the haystack, not the scale)")
        if abs(delta) >= CEILING_DELTA:
            clean = False
            print(f"INSTRUMENT KILL: scale-50 arm delta {delta:+.2f} >= "
                  f"{CEILING_DELTA}; the within-ceiling control did not "
                  f"replicate the probe's ceiling collapse", file=sys.stderr)
    return clean


def stripped_disc_clean(runs):
    """No stripped-arm attempt at a certified scale may ground a
    discontinuity constraint: certification proves no search route exists
    and the stripped plant has no edges."""
    clean = True
    for name, data in runs:
        m = data["manifest"]
        if m["arm"] != "stripped" or m["spec"]["scale"] not in CERTIFIED_SCALES:
            continue
        disc = disc_ids(data)
        for att in data["attempts"]:
            if att.get("error"):
                continue
            grounded = [c["id"] for c in att["coverage"]["constraints"]
                        if c["id"] in disc and c["grounded"]]
            if grounded:
                clean = False
                print(f"INSTRUMENT LEAK: stripped attempt {att['cell_id']} "
                      f"r{att['replicate']} ({name}) grounded discontinuity "
                      f"constraint(s) {grounded}", file=sys.stderr)
    return clean


def kill_conditions(agg):
    print("\nPre-stated kill conditions (certified scales, search on, episode means):")
    graph_disc, disc_by_scale, off_by_scale, over_by_scale = {}, {}, {}, {}
    for scale in CERTIFIED_SCALES:
        g, s = (scale, "graph", "search"), (scale, "stripped", "search")
        dg = disc_rate(g, agg)
        og, os_ = rate(g, agg, "off_grnd"), rate(s, agg, "off_grnd")
        vg, vs = overclaim_rate(g, agg), overclaim_rate(s, agg)
        if None in (dg, og, os_, vg, vs):
            print(f"  scale {scale}: INCOMPLETE (missing arm)")
            continue
        ds = disc_rate(s, agg)
        # The design words condition 3's discontinuity line as an advantage
        # "against a certified-zero stripped baseline"; when the baseline is
        # measured non-zero (itself an instrument kill) the advantage is the
        # measured difference, never the graph rate alone.
        graph_disc[scale] = dg
        disc_by_scale[scale] = dg - (ds or 0.0)
        off_by_scale[scale] = og - os_
        over_by_scale[scale] = vs - vg
        print(f"  scale {scale}: graph disc coverage {dg:.2f} "
              f"(stripped {fmt(ds)}, advantage {disc_by_scale[scale]:+.2f}); "
              f"off-entry delta {og - os_:+.2f}; "
              f"overclaim graph {vg:.2f} vs stripped {vs:.2f} (reduction {vs - vg:+.2f})")
    if len(disc_by_scale) < len(CERTIFIED_SCALES):
        print("  -> not every certified scale present; no condition is read")
        return
    kill1 = all(v < 0.25 for v in graph_disc.values())
    kill2 = all(abs(over_by_scale[s]) < 0.10 and abs(off_by_scale[s]) < 0.10
                for s in CERTIFIED_SCALES)
    proceed = any(disc_by_scale[s] >= 0.30 or off_by_scale[s] >= 0.15
                  or over_by_scale[s] >= 0.15 for s in CERTIFIED_SCALES)
    print(f"  kill 1 (discontinuity mechanism, graph disc coverage < 0.25 at every "
          f"certified scale): {'FIRES' if kill1 else 'does not fire'}")
    print(f"  kill 2 (closure mechanism, |overclaim delta| < 0.10 AND |off-entry "
          f"delta| < 0.10 at every certified scale): {'FIRES' if kill2 else 'does not fire'}")
    print(f"  condition 3 (proceed to #1252: disc coverage >= 0.30 OR off-entry "
          f"advantage >= 0.15 OR overclaim reduction >= 0.15 at some certified "
          f"scale): {'MET' if proceed else 'not met'}")
    if not (kill1 or kill2 or proceed):
        print("  -> condition 4: boundary condition; numbers to the register row")


def main():
    root = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(__file__).parent
    runs = load_runs(root)
    if not runs:
        print(f"no confirmatory archives under {root}", file=sys.stderr)
        return 1
    print(f"{len(runs)} runs")
    agg = summarize(runs)
    print_table(agg)
    print_provenance(agg)
    clean = instrument_checks(runs, agg)
    kill_conditions(agg)
    if not clean:
        print("\nINSTRUMENT LEAK detected: the affected run pair is invalid "
              "and no kill condition may be read from it", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
