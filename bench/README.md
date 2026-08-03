# mcp-data-platform benchmark report series

`bench/` is the harness behind the platform's published benchmark reports. It
measures how *well* an agent connected to this platform works — accuracy,
efficiency, and what an agent does with knowledge it has been given — by
ablating the PLATFORM rather than the model: a run holds the model, prompt
scaffold, seed data, and task set constant and varies only the platform
configuration.

This is distinct from the load harness ([`test/load`](../test/load)), which
answers "how much" (throughput, latency, memory). This one answers "how well".

## The studies

Every artifact belongs to exactly one study. Run-family directories under
[`results/`](results/) are never shared between them.

| | Knowledge-layer effectiveness | Knowledge use | Knowledge pollution | API-connection architecture |
| --- | --- | --- | --- | --- |
| **Question** | Does a semantic knowledge layer make an agent measurably more correct? | When an agent is handed stored knowledge, does it use it? | When a stored insight is wrong, do other identities adopt it over a co-present correct source? | Does connection architecture change an agent's accuracy over a large API? |
| **Published report** | [`benchmark-report.md`](../docs/reference/benchmark-report.md) ([site](https://mcp-data-platform.txn2.com/reference/benchmark-report/)) | [`benchmark-report-knowledge-use.md`](../docs/reference/benchmark-report-knowledge-use.md) ([site](https://mcp-data-platform.txn2.com/reference/benchmark-report-knowledge-use/)) | none yet | none |
| **DOI** | [10.5281/zenodo.21438044](https://doi.org/10.5281/zenodo.21438044) (concept) | [10.5281/zenodo.21614059](https://doi.org/10.5281/zenodo.21614059) | not yet minted | none |
| **Protocol** | [`docs/knowledge-layer-protocol.md`](docs/knowledge-layer-protocol.md) | [`docs/knowledge-use-protocol.md`](docs/knowledge-use-protocol.md) | [`docs/knowledge-pollution-study-design.md`](docs/knowledge-pollution-study-design.md) | [`docs/api-connection-study-design.md`](docs/api-connection-study-design.md) |
| **Pre-registration** | issues #930, #942-#945 | [`docs/perishable-knowledge-study-design.md`](docs/perishable-knowledge-study-design.md), [fixture](docs/perishable-knowledge-fixture.md), [estimator audit](docs/perishable-knowledge-estimator-audit.md) | issue #1163 (filed after its premise probe held), then [`docs/knowledge-pollution-study-design.md`](docs/knowledge-pollution-study-design.md) (the confirmatory matrix, its estimator audit in section 6) | the protocol above |
| **Toolchain** | [`reports/knowledge-layer/`](reports/knowledge-layer/) — `make bench-report-knowledge-layer-pdf` | [`reports/knowledge-use/`](reports/knowledge-use/) — `make bench-report-knowledge-use-pdf` | pending (#1168) | none |
| **Run data** | top-level families under [`results/`](results/) | [`results/knowledge-use/`](results/knowledge-use/) | [`results/knowledge-pollution/`](results/knowledge-pollution/) | [`results/api-study-pilot/`](results/api-study-pilot/) |
| **Status** | published, report version 2.0 | published, report version 1.0, pinned to v1.116.0 | in progress: premise probe held, harness merged, protocol pre-registered (#1163) | closed not planned; postmortem on #1027 |

Negative results and evidence-backed platform decisions are indexed in
[`docs/findings-register.md`](docs/findings-register.md) — a retired study
candidate gets a register row, not silence, so concluded research is never
repeated unknowingly.

Each directory under [`results/`](results/) carries a README stating what that
run family does and does not establish; [`results/README.md`](results/README.md)
maps every family to its study.

One kind of measurement here belongs to no study by design.
[`docs-eval/`](docs-eval/) holds documentation probes, which measure what a
model concludes after reading one of this repository's pages. A probe is
evidence about prose, never about the platform, so it carries no protocol, no
DOI, and no row in the table above, and it lives outside `results/` so the
one-artifact-one-study rule stays exact.

## Reading the reports

Both reports are neutral evaluations, not marketing pages: every statistic is
recomputed from raw run data committed under `results/` by a notebook that needs
no network access and no API key, and every claim cites the run directory it
comes from. Both carry a threats-to-validity section.

What each report holds fixed differs, and reading a number without that framing
will mislead:

- **Knowledge layer: arm versus arm, on a pinned model.** The subject is the
  difference a platform configuration makes, holding the model and tasks fixed.
  Model identity is disclosed and is never the subject. It also evaluates one
  feature — cross-enrichment, `search`, and the memory/`apply_knowledge`
  lifecycle — and says nothing about OAuth 2.1 auth, personas, audit, the
  gateways, the portal, or the raw toolkits.
- **Knowledge use: condition versus control, with capability as a declared
  axis.** The platform configuration is fixed and the manipulated variables are
  the delivered belief and the world; every cell has a no-knowledge control.
  Model capability is a deliberate second axis there, and the result inverts
  between the two tiers tested, so a number from that report is meaningless
  without its model.

[`docs/reference/benchmarks.md`](../docs/reference/benchmarks.md) is the
product-framed introduction for readers who want the result before the method.

## The convention for every study

One slug per study, used in exactly four places, so the next one is organized
before its first run exists. Ordinals ("report 2") never appear in anything a
reader sees — not titles, subtitles, version strings, nav labels, or citations.
A report's version is its own (starting at 1.0), series membership is a prose
line linking the siblings, and the series name is what a citation carries.

1. Published page: `docs/reference/benchmark-report-<slug>.md`, plus a nav
   entry, the llms files, and the doc-check tool-token gate list.
2. Toolchain: `bench/reports/<slug>/` — recompute (`report.ipynb` and/or a
   script), `render-report.sh`, `pandoc/`, and a
   `make bench-report-<slug>-pdf` target.
3. Run data: `bench/results/<slug>/<family>/<run-dir>/`, each family with a
   README stating what it does and does not establish.
4. Protocol and design docs: `bench/docs/<slug>-*.md`, each with a status
   banner at the top.

The knowledge-layer study predates the convention in two respects, both
permanent and both recorded here rather than fixed: its published page is
`benchmark-report.md` with no slug, and its `results/` families sit at the top
level instead of under a slug directory. The deposited PDF cites those paths.

## Running

Each study has its own stack and its own protocol document; the recipes below
are the entry points, and the full procedure for each is in its protocol.

**Knowledge layer** ([protocol](docs/knowledge-layer-protocol.md)) — compose
stack, seeded Trino warehouse, DataHub quickstart for the `a1`/`a2`/`a3` arms:

```bash
make bench-up BENCH_ARM=a0        # compose stack + seeded warehouse + platform
make bench-smoke                  # scripted no-API-key end-to-end validation
make bench-run BENCH_ARM=a0 K=3   # real run (needs ANTHROPIC_API_KEY)
make bench-compare                # cross-arm tables + bootstrap CIs -> markdown
make bench-down
```

Its S5 lifecycle, supersede, and cold-start suites (`make bench-lifecycle`,
`bench-supersede`, `bench-cold-start`) boot the same stack on the `a3` arm; the
cold-start suite additionally requires a fresh DataHub quickstart and an empty
enrichment layer. See the protocol.

**Knowledge use** ([protocol](docs/knowledge-use-protocol.md)) — one arm, the
perishable fixture surface, its own database. No DataHub or Trino, but it does
need `ollama serve` with `nomic-embed-text`:

```bash
make bench-pk-up                                  # Postgres + fixture + platform
make bench-pk-corpus REPLICATES=3 MODEL=sonnet    # capture-corpus episodes
make bench-pk-run CELLS=prerun K=8 MODEL=sonnet   # a cell set
make bench-pk-down
```

The runner's `-scaffold` flag selects the episode system prompt: `default`
(the study scaffold, including its "use the search tool" bullet) or
`no-discovery` (the same text minus that one bullet, for probes that ask
whether the agent discovers when only the platform's own steering channels
— `platform_info` agent instructions and tool descriptions — tell it to).
The text used is recorded verbatim in the run manifest. The search-first
gate itself is a platform config: `bench/config/platform.bench.pk-gateoff.yaml`
is the pk arm's single-deviation copy with `workflow.require_search: false`,
selected with `make bench-pk-up BENCH_PK_CONFIG=bench/config/platform.bench.pk-gateoff.yaml`.

**Knowledge pollution** ([protocol](docs/knowledge-pollution-study-design.md)) — the `a3` arm on a
disposable database. The evaluation arms are ordinary `benchrun` runs over the
committed S3 tasks; what differs between them is the stack, which
`bench/pollutionplant` changes:

```bash
cd bench && go run ./pollutionplant -mode table            # the attribution table, computed from the fixtures
cd bench && go run ./pollutionplant -mode check            # matrix vs the committed graders
cd bench && go run ./pollutionplant -mode plant \
  -treatment fiscal-boundary-wrong -url http://localhost:8098 > planted.json
cd bench && go run ./pollutionplant -mode remediate \
  -treatment fiscal-boundary-wrong -remediation rollback -planted planted.json
```

A plant is not complete until a second identity has been shown to reach the
claim, and a remediation is not complete until the claim's status and its
reachability have been read back; both refuse rather than report a state they
could not verify. Planting writes to the stack, so run it against a database a
measured run will not reuse without a reset.

Every run writes into its own timestamped directory under
`build/bench-results/`, and the runners refuse an output path that already
exists, so a re-run can never overwrite paid-for results.

## Why a separate module

`bench/` is its own Go module (same rationale as `test/load`): the repository
root runs coverage, test, and lint gates over `./...`, and a nested module is
never matched by the root's `./...`. Run its checks from this directory:

```bash
cd bench
go build ./... && go vet ./... && go test ./...
golangci-lint run ./...
```

Or from the repo root: `make bench-test` and `make bench-lint`. Both are part
of `make verify`: they are pure module checks (build, vet, test, full-module
lint) that mirror CI's "Harness module checks" job, and because this module is
outside the root `./...`, the root `lint`/`test` targets never reach it — a
bench-only finding would otherwise surface first in CI (the `bench-lint` full-
module scope also matters: the root lint's --new-from-patch scoping cannot see
a finding anchored on an unchanged line, such as a gocognit report on a func
whose body grew).

Like mutation and load testing, benchmark **runs** are deliberately not part of
`make verify` — they stand up Docker services, a real server binary, and (for
real runs) a model API. Do not add the stack-dependent `bench-*` run targets
(`bench-up`, `bench-run`, `bench-smoke`, the lifecycle/supersede/cold-start
runs, the `bench-pk-*` targets) to the `verify` target; `bench-test` and
`bench-lint` are the only exceptions because they touch nothing outside the
module.

## Layout

```
bench/
├── docs/                study protocols, pre-registrations, findings register
│   ├── knowledge-layer-protocol.md
│   ├── knowledge-use-protocol.md
│   ├── perishable-knowledge-study-design.md    pre-registration (#1054)
│   ├── perishable-knowledge-fixture.md         fixture reference
│   ├── perishable-knowledge-estimator-audit.md treatment-string audit
│   ├── api-connection-study-design.md          closed study (#1027)
│   └── findings-register.md
├── reports/             per-study recompute + render toolchains
├── results/             archived run data, one directory per family
├── docs-eval/           documentation probes (measure a page, not the platform; no study, no DOI)
├── benchrun/            CLI entry point (run, summarize, compare, calibrate)
├── seedgen/             deterministic artifact/task generator CLI
├── apigen/              fixture catalog + spec + task + seed generator CLI
├── apisvc/              fixture HTTP service CLI (#1027 catalog, or -surface perishable)
├── apisetup/            registers a fixture spec as a platform connection
├── epmcp/               per-endpoint MCP server used by the #1027 b0 arm
├── pkcorpus/            capture-corpus runner CLI (knowledge use, stage 1)
├── pkrun/               cell runner CLI (knowledge use)
├── pollutionplant/      plant / remediate / attribution-table CLI (knowledge pollution)
├── config/              arm profiles (a0/a1/a2/a3 and the pk profile)
├── seed/                generated seed artifacts (committed; bench-gen)
├── specs/               generated fixture OpenAPI specs + world/fixture data (committed; bench-api-gen)
├── tasks/               generated task YAML + smoke script (committed)
├── tasks-api/           generated API-study task YAML (committed)
├── protocols/           generated S5 lifecycle protocol YAML + smoke (committed)
├── curriculum/          generated cold-start curriculum YAML + smoke (committed)
├── judge/               versioned rubric + human-labeled calibration set
└── internal/
    ├── gen/             dataset model, emitters, ground-truth computation, protocols, curriculum
    ├── apigen/          fixture catalog model, spec emitter, seeded state, perishable world registry
    ├── apisvc/          fixture HTTP service: catalog handlers, insights surface, /_bench/ control plane
    ├── apisetup/        registers fixture connections against a running platform
    ├── apistudy/        per-attempt retrieval, write detection, failure taxonomy
    ├── epmcp/           per-endpoint MCP server built from a spec fixture
    ├── fixturectl/      control-plane client: reset, world change, phase, state dumps, state grading
    ├── pkcorpus/        capture-corpus scenarios, episode runner, archive
    ├── pkseed/          frozen belief set, the RQ2 phrasing factorial, and delivery metadata
    ├── pkplant/         plants a delivered belief as the identity that will be asked
    ├── pkcell/          cells, derived correct behavior, ground truths, deterministic grading
    ├── pkrun/           cell runner: plant, move the world, ask, grade
    ├── pollutionplant/  knowledge pollution: treatments, cells, plant, remediation drivers
    ├── task/            task schema, loader, task-set hash
    ├── protocol/        S5 lifecycle protocol schema, loader, protocol-set hash
    ├── curriculum/      cold-start curriculum schema, loader, curriculum-set hash
    ├── llm/             adapter interface + anthropic + scripted
    ├── claudecli/       real Claude Code client path (claude -p) + stream parse
    ├── agent/           model-driven tool loop with budget
    ├── mcpc/            MCP session, handle mint, session_id threading
    ├── auditapi/        admin audit API read-back + metrics (+ enrichment coverage)
    ├── capture/         capture attempt signal, miss attribution, attempted/landed split (S5 + cold-start)
    ├── lifecycleapi/    admin insights + changesets read-back, approve + apply drivers
    ├── promote/         shared reviewer-promotion path (approve + apply_knowledge + verify)
    ├── grade/           deterministic graders (numeric, entity, execution-result)
    ├── judge/           LLM judge + calibration harness
    ├── pipeline/        task x k orchestration
    ├── lifecycle/       S5 protocol runner, stage graders, metrics, results model
    ├── coldstart/       cold-start curriculum runner, learning-curve metrics, results model
    ├── pool/            identity-pool allocation
    ├── report/          results model, aggregates, cross-arm comparison
    ├── stats/           bootstrap confidence intervals + the shared num/den rate type
    └── target/          endpoint + Bearer auth
```
