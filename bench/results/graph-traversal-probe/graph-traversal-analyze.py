#!/usr/bin/env python3
"""Recompute the graph-traversal probe's headline numbers from the archives.

Offline: reads only the run directories beside this file. No platform, no API
key, no network, stdlib only.

For each run it reports, per episode:

  searches       how many times the episode called `search`
  limits         the `limit` each of those calls asked for (None = the tool
                 default). The agent chooses this, which is the whole point.
  pages/search   how many distinct corpus pages each search returned
  answer@        the search call that first returned the page holding the
                 ground truth, or "-" if search never returned it
  fetches        each dereference, as depth-on-chain and where the reference
                 could first have been learned: `search` if a search result had
                 already carried it, `page` if only a fetched page had, `unseen`
                 if nothing had.

`page` is the count the probe turns on: it is the only class of dereference
that required the reference graph. Everything else is search reach.
"""

import json
import os
import re
import sys

REF = re.compile(r"mcp:knowledge_page:[A-Za-z0-9_.\-]+")
TRAILING = ".,;:!?"


def tool(name):
    """Strip the client's mcp__<server>__ namespace from a transcript tool name."""
    return name.rsplit("__", 1)[-1]


def refs_in(text, known_ids):
    """Corpus page ids mentioned in one tool result."""
    out = set()
    for token in REF.findall(text):
        page_id = token.rstrip(TRAILING).split(":")[-1]
        if page_id in known_ids:
            out.add(page_id)
    return out


def read_run(path):
    """Recompute one run directory."""
    with open(os.path.join(path, "results.json"), encoding="utf-8") as fh:
        res = json.load(fh)
    pages = res["planted"]["pages"]
    key_by_id = {v: k for k, v in pages.items()}
    known_ids = set(pages.values())
    cells = {c["ID"]: c for c in res["cells"]}
    rows = []
    for attempt in res["attempts"]:
        cell = cells[attempt["cell_id"]]
        answer_id = pages[cell["Chain"][-1]]
        tpath = os.path.join(path, "transcripts", "%s-r%d.json" % (attempt["cell_id"], attempt["replicate"]))
        if not os.path.exists(tpath):
            rows.append({"cell": attempt["cell_id"], "rep": attempt["replicate"], "error": attempt.get("error", "no transcript")})
            continue
        with open(tpath, encoding="utf-8") as fh:
            transcript = json.load(fh)
        rows.append(read_episode(transcript, attempt, cell, answer_id, known_ids, key_by_id))
    return res, rows


def read_episode(transcript, attempt, cell, answer_id, known_ids, key_by_id):
    """Recompute one episode's searches and dereferences, in call order."""
    call_tool, call_args = {}, {}
    first_seen = {}
    limits, per_search, fetches = [], [], []
    answer_at, search_index = None, 0
    for message in transcript:
        for call in message.get("tool_calls") or []:
            call_tool[call["id"]] = tool(call["name"])
            call_args[call["id"]] = call.get("args") or {}
            if call_tool[call["id"]] == "search":
                limits.append(call_args[call["id"]].get("limit"))
            elif call_tool[call["id"]] == "fetch":
                ref = call_args[call["id"]].get("reference", "")
                page_id = ref.split(":")[-1]
                fetches.append({
                    "key": key_by_id.get(page_id, ref),
                    "depth": cell["Chain"].index(key_by_id[page_id]) if page_id in key_by_id and key_by_id[page_id] in cell["Chain"] else -1,
                    "provenance": first_seen.get(ref.rstrip(TRAILING), "unseen"),
                    "id": page_id,
                })
        for result in message.get("tool_results") or []:
            kind = call_tool.get(result["call_id"])
            if kind not in ("search", "fetch"):
                continue
            found = refs_in(result["text"], known_ids)
            if kind == "search":
                search_index += 1
                per_search.append(len(found))
                if answer_at is None and answer_id in found:
                    answer_at = search_index
            source = "search" if kind == "search" else "page"
            for page_id in found:
                ref = "mcp:knowledge_page:" + page_id
                first_seen.setdefault(ref, source)
    return {
        "cell": attempt["cell_id"], "rep": attempt["replicate"], "depth": cell["Depth"],
        "correct": attempt["outcome"]["correct"], "answer": attempt["outcome"]["final_answer"],
        "limits": limits, "per_search": per_search, "answer_at": answer_at,
        "fetches": fetches, "error": attempt.get("error", ""),
    }


def report(path):
    """Print one run's episodes and its totals."""
    res, rows = read_run(path)
    manifest = res["manifest"]
    print("== %s  model=%s  corpus=%d pages  k=%d" % (
        os.path.basename(path.rstrip("/")), manifest["model"], manifest["corpus_pages"], manifest["k"]))
    totals = {"search": 0, "page": 0, "unseen": 0}
    correct = graded = answered_by_search = 0
    for row in rows:
        if row.get("error"):
            print("  %-20s r%d  HARNESS FAILURE: %s" % (row["cell"], row["rep"], row["error"]))
            continue
        graded += 1
        correct += 1 if row["correct"] else 0
        answered_by_search += 1 if row["answer_at"] else 0
        for fetch in row["fetches"]:
            totals[fetch["provenance"]] = totals.get(fetch["provenance"], 0) + 1
        print("  %-20s r%d d%d correct=%-5s searches=%d limits=%s pages/search=%s answer@%s" % (
            row["cell"], row["rep"], row["depth"], row["correct"], len(row["limits"]),
            row["limits"], row["per_search"], row["answer_at"] or "-"))
        for fetch in row["fetches"]:
            print("      fetch %-30s depth=%2d via=%s" % (fetch["key"], fetch["depth"], fetch["provenance"]))
    print("  totals: %d/%d correct; dereferences by provenance %s; "
          "answer page reached by the episode's own search in %d/%d" % (
              correct, graded, totals, answered_by_search, graded))
    return totals, graded, answered_by_search


def main(argv):
    """Report every run directory named on the command line, or all of them."""
    here = os.path.dirname(os.path.abspath(__file__))
    paths = argv[1:] or sorted(
        os.path.join(here, name) for name in os.listdir(here)
        if name.startswith("gt-") and os.path.isdir(os.path.join(here, name)))
    grand = {"search": 0, "page": 0, "unseen": 0}
    episodes = reached = 0
    for path in paths:
        totals, graded, by_search = report(path)
        for key, value in totals.items():
            grand[key] = grand.get(key, 0) + value
        episodes += graded
        reached += by_search
        print()
    print("ALL RUNS: %d episodes; dereferences by provenance %s; "
          "answer page reached by the episode's own search in %d/%d" % (
              episodes, grand, reached, episodes))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
