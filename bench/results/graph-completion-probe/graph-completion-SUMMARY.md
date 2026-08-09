# Graph-completion probe: run record (#1241)

Exploratory. This is the corrected instrument for the graph-traversal
candidate after the lookup-shaped attempt
([`../graph-traversal-probe/`](../graph-traversal-probe/)) measured search on
search's own ground. Design and kill conditions were pre-stated in
[`graph-completion-design.md`](graph-completion-design.md) and posted to
issue #1241 before any episode ran.

## Design in one paragraph

Three completion cells ("write the complete change plan / incident-handling
document / export onboarding document") whose graded constraint sets are
spread over 5-8 pages at reference distances up to 4 from an entry page,
under hard-token signatures that live only on their source pages. A 2x2 of
corpus arm (graph edges vs the same prose with edges stripped to their
authored fallbacks) and search condition (available vs client-disallowed,
with the entry reference handed in the prompt), two capability tiers, k=3:
72 episodes, 8 runs, zero harness failures. Primary dependent variable:
off-entry grounded coverage — a constraint counts only when its signature is
in the final document AND a source page was actually fetched. Coverage
without a read is reported separately as confabulation.

## Results

Grounded off-entry coverage (mean over 9 episodes per condition), searches
per episode, and searches per grounded constraint:

| Arm | Search | Model | Grounded | Unread | Searches/ep | Fetches/ep | Searches/grounded |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| graph | no-search | haiku | 0.42 | 0 | 0 | 4.6 | - |
| graph | no-search | opus | 0.96 | 0 | 0 | 9.0 | - |
| graph | search | haiku | 0.29 | 4 | 5.1 | 2.4 | 2.30 |
| graph | search | opus | 0.99 | 0 | 4.3 | 19.0 | 0.57 |
| stripped | no-search | haiku | 0.00 | 1 | 0 | 0.7 | - |
| stripped | no-search | opus | 0.00 | 13 | 0 | 6.0 | - |
| stripped | search | haiku | 0.10 | 3 | 6.2 | 1.0 | 8.00 |
| stripped | search | opus | 1.00 | 0 | 4.9 | 21.1 | 0.64 |

## The pre-stated kill conditions, applied

**Instrument kills: none fired.** Both stripped/no-search floors read 0.00
grounded (the arm's ceiling by construction), both plants verified their
reference tables (exact edges in the graph arm, empty in the stripped arm),
and both sweep gates passed with zero signature leaks across 27 query-limit
combinations per arm.

**Kill 1 (traversal floor) did not fire.** In graph/no-search, haiku reached
0.42 and opus 0.96, both above the 0.25 floor. With no search tool, both
tiers walked the reference graph to the deepest page of every cell (distance
4 in the billing cell); opus did it in all nine episodes, haiku in four of
nine, and haiku's failures were stopping early rather than not starting.

**Kill 2 (graph adds nothing over search) did not fire**, because it
required the null for every tier. It holds for opus alone: coverage delta
-0.01 and cost inside the 25 percent threshold (0.57 vs 0.64 searches per
grounded constraint) — at 42 pages the strong tier enumerates
with a raised limit and reads everything, graph or no graph, exactly the
enumeration ceiling the design pre-stated. It fails for haiku: the graph
nearly tripled grounded coverage in the search test (0.29 vs 0.10) and cut
searches per grounded constraint from 8.0 to 2.3.

**Condition 3 (premise holds) fired.** Opus exceeds 0.50 in graph/no-search
(0.96) and haiku exceeds the 0.15 coverage-gain threshold in the search test
(+0.19). The candidate proceeds toward a density design (stage 3), per the
pre-registration.

## What the runs establish

- **Agents traverse when traversal is the only route, scaling with tier.**
  Handed one entry reference and no search, opus recovered 96 percent of the
  spread constraint set by pure edge-following at zero queries; haiku
  recovered 42 percent. This is the measurement the lookup fixture could not
  produce, and it is the first voluntary-traversal reading on authored
  operational pages in either direction.
- **When search is available, traversal is rare and retrieval does the
  work.** In the search arms nearly every fetch dereferenced a
  search-returned reference (2 of 18 opus episodes and 0 of 18 haiku
  episodes reached a page past the entry through a page-learned reference),
  replicating the lookup-era provenance result under completion tasks.
- **The tier contrast is a reading-budget contrast, not a capability
  ranking.** The agent that could afford to read half the corpus per episode
  (opus, ~21 reads) hit ceiling with or without edges: when exhaustive
  reading is affordable, every discovery structure is equivalent. The agent
  operating under a budget where exhaustive reading did not happen (haiku)
  is the one for whom edges changed the outcome (0.29 vs 0.10 grounded, a
  third of the searches per grounded constraint). The durable statement is
  about the ratio of reading budget to corpus size — edges matter when
  enumeration is unaffordable — and at deployment scales of thousands of
  pages that condition binds every model. Nothing here speaks to those
  scales directly; measuring there is the follow-on's job.
- **Ungrounded coverage is confabulation at a measurable rate.** In
  stripped/no-search, opus produced signatures for 19 percent of the
  off-entry constraint slots it had no way to read (13 slots across 9
  episodes), stating plausible-but-unverifiable values in otherwise honest
  documents. Grounded coverage, not raw coverage, is the only defensible
  headline for any completion-task instrument.
- **The weak tier's failure mode under search is not reading at all.** In 6
  of 9 stripped/search haiku episodes and 6 of 9 graph/search ones, haiku
  issued only searches, fetched nothing, and wrote the document from hit
  text. Its entry-control coverage (0.40-0.60) shows the same satisficing on
  content one fetch away.

## What the runs do not establish

- **Anything at larger corpus scales.** The enumeration ceiling binds every
  search-arm number to tens-of-pages deployments; the graph-vs-search
  contrast at scales where enumeration is unaffordable is exactly the open
  stage-3 question.
- **The conditions where search is the wrong path, not just the slower
  one.** Every constraint page in this fixture was semantically adjacent to
  its task — the gate's enumeration profile shows nearly all of them inside
  the top 25 for task-derived queries — so the probe never posed a
  constraint reachable only through an edge. Two mechanisms remain untested
  and are what a follow-on study exists for: **semantic discontinuity**
  (a constraint whose source page shares no vocabulary or embedding
  neighborhood with the task — institutional connections like a finance
  close calendar governing a schema change — verified by the gate as absent
  from the top 100 for every task-derived phrasing) and **completeness
  closure** (search returns some relevant things and can never certify all
  of them; a graph closure from an entry node is enumerable and terminating,
  which is the difference between finding six constraints and knowing there
  are six).
- **Density or page-size effects.** Held constant by design in this probe;
  they are the axes the density design would vary.
- **A capability claim about either model.** Two tiers, one client
  (claude-cli, version in each manifest), k=3, one authored corpus.
- **Production behavior.** The corpus is authored and the tasks are posed;
  the production audit signal (dereference appetite without provenance)
  remains a separate instrument.

## Reproducing

```bash
cd bench/results/graph-completion-probe
python3 graph-completion-analyze.py
```

Offline, stdlib only: recomputes every table above from the archived
results.json files. The harness's own classifier re-derives readings and
coverage from raw transcripts:

```bash
cd bench && go run ./graphprobe -mode reread -out results/graph-completion-probe/gc-opus-graph-nosearch-20260809-060046
```

Re-running episodes needs the gt stack (`make bench-gt-up`, ollama with
`nomic-embed-text`); the full sequence is in [`driver.log`](driver.log),
which also records the two aborted starts (a leftover corpus from the
lookup-era runs, and an embedding-readiness check that counted soft-deleted
pages' chunks) and their fixes.

## Runs

Each directory carries `results.json` (manifest with arm and search
condition, sweep-gate reading, plant record, per-attempt readings, coverage
and final documents) and `transcripts/`.

| Directory | Arm | Search | Model |
| --- | --- | --- | --- |
| [`gc-haiku-graph-search-20260809-050354/`](gc-haiku-graph-search-20260809-050354/) | graph | on | haiku |
| [`gc-opus-graph-search-20260809-051816/`](gc-opus-graph-search-20260809-051816/) | graph | on | opus |
| [`gc-haiku-graph-nosearch-20260809-055113/`](gc-haiku-graph-nosearch-20260809-055113/) | graph | off | haiku |
| [`gc-opus-graph-nosearch-20260809-060046/`](gc-opus-graph-nosearch-20260809-060046/) | graph | off | opus |
| [`gc-haiku-stripped-search-20260809-062851/`](gc-haiku-stripped-search-20260809-062851/) | stripped | on | haiku |
| [`gc-opus-stripped-search-20260809-064224/`](gc-opus-stripped-search-20260809-064224/) | stripped | on | opus |
| [`gc-haiku-stripped-nosearch-20260809-071918/`](gc-haiku-stripped-nosearch-20260809-071918/) | stripped | off | haiku |
| [`gc-opus-stripped-nosearch-20260809-072916/`](gc-opus-stripped-nosearch-20260809-072916/) | stripped | off | opus |

`graph-completion-plant-<arm>.json` and `graph-completion-gate-<arm>.json`
are each arm's plant record and sweep-gate reading, extracted from the run
archives (each run's own copies are embedded in its results.json).
