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

## 2. Honest caveats

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

## 3. Result history

| Date | Phase | Arms | Model | Episodes | Raw results |
| --- | --- | --- | --- | --- | --- |
| 2026-07-13 | 1 (pilot) | a0, a2 | claude-sonnet-5 | 60 | `bench/results/v1.102.0-pilot/` |
