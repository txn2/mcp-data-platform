# Agent-Effectiveness Benchmarks

This page publishes results from the agent-effectiveness benchmark
(`bench/`, issue #930): does an agent connected to this platform answer real
data questions more correctly and more efficiently than the same agent
connected to bare data tools? The benchmark ablates the platform, not the
model: every run holds the model, prompt scaffold, seed data, and task set
constant and varies only the platform configuration.

This is a living page. It evolves as the benchmark phases land (#943 full
suites and judge calibration, #944 memory-lifecycle protocols, #945 regression
gate); each published run's raw results are committed under `bench/results/`
so history accumulates. Methodology, arm definitions, and reproduction
commands live in [`bench/README.md`](https://github.com/txn2/mcp-data-platform/tree/main/bench).
The load harness's throughput numbers are a separate concern; see
[Tuning and Scaling](tuning-and-scaling.md).

Reading rule: the headline is always arm-vs-arm on a pinned model (the
platform's effect). Model identity is disclosed but is never the subject.

## 1. Phase 1 pilot (design validation)

Status: pilot-scale. Ten tasks, two trap classes, two arms, one model, no
confidence intervals. These numbers validate the instrument and the direction
of the effect; they are not the final reference numbers (those follow with
phase 2's larger suites and judge-calibrated scoring).

Manifest:

| Field | Value |
| --- | --- |
| Arms | a0 (raw toolkit tools) vs a2 (semantic-first platform) |
| Model | claude-sonnet-5, no sampling parameters |
| Repeats | k = 3 (pass^k reported) |
| Dataset seed | 930 |
| Task set hash | f36a24207790 |
| Platform | v1.102.0-5-g1984dd2e |
| Semantic layer | DataHub v1.6.0 quickstart on OpenSearch |
| Episodes | 60 (all graded; zero harness failures) |
| Efficiency source | platform audit API, `delivery: sync` |
| Raw results | `bench/results/v1.102.0-pilot/` |

Arm-by-suite results:

| Metric | a0 (raw tools) | a2 (platform) |
| --- | --- | --- |
| S1 discovery accuracy / pass^3 | 100% / 100% | 100% / 100% |
| S3 knowledge traps accuracy / pass^3 | 60% / 60% | 100% / 100% |
| S3 median / p90 tool calls | 16 / 30 (budget exhausted) | 8 / 13 |
| S3 median wall clock | 67s | 25s |
| Arm output tokens | 88k | 33k |

Trap-class breakdown (S3, 3 attempts per task per arm):

| Task | Trap class | a0 | a2 |
| --- | --- | --- | --- |
| s3-units-q1-total | units_cents | 3/3 | 3/3 |
| s3-units-avg-enterprise | units_cents | 3/3 | 3/3 |
| s3-net-east-march | net_revenue + units_cents | 0/3 | 3/3 |
| s3-net-top-region | net_revenue | 3/3 | 3/3 |
| s3-net-total-2025 | net_revenue + units_cents | 0/3 | 3/3 |

The distinctive result: on the two tasks whose disambiguating fact exists only
in the knowledge layer (the net-revenue reporting policy), the baseline arm
went 0/6 while exhausting its full 30-call budget on every attempt, and the
platform arm went 6/6 with pass^3. This is the external-knowledge gap that
BIRD measured with hand-curated evidence, reproduced here with automatic
retrieval (cross-enrichment and knowledge pages) in place of hand-curation.
The platform arm also answered knowledge questions with half the tool calls
and roughly a third of the wall clock and output tokens: a model without
context flails and reasons more.

## 2. Phase 3 S5 lifecycle pilot (memory-insight-knowledge)

Status: partial pilot. A single arm (a3, the lifecycle configuration), one
model, k = 1, covering 13 of the 15 protocols in one pass. S5 is arm-internal
by design — it measures the lifecycle across fresh sessions rather than
ablating arms — so there is no arm-vs-arm headline here; these numbers
establish that the lifecycle works end to end with a real model, and where it
is weak. Not statistically robust (no repeats, no confidence intervals); treat
every number as directional.

Manifest:

| Field | Value |
| --- | --- |
| Arm | a3 (semantic-first platform plus the memory / apply_knowledge lifecycle) |
| Model | claude-sonnet-5, no sampling parameters |
| Repeats | k = 1 (no pass^k reliability) |
| Dataset seed | 930 |
| Protocol set hash | 0920c55292d1 |
| Platform | v1.102.0-9-gadfb9d90 |
| Semantic layer | DataHub quickstart; memory embeddings via Ollama nomic-embed-text |
| Protocols | 13 of 15 graded in this pass |
| Lifecycle state source | admin insights + changesets API (never inferred from transcripts) |
| Raw results | `bench/results/v1.102.0-s5-partial/` |

Lifecycle metrics (numerator/denominator over the applicable graded protocols):

| Metric | Result | What it measures |
| --- | --- | --- |
| Capture rate | 85% (11/13) | the agent recorded the taught fact and entity-linked it |
| Personal recall | 62% (8/13) | fresh-session recall of the taught fact |
| Unprompted surface | 100% (11/11) | search surfaced the memory without being pointed at it |
| Transfer rate | 67% (4/6) | a different identity answered correctly after promotion |
| Update correctness | 100% (5/5) | a correction flipped recall to the new value |
| Duplicate rate | 40% (2/5) | supersede left the old insight live (lower is better) |
| Abstention | 100% (13/13) | the agent never fabricated a fact that was never taught |
| Full-lifecycle pass | 38% (5/13) | every applicable stage of the protocol succeeded |

The two strongest results: abstention held at 100% — the knowledge layer
suppressed rather than amplified fabrication, the failure mode LongMemEval
warns about — and every correction the memory layer detected flipped recall to
the new value. The weak spots are equally honest: personal recall at 62% is the
load-bearing gap (the agent captures a fact but does not always recall it in a
fresh session), and the 40% duplicate rate reflects that recall-first supersede
is gated on embedding similarity, so two short, near-identical facts were not
recognized as restatements and each left a duplicate. Teach-once-answer-forever
held on 4 of 6 promotion chains, across both sinks (an entity description and a
knowledge page).

## 3. Honest caveats

- On easy discovery (S1) a capable model needs no help: both arms scored
  100%, and the platform arm paid a small friction cost (median 8 tool calls
  vs 5), mostly SEARCH_REQUIRED refusals while the model adopts the
  search-first workflow. The platform's value concentrates where knowledge is
  load-bearing, not on trivial lookups.
- Two of five trap tasks were defeated by raw model inference (the model
  guessed cents from value magnitudes, and computed net revenue unprompted on
  one ranking task). Trap classes vary in strength; phase 2 grades required
  caveats with a calibrated judge to separate "right number" from "right
  number for the right reason".
- Pilot scale: one model, one machine, ten tasks, no bootstrap CIs. Treat
  every number above as directional.
- Model spend for the full pilot, including calibration: about $7.40.
- The S5 pilot is a single arm at k = 1 covering 13 of 15 protocols, so it has
  no pass^k reliability and no arm comparison; personal recall (62%) and the
  similarity-gated duplicate rate (40%) are the honest weak spots to study.
- S5 cost basis, for anyone budgeting a fuller run: about 9.0M input and 0.15M
  output tokens across the 13 graded protocols, roughly $1.3 per protocol on
  claude-sonnet-5. A k = 3 run over all 15 protocols is therefore on the order
  of $55-60. Cost is dominated by the size of the search and enrichment results
  re-sent each turn, not by the tool-call count.

## 4. Result history

| Date | Phase | Arms | Model | Episodes | Raw results |
| --- | --- | --- | --- | --- | --- |
| 2026-07-13 | 1 (pilot) | a0, a2 | claude-sonnet-5 | 60 | `bench/results/v1.102.0-pilot/` |
| 2026-07-13 | 3 (S5 pilot) | a3 | claude-sonnet-5 | 13 protocols | `bench/results/v1.102.0-s5-partial/` |
