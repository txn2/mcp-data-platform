# Agent-effectiveness benchmark harness

The benchmark for issue #930 (phase 1 pilot: #942). It measures whether an
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
surface is the ablation mechanism:

| Arm | Profile | What the agent gets |
| --- | --- | --- |
| `a0` | `platform.bench.a0.yaml` | Raw toolkit tools only (`trino_*`, `s3_*`); no semantic provider, no search, all cross-enrichment off, search-first gate off. Equivalent to wiring the standalone toolkit libraries. |
| `a2` | `platform.bench.a2.yaml` | The shipped semantic-first platform: DataHub enrichment, the `search` tool, the search-first gate, seeded metadata and knowledge pages. |

Arms `a1` (enrichment-only) and `a3` (memory lifecycle) join in phase 2/3
(#943, #944). The search-first gate is not persona-aware, so `a0` must disable
`workflow.require_search`; the profile documents this.

## Seeded ground truth

`seedgen` generates everything from one fixed-seed dataset model
(`internal/gen`, seed 930): Trino DDL/DML (memory catalog), DataHub metadata
proposals (descriptions, column units, deprecation), knowledge-page SQL, and
the task YAML whose ground truths are computed from the generated rows —
derived, never hand-typed. `TestCommittedArtifactsMatch` fails if the
committed artifacts drift from regeneration (`make bench-gen` refreshes them).

The pilot task set (10 tasks):

- **S1 discovery** (5): "which dataset answers X", graded by entity alias
  match.
- **S3 knowledge traps** (5): answerable plausibly-but-wrongly without the
  knowledge layer. Two seeded trap classes: `units_cents` (monetary columns
  are integer cents; the fact lives only in metadata) and `net_revenue`
  (policy revenue = amount - discount over completed orders only; the fact
  lives in a knowledge page and dataset description; the gross leader and the
  net leader differ by construction). Graded numerically or by entity; each
  task carries rubric notes recorded into transcripts for manual review
  (LLM-judged in phase 2).

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
  its own pool identity (`<key>-01`..`-32`, defined in the arm configs;
  `-identity-keys` must match, and a run refuses to start when tasks x k
  exceeds the pool). `make bench-run` additionally resets the persisted
  discovery state (`search_gate_discovery`) so repeated runs start clean.
- **Harness failures never grade.** Attempts that fail at the harness level
  (connect, adapter, audit read-back) are excluded from accuracy and reported
  separately; pass^k requires all k attempts graded and correct.

Grading is deterministic and scores only the first line after the mandated
"FINAL ANSWER:" marker: numeric answers prefer the first decimal-bearing
number (a restated year is a bare integer and is skipped when a decimal
candidate exists); entity answers must name a correct alias and must not name
any of the task's known trap answers (`wrong_aliases`, generated with the
task). Judgment-call scoring is deliberately deferred to the phase-2 judge.

## Running

From the repository root:

```bash
make bench-up BENCH_ARM=a0        # compose stack + seeded warehouse + platform
make bench-smoke                  # scripted no-API-key end-to-end validation
make bench-run BENCH_ARM=a0 K=3   # real run (needs ANTHROPIC_API_KEY)
make bench-report BENCH_ARM=a0
make bench-down
```

For `a2`, start a DataHub quickstart first (same external convention as e2e
and load), then `make bench-seed-datahub` and `make bench-up BENCH_ARM=a2`.

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
model, dataset seed, task-set hash, arm, k; per-attempt records; per-task and
per-suite aggregates) plus per-attempt transcripts, and prints a human
summary. Pilot-phase reporting is manual from these files; the report
generator, CIs, and the published `docs/reference/benchmarks.md` page land in
phases 2 and 4 (#943, #945).

## Layout

```
bench/
├── benchrun/            CLI entry point
├── seedgen/             deterministic artifact/task generator CLI
├── config/              arm profiles (platform configs)
├── seed/                generated seed artifacts (committed; bench-gen)
├── tasks/               generated task YAML + smoke script (committed)
└── internal/
    ├── gen/             dataset model, emitters, ground-truth computation
    ├── task/            task schema, loader, task-set hash
    ├── llm/             adapter interface + anthropic + scripted
    ├── agent/           model-driven tool loop with budget
    ├── mcpc/            MCP session, handle mint, session_id threading
    ├── auditapi/        admin audit API read-back + metrics
    ├── grade/           deterministic graders (numeric, entity)
    ├── pipeline/        task x k orchestration
    ├── report/          results model, aggregates, human summary
    └── target/          endpoint + Bearer auth
```
