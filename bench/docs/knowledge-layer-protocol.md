# Knowledge-layer effectiveness: harness protocol (#930)

> **Status: study published.** Results are in
> [`docs/reference/benchmark-report.md`](../../docs/reference/benchmark-report.md)
> (report version 1.1, concept DOI
> [10.5281/zenodo.21438044](https://doi.org/10.5281/zenodo.21438044)). This
> document is the protocol of record for that report: the arms, the seeded
> ground truth, the measurement instrument, the grading rules, and the four
> suites. The report cites its sections by name, so a section heading here is
> a citation target and renaming one is a breaking change (enforced by
> `TestHarnessCitationsResolve`).

The study asks whether an agent connected to this platform answers real data
questions more correctly and more efficiently than the same agent connected to
bare data tools, by ablating the PLATFORM rather than the model: every run
holds the model, prompt scaffold, seed data, and task set constant and varies
only the platform configuration.

Entry point and sibling studies: [`bench/README.md`](../README.md).

## Arms

Arms are platform config profiles (`bench/config/`), not code forks — the config
surface is the ablation mechanism. All four ablate one layer at a time:

| Arm | Profile | What the agent gets |
| --- | --- | --- |
| `a0` | `platform.bench.a0.yaml` | Raw toolkit tools only (`trino_*`, `s3_*`); no semantic provider, no search, all cross-enrichment off, search-first gate off. Equivalent to wiring the standalone toolkit libraries. |
| `a1` | `platform.bench.a1.yaml` | A0 plus semantic cross-enrichment: `trino_*`/`s3_*` results carry DataHub context automatically, but the agent still has no `search` and no `datahub_*` tools (the persona withholds them; the datahub instance exists only to feed enrichment). Isolates enrichment from discovery. |
| `a2` | `platform.bench.a2.yaml` | The shipped semantic-first platform: A1 plus the `search` tool, the search-first gate, and seeded knowledge pages. |
| `a3` | `platform.bench.a3.yaml` | A2 plus the lifecycle surface: `memory_*` and `apply_knowledge`. On the single-episode S1–S4 suites A3 has nothing seeded to recall, so it tracks A2; the lifecycle's effect is measured by the S5 protocols (#944, below). |

The search-first gate is not persona-aware, so the arms without a discovery
tool (`a0`, `a1`) disable `workflow.require_search`; the profiles document this.
The DataHub arms (`a1`, `a2`, `a3`) require a DataHub quickstart seeded via
`make bench-seed-datahub`.

## Seeded ground truth

`seedgen` generates everything from one fixed-seed dataset model
(`bench/internal/gen`, seed 930): Trino DDL/DML (memory catalog), DataHub metadata
proposals (descriptions, column units, deprecation), knowledge-page SQL, and
the task YAML whose ground truths are computed from the generated rows —
derived, never hand-typed. `TestCommittedArtifactsMatch` fails if the
committed artifacts drift from regeneration (`make bench-gen` refreshes them).

The phase-2 task set (87 tasks) spans three suites, all applicable to every
arm:

- **S1 discovery** (17): "which table answers X", graded by entity alias
  match. Several are knowledge-dependent (deprecation of `legacy_orders`, the
  gross-only/stale nature of the pre-aggregated index).
- **S2 analytical accuracy** (45): exact numeric questions at BIRD-style tiers
  (single-table, join, temporal, cross-tab, top-N). Graded numerically or by
  entity; four are SQL-producing tasks graded by execution-result comparison
  (see below). S2 states monetary units explicitly (cents) so it measures
  query formulation, not the units trap.
- **S3 knowledge traps** (25): answerable plausibly-but-wrongly without the
  knowledge layer, across six seeded trap classes, each mirroring a fixture
  the generator plants:
  - `units_cents` — monetary columns are integer cents; the fact lives in
    column and dataset descriptions (enrichment-visible).
  - `net_revenue` — revenue = amount − discount over completed orders only;
    lives in the dataset description and the revenue-policy page. The gross
    leader and the net leader differ by construction.
  - `fiscal_calendar` — the fiscal year runs Feb 1 – Jan 31; lives ONLY in the
    fiscal-calendar page, so it separates the knowledge arm from enrichment.
  - `freshness_cutoff` — the daily index stops at 2025-11-30, so post-cutoff
    questions must use raw orders; lives in the index description and page.
  - `tier_boundary` — a "key account" is any plus- or enterprise-tier
    customer; lives only in the tier-definitions page.
  - `deprecated_table` — `legacy_orders` is a partial, deprecated extract;
    the deprecation lives in metadata and the warehouse page.

  Each S3 task carries a rubric note (the required caveat) scored by the phase-2
  LLM judge (`bench/judge/`).

## Measurement

The platform's audit log is the measurement instrument. The harness calls
`platform_info` to mint the `dps_` session handle, strips the injected
`session_id` property from every tool schema shown to the model, and threads
the handle itself — measurement plumbing invisible to the agent, uniform
across arms. Efficiency metrics are read back from
`GET /api/v1/admin/audit/events?session_id=...`; arm profiles run audit with
`delivery: sync`, and a run **fails loudly** when a session's audit rows fall
outside the bounds of the harness's client-side accounting: confirmed calls
must all have rows, platform refusals (authz, the gates, the per-user
limiter) short-circuit outer to the audit middleware and have none, and
transport-level failures are counted as indeterminate (the platform may have
audited before the error surfaced).

Two independence guarantees keep attempts comparable:

- **Identity pool.** The search-first gate keys discovery on the
  authenticated USER, not the MCP session, so every attempt authenticates as
  its own pool identity (`<key>-001`..`-320`, defined in the arm configs;
  `-identity-keys` must match, and a run refuses to start when tasks x k
  exceeds the pool). The pool is sized to the thirty-protocol lifecycle suite at
  k=5, its largest single consumer (30 protocols x 5 x 2 identities = 300
  attempts), which also covers the full phase-2 task set at k=3 (87 tasks x 3 =
  261 attempts); to resize it, run this from `bench/` (and set the matching
  `-identity-keys`):

  ```python
  import re, glob
  N = 320  # >= identities needed by the largest single run
  entries = "\n".join(
      f'      - {{key: "${{API_KEY_ADMIN}}-{i:03d}", name: "bench-agent-{i:03d}", roles: ["admin"]}}'
      for i in range(1, N + 1)) + "\n"
  # Replace the existing flow-style pool (every "- {key: ...-NNN...}" line) in place.
  pool_re = re.compile(r'(?:      - \{key: "\$\{API_KEY_ADMIN\}-\d+".*\n)+')
  for p in glob.glob("config/platform.bench.a*.yaml"):
      src = open(p).read()                       # read before opening for write
      open(p, "w").write(pool_re.sub(entries, src, count=1))
  ```

  `make bench-run` additionally resets the persisted discovery state
  (`search_gate_discovery`) so repeated runs start clean.
- **Harness failures never grade.** Attempts that fail at the harness level
  (connect, adapter, audit read-back) are excluded from accuracy and reported
  separately; pass^k requires all k attempts graded and correct.

Grading is deterministic and scores only the first line after the mandated
"FINAL ANSWER:" marker: numeric answers prefer the first decimal-bearing
number (a restated year is a bare integer and is skipped when a decimal
candidate exists); entity answers must name a correct alias and must not name
any of the task's known trap answers (`wrong_aliases`, generated with the
task).

**Execution-result grading (BIRD-style).** SQL-producing S2 tasks put a query
on the FINAL ANSWER line; the grader extracts it, executes the candidate and
the task's reference query, and compares result sets as multisets of rows by
cell value (column aliasing and row order do not matter), so two
different-but-equivalent queries both pass. The grader runs its queries through
a dedicated admin-credentialed MCP session, separate from every attempt handle,
so its own tool calls never perturb an attempt's audit accounting.

**The LLM judge** scores only the judgment-call rubric items the deterministic
graders cannot — whether an S3 answer carried the required caveat. The judge
model is pinned and the rubric versioned (`bench/judge/rubric.yaml`); the judge's
agreement with human labels is measured over a committed calibration set
(`bench/judge/calibration.yaml`, 30 items) and published with any judged scores.
`make bench-calibrate` runs the calibration.

## S5 memory-insight-knowledge lifecycle (#944)

The S5 suite is not single-episode questions but multi-episode **protocols**
(`bench/protocols/`, generated), each a sequence of fresh sessions that exercises the
teach-once-answer-forever lifecycle and verifies every state transition through
the platform's own admin APIs — the insights and changesets endpoints — never
inferred from a transcript. It runs only on the `a3` arm (the lifecycle tools
`memory_capture` and `apply_knowledge` exist there).

Every protocol runs **teach**, **recall**, and **abstain**; each additionally
runs EITHER promote+transfer OR supersede, never both (see below). The stages:

1. **Teach** — an identity states a fact conversationally and saves it. The
   harness verifies via `GET /api/v1/admin/knowledge/insights?captured_by=...`
   that a pending insight was captured and linked to the entity.
2. **Recall** — the same identity, a fresh session, answers a question needing
   the fact. Graded deterministically; the run also records whether `search`
   surfaced the memory unprompted.
3. **Promote** — the harness plays the reviewer: it approves the insight and
   applies it via `apply_knowledge` to one of two sinks (an entity description
   or a knowledge page), then confirms through the changesets API that the
   insight is `applied` and a live changeset lists it.
4. **Transfer** — a DIFFERENT identity, a fresh session, answers the same
   question. It can only succeed because the promotion pushed the fact into
   shared knowledge (cross-enrichment for the entity sink, `search` for the
   page sink): the teach-once-answer-forever claim.
5. **Update** — the teacher corrects the fact; a later recall must flip to the
   new value, and the taught insight must show `superseded` (not left live
   alongside the correction, which the run flags as a duplicate).
6. **Abstain** — a question about a fact never taught must be answered "I do
   not know", not fabricated.

**Promote and update are mutually exclusive per protocol** (enforced by the
protocol validator). The platform deliberately never supersedes an
already-applied insight — a newer capture must not clobber a reviewed one — so a
fact that has been promoted (and is therefore `applied`) can no longer be cleanly
superseded. The twenty promote protocols therefore exercise stages 1–4 + 6; the
ten supersede protocols exercise stages 1, 2, 5, 6 on a fact that stays pending.
Both mechanics are measured, on different facts.

Each protocol teaches a **novel** definition — one deliberately absent from the
seeded knowledge pages and catalog metadata — so recall and transfer are clean:
the only way to answer is the taught memory (recall) or the promoted knowledge
(transfer), never the pre-seeded fixtures. The ten supersede protocols redefine
a computable quantity so the corrected recall is a different, generator-computed
value than the original (e.g. the "primary region" flips from the gross-revenue
leader to the net-revenue leader). Ground truth is computed from the dataset,
never hand-typed, exactly as the S1–S3 truths.

The protocol set was doubled from fifteen to thirty for issue #965 (twenty
promote + ten supersede), which roughly doubles every metric's denominator — the
supersede metrics most of all, whose applicable denominators the phase-4 data
exposed as small as 7–10 — so the lifecycle rates can be reported with tighter
confidence intervals rather than the noisy ranges the small set produced.

Each protocol attempt consumes two identities from the pool (a teacher and a
learner) so the search-first gate's per-user discovery scope never leaks between
attempts; the lifecycle requires a pool (there is no single-identity mode, since
teacher and learner must differ), and a run refuses to start when
`protocols x k x 2` exceeds it. Capture and duplicate verification are scoped to
the specific taught insight, so re-running against a persistent knowledge store
that reuses pool identities does not cross-contaminate the metrics.

Metrics (each a numerator/denominator over the applicable, non-harness-failed
runs): **capture rate**, **personal recall**, **unprompted surface**, **transfer
rate**, **update correctness**, **update capture rate**, **duplicate rate**
(lower is better), and **abstention rate**, plus **pass^k** over protocols (all
k attempts pass the full applicable lifecycle). Capture leads the scorecard
because it caps every metric under it: an insight that was never recorded can be
neither recalled nor promoted, so a capture miss removes the attempt from the
downstream stages rather than merely lowering one number. It is therefore
reported with its own confidence interval and a decomposition (below), not as a
supporting count. The duplicate rate counts only
attempts whose correction capture actually executed: an update episode that
never called capture left one live insight and the supersede gate never ran, so
it is an update-capture miss (measured on its own rate, and a pass^k failure),
not a duplicate. Harness-level failures (connect, adapter, API read-back)
are excluded from the metrics and reported separately, mirroring the S1–S3
pipeline.

Every rate carries a **95% percentile-bootstrap confidence interval** (issue
#965), resampled from its numerator/denominator with a fixed seed (the shared
`bench/internal/stats` machinery the S1–S3 report uses), so the interval is
reproducible and a reader sees the uncertainty directly instead of inferring it
from a range across replicates. The bootstrap treats each applicable outcome as
an independent draw and does not model protocol-level correlation across the k
replicates, so a narrow interval over a small, few-protocol denominator still
warrants caution — the point of growing the set is to enlarge that denominator.

### Per-stage diagnosis instrumentation (#964)

Three metrics decompose the S5 gaps the phase-4 data exposed, so a weak headline
number can be attributed rather than guessed:

- **transfer surfaced** and **used given surfaced** split the transfer rate. A
  transfer attempt records whether the promoted fact actually appeared in a tool
  result the learner saw (`transfer_surfaced`), and among those, whether the
  answer was correct (`transfer_used_given_surfaced`). A low surfaced rate points
  at delivery (cross-enrichment or search did not carry the fact to the second
  identity); a low used-given-surfaced rate points at reasoning (the agent had
  the fact and ignored it). Surfacing is a normalized-substring match of the
  promoted content against the episode's tool results, so it is a conservative
  "the fact was present", not a paraphrase match.
- **capture budget-starved** is, among capture misses, the fraction where the
  teach episode exhausted its tool-call budget without executing a capture call —
  the discovery-budget-exhaustion failure mode. A capture request the budget
  refused (emitted only after the budget was spent) counts as starved, not as an
  attempted capture. The rate is measured on the in-process loop path, which owns
  the tool-call budget; claude-cli runs manage their own turn budget, so their
  capture misses are excluded from this rate (`teach_budget_exhausted` is left
  unset) rather than miscounted as not-starved.

The **capture-budget lever** `-teach-budget N` overrides the per-episode tool-call
budget for the capture-bearing stages (teach and update), so the capture-rate
lift from a larger teach budget can be measured directly against the same
protocol set.

### Capture decomposition and miss attribution (#1136)

Capture is a headline metric with an interval in both the S5 lifecycle and the
cold-start suite, and both report the same decomposition under
`metrics.capture_split`, built by `bench/internal/capture`:

- **capture attempted** (`capture_split.attempt_rate`) is, over the same graded
  denominator as the capture rate, the fraction of teach episodes that actually
  executed a capture call. A budget-refused request never ran, so it does not
  count as an attempt. The one case where its denominator is smaller than the
  capture rate's is a results file written before the attempt signal existed:
  those outcomes are excluded rather than assumed not-attempted, and any miss
  among them is reported as `unattributed` with a warning.
- **landed given attempt** (`capture_split.given_attempted`) is, among those
  attempts, the fraction that produced an entity-linked insight.
- **capture misses** (`capture_split.misses`) attributes every graded miss to
  exactly one cause, and reports the total so the buckets can be checked to sum:
  `attempted_failed` (capture ran, nothing landed — the capture path itself),
  `budget_starved` (never executed, budget spent — a harness-budget concept, so
  no platform change follows from it), `never_attempted` (never executed with
  budget to spare — the model or its steering), and `budget_unobservable`
  (never executed on the claude-cli path, whose turn budget the harness cannot
  see, so the miss is attributed as far as the evidence allows and no further).
  A fifth bucket, `unattributed`, exists only for results written before the
  attempt signal did; a run from this harness leaves it zero.

The three real causes imply fixes in different layers, which is the point of
splitting them: a run that reports mostly `never_attempted` argues for a
platform-side nudge, one that reports mostly `budget_starved` argues for a
harness budget change and nothing product-side, and one that reports mostly
`attempted_failed` argues for the capture path. Cold-start additionally names
the cause on each missed lesson in its summary, since a lesson that misses
capture is never promoted and its trap class stays flat for the rest of the
curve.

Like the other #964 diagnostics, the decomposition is deliberately **not**
gated by `-baseline`: its denominators are small enough that gating would trip
on noise. It exists to explain a capture regression, not to define one.

### Harness hardening (#966)

- **Regression gate.** `-baseline <committed.json>` with `-lifecycle` gates the run against a committed S5 baseline and exits nonzero when a headline lifecycle metric regresses, so CI catches a lifecycle capability loss the same way it already catches an S1-S3 one. It applies to both a single-process run and a `-merge`d k=N scorecard (the canonical multi-pass artifact CI gates). The gated metrics are capture rate, personal recall, transfer rate, update correctness, abstention rate, duplicate rate (an increase past tolerance is the regression), and pass^k; a metric that either run did not exercise (zero denominator) is skipped as a coverage gap, not scored as a drop. The #964 diagnostic decompositions are deliberately not gated — their small denominators would trip the gate on noise; they exist to explain a regression, not define one. The gate refuses a cross-arm comparison, and a cross-client-path one (anthropic vs claude-cli), rather than producing a meaningless verdict; it compares the client path, not the exact CLI version, so a benign `claude` bump does not disable it. Default tolerances are loose (5 points) to absorb run-to-run variance.
- **Output isolation.** The transcript directory is keyed on the full `-out` filename (`results.json` -> `results.json.transcripts/`), so several passes written into the same directory under different output names — even ones sharing a stem but differing by extension — never overwrite one another's raw transcripts. The `-merge` step refuses an `-out` that is the same on-disk file as one of its input passes (compared by device+inode, so a case-variant or symlinked alias is caught too), so a merged scorecard never clobbers the raw per-pass evidence it was built from. Together these mean multi-pass orchestration cannot silently discard paid-for data.
- **claude-cli cache tokens.** A cached `claude -p` run's `cache_read_input_tokens` / `cache_creation_input_tokens` flow through the stream parser into each lifecycle `EpisodeRecord` and are summed into the run's `total_cache_read_tokens` / `total_cache_creation_tokens`, so a cached run self-reports its true cost basis (cache reads bill far below fresh input). The full parser -> `EpisodeRecord` -> aggregate path is covered by a test that drives the real parser on a canned cached stream, and the parser itself is confirmed against a real `claude` process by the cold-start runs, which are claude-cli and record non-zero `total_cache_read_tokens` and `total_cache_creation_tokens` (`bench/results/cold-start-a3-20260717-142008-3064/results.json`). No real claude-cli *lifecycle* run exists yet, so the lifecycle record's own field mapping rests on that test rather than on archived evidence; the runner warns loudly when an episode with tool calls reports zero usage, so a silent field move cannot go unnoticed.

### Statistical power and identity-pool sizing (#965)

The lifecycle rates are reported with confidence intervals (above), but a CI only
describes the run you did — it does not tell you how large a run you *need* to
resolve a real change. This section sizes both the protocol set and the identity
pool against a target effect.

**Applicable denominators.** Not every metric spans all thirty protocols. The
transfer stage runs only on the twenty promote protocols; the supersede,
duplicate, and update-correctness stages run only on the ten supersede protocols;
capture, personal recall, and abstention span all thirty. At k replicates the
applicable count per metric group is:

| metric group | protocols | applicable n at k=3 | at k=5 |
| --- | --- | --- | --- |
| capture / personal recall / abstention | 30 | 90 | 150 |
| transfer (+ its surfaced/used split) | 20 | 60 | 100 |
| supersede / duplicate / update correctness | 10 | 30 | 50 |

**Detectable effect.** For a two-sided test at α = 0.05 and 80% power, the
normal-approximation sample size for a proportion is n ≈ (z_{α/2} + z_β)² ·
p(1−p) / Δ² = 7.84 · p(1−p) / Δ². Taking the worst-case p = 0.5 (variance 0.25),
the smallest shift Δ a given applicable n can resolve is Δ ≈ √(1.96 / n):

| applicable n | smallest resolvable Δ (pts) |
| --- | --- |
| 30 | ~26 |
| 50 | ~20 |
| 60 | ~18 |
| 90 | ~15 |
| 100 | ~14 |
| 150 | ~11 |

Reading the two tables together: at k = 3 the headline rates resolve a ~15-point
change, transfer a ~18-point one, and supersede/duplicate only a ~26-point one —
which is why the phase-4 supersede numbers (denominator 7–10 before this ticket)
read as noise. At k = 5 the supersede denominator reaches 50 (~20-point
resolution) and transfer reaches 100 (~14 points). To resolve a 15-point transfer
change — the size of the observed transfer ceiling — plan for k = 5 over the
twenty promote protocols (n = 100 ≥ the 87 the formula requires at Δ = 0.15).
These are lower bounds: the normal approximation is optimistic at small n and
extreme p, and the bootstrap CIs treat replicates as independent draws (they do
not model protocol-level correlation across the k attempts of one protocol), so
treat the k = 5 column as the floor for a headline claim, not a guarantee.

**Identity-pool sizing.** Each attempt consumes two pool identities (teacher +
learner), so a run needs `2 × protocols × k` keys and refuses to start otherwise.
For the thirty-protocol set:

| k | identities needed (2 × 30 × k) | fits the 320-key pool? |
| --- | --- | --- |
| 3 | 180 | yes |
| 4 | 240 | yes |
| 5 | 300 | yes |

The committed pool in `bench/config/platform.bench.a*.yaml` is 320 keys, grown from
264 for the k = 5 lifecycle run (#1139) — the size the power analysis recommends
for a firm transfer claim, needing 300 identities, with the remainder as headroom
against a protocol set that grows again. The `-identity-keys` default matches it
(`bench/benchrun/main.go`), so the lifecycle target, which passes no such flag,
inherits the right pool. A run that would exceed the pool refuses to start rather
than sharing a discovery scope between attempts.

## Supersede sub-benchmark (#964)

`-supersede` runs the recall-first supersede gate **in isolation**: it drives
only the supersede protocols (those with an `update` stage) through teach →
capture-verify → correct → supersede-status check, skipping the promote,
transfer, personal-recall, and abstain stages that otherwise dilute the signal.
The isolated harness exists because the S5 duplicate rate was the noisiest S5
metric (0% vs 42.9% between identical runs); measuring supersede on its own, with
a per-protocol stability breakdown, makes that instability visible per protocol
instead of hidden in one blended range.

Metrics: **capture rate**, **update capture rate** (the correction capture
executed; a miss is excluded from the supersede/duplicate denominator — with no
correction on the platform the gate never ran), **supersede rate** (original
superseded, higher is better), **duplicate rate** (its complement, lower is
better), **update correctness**, **pass^k**, and a per-protocol
`superseded`/`duplicated`/`update_capture_missed` count across the k attempts. Because the supersede gate is embedding-similarity based
(nomic-embed-text), the threshold / embedding-model / deterministic-fallback
evaluation is done by re-running this sub-benchmark against platforms configured
differently and comparing the supersede rate — the sub-benchmark is the measuring
instrument; the platform config is the independent variable.

```bash
make bench-up BENCH_ARM=a3
make bench-supersede-smoke               # scripted no-API-key validation
make bench-supersede K=3                 # real run (needs ANTHROPIC_API_KEY)
make bench-supersede K=3 TEACH_BUDGET=20 # same, with the larger teach budget lever
make bench-supersede-report RESULTS=build/bench-results/supersede-a3-<stamp>/supersede-a3.json
```

## Cold-start knowledge growth (#963)

The S1-S3 and S5 suites ablate the platform with a **pre-seeded** knowledge base.
The cold-start suite (`bench/curriculum/`, generated) instead starts from an **empty
enrichment layer** and measures the platform getting smarter as knowledge
accumulates — a learning curve whose independent variable is the amount of
**promoted (shared)** knowledge, holding the model, prompt, task set, and dataset
constant.

It runs on the `a3` arm against an **empty baseline**: an undocumented DataHub
(`bench/seed/datahub/bench_mces_empty.json` — entities present, but no descriptions,
column docs, tags, or glossary) and **no knowledge pages** (`bench-up` with
`BENCH_SEED_PAGES=0`). Over an ordered **curriculum** of six lessons — one per S3
trap class — the harness:

1. **Teaches** each fact (a teacher identity states it and captures it via
   `memory_capture`), then **promotes** it to its sink through `apply_knowledge`:
   a DataHub entity description (units, freshness, deprecation) or a portal
   knowledge page (net-revenue policy, fiscal calendar, tier definitions). Each
   lesson teaches the same S3 trap fact the A2 seed pre-loads, so the trap
   suite reaches its A2 accuracy ceiling once all six are promoted (the
   fact-bearing description and page channels are restored; A2's auxiliary
   aspects — tags, the structured deprecation flag, column docs — are not, but
   the S3 traps read the fact text, not those). Capture and promotion are verified through
   the admin insights and changesets APIs, reusing the same reviewer-promotion
   path (`bench/internal/promote`) the S5 lifecycle uses; after the API verify, the
   reviewer also reads the promoted content back from its sink (the entity's
   effective description via `datahub_get_entity`, the page's summary via the
   portal list) and fails the run when an API-confirmed apply is not readable
   there — a silent sink-write loss must abort before episodes are spent, not
   surface later as an unexplained flat curve.
2. **Evaluates** at every checkpoint (the empty baseline and after each lesson)
   by re-running the fixed S3 trap suite with a **fresh, never-taught evaluator
   identity**. Its only knowledge source is what the platform surfaces —
   cross-enrichment for the DataHub-sink facts, `search` for the page-sink facts
   — so accuracy climbs only because promotion pushed the fact into shared
   knowledge. This isolates the delivery of *promoted* knowledge (the coupling
   between the lifecycle and the enrichment layer), not an evaluator's own memory.

The report leads with the **capture rate** — the fraction of lessons whose teach
episode landed an entity-linked insight, with a confidence interval, the
attempted/landed split, and every miss attributed to a cause (see the capture
decomposition above). Capture bounds the whole curve: a lesson that misses
capture is never promoted, so its trap class stays flat for every checkpoint
after it, and a bare captured-lesson count cannot say which layer to fix.

The rest of the report is the **learning curve**: per checkpoint, the eval set's
accuracy, a per-trap-class breakdown (which lesson unlocked which class), and the
delivery-side **enrichment coverage** (the fraction of tool calls whose response
carried cross-enrichment, from the audit trail). Lesson order is the x-axis, run
foundational-first (units before net-revenue, then the calendar/freshness/tier/
deprecation facts) so a multi-fact trap flips to correct only once every fact it
needs has landed.

Grading is the deterministic S3 grading (numeric tolerance, entity alias); the
suite reuses one identity pool (a distinct teacher per lesson, fresh evaluators
per checkpoint), and a run refuses to start when the lessons plus per-checkpoint
evaluators exceed the pool.

**A cold-start run requires a FRESH DataHub quickstart**, not just re-ingesting
the empty seed. `apply_knowledge` description promotions write the
`editableDatasetProperties` aspect, and a prior a2 seed leaves
`editableSchemaMetadata` column docs, tags, and deprecation; the empty seed
(`bench_mces_empty.json`) upserts only `datasetProperties`, so re-ingesting it
cannot clear any of that, and the read path prefers the editable description
when non-empty. A **baseline-integrity preflight** enforces this: before any
episode is spent, the run reads every lesson entity through the platform,
lists insights for every (teacher, lesson URN) pair, and scans knowledge pages
for the curriculum slugs, refusing to start if anything is already there.
Postgres state (search gate, memory records, changesets, knowledge pages) is
reset by the `bench-cold-start` target's TRUNCATE; DataHub state requires
`datahub docker nuke`, a re-quickstart, then `make bench-seed-datahub-empty`.

Between a successful datahub-sink promote and the next eval checkpoint the
runner pauses for the `-settle` window (default `5m`, matching the a3 semantic
cache TTL, `SETTLE=` on the make target) so a table-context cache entry
populated by the previous checkpoint's evaluators can never serve the stale
pre-promotion description to the next ones. Page-sink promotes skip the pause
(page hits are served live from the portal store, nothing is cached), as do
lessons that did not promote. The window is recorded on the results manifest,
and the scripted smoke runs with `SETTLE=0s`.

**Pass criteria.** A zero exit code is NOT a pass signal: capture misses and
promote refusals are measured outcomes by design, so a run can exit 0 with a
flat curve. A valid full run's summary must read `lessons 6 (captured 6,
promoted 6)` with `harness failures 0`, enrichment coverage climbing from the
baseline, no evaluator memory-write warning (evaluators are forbidden from
saving memories; the summary warns if the audit-side count is non-zero), and no
audit-read-back warning (a lost audit read contributes zero to coverage through
signal loss, recorded per attempt as `audit_read_error` and totaled on the
summary).


## Running

From the repository root:

```bash
make bench-up BENCH_ARM=a0        # compose stack + seeded warehouse + platform
make bench-smoke                  # scripted no-API-key end-to-end validation
make bench-run BENCH_ARM=a0 K=3   # real run (needs ANTHROPIC_API_KEY)
make bench-report BENCH_ARM=a0    # single-arm human summary
make bench-compare                # cross-arm tables + bootstrap CIs -> markdown
make bench-calibrate              # judge-vs-human agreement rate
make bench-down
```

The S5 lifecycle protocols run against a booted `a3` arm (which needs the same
DataHub quickstart as `a2`, plus the memory/knowledge Postgres tables that
auto-enable):

```bash
make bench-up BENCH_ARM=a3        # boot the lifecycle arm
make bench-lifecycle-smoke        # scripted no-API-key lifecycle validation
make bench-lifecycle K=3          # real run (needs ANTHROPIC_API_KEY)
make bench-lifecycle-report RESULTS=build/bench-results/lifecycle-a3-<stamp>/lifecycle-a3.json
```

The **scripted lifecycle smoke** (`-llm scripted`) plays
`bench/protocols/scripted-lifecycle-smoke.json` (generated): it captures via
`memory_capture`, recalls by answering with each stage's computed ground truth,
drives the reviewer-side promotion, and abstains — validating handle threading,
the insight/changeset APIs, supersede, grading, and the metrics against the live
platform with no API key and no model variance.

The cold-start suite (#963) boots the same `a3` arm but with the empty baseline
(no knowledge pages, undocumented DataHub, on a FRESH quickstart; the preflight
refuses leftovers from a prior run or an a2 seed, see the cold-start section
above), then teaches the curriculum:

```bash
# Fresh DataHub quickstart first (datahub docker nuke + re-quickstart if reused),
# then boot a3 with an empty enrichment layer: no knowledge pages, empty DataHub.
make bench-up BENCH_ARM=a3 BENCH_SEED_PAGES=0
make bench-seed-datahub-empty            # entities present, undocumented

make bench-cold-start-smoke              # scripted no-API-key loop validation (SETTLE=0s)
make bench-cold-start K=1                # real learning-curve run (needs a model)
make bench-cold-start LLM=claude-cli MODEL=sonnet K=1   # subscription run
make bench-cold-start-report RESULTS=build/bench-results/cold-start-a3-<stamp>/results.json
```

Every `bench-cold-start` invocation writes into its own timestamped directory
(`build/bench-results/cold-start-a3-<stamp>/results.json` plus a
`results.json.transcripts/` directory beside it) and `benchrun` refuses an
`-out` that already exists, so a re-run can never overwrite a prior run's
paid-for results; `bench-cold-start-report` therefore takes the run to
summarize via `RESULTS=` (it lists the available run dirs when unset).

Two model paths, two cost profiles. `LLM=claude-cli` runs each episode through
a real `claude -p` client and is subscription-funded (the runner strips
`ANTHROPIC_API_KEY` from the child environment): a k=1 full run is 181 episodes
(6 teaches + 7 checkpoints x 25 eval tasks) at roughly 50s each, so plan for
2.5-3 hours of wall clock. The `anthropic` adapter bills the API: k=1 is
estimated around USD 20 at claude-sonnet-5 pricing, extrapolated from the
phase-2 per-attempt token data. Either way a full run exceeds an interactive
session: launch it in the background and read the summary from the run dir.

The **scripted cold-start smoke** (`-llm scripted`) plays
`bench/curriculum/scripted-cold-start-smoke.json` (generated): each lesson captures its
fact and the harness drives the real promotion; each eval task answers with its
computed ground truth. One run validates the whole teach → capture → promote →
eval loop, the insight/changeset APIs, deterministic grading, and the
learning-curve metrics against the live platform with no model. Its eval answers
are always correct (the smoke measures plumbing, not model behavior), so its
curve is flat-high; the climbing curve is a property of a real model run against
the empty baseline.

For the DataHub arms (`a1`, `a2`, `a3`), start a DataHub quickstart first (same
external convention as e2e and load), then `make bench-seed-datahub` and
`make bench-up BENCH_ARM=a2` (or `a1`/`a3`). Run each arm, then
`make bench-compare` renders the arm-by-suite comparison (accuracy with 95%
bootstrap CIs, pass^k, median tool calls, the S3 trap-class breakdown, and the
headline deltas vs the baseline) from all `results-*.json` files.

The **scripted adapter** (`-llm scripted`) plays a deterministic script
(`bench/tasks/scripted-smoke.json`, generated) instead of a model: SQL-backed tasks
run their reference SQL through the live `trino_query` and answer with the
live result. One smoke run therefore validates the seed data, the stored
ground truths, handle threading, audit read-back, and both graders against the
real platform — with no API key and no model variance. Real runs use the
Anthropic adapter (`-llm anthropic`, model pinned in the manifest, no sampling
parameters; run-to-run variance is handled by k-repeats and pass^k).

### claude-cli adapter (subscription, no API key)

`-llm claude-cli` runs each attempt through a real Claude Code client
(`claude -p`) instead of the harness's in-process agent loop, so a
subscription (Pro/Max) user can run real episodes with **no metered
`ANTHROPIC_API_KEY`**. It works for both the S1-S3 task pipeline and the S5
lifecycle protocols.

```bash
make bench-run BENCH_ARM=a2 LLM=claude-cli MODEL=sonnet K=3        # subscription task run
make bench-lifecycle LLM=claude-cli MODEL=sonnet K=1               # subscription lifecycle run
```

How it differs from the in-process adapters:

- Each attempt gets a generated `--mcp-config` pointing Claude Code at the
  platform's streamable endpoint, authenticated with the attempt's identity-pool
  key (the same per-attempt Bearer rotation the loop path uses). Claude Code
  connects directly, calls `platform_info`, and threads the minted `dps_` handle
  itself — exercising the real handle-threading and search-first steering a
  production agent does, rather than the harness's synthetic loop.
- The child `claude` always runs in subscription mode: the runner strips
  `ANTHROPIC_API_KEY` from its environment, so a key sourced for `-llm anthropic`
  never silently bills a claude-cli run. It runs in an isolated temp working
  directory with `--strict-mcp-config` and the built-in file/shell/web tools
  disallowed, so only the platform's MCP tools are in play.
- Audit-derived efficiency metrics are correlated by the `dps_` handle Claude
  Code threads onto every data call — exactly the anchor the in-process loop
  uses, read from the stream's `platform_info` result. This reuses the same
  min/max read-back contract: confirmed successful calls are the lower bound
  (each must have an audit row, or the run fails loudly), errored and unresolved
  calls raise the upper bound, and `platform_info`'s own row is not under the
  handle so it is naturally excluded, matching the other adapters.
- The manifest records the client path (`llm_provider: claude-cli`), the
  `claude --version` string (`client_version`), and the model.

**Comparability caveat.** The raw `anthropic` adapter stays canonical for
published and regression numbers. `claude -p` reinserts Claude Code's own system
prompt, tool-use policy, context management, and retries, all of which change
across Claude Code releases. Within one run the arm-vs-arm delta is internally
valid (both arms see the same client), but across runs a Claude Code upgrade
could move the numbers with no platform change, which would break the
regression-baseline contract. `client_version` in the manifest exists so a
claude-cli run is never silently compared against a raw Messages API run.

## Output

`benchrun` writes a results JSON (manifest: git commit, platform version,
model, dataset seed, task-set hash, arm, k, and — on the claude-cli path — the
client version; per-attempt records with their trap classes; per-task and
per-suite aggregates) plus per-attempt transcripts, and
prints a human summary. `benchrun -compare a0.json,a1.json,...` builds the
cross-arm comparison and `-compare-out page.md` writes the markdown page
(`make bench-compare`); bootstrap CIs use a fixed resampling seed so identical
inputs produce identical intervals. Phase 4 (#945) delivered the published
pages — [`docs/reference/benchmarks.md`](../../docs/reference/benchmarks.md)
and [`docs/reference/benchmark-report.md`](../../docs/reference/benchmark-report.md)
— and the `-baseline` regression gate described under
[Harness hardening (#966)](#harness-hardening-966).

