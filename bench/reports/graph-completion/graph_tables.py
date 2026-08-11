#!/usr/bin/env python3
"""Recompute every table in the graph-completion report from committed run data.

Reads the confirmatory archives under bench/results/graph-completion-confirmatory/
and the pilot archives under bench/results/graph-completion-probe/, and prints
the report's tables. No network, no API key, stdlib only: the same contract the
knowledge-use and knowledge-pollution toolchains honor. Run from anywhere:

    python3 bench/reports/graph-completion/graph_tables.py

The metric definitions mirror the archived analyzer
(bench/results/graph-completion-confirmatory/graph-confirmatory-analyze.py).
One deliberate difference: the archived analyzer exits non-zero on these
archives because the pre-registered instrument kill is present in the data
(stripped-arm discontinuity grounding at both certified scales). That is the
study's recorded outcome, not a drift, so this script PINS the kill's presence
- it fails if the leak ever stops reproducing from the archives - and exits
zero when every pin holds. check_pins() also re-runs the archived analyzer and
asserts its exit code is 1, so the recorded outcome is surfaced, never
suppressed.
"""
import glob
import json
import math
import os
import subprocess
import sys

_d = os.path.dirname(os.path.abspath(__file__))
while os.path.basename(_d) != "bench":
    _d = os.path.dirname(_d)
CONFIRMATORY = os.path.join(_d, "results", "graph-completion-confirmatory")
PROBE = os.path.join(_d, "results", "graph-completion-probe")
ANALYZER = os.path.join(CONFIRMATORY, "graph-confirmatory-analyze.py")

# The design doc marks these scales as certified for the discontinuity DV
# (bench/docs/graph-completion-study-design.md, "Twofold discontinuity
# certification"); scale 50 is the within-ceiling control by construction.
CERTIFIED_SCALES = (500, 5000)


def _load(root, pattern, want_spec):
    runs = []
    for path in sorted(glob.glob(os.path.join(root, pattern, "results.json"))):
        with open(path) as f:
            data = json.load(f)
        m = data.get("manifest", {})
        if m.get("probe") != "graph-completion":
            continue
        if want_spec != bool(m.get("spec")):
            continue
        runs.append((os.path.basename(os.path.dirname(path)), data))
    return runs


def load_confirmatory():
    return _load(CONFIRMATORY, "gs*", want_spec=True)


def load_probe():
    return _load(PROBE, "gc-*", want_spec=False)


def disc_ids(data):
    ids = set()
    for cell in data.get("cells", []):
        for c in cell.get("constraints", []):
            if c.get("discontinuity"):
                ids.add(c["id"])
    return ids


def condition(data):
    m = data["manifest"]
    scale = (m.get("spec") or {}).get("scale", m.get("corpus_pages"))
    search = "search" if m.get("search_enabled") else "nosearch"
    return scale, m["arm"], search, m.get("model", "")


def episode_metrics(attempt, disc):
    cov = attempt["coverage"]
    reading = attempt["reading"]
    # Go's encoding/json writes empty slices as null; normalize to [] once.
    searches = reading.get("searches") or []
    fetches = reading.get("fetches") or []
    constraints = cov.get("constraints") or []
    total = cov["off_entry_total"]
    grounded_ids = {c["id"] for c in constraints if c.get("grounded")}
    disc_grounded = [c for c in constraints if c["id"] in disc]
    cl = attempt.get("claim") or {}
    if cl.get("complete"):
        claim = "complete"
    elif cl.get("stated"):
        claim = "gaps"
    else:
        claim = "nostmt"
    return {
        "off_cov": cov["off_entry_covered"] / total if total else 0.0,
        "off_grnd": cov["off_entry_grounded"] / total if total else 0.0,
        "grounded_n": cov["off_entry_grounded"],
        "total_n": total,
        "unread": cov.get("unread_covered", 0),
        "disc_grnd": (sum(1 for c in disc_grounded if c.get("grounded")) / len(disc_grounded))
        if disc_grounded else None,
        "disc_grounded_ids": sorted(grounded_ids & disc),
        "claim": claim,
        "overclaim": bool(attempt.get("overclaim")),
        "searches": len(searches),
        "fetches": len(fetches),
        "provenance": [f.get("provenance", "unseen") for f in fetches],
        "traversal": reading.get("max_traversal_depth", 0) > 0,
    }


def _mean(xs):
    return sum(xs) / len(xs) if xs else 0.0


def _sd(xs):
    if len(xs) < 2:
        return 0.0
    m = _mean(xs)
    return math.sqrt(sum((x - m) ** 2 for x in xs) / (len(xs) - 1))


def aggregate(runs):
    """One row per (scale, arm, search): the analyzer's episode-mean table."""
    agg = {}
    for _, data in runs:
        scale, arm, search, _ = condition(data)
        disc = disc_ids(data)
        row = agg.setdefault((scale, arm, search), {
            "eps": [], "failed": 0, "searches": 0, "grounded": 0, "fetch_prov": []})
        for attempt in data["attempts"]:
            if attempt.get("error"):
                row["failed"] += 1
                continue
            ep = episode_metrics(attempt, disc)
            row["eps"].append(ep)
            row["searches"] += ep["searches"]
            row["grounded"] += ep["grounded_n"]
            row["fetch_prov"].extend(ep["provenance"])
    return agg


def row_stats(row):
    eps = row["eps"]
    disc = [e["disc_grnd"] for e in eps if e["disc_grnd"] is not None]
    prov = row["fetch_prov"]
    total = len(prov) or 1
    return {
        "n": len(eps),
        "fail": row["failed"],
        "off_cov": round(_mean([e["off_cov"] for e in eps]), 2),
        "off_grnd": round(_mean([e["off_grnd"] for e in eps]), 2),
        "sd": round(_sd([e["off_grnd"] for e in eps]), 2),
        "disc_grnd": round(_mean(disc), 2) if disc else None,
        "unread": sum(e["unread"] for e in eps),
        "complete": sum(1 for e in eps if e["claim"] == "complete"),
        "gaps": sum(1 for e in eps if e["claim"] == "gaps"),
        "nostmt": sum(1 for e in eps if e["claim"] == "nostmt"),
        "overclaim": sum(1 for e in eps if e["overclaim"]),
        "srch_ep": round(_mean([e["searches"] for e in eps]), 1),
        "ftch_ep": round(_mean([e["fetches"] for e in eps]), 1),
        "srch_grnd": round(row["searches"] / row["grounded"], 2) if row["grounded"] else None,
        "trav_ep": sum(1 for e in eps if e["traversal"]),
        "prov_search": round(prov.count("search") / total, 2),
        "prov_page": round(prov.count("page") / total, 2),
        "prov_unseen": round(prov.count("unseen") / total, 2),
        "fetch_total": len(prov),
    }


def section(title):
    print()
    print(f"== {title} ==")


def t1_matrix(agg):
    section("T1: the confirmatory matrix (episode means; coverage grounded)")
    print(f"{'scale':>5} {'arm':<9} {'search':<9} {'n':>3} {'fail':>4} "
          f"{'off-cov':>7} {'off-grnd':>8} {'sd':>5} {'disc-grnd':>9} "
          f"{'ovrclm':>6} {'srch/ep':>7} {'ftch/ep':>7} {'srch/grnd':>9} {'trav-ep':>7}")
    for key in sorted(agg):
        s = row_stats(agg[key])
        disc = f"{s['disc_grnd']:.2f}" if s["disc_grnd"] is not None else "-"
        sg = f"{s['srch_grnd']:.2f}" if s["srch_grnd"] is not None else "-"
        print(f"{key[0]:>5} {key[1]:<9} {key[2]:<9} {s['n']:>3} {s['fail']:>4} "
              f"{s['off_cov']:>7.2f} {s['off_grnd']:>8.2f} {s['sd']:>5.2f} {disc:>9} "
              f"{s['overclaim']:>6} {s['srch_ep']:>7.1f} {s['ftch_ep']:>7.1f} "
              f"{sg:>9} {s['trav_ep']:>7}")


def t2_provenance(agg):
    section("T2: dereference provenance (share of fetches by where the reference was first seen)")
    for key in sorted(agg):
        s = row_stats(agg[key])
        print(f"{key[0]:>7} {key[1]:<9} {key[2]:<9} "
              f"search {s['prov_search']:.2f}  page {s['prov_page']:.2f}  "
              f"unseen {s['prov_unseen']:.2f}  ({s['fetch_total']} fetches)")


def t3_claims(agg):
    section("T3: the elicited completeness claim, per condition")
    tot = {"complete": 0, "gaps": 0, "nostmt": 0, "n": 0}
    for key in sorted(agg):
        s = row_stats(agg[key])
        tot["complete"] += s["complete"]
        tot["gaps"] += s["gaps"]
        tot["nostmt"] += s["nostmt"]
        tot["n"] += s["n"]
        print(f"{key[0]:>7} {key[1]:<9} {key[2]:<9} "
              f"complete {s['complete']:>2}  gaps declared {s['gaps']:>2}  "
              f"no statement {s['nostmt']:>2}  (n={s['n']})")
    print(f"  pooled: {tot['complete']} complete, {tot['gaps']} gaps declared, "
          f"{tot['nostmt']} no statement, over {tot['n']} surviving episodes")
    return tot


def t4_kill(runs, agg):
    """The instrument kill, recomputed: it is the recorded outcome, not an error."""
    section("T4: the pre-registered instrument kill, recomputed from the archives")
    leaks = {500: [], 5000: []}
    for name, data in runs:
        scale, arm, search, _ = condition(data)
        if arm != "stripped" or scale not in CERTIFIED_SCALES:
            continue
        disc = disc_ids(data)
        for attempt in data["attempts"]:
            if attempt.get("error"):
                continue
            ep = episode_metrics(attempt, disc)
            if ep["disc_grounded_ids"]:
                leaks[scale].append(
                    (attempt["cell_id"], attempt["replicate"], ep["disc_grounded_ids"]))
    for scale in CERTIFIED_SCALES:
        s = row_stats(agg[(scale, "stripped", "search")])
        print(f"  scale {scale}: stripped-arm discontinuity grounding {s['disc_grnd']:.2f} "
              f"across {s['n']} surviving episodes; {len(leaks[scale])} episodes grounded "
              f"a certified-unreachable constraint")
    print("  Per the frozen design, the certified-scale run pairs are invalid for the")
    print("  discontinuity DV and no kill condition is read as a confirmatory finding.")
    section("T5: kill-condition readings (informational, per the kill application)")
    for scale in CERTIFIED_SCALES:
        g = row_stats(agg[(scale, "graph", "search")])
        st = row_stats(agg[(scale, "stripped", "search")])
        print(f"  scale {scale}: graph disc {g['disc_grnd']:.2f} vs stripped {st['disc_grnd']:.2f} "
              f"(advantage {g['disc_grnd'] - st['disc_grnd']:+.2f}); "
              f"off-entry delta {g['off_grnd'] - st['off_grnd']:+.2f}; "
              f"overclaim {g['overclaim']}/{g['n']} vs {st['overclaim']}/{st['n']}")
    g50 = row_stats(agg[(50, "graph", "search")])
    s50 = row_stats(agg[(50, "stripped", "search")])
    print(f"  scale-50 within-ceiling control: graph {g50['off_grnd']:.2f} vs "
          f"stripped {s50['off_grnd']:.2f} (delta {g50['off_grnd'] - s50['off_grnd']:+.2f})")
    return leaks


def t6_probe(probe_runs):
    """The pilot table, aggregated the way the probe's own archived analyzer
    aggregates (bench/results/graph-completion-probe/graph-completion-analyze.py):
    grounded coverage pooled over the condition's constraint slots, not a mean
    of per-episode fractions, so this table reproduces the probe SUMMARY."""
    section("T6: the pilot (graph-completion probe, #1241) - instruments validated, "
            "no-search floors")
    print(f"{'arm':<9} {'search':<9} {'model':<6} {'n':>3} {'off-grnd':>8} "
          f"{'srch/ep':>7} {'ftch/ep':>7} {'srch/grnd':>9}")
    out = {}
    for _, data in sorted(probe_runs, key=lambda r: condition(r[1])):
        scale, arm, search, model = condition(data)
        eps = [episode_metrics(a, set()) for a in data["attempts"] if not a.get("error")]
        grounded = sum(e["grounded_n"] for e in eps)
        total = sum(e["total_n"] for e in eps)
        searches = sum(e["searches"] for e in eps)
        s = {
            "n": len(eps),
            "off_grnd": round(grounded / total, 2) if total else 0.0,
            "srch_ep": round(_mean([e["searches"] for e in eps]), 1),
            "ftch_ep": round(_mean([e["fetches"] for e in eps]), 1),
            "srch_grnd": round(searches / grounded, 2) if grounded else None,
        }
        sg = f"{s['srch_grnd']:.2f}" if s["srch_grnd"] is not None else "-"
        print(f"{arm:<9} {search:<9} {model:<6} {s['n']:>3} {s['off_grnd']:>8.2f} "
              f"{s['srch_ep']:>7.1f} {s['ftch_ep']:>7.1f} {sg:>9}")
        out[(arm, search, model)] = s
    return out


def manifest_pins(runs):
    models = {d["manifest"]["model"] for _, d in runs}
    clients = {d["manifest"]["client_version"] for _, d in runs}
    commits = {d["manifest"]["git_commit"] for _, d in runs}
    return sorted(models), sorted(clients), sorted(commits)


def archived_analyzer_exit():
    """The archived analyzer must exit 1 on these archives: the instrument kill
    is present in the data by design, and the analyzer refusing to read a kill
    condition from an invalid pair is the recorded outcome."""
    proc = subprocess.run(
        [sys.executable, ANALYZER], cwd=CONFIRMATORY,
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
    return proc.returncode


def check_pins(agg, leaks, claims, probe):
    """Pin the published headline numbers to the archives.

    Every entry states a number the published report
    (docs/reference/benchmark-report-graph-completion.md) prints, computed here
    from the committed archives. `make bench-report-check` runs this in
    `make verify` and in CI's harness job, so a drift between the archives,
    this script, and the published page fails the build instead of reaching a
    reader. The instrument kill's presence is itself pinned: these archives
    stopping to reproduce the kill would be drift, not health."""
    section("T7: published-headline pin (report vs archives)")

    def s(scale, arm, search):
        return row_stats(agg[(scale, arm, search)])

    pins = [
        ("instrument kill present: stripped disc grounding 1.00 (500) / 0.93 (5000)",
         [s(500, "stripped", "search")["disc_grnd"], s(5000, "stripped", "search")["disc_grnd"]],
         [1.00, 0.93]),
        ("leak episodes 15 at scale 500, 13 at scale 5000",
         [len(leaks[500]), len(leaks[5000])], [15, 13]),
        ("archived analyzer exits 1 on these archives (kill present by design)",
         [archived_analyzer_exit()], [1]),
        ("coverage at ceiling in every cell: off-grnd 1.00 everywhere except stripped/5000 at 0.95",
         [s(50, "graph", "search")["off_grnd"], s(50, "stripped", "search")["off_grnd"],
          s(500, "graph", "search")["off_grnd"], s(500, "stripped", "search")["off_grnd"],
          s(5000, "graph", "search")["off_grnd"], s(5000, "stripped", "search")["off_grnd"],
          s(5000, "graph", "nosearch")["off_grnd"]],
         [1.00, 1.00, 1.00, 1.00, 1.00, 0.95, 1.00]),
        ("searches per grounded constraint 0.59/0.73, 0.67/1.02, 0.63/1.44 (graph/stripped by scale)",
         [s(50, "graph", "search")["srch_grnd"], s(50, "stripped", "search")["srch_grnd"],
          s(500, "graph", "search")["srch_grnd"], s(500, "stripped", "search")["srch_grnd"],
          s(5000, "graph", "search")["srch_grnd"], s(5000, "stripped", "search")["srch_grnd"]],
         [0.59, 0.73, 0.67, 1.02, 0.63, 1.44]),
        ("graph-arm page-provenance fetch share rises 0.09 / 0.28 / 0.34; stripped stays 1.00 search",
         [s(50, "graph", "search")["prov_page"], s(500, "graph", "search")["prov_page"],
          s(5000, "graph", "search")["prov_page"],
          s(50, "stripped", "search")["prov_search"], s(500, "stripped", "search")["prov_search"],
          s(5000, "stripped", "search")["prov_search"]],
         [0.09, 0.28, 0.34, 1.00, 1.00, 1.00]),
        ("auxiliary graph/no-search at 5000: n=9, grounded 1.00, 0 searches, 9 traversal episodes",
         [s(5000, "graph", "nosearch")["n"], s(5000, "graph", "nosearch")["off_grnd"],
          s(5000, "graph", "nosearch")["srch_ep"], s(5000, "graph", "nosearch")["trav_ep"]],
         [9, 1.00, 0.0, 9]),
        ("~11 fetches ground everything at 5000 (10.7 graph / 11.2 stripped per episode)",
         [s(5000, "graph", "search")["ftch_ep"], s(5000, "stripped", "search")["ftch_ep"]],
         [10.7, 11.2]),
        ("overclaim channel inert: 0 complete, 91 gaps declared, 7 no statement, 98 surviving",
         [claims["complete"], claims["gaps"], claims["nostmt"], claims["n"]],
         [0, 91, 7, 98]),
        ("exactly one failed episode, in stripped/5000",
         [sum(row_stats(agg[k])["fail"] for k in agg),
          s(5000, "stripped", "search")["fail"]], [1, 1]),
        ("scale-50 within-ceiling control replicated (delta 0.00)",
         [round(s(50, "graph", "search")["off_grnd"] - s(50, "stripped", "search")["off_grnd"], 2)],
         [0.0]),
        ("kill readings: disc advantage +0.00/+0.07, off-entry delta +0.00/+0.05, overclaim 0/0",
         [round(s(500, "graph", "search")["disc_grnd"] - s(500, "stripped", "search")["disc_grnd"], 2),
          round(s(5000, "graph", "search")["disc_grnd"] - s(5000, "stripped", "search")["disc_grnd"], 2),
          round(s(500, "graph", "search")["off_grnd"] - s(500, "stripped", "search")["off_grnd"], 2),
          round(s(5000, "graph", "search")["off_grnd"] - s(5000, "stripped", "search")["off_grnd"], 2),
          claims["complete"]],
         [0.0, 0.07, 0.0, 0.05, 0]),
        ("pilot no-search floors: graph 0.96 opus / 0.42 haiku, stripped 0.00 both",
         [probe[("graph", "nosearch", "opus")]["off_grnd"],
          probe[("graph", "nosearch", "haiku")]["off_grnd"],
          probe[("stripped", "nosearch", "opus")]["off_grnd"],
          probe[("stripped", "nosearch", "haiku")]["off_grnd"]],
         [0.96, 0.42, 0.00, 0.00]),
        ("pilot budget-bound search cells: haiku 0.29 graph vs 0.10 stripped, srch/grnd 2.30 vs 8.00",
         [probe[("graph", "search", "haiku")]["off_grnd"],
          probe[("stripped", "search", "haiku")]["off_grnd"],
          probe[("graph", "search", "haiku")]["srch_grnd"],
          probe[("stripped", "search", "haiku")]["srch_grnd"]],
         [0.29, 0.10, 2.30, 8.00]),
    ]
    failed = 0
    for label, got, want in pins:
        ok = got == want
        failed += 0 if ok else 1
        print(f"  {'PASS' if ok else 'FAIL'}: {label}" + ("" if ok else f"  (got {got}, want {want})"))
    if failed:
        print(f"  {failed} pin(s) FAILED: the archives no longer reproduce the published report.")
    return failed


def main():
    runs = load_confirmatory()
    probe_runs = load_probe()
    print(f"{len(runs)} confirmatory runs under {CONFIRMATORY}")
    print(f"{len(probe_runs)} pilot runs under {PROBE}")
    models, clients, commits = manifest_pins(runs)
    print(f"confirmatory manifests: model {models}, client {clients}, commit {commits}")
    agg = aggregate(runs)
    t1_matrix(agg)
    t2_provenance(agg)
    claims = t3_claims(agg)
    leaks = t4_kill(runs, agg)
    probe = t6_probe(probe_runs)
    return 1 if check_pins(agg, leaks, claims, probe) else 0


if __name__ == "__main__":
    sys.exit(main())
