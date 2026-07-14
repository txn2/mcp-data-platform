# Agent-Effectiveness Benchmarks

This page publishes results from the agent-effectiveness benchmark
(`bench/`, issue #930): does an agent connected to this platform answer real
data questions more correctly and more efficiently than the same agent
connected to bare data tools — and does the platform's memory layer let a fact
taught in one session survive into later ones? The benchmark ablates the
**platform**, not the model: every run holds the model, prompt scaffold, seed
data, and task set constant and varies only the platform configuration.

The platform has two halves, and each is measured by its own benchmark:

- The **semantic / data-analysis layer** — cross-enrichment, search, and the
  curated knowledge layer — is measured by the **S1–S3 arm ablation** (single
  session, four platform configurations).
- The **memory / knowledge lifecycle layer** — capture a fact once, recall and
  reuse it across later, separate sessions — is measured by the **S5 lifecycle
  protocols**. This is a first-class platform capability whose value is
  inherently cross-session, so it cannot be measured by the single-session
  S1–S3 tasks; it has its own multi-session benchmark instead.

Both layers are valuable, and each is proven by the benchmark built to exercise
it. Methodology, arm definitions, and reproduction commands live in
[`bench/README.md`](https://github.com/txn2/mcp-data-platform/tree/main/bench).
The load harness's throughput numbers are a separate concern; see
[Tuning and Scaling](tuning-and-scaling.md).

Reading rule: the headline is always arm-vs-arm (or, for S5, works-vs-fails) on
a pinned model — the platform's effect. Model identity is disclosed but is
never the subject.

## How to read this page

Every result compares the **same model on the same tasks**, changing only what
the agent is connected to. The shorthand in the tables:

**Arms** — the platform configuration being measured. Each ablates one layer:

| Arm | What the agent is connected to |
| --- | --- |
| **A0 — raw tools** | The underlying data tools directly (`trino_*`, `s3_*`), no semantic provider, no search, all cross-enrichment off. Equivalent to wiring the standalone toolkit libraries. |
| **A1 — enrichment** | A0 plus semantic cross-enrichment: tool results carry DataHub context automatically, but the agent still has no `search` and no `datahub_*` tools. Isolates enrichment from discovery. |
| **A2 — platform** | The shipped semantic-first platform: A1 plus the `search` tool, the search-first gate, and curated knowledge pages. |
| **A3 — lifecycle** | A2 plus the memory / `apply_knowledge` lifecycle. On the single-session S1–S3 tasks A3 has nothing seeded to recall, so it tracks A2; the lifecycle's effect is what the S5 protocols measure. |

**Suites** — tasks grouped by what they test:

| Suite | Question it asks |
| --- | --- |
| **S1 — discovery** | Can the agent find the right table and columns? (straightforward lookups) |
| **S2 — analytical accuracy** | Can the agent compute exact numeric answers (single-table, join, temporal, cross-tab, top-N)? Four tasks emit SQL graded by execution-result comparison. |
| **S3 — knowledge traps** | Questions with a plausible-but-wrong answer, where the disambiguating fact lives in business knowledge, not in the raw data. |
| **S5 — memory lifecycle** | Does a fact taught in one session get captured, recalled, promoted, transferred to a different user, and correctly updated across later, separate sessions? |

**Scores:**

- **Accuracy** — correct graded attempts / graded attempts, averaged across the k = 3 repeats. The bracket is the 95% bootstrap confidence interval.
- **pass^k / pass^3** — a stricter bar than accuracy: a task counts as passed only if it passed **every one of the k = 3** attempts (tau-bench pass^k). "Accuracy" is the per-attempt average; "pass^3" is all-or-nothing per task.
- **Median / p90 tool calls** — how many tool calls the agent made to reach an answer, read from the platform's own audit log (p90 = 90th percentile). Fewer is better — efficiency is a first-class axis, following BIRD's Valid Efficiency Score.
- **`3/3`** (trap table) — passed 3 of 3 attempts on that task; **`0/3`** — failed all three.

## Methodology

**The four arms** are platform config profiles (`bench/config/`), not code
forks — the config surface is the ablation mechanism, which is itself a product
property worth proving. Each arm turns on exactly one more layer: A0 raw tools,
A1 adds cross-enrichment, A2 adds search and curated knowledge, A3 adds the
memory/`apply_knowledge` lifecycle. The arms without a discovery tool (A0, A1)
disable the search-first gate, which is not persona-aware; the DataHub arms (A1,
A2, A3) run against a seeded DataHub quickstart.

**Three model adapters.** The canonical `anthropic` adapter drives an
in-process agent loop against the Messages API with prompt caching on; it
produces all published and regression numbers. A `scripted` adapter replays a
deterministic script with no API key for smoke validation. A `claude-cli`
adapter drives a real Claude Code client (`claude -p`) for subscription/keyless
runs (#949). **Comparability caveat:** `claude -p` reinserts Claude Code's own
system prompt, tool policy, and retries, which shift across Claude Code
releases; within one run the arm-vs-arm delta is internally valid, but
claude-cli numbers are a labeled secondary path and are never mixed with
`anthropic` numbers in a published table. The manifest records the adapter and,
for claude-cli, the client version.

**Grading is deterministic**, scoring only the first line after a mandated
`FINAL ANSWER:` marker: numeric answers compare the first decimal-bearing
number; entity answers must name a correct alias and must not name any of the
task's generated trap answers. SQL-producing S2 tasks are graded BIRD-style — the
grader executes both the candidate and a reference query and compares result
sets as multisets of rows, so two different-but-equivalent queries both pass.
**A pinned LLM judge** scores only the one thing the deterministic graders
cannot: whether an S3 answer carried the required caveat. The judge's agreement
with human labels is measured over a committed 30-item calibration set and
published with any judged scores.

**The audit log is the measurement instrument.** The harness mints a session
handle via `platform_info`, threads it invisibly, and reads efficiency metrics
back from the admin audit API with `delivery: sync`; a run fails loudly when a
session's audit rows fall outside the harness's client-side accounting.
Attempts that fail at the harness level (connect, adapter, audit read-back)
never grade and are reported separately.

**Cost basis.** Every attempt records its exact token split, including
`cache_read_tokens` and `cache_creation_tokens`, so a run's cost is computed
from committed data rather than estimated. Cache reads bill at roughly a tenth
of fresh input, which is what makes a full four-arm k = 3 run land in the tens
of dollars rather than hundreds. All dollar figures on this page are computed
from those committed per-attempt token records at the standard
`claude-sonnet-5` rates ($3 / $15 per million input / output tokens, cache read
$0.30, cache write $3.75 per million), so they are reproducible regardless of
any promotional pricing in effect on the run date.

**Reproducibility parameters** (shared by both suites): dataset seed 930; k = 3
repeats; a fixed bootstrap resampling seed, so identical inputs produce
identical confidence intervals. The task set and protocol set are content-hashed
and pinned in each run's manifest.

> **Build caveat.** Both suites below were produced on the development platform
> build `v1.102.0-9-gadfb9d90-dirty`, recorded in each manifest. This is a
> deliberate cost decision: the S1–S3 data is complete, reproducible, and
> manifest-pinned, so it is reused as-is rather than regenerated for a cleaner
> tag, and the S5 run was made on the same build for coherence. A future
> release-tagged run can refresh both suites under the identical pipeline if a
> tagged publication warrants it.

## 1. Semantic layer — S1–S3 arm ablation

Status: full phase-2 suites, four arms, k = 3, 87 tasks × 3 = 261 graded
attempts per arm, zero harness failures.

Manifest:

| Field | Value |
| --- | --- |
| Arms | a0 (raw tools), a1 (enrichment), a2 (platform), a3 (lifecycle) |
| Model | claude-sonnet-5 (anthropic adapter, prompt caching on) |
| Repeats | k = 3 (pass^3 reported) |
| Dataset seed | 930 |
| Task-set hash | `7e727f891ee5` |
| Platform | v1.102.0-9-gadfb9d90-dirty @ commit `a373ff7f98e3` |
| Semantic layer | DataHub quickstart on OpenSearch |
| Attempts | 261 per arm (all graded; zero harness failures) |
| Efficiency source | platform audit API, `delivery: sync` |
| Raw results | `bench/results/phase2-anthropic-k3/` |

### Accuracy by suite

Accuracy is over graded attempts; the bracket is the 95% bootstrap CI.

| Suite | a0 (raw tools) | a1 (enrichment) | a2 (platform) | a3 (lifecycle) |
| --- | --- | --- | --- | --- |
| S1 discovery | 98.0% [94–100] | 98.0% [94–100] | 100.0% [100–100] | 98.0% [94–100] |
| S2 analytical | 100.0% [100–100] | 100.0% [100–100] | 97.8% [95–100] | 97.8% [95–100] |
| S3 knowledge traps | 42.7% [32–55] | 57.3% [45–68] | 98.7% [96–100] | 98.7% [96–100] |
| Overall | 83.1% [79–88] | 87.4% [83–91] | 98.5% [97–100] | 98.1% [96–100] |
| Overall pass^3 | 80% | 86% | 98% | 97% |

The effect concentrates in S3, exactly where BIRD showed a ~20-point
external-knowledge gap with hand-curated evidence. Here the platform supplies
that evidence automatically: S3 accuracy climbs 42.7% → 57.3% → 98.7% as
enrichment and then the knowledge layer come online, a **+56.0-point** gain for
the full platform over bare tools (95% CI +44 to +67). Enrichment alone (a1)
recovers about a quarter of the gap (+14.7, CI −1 to +31); the curated
knowledge layer and search (a2) close nearly all of it. S1 and S2 are near
ceiling for every arm — a capable model finds tables and computes arithmetic
without help — so the platform's value is not in easy lookups but where a fact
outside the raw data disambiguates a plausible-but-wrong answer.

### S3 knowledge-trap accuracy by class

Each trap is answerable plausibly-but-wrongly without the knowledge layer. The
six classes separate what enrichment can carry (a fact in column/dataset
metadata) from what requires the curated knowledge pages.

| Trap class | a0 | a1 | a2 | a3 |
| --- | --- | --- | --- | --- |
| units_cents | 61.9% [48–76] | 88.1% [79–98] | 97.6% [93–100] | 97.6% [93–100] |
| net_revenue | 21.2% [9–36] | 48.5% [30–67] | 100.0% [100–100] | 100.0% [100–100] |
| fiscal_calendar | 0.0% [0–0] | 0.0% [0–0] | 100.0% [100–100] | 100.0% [100–100] |
| freshness_cutoff | 91.7% [75–100] | 100.0% [100–100] | 100.0% [100–100] | 100.0% [100–100] |
| tier_boundary | 0.0% [0–0] | 0.0% [0–0] | 93.3% [80–100] | 100.0% [100–100] |
| deprecated_table | 95.2% [86–100] | 95.2% [86–100] | 100.0% [100–100] | 100.0% [100–100] |

The pattern is legible. `units_cents` and `net_revenue` live in column and
dataset descriptions, so enrichment alone (a1) lifts them materially (62→88,
21→49). `fiscal_calendar` and `tier_boundary` live **only** in curated
knowledge pages — invisible to enrichment — so a0 and a1 score 0% and only the
knowledge arm (a2) recovers them. This is the design working as intended: each
trap is defeated exactly when the layer that carries its fact is switched on.

### Efficiency (median tool calls by suite)

Fewer is better. Read from the audit log, not self-reported.

| Suite | a0 | a1 | a2 | a3 |
| --- | --- | --- | --- | --- |
| S1 discovery | 6 | 5 | 8 | 7 |
| S2 analytical | 6 | 6 | 9 | 9 |
| S3 knowledge traps | 16 | 11 | 10 | 11 |

On the knowledge traps, the platform reaches a **correct** answer in fewer calls
(16 → 10) than bare tools reach a mostly-wrong one: without the disambiguating
fact, the model flails — issuing exploratory queries and reasoning in circles —
whereas the enriched, search-first path retrieves the fact and answers. On the
easy suites the platform pays a small friction cost (a few extra calls, largely
SEARCH_REQUIRED refusals while the model adopts the search-first workflow),
consistent with the platform's value concentrating where knowledge is
load-bearing rather than on trivial lookups.

### Why a2 ties a3 here (and what it does *not* mean)

On S1–S3, arm a3 (platform + memory lifecycle) tracks arm a2 (platform)
because **these tasks are single-session and never exercise the memory layer**:
each task is one fresh session with nothing previously taught to recall, so the
memory/`apply_knowledge` surface has nothing to act on. a2 ties a3 because
S1–S3 does not test memory — **not** because the memory layer is without value.
The memory layer's value is inherently cross-session (teach once, answer
forever), which single-session accuracy cannot capture by construction. That
capability is measured separately, and directly, by the S5 lifecycle protocols
below.

## 2. Memory layer — S5 lifecycle protocols

Status: two full lifecycle runs on the canonical `anthropic` adapter, arm a3,
k = 3 each — a **shared-store** run and an **isolated** run (defined below).
Unlike the S1–S3 ablation, S5 has no meaningful "memory off" baseline — a recall
task with no memory is trivially 0% — so it does not report an accuracy delta
over a baseline. Instead it measures whether the memory subsystem **works
reliably**: across fresh sessions, is a taught fact captured, recalled,
surfaced, promoted to shared knowledge, transferred to a different user, and
correctly updated?

Two runs are reported because the k-repeats interact with a shared knowledge
store (detailed under Limitations). The **shared-store** run is a single
benchmark process: all 45 attempts run against one knowledge/memory store, so a
protocol's three repeats are *not* independent (an earlier attempt's promotion
persists as searchable knowledge). The **isolated** run removes that
coupling — three independent passes of every protocol at k = 1, with the platform
reset to clean seeded state between passes (fresh Postgres, DataHub descriptions
and knowledge pages re-seeded), then merged into one k = 3 result — so each
protocol's three attempts are genuinely independent. Publishing both quantifies
the confound directly: the gap between them *is* the measured cost of an
accumulating store.

S5 is not single questions but multi-episode **protocols**, each a sequence of
fresh sessions that exercises the teach-once-answer-forever lifecycle and
verifies every state transition through the platform's own admin APIs (the
insights and changesets endpoints), never inferred from a transcript. Each
protocol teaches a **novel** definition — deliberately absent from the seeded
knowledge and catalog — so the intended path to the answer is the taught memory
(recall) or the promoted knowledge (transfer), not a pre-seeded fixture (see the
limitations on re-derivability below). Ground truth is computed from the dataset,
never hand-typed, exactly as the S1–S3 truths. Each attempt consumes two pool
identities (a teacher and a learner) so the search-first gate's per-user
discovery scope never leaks between attempts.

Manifest (both runs share arm a3, model claude-sonnet-5 with prompt caching,
dataset seed 930, protocol-set hash `0920c55292d1`, 15 protocols — 10 promote,
5 supersede — and DataHub-quickstart enrichment with Ollama `nomic-embed-text`
memory embeddings; every lifecycle state transition is verified through the
admin insights + changesets API, never inferred from a transcript):

| Field | Shared-store run | Isolated run |
| --- | --- | --- |
| Regime | 45 attempts, one shared store (k-repeats coupled) | 3 independent k = 1 passes merged to k = 3 (clean reset between) |
| Repeats | k = 3 (pass^3 reported) | 3 passes → k = 3 (pass^3 reported) |
| Platform build | v1.102.0-9-gadfb9d90-dirty | v1.102.0-10-g32d61254-dirty |
| Raw results | `bench/results/s5-anthropic-k3/` | `bench/results/s5-anthropic-k3-isolated/` (+ `pass{1,2,3}.json`) |

The two runs used different development-build strings because the isolated run
rebuilt the platform from the current tree; the difference is
benchmark-harness commits only — the arm config, protocol set (same hash), model,
and seed are identical, so the runs are directly comparable.

### Lifecycle scorecard

Each metric is a numerator/denominator over the applicable, non-harness-failed
runs. Duplicate rate is lower-is-better; every other metric is higher-is-better.

| Metric | Shared-store | Isolated | What it measures |
| --- | --- | --- | --- |
| Capture rate | 80.0% (36/45) | 84.4% (38/45) | the agent recorded the taught fact and entity-linked it (verified via the insights API) |
| Personal recall | 84.4% (38/45) | 88.9% (40/45) | a fresh same-identity session answered the fact-dependent question correctly — graded on answer-correctness like an S1–S3 question, not on proven retrieval (see limitations) |
| Unprompted surface | 100.0% (36/36) | 100.0% (38/38) | among captured runs, `search` surfaced the *saved memory* itself, unprompted — the cleaner "memory was actually used" signal |
| Transfer rate | 42.3% (11/26) | 43.3% (13/30) | a *different* identity answered correctly after the reviewer promoted the insight to shared knowledge |
| Update correctness | 100.0% (10/10) | 100.0% (8/8) | a correction flipped a later recall to the new value |
| Duplicate rate | 10.0% (1/10) | 0.0% (0/8) | supersede left more than one live insight (lower is better) |
| Abstention | 100.0% (45/45) | 100.0% (45/45) | the agent refused to fabricate a fact it was never taught |
| Full-lifecycle pass^3 | 20.0% (3/15) | 26.7% (4/15) | every applicable stage passed all 3 attempts of the protocol |

**The two runs agree on the load-bearing findings.** In both, the platform
**never fabricated a fact it was not taught** (abstention 100%, 45/45 — the
failure mode LongMemEval warns about), **every correction it detected flipped a
later recall to the new value** (update correctness 100%), and **whenever a
memory was captured, search surfaced it unprompted** (unprompted surface 100%).
Transfer sits at ~43% in **both** runs, so it is a **genuine reliability
limit** — a fact promoted to shared knowledge is reused by a different identity
under half the time — not an artifact of the store regime.

**Isolation lifts exactly the metrics the shared-store confound depressed, and
only those.** Removing the cross-attempt coupling raises capture 80.0% → 84.4%,
personal recall 84.4% → 88.9%, and pass^3 20% → 26.7%, and drops the duplicate
rate to 0% (0/8) from 10% — while leaving abstention, update-correctness,
unprompted surfacing, and transfer essentially unchanged. That pattern is the
expected signature of the confound (documented under Limitations): an earlier
attempt's promotion adds search noise that occasionally exhausts a later
attempt's tool-call budget before it captures, and the strict pass^3 penalizes
any single such attempt. The **gap between the two runs is the measured cost of
an accumulating knowledge store** — a few points of capture/recall and roughly
seven points of pass^3 — and it does not manufacture the transfer weakness, which
is real in both. Neither run frames memory as low-value: recall,
update-correctness, unprompted surfacing, and abstention all demonstrate the
lifecycle working end to end.

### Limitations and the shared-store confound

The **shared-store** run measures the lifecycle against a **single
knowledge/memory store shared across all 45 attempts** — state is truncated once
before the run, not between attempts — so the k-repeats of a protocol are not
fully independent. Promotions from earlier attempts persist as globally
searchable knowledge pages. Two consequences are visible in the transcripts and
shape its numbers. The **isolated** run exists precisely to bound this effect,
and the scorecard delta above quantifies it; this subsection documents the
mechanism.

- **Capture failures from discovery-budget exhaustion.** In some later attempts
  the teacher spent its entire tool-call budget searching for and trying to read
  a page a *prior* attempt had promoted, and hit the 30-call cap before invoking
  the capture tool — recorded correctly as a capture miss (the insights API
  confirms zero insights for that identity). In `lc-house-tier` attempt 2, for
  example, the teacher's transcript ends "I ran out of tool-call budget before I
  could actually write the memory record." These are genuine misses, but their
  *cause* is search noise from the shared store, not a broken capture path.
- **Recall can be aided by shared knowledge.** Because personal recall is graded
  on answer-correctness (exactly like an S1–S3 question), a fresh session can
  reach the right answer via a prior attempt's promoted page rather than its own
  memory; some taught definitions are also partly re-derivable from the data.
  The **unprompted-surface** metric (100%) is the cleaner evidence that the saved
  memory itself was used, and should be read alongside recall rather than recall
  being taken as proof of pure retrieval.

These effects reflect a **realistic, accumulating knowledge store** — in
production the store does grow — rather than a defect. The **isolated** run
resets the platform to clean seeded state between each of the three passes (fresh
Postgres via a volume wipe, DataHub descriptions and knowledge pages re-seeded,
verified clean before each pass), so a protocol's three attempts never share a
store and the confound cannot operate. Its higher capture (84.4%), recall
(88.9%), and pass^3 (26.7%) numbers, and its 0% duplicate rate, are the
confound's cost made explicit — a few points of capture/recall and roughly seven
points of pass^3. The isolated run is not a *correction* of the shared-store
numbers; both are valid measurements of different operating conditions
(accumulating vs. reset), and reporting the pair is more informative than either
alone. Two residual caveats apply to **both** runs: personal recall is graded on
answer-correctness (so some taught definitions being partly re-derivable from the
data can inflate it — read it alongside unprompted surface), and cross-*protocol*
search noise within a pass is present in both (different facts, so it adds
discovery noise but does not leak a protocol's own answer).

### Reading S5 correctly

S5 answers a different question than S1–S3. S1–S3 asks "is the platform's
answer more correct?" and reports an accuracy delta between arms. S5 asks "does
the memory subsystem reliably carry a fact across sessions and users?" and
reports whether each stage of that lifecycle works — because the alternative (an
agent with no memory) does not fail *less accurately*, it simply cannot do the
task at all. A recall or transfer task run without the memory layer scores 0% by
construction, so a baseline comparison would be vacuous; the honest measurement
is reliability of the mechanism itself, which is what this scorecard reports.

## 3. Reproducibility and cost

Both suites are reproducible from committed data and a recorded platform build.

**Semantic layer (S1–S3), reused as-is.** From a booted arm:

```bash
make bench-up BENCH_ARM=a0        # then a1, a2, a3 (a1/a2/a3 need a seeded DataHub quickstart)
make bench-run BENCH_ARM=a0 LLM=anthropic MODEL=claude-sonnet-5 K=3
make bench-compare                # cross-arm tables + bootstrap CIs
```

Computed from the committed per-attempt cache-token records across all four
arms (1,044 graded attempts), the S1–S3 suites cost **$67.22** at the standard
`claude-sonnet-5` rates. Prompt caching is what keeps this affordable: the four
arms read ~80.7M cached input tokens (billed at ~$0.30/M) against only ~4.6M
freshly cached-written tokens, so the dominant input cost is cache reads, not
fresh input. The lifecycle arm (a3) is the most expensive single arm ($24.96)
because its larger memory/search context is re-sent each turn.

**Memory layer (S5).** Boot the a3 arm with the metrics port overridden to
avoid the DataHub Kafka :9092 clash, and with `ollama serve` +
`nomic-embed-text` available so supersede detection has an embedding provider:

```bash
make bench-up BENCH_ARM=a3 BENCH_METRICS_ADDR=:9095
make bench-lifecycle LLM=anthropic MODEL=claude-sonnet-5 K=3
make bench-lifecycle-report
```

Computed from the committed per-attempt cache-token records, the shared-store S5
run cost **$18.16** (2.6K fresh input, 356K output, 23.9M cache-read, and 1.5M
cache-write tokens across the 45 attempts), bounded during the run by a spend
watchdog. The local Ollama embedding adds no API cost.

The **isolated** run is three independent single-pass runs with a clean reset
between them, merged into one k = 3 result:

```bash
for pass in 1 2 3; do
  make bench-down && make e2e-down          # wipe Postgres volume
  make bench-seed-datahub                    # reset DataHub descriptions to seed
  make bench-up BENCH_ARM=a3 BENCH_METRICS_ADDR=:9095
  make bench-lifecycle LLM=anthropic MODEL=claude-sonnet-5 K=1 \
    && cp build/bench-results/lifecycle-a3.json bench/results/s5-anthropic-k3-isolated/pass$pass.json
done
build/benchrun -lifecycle -merge bench/results/s5-anthropic-k3-isolated/pass1.json,\
bench/results/s5-anthropic-k3-isolated/pass2.json,bench/results/s5-anthropic-k3-isolated/pass3.json \
  -out bench/results/s5-anthropic-k3-isolated/lifecycle-a3.json
```

It cost **$15.82** (2.4K fresh input, 297K output, 21.3M cache-read, 1.3M
cache-write across its 45 attempts), also watchdog-bounded.

**Regression gate.** Any committed run can serve as a baseline for future runs:

```bash
make bench-run BENCH_ARM=a2 LLM=anthropic K=3 BASELINE=bench/results/phase2-anthropic-k3/full-a2/results.json
```

`benchrun -baseline <results.json>` compares the fresh run to the baseline
suite-by-suite and exits nonzero if any suite regresses beyond the default
thresholds (accuracy or pass^k drop > 5 points, or median tool calls > 1.25×),
so a broken enrichment path or a persona that stops granting search fails CI
loudly. The gate's failure behavior is covered deterministically by unit tests,
and a scripted, no-API-key smoke of the full pipeline (plus a live self-check of
the gate) runs on demand via the `workflow_dispatch` path of the Bench Harness
CI workflow.

**Total published spend: $101.20** — $67.22 for S1–S3 (reused as-is, not
regenerated) plus $18.16 for the shared-store S5 run and $15.82 for the isolated
S5 run.

## 4. Honest caveats

- Results are model-dependent; the headline is arm-vs-arm (or, for S5,
  works-vs-fails) on a single pinned model, never model-vs-model.
- The seed dataset is small by design (fixed seed, airgapped); absolute
  accuracies are not real-world estimates. What the ablation isolates — the
  platform's effect, holding everything else constant — is the point.
- All runs used development builds, not a release tag: S1–S3 and the
  shared-store S5 run on `v1.102.0-9-gadfb9d90-dirty`, the isolated S5 run on
  `v1.102.0-10-g32d61254-dirty` (adjacent builds differing only by
  benchmark-harness commits). The manifests pin each; a tagged run can refresh
  all under the same pipeline.
- CIs are percentile bootstrap over graded attempts with a fixed resampling
  seed — reproducible, but they do not model task-selection variance.
- S1–S3 does not exercise the memory layer, so a2 tying a3 there reflects the
  test scope, not the memory layer's value; the memory layer is measured by S5.

## 5. Result history

| Date | Suite | Arms | Model | Attempts | Raw results |
| --- | --- | --- | --- | --- | --- |
| 2026-07-13 | 1 (pilot) | a0, a2 | claude-sonnet-5 | 60 | `bench/results/v1.102.0-pilot/` |
| 2026-07-13 | 3 (S5 pilot) | a3 | claude-sonnet-5 | 13 protocols | `bench/results/v1.102.0-s5-partial/` |
| 2026-07-14 | Semantic (S1–S3) | a0, a1, a2, a3 | claude-sonnet-5 | 261/arm | `bench/results/phase2-anthropic-k3/` |
| 2026-07-14 | Memory (S5, shared-store) | a3 | claude-sonnet-5 | 15 protocols × 3 | `bench/results/s5-anthropic-k3/` |
| 2026-07-14 | Memory (S5, isolated) | a3 | claude-sonnet-5 | 15 protocols × 3 passes | `bench/results/s5-anthropic-k3-isolated/` |
