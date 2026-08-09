# Graph-completion probe (#1241 premise probe, corrected instrument)

Exploratory. The second premise probe for the graph-traversal candidate,
built after the lookup-shaped attempt
([`../graph-traversal-probe/`](../graph-traversal-probe/)) was recorded as an
instrument defect: its cells posed single-fact questions, which search
answers directly, so it never varied the condition a reference graph exists
for. These cells are completion tasks — a complete operational document whose
graded constraints are spread over 5-8 pages under names the prompt does not
contain — crossed with two manipulations: the corpus with its edges against
the same corpus stripped to prose mentions, and search available against
search disallowed at the client with the entry reference handed in the
prompt.

[`graph-completion-design.md`](graph-completion-design.md) is the design and
the pre-stated kill conditions, written before the fixture was rebuilt and
posted to the ticket before any episode ran.
[`graph-completion-SUMMARY.md`](graph-completion-SUMMARY.md) is the run
record: the full table, the kill conditions applied, and the boundary
conditions.

## What it establishes

- Handed one entry reference and no search tool, agents traverse the
  reference graph voluntarily, scaling with tier: opus recovered 0.96 of the
  spread constraint set by pure edge-following (zero queries, full depth in
  every cell, distance 4 included), haiku 0.42.
- With search available, the outcome is governed by reading budget against
  corpus size: the agent that could afford to read half the corpus per
  episode hit ceiling with or without edges, and the agent under a budget
  where exhaustive reading did not happen is the one edges helped (0.29
  grounded with edges against 0.10 without, at a third of the searches per
  grounded constraint). At corpus scales of thousands of pages that budget
  condition binds every model.
- Ungrounded coverage is confabulation at a measurable rate (opus stated
  signatures for 19 percent of constraint slots it could not have read, in
  the arm built to expose exactly that), so grounded coverage is the only
  defensible headline for completion-task instruments.
- The premise-probe verdict, per the pre-registered conditions: the
  candidate holds and proceeds toward a density design; no kill fired, no
  instrument leak.

## What it does not establish

- Anything at corpus scales where enumeration stops being affordable — the
  binding condition on every search-arm number, pre-stated in the design.
- Density or page-size effects (held constant here; they are the next
  stage's axes).
- Capability claims about either model, or production behavior.

## Reproducing

```bash
python3 graph-completion-analyze.py
```

Offline, stdlib only, reads the committed archives. The harness re-derives
readings and coverage from raw transcripts with
`cd bench && go run ./graphprobe -mode reread -out results/graph-completion-probe/<run>`;
re-running episodes needs the gt stack (see [`../../README.md`](../../README.md)).

## Environment

`bench/config/platform.bench.gt.yaml` on its own database (`mcp_bench_gt`),
Postgres plus the platform and nothing else. Page embeddings through ollama
`nomic-embed-text`. Episodes through claude-cli on a subscription, so no run
here carries metered cost; each manifest records the client version, the
arm, the search condition and the exact scaffold. The eight run directories,
the per-arm plant and sweep-gate records, and the driver log (including two
aborted starts and their fixes) are all committed beside this file.
