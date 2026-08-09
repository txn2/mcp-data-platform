# Graph-traversal probe (#1241 premise probe)

Exploratory. The premise probe for a proposed graph-traversal study: when the
answer to a task lives in a knowledge page reachable only by following
references from the page `search` surfaces, does the agent follow them, and how
deep does it get? Kill conditions were stated in the ticket before any run:
floor (no tier ever fetches a referenced page), ceiling (every tier reliably
reaches depth 3), and ambiguity is a kill.

It did not settle the question, and the reason is the fixture rather than the
result. Every cell posed a single-fact lookup question, which is precisely the
shape `search` answers directly, so the design never varied the condition under
which a reference graph is the cheaper route or the only one: a task that cannot
be completed from the page search returns, whose missing pieces the asker cannot
name and therefore cannot query for. With that condition held at zero, search
and the graph competed on search's own ground.

What the runs measured, and measured cleanly: **across 76 episodes and 162
dereferences on two capability tiers, 160 dereferences used a reference `search`
had already returned**, and the page holding the ground truth came back to the
episode's own search in 75 of 76 episodes. On a 42-page corpus an agent that
sets `limit: 25` (which opus did on 65 of its 67 searches) sees half the corpus
in one call. That is a real boundary condition worth keeping: for lookup-shaped
questions at this corpus size, reference following adds nothing search does not
already do. It is not an answer to whether agents traverse when a task needs
content they cannot name.

The candidate therefore stays open in the ledger, and this family is archived as
a design postmortem: the harness, the corpus and the analysis are reusable, the
cells are not. The corrected instrument ran as
[`../graph-completion-probe/`](../graph-completion-probe/), where the premise
held.

[`graph-traversal-SUMMARY.md`](graph-traversal-SUMMARY.md) is the run's own
record: design, the pre-stated fixture gate and how it was passed, the full
result table, the mechanism, and the instrument lessons. The register carries
this family twice in [`../../docs/findings-register.md`](../../docs/findings-register.md):
as an instrument defect, and as the candidate it left open.

## What it establishes

- For a question with one findable answer, on a corpus of tens of pages, search
  reaches the page holding it directly and reference following adds nothing.
  Episodes read three or four pages of a chain in one pass after a single
  search; opus answered 24/24 correctly across the two current runs.
- Splitting a page into linked pieces does not strand its content at this scale:
  every split page stays one query away.
- A fixture gate run at the tool's default `limit` certifies nothing, because
  the agent chooses its own limit and its own phrasing. The gate passed on all
  four cells and the episodes defeated it on their first call.

## What it does not establish

- **Whether agents traverse when a task requires content they cannot name.**
  That is the question the candidate asks and the one this fixture could not
  put. Two dereferences out of 162 used a reference only a page could have
  supplied, and one of those is the single episode whose own search had not
  returned the answer page, which is suggestive of where traversal lives rather
  than evidence about it.
- **Anything at larger corpus scales.** The result is scale-conditioned
  throughout.
- **That the reference graph is unused.** A production deployment's audit log
  records 31 `fetch` calls on knowledge pages across 23 sessions, 6 of them
  reading two or more pages in one session. The audit row does not record where
  a reference came from, so that is dereference appetite, not traversal, but it
  is the appetite this fixture failed to elicit.
- **A capability or accuracy claim.** Two tiers, one client, k=3, and a corpus
  authored until the fixture gate passed.

## Runs

Each run directory carries `results.json` (manifest, gate reading, plant record,
per-attempt readings) and `transcripts/` (one file per episode). Newest first.

| Directory | Model | k | Fixture | Note |
| --- | --- | ---: | --- | --- |
| [`gt-opus-20260809-023739/`](gt-opus-20260809-023739/) | opus | 3 | committed | 12/12 correct, full depth read in every cell, 30 dereferences, 0 from a page |
| [`gt-haiku-20260809-022815/`](gt-haiku-20260809-022815/) | haiku | 3 | committed | 4/12 correct, 13 dereferences, the only 2 from a page anywhere in the probe |
| [`gt-opus-20260809-020914/`](gt-opus-20260809-020914/) | opus | 3 | pre-spelling | 12/12 correct, 31 dereferences, 0 from a page |
| [`gt-haiku-20260809-015944/`](gt-haiku-20260809-015944/) | haiku | 3 | pre-spelling | 9/12 correct, 25 dereferences, 0 from a page |
| [`gt-opus-20260809-014735/`](gt-opus-20260809-014735/) | opus | 3 | pre-correction | superseded for correctness on `gt-d3-ledger`; traversal reading stands |
| [`gt-haiku-20260809-013323/`](gt-haiku-20260809-013323/) | haiku | 3 | pre-correction | superseded for correctness on `gt-d3-ledger`; traversal reading stands |
| [`gt-haiku-20260809-013001/`](gt-haiku-20260809-013001/) | haiku | 1 | pre-correction | the first end-to-end run of the harness, kept as data |

Three fixture generations, all 42 pages:

- **pre-correction.** The depth-3 cell's ground truth named the clock for the
  rung after the one the question asked about, and an overnight doubling rule
  made the question ambiguous as well. Correctness on that cell is not
  interpretable in these runs.
- **pre-spelling.** The depth-3 cell rephrased and the overnight rule narrowed.
  Identical to the committed fixture except for the British spelling of three
  words in three page summaries.
- **committed.** The fixture in the tree, after the repository's spelling gate.
  Re-run rather than footnoted, so the committed corpus is exactly the corpus
  that produced the current numbers.

`graph-traversal-plant.json` is the final plant (fixture key to platform page
id) and `graph-traversal-gate.json` the final fixture-gate reading. Each run's
own `planted` and `gate` blocks are inside its `results.json`, so a run is read
against the corpus it actually ran on.

Superseded runs are kept because a run that produced data is evidence of what
happened, and because their traversal reading, which is the probe's subject, is
untouched by either revision.

## Reproducing

```bash
cd bench/results/graph-traversal-probe
python3 graph-traversal-analyze.py
```

Offline, no API key, no network, stdlib only: the analyzer reads only the
transcripts committed here and recomputes the searches, the limits each one
asked for, how much of the corpus each returned, and every dereference with the
provenance of its reference.

The harness's own classifier recomputes the same readings:

```bash
cd bench && go run ./graphprobe -mode reread -out results/graph-traversal-probe/gt-opus-20260809-023739
```

Re-running the episodes needs the gt stack and is described in
[`../../README.md`](../../README.md); `cd bench && go run ./graphprobe -mode table`
prints the fixture with no stack at all.

## Environment

`bench/config/platform.bench.gt.yaml` on its own database (`mcp_bench_gt`),
Postgres plus the platform and nothing else: no Trino, no DataHub, no S3, no API
fixture. Page embeddings through ollama `nomic-embed-text`, so search ranked
pages by best-matching chunk (#1244) rather than degrading to lexical. Episodes
through claude-cli on a subscription, so no run here carries metered cost.
