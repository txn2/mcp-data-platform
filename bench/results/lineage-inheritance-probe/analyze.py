"""Recompute the #2379 lineage-inheritance probe readings from the archived transcripts.

Usage: python3 -I analyze.py   (run from this directory)

Prints, per arm: graded and correct attempts, episodes whose final answer flags
the unit as unverified, episodes where trino_describe_table carried
inherited_from, and episodes whose search returned a prior-session hit (the
#2380 leak), plus how many of those hits contained the word "cents".
"""

import json
import os
import re
import sys

HEDGE = re.compile(r"assum|if `?amount`? is already|no column metadata|whole dollars", re.I)


def arm(name, results):
    base = os.path.dirname(os.path.abspath(__file__))
    data = json.load(open(os.path.join(base, name, results)))
    graded = [a for a in data["attempts"] if "correct" in a]
    correct = sum(1 for a in graded if a["correct"])
    tdir = os.path.join(base, name, results + ".transcripts")
    hedged = inherited = leaked = leaked_cents = 0
    for fn in sorted(os.listdir(tdir)):
        t = json.load(open(os.path.join(tdir, fn)))["transcript"]
        texts = [m["text"] for m in t if m.get("role") == "assistant" and m.get("text")]
        if texts and HEDGE.search(texts[-1]):
            hedged += 1
        results_text = [r.get("text", "") for m in t for r in (m.get("tool_results") or [])]
        if any("inherited_from" in x for x in results_text):
            inherited += 1
        hits = [x for x in results_text if '"source":"sessions"' in x or '"source":"calls"' in x]
        if hits:
            leaked += 1
            if any("cents" in x.lower() for x in hits):
                leaked_cents += 1
    n = len(os.listdir(tdir))
    print(f"{name}: correct {correct}/{len(graded)}; unit flagged unverified {hedged}/{n}; "
          f"inherited_from on a response {inherited}/{n}; prior-session hits {leaked}/{n} "
          f"({leaked_cents} carrying 'cents')")


if __name__ == "__main__":
    arm("inherit-on", "results-on.json")
    arm("inherit-off", "results-off.json")
    sys.exit(0)
