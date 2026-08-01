#!/usr/bin/env python3
"""Gate-probe analysis (study-3 due diligence).

For every attempt in the named run directories, reads the transcript and
reports, per cell: whether the agent called the search tool at all, whether
the first search preceded the first data-bearing call (api_* or fetch),
whether the planted note's threshold sentence surfaced in a tool result,
and the final answer. Wilson 95% intervals on the search-first rate.

Usage: pk-gateprobe-analyze.py <run-dir> [<run-dir> ...]
"""
import json
import math
import re
import sys
from pathlib import Path

# Data-bearing tools: the calls discovery is supposed to precede. platform_info
# and ToolSearch are handshake/plumbing, not data access.
DATA_CALL = re.compile(r"api_invoke_endpoint|api_get_endpoint_schema|api_list_endpoints|api_list_specs|fetch")
SEARCH = re.compile(r"(^|__)search$")
# The convention note's load-bearing content: the threshold only the note holds.
NOTE_MARK = "70"
NOTE_PHRASE = "positive coverage"


def wilson(k, n, z=1.96):
    if n == 0:
        return (0.0, 0.0)
    p = k / n
    d = 1 + z * z / n
    c = p + z * z / (2 * n)
    h = z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n))
    return ((c - h) / d, (c + h) / d)


def analyze_attempt(tr):
    first_search = first_data = None
    note_seen = False
    searched_queries = []
    for i, e in enumerate(tr):
        if e.get("role") == "assistant":
            for c in e.get("tool_calls", []):
                n = c.get("name", "")
                if SEARCH.search(n):
                    if first_search is None:
                        first_search = i
                    args = c.get("args", {})
                    searched_queries.append(args.get("intent") or args.get("query") or "")
                elif DATA_CALL.search(n) and first_data is None:
                    first_data = i
        elif e.get("role") == "user":
            for r in e.get("tool_results", []):
                t = r.get("text") or ""
                if NOTE_PHRASE in t.lower() and NOTE_MARK in t:
                    note_seen = True
    return first_search, first_data, note_seen, searched_queries


def main(dirs):
    for d in dirs:
        d = Path(d)
        res = json.loads((d / "results.json").read_text())
        man = res["manifest"]
        scaffold = man.get("scaffold")
        if scaffold is None:
            bullet = "present (pre-knob archive: default scaffold)"
        else:
            bullet = "present" if "search tool" in scaffold else "ABSENT"
        print(f"\n=== {d.name}  model={man['model']} k={man['k']} scaffold_bullet={bullet}")
        by_cell = {}
        for a in res["attempts"]:
            if a.get("error"):
                print(f"  ATTEMPT ERROR cell={a['cell_id']} rep={a['replicate']}: {a['error'][:120]}")
                continue
            flat = a["cell_id"].replace("/", "_")
            tp = d / "transcripts" / f"{flat}-r{a['replicate']}.json"
            if not tp.exists():
                print(f"  MISSING transcript for {a['cell_id']} rep {a['replicate']}")
                continue
            tr = json.loads(tp.read_text())
            if isinstance(tr, dict):
                tr = tr.get("transcript", [])
            fs, fd, note, queries = analyze_attempt(tr)
            row = by_cell.setdefault(a["cell_id"], [])
            row.append({
                "rep": a["replicate"],
                "searched": fs is not None,
                "search_first": fs is not None and (fd is None or fs < fd),
                "made_data_call": fd is not None,
                "note_seen": note,
                "answer": a.get("final_answer", "")[-160:],
                "outcome": a.get("outcome"),
                "queries": queries,
            })
        for cell, rows in sorted(by_cell.items()):
            n = len(rows)
            s = sum(r["searched"] for r in rows)
            sf = sum(r["search_first"] for r in rows)
            ns = sum(r["note_seen"] for r in rows)
            lo, hi = wilson(sf, n)
            print(f"  cell {cell}: n={n} searched={s}/{n} search_first={sf}/{n} "
                  f"[{lo:.2f},{hi:.2f}] note_surfaced={ns}/{n}")
            for r in rows:
                ans = re.search(r"FINAL ANSWER:\s*(\S+)", r["answer"] or "")
                print(f"    r{r['rep']}: searched={int(r['searched'])} first={int(r['search_first'])} "
                      f"data_call={int(r['made_data_call'])} note={int(r['note_seen'])} "
                      f"answer={ans.group(1) if ans else '?'}")


if __name__ == "__main__":
    main(sys.argv[1:])
