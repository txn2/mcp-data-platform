# Agent-effectiveness benchmark harness

The benchmark for issue #930 (phase 1 pilot #942; phase 2 suites and graders
#943). It measures whether an
agent connected to this platform answers real data questions more correctly
and more efficiently than the same agent connected to bare data tools — by
ablating the PLATFORM, not the model: every run holds the model, prompt
scaffold, seed data, and task set constant and varies only the platform
configuration (arms).

This is distinct from the load harness (`test/load`): that suite answers "how
much" (throughput, latency, memory); this one answers "how well" (accuracy,
tool-call efficiency, knowledge-trap resistance).

## Why a separate module

`bench/` is its own Go module (same rationale as `test/load`): the repository
root runs coverage, test, and lint gates over `./...`, and a nested module is
never matched by the root's `./...`. Run its checks from this directory:

```bash
cd bench
go build ./... && go vet ./... && go test ./...
golangci-lint run ./...
```

Or from the repo root: `make bench-test`.

Like mutation and load testing, benchmarking is **deliberately not part of
`make verify`** — it stands up Docker services, a real server binary, and (for
real runs) a model API. Do not add `bench-*` to the `verify` target.

## Arms

Arms are platform config profiles (`config/`), not code forks — the config
surface is the ablation mechanism. All four ablate one layer at a time:

| Arm | Profile | What the agent gets |
| --- | --- | --- |
| `a0` | `platform.bench.a0.yaml` | Raw toolkit tools only (`trino_*`, `s3_*`); no semantic provider, no search, all cross-enrichment off, search-first gate off. Equivalent to wiring the standalone toolkit libraries. |
| `a1` | `platform.bench.a1.yaml` | A0 plus semantic cross-enrichment: `trino_*`/`s3_*` results carry DataHub context automatically, but the agent still has no `search` and no `datahub_*` tools (the persona withholds them; the datahub instance exists only to feed enrichment). Isolates enrichment from discovery. |
| `a2` | `platform.bench.a2.yaml` | The shipped semantic-first platform: A1 plus the `search` tool, the search-first gate, and seeded knowledge pages. |
| `a3` | `platform.bench.a3.yaml` | A2 plus the lifecycle surface: `memory_*` and `apply_knowledge`. On the single-episode S1–S4 suites A3 has nothing seeded to recall, so it tracks A2; the lifecycle's effect is measured by the S5 protocols (#944). |

The search-first gate is not persona-aware, so the arms without a discovery
tool (`a0`, `a1`) disable `workflow.require_search`; the profiles document this.
The DataHub arms (`a1`, `a2`, `a3`) require a DataHub quickstart seeded via
`make bench-seed-datahub`.

## Seeded ground truth

`seedgen` generates everything from one fixed-seed dataset model
(`internal/gen`, seed 930): Trino DDL/DML (memory catalog), DataHub metadata
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
  LLM judge (`judge/`).

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
  its own pool identity (`<key>-001`..`-264`, defined in the arm configs;
  `-identity-keys` must match, and a run refuses to start when tasks x k
  exceeds the pool). The pool is sized to the full phase-2 task set at k=3 (87
  tasks x 3 = 261 attempts); to resize it, run this from `bench/` (and set the
  matching `-identity-keys`):

  ```python
  import re, glob
  N = 264  # >= tasks x k for the largest single run
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
model is pinned and the rubric versioned (`judge/rubric.yaml`); the judge's
agreement with human labels is measured over a committed calibration set
(`judge/calibration.yaml`, 30 items) and published with any judged scores.
`make bench-calibrate` runs the calibration.

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

For the DataHub arms (`a1`, `a2`, `a3`), start a DataHub quickstart first (same
external convention as e2e and load), then `make bench-seed-datahub` and
`make bench-up BENCH_ARM=a2` (or `a1`/`a3`). Run each arm, then
`make bench-compare` renders the arm-by-suite comparison (accuracy with 95%
bootstrap CIs, pass^k, median tool calls, the S3 trap-class breakdown, and the
headline deltas vs the baseline) from all `results-*.json` files.

The **scripted adapter** (`-llm scripted`) plays a deterministic script
(`tasks/scripted-smoke.json`, generated) instead of a model: SQL-backed tasks
run their reference SQL through the live `trino_query` and answer with the
live result. One smoke run therefore validates the seed data, the stored
ground truths, handle threading, audit read-back, and both graders against the
real platform — with no API key and no model variance. Real runs use the
Anthropic adapter (`-llm anthropic`, model pinned in the manifest, no sampling
parameters; run-to-run variance is handled by k-repeats and pass^k).

## Output

`benchrun` writes a results JSON (manifest: git commit, platform version,
model, dataset seed, task-set hash, arm, k; per-attempt records with their trap
classes; per-task and per-suite aggregates) plus per-attempt transcripts, and
prints a human summary. `benchrun -compare a0.json,a1.json,...` builds the
cross-arm comparison and `-compare-out page.md` writes the markdown page
(`make bench-compare`); bootstrap CIs use a fixed resampling seed so identical
inputs produce identical intervals. The published `docs/reference/benchmarks.md`
page and the regression gate land in phase 4 (#945).

## Layout

```
bench/
├── benchrun/            CLI entry point (run, summarize, compare, calibrate)
├── seedgen/             deterministic artifact/task generator CLI
├── config/              arm profiles (a0/a1/a2/a3 platform configs)
├── seed/                generated seed artifacts (committed; bench-gen)
├── tasks/               generated task YAML + smoke script (committed)
├── judge/               versioned rubric + human-labeled calibration set
└── internal/
    ├── gen/             dataset model, emitters, ground-truth computation
    ├── task/            task schema, loader, task-set hash
    ├── llm/             adapter interface + anthropic + scripted
    ├── agent/           model-driven tool loop with budget
    ├── mcpc/            MCP session, handle mint, session_id threading
    ├── auditapi/        admin audit API read-back + metrics
    ├── grade/           deterministic graders (numeric, entity, execution-result)
    ├── judge/           LLM judge + calibration harness
    ├── pipeline/        task x k orchestration
    ├── report/          results model, aggregates, cross-arm comparison
    └── target/          endpoint + Bearer auth
```
