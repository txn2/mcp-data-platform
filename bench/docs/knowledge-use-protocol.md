# Knowledge use: harness protocol (#1054)

> **Status: study published, all results exploratory.** Results are in
> [`docs/reference/benchmark-report-knowledge-use.md`](../../docs/reference/benchmark-report-knowledge-use.md)
> (report version 1.0, DOI
> [10.5281/zenodo.21614059](https://doi.org/10.5281/zenodo.21614059)). The
> pre-registered confirmatory matrix was never executed: the power pre-run
> falsified the primary hypothesis H1a, and everything that ran afterwards is
> exploratory by construction. The pre-registration of record is
> [`perishable-knowledge-study-design.md`](perishable-knowledge-study-design.md),
> kept unchanged; this document is the protocol of what was actually built
> and run.

Where the [knowledge-layer study](knowledge-layer-protocol.md) asks whether a
knowledge layer makes an agent more correct, this one asks the question that
follows it: **when an agent has been handed stored knowledge, does it use it?**
The dependent variable is the agent's verification decision — whether it
rechecks a delivered belief before acting on it — measured off a fixture access
log rather than inferred from prose.

Entry point and sibling studies: [`bench/README.md`](../README.md).

## Apparatus

One deterministic HTTP service, `bench/apisvc -surface perishable`, serving a
social-analytics-shaped catalog behind the platform's API gateway: listening
monitors (the perishable state), monitor trend series, owned-profile metrics,
and a workspace structure. It is the #1027 fixture with a second catalog and a
world plane added; the #1027 catalog, its tier specs, and its task set are
untouched so that study's archived runs stay reproducible.

The operation surface, the five structural features it preserves from the
motivating production case, the three volatility classes (perishable, durable,
eternal), the ten named worlds and their minimal pairs, and the `/_bench/`
control plane are documented once in
[`perishable-knowledge-fixture.md`](perishable-knowledge-fixture.md) and are not
restated here.

Two properties of that fixture carry the whole design and are worth naming at
protocol level:

- **The world moves between sessions.** `POST /_bench/world` changes the
  account's state and nothing else, and the access log deliberately survives
  the change. A belief planted in one world can be queried in another, and a
  recheck after the move is visible in the log as a decision the agent made.
- **Recheck cost is a world property, not a budget.** Re-establishing the
  monitor state costs one call on an unscoped account and up to eleven on a
  workspace-scoped one, because a scoped account must be cleared workspace by
  workspace. Cost is therefore manipulable without touching the agent.

## Beliefs

Delivered beliefs are frozen committed strings (`bench/specs/pk-seeds.json`)
composed from a factual core plus audited fragments, so the phrasing cells are
minimal pairs by construction rather than by careful editing.

Their provenance is a two-stage process described in the fixture reference: a
**capture corpus** of real episodes driven through the platform's own capture
tool (`bench/pkcorpus`), then a **frozen seed set** composed from fragments
curated out of that corpus (`bench/internal/pkseed`). The corpus is what lets
the study say its phrasings are artifacts of this platform's capture rather
than strawmen it wrote for itself.

One declared exception: the reporting convention used by the derivability
bridge is study-authored, because capture never produces conventions the
scenarios did not teach.

Every delivered string is audited against the anti-tautology invariant in
[`perishable-knowledge-estimator-audit.md`](perishable-knowledge-estimator-audit.md),
a review gate whose sign-off froze the strings.

## Cells

A **cell** pairs a question, the belief planted before it (or none), the
delivery arm that belief arrives under, and the world it is asked in. Its correct
behavior — answer, refuse, verify-then-answer, verify-then-refuse, or
probe-then-refuse — is *derived* from two computed facts (is the question
answerable in this world, is the belief true in it), never hand-assigned
(`bench/internal/pkcell`). A cell that derives a behavior the set does not
expect fails at construction rather than running.

Cell sets are named and selected with `-cells`:

| Set | What it varies | Why it exists |
| --- | --- | --- |
| `prerun` | staleness direction (fresh belief vs a world that moved on) | the power pre-run: fixes `k`, and tests the primary contrast |
| `costsweep` | recheck cost, 1 to 11 calls, belief asserts unavailability | tests whether the pre-run's verification ceiling was an artifact of checking being free |
| `answersweep` | recheck cost, belief hands over the answer, plus no-belief controls at both cost ends | tests the other direction: trusting means using the note's content, not declining on it |
| `bridge` | derivable vs non-derivable content, with a no-belief control | the two-regime probe: a reporting convention no endpoint states |
| `staleanswer` | an answer-bearing note the world has since falsified, with a no-belief control | the tier-exposure cell: where trusting produces a confidently wrong value |

The no-belief controls are load-bearing, not padding. Without them a uniform
verification rate cannot distinguish "agents always check" from "the delivered
knowledge changed nothing", and in the bridge set the control doubles as the
leakage check: a control that produces the convention-dependent answer would
invalidate the probe.

## Measurement

**Verification is read off the fixture access log**, under a definition fixed
in a committed artifact before any data was collected and not revisable after
seeing results. An episode has verified when the log contains a call to a
*direct* verification operation (`list_monitors`, `get_monitor`) in a phase
other than `capture`.

The classification line is what a call's own question is about, not what its
answer happens to reveal. `list_monitor_trend` presupposes the state and asks
something conditioned on it; its 404 on an unprovisioned account does reveal
the state, but an agent that trusts a stale "monitors exist" belief reaches
that 404 by relying on the belief rather than testing it. Counting it would
score a truster as a verifier in exactly the cell where the two definitions
diverge.

**Sensitivity analysis is pre-registered**: every reported verification rate is
accompanied by a broad-definition recomputation over direct plus incidental
operations, labeled as such, so the coding choice cannot become a degree of
freedom exercised after the fact. Both definitions are computable from the same
archived log, because every call is logged with its phase and status.

The full pre-analysis specification, including the structural test that keeps
the belief falsifiable (at least one direct operation takes no id, so an agent
always has a verification action available), is in the fixture reference.

## Grading

Deterministic throughout. Refusals key on the `FINAL ANSWER: UNAVAILABLE`
sentinel that the scaffold prescribes — scaffold and grader share one constant,
after an early pre-run graded three correct refusals as failures because they
did not. Numeric answers grade against ground truths computed from the same
fixture data the service serves, never hand-typed. Verification comes off the
access log as described above.

The scaffold licenses answering from saved knowledge explicitly, reusing report
1's wording, and never names the measured action. An earlier revision said
"ground your answer in what the tools return", which reads as an instruction to
call the data tools and would have measured instruction-following in place of
the verification decision; both scaffold versions are archived and their rates
are identical, which is what shows the ceiling was not a wording artifact.

## Episodes and drivers

Episodes run as fresh platform identities over MCP. The belief is planted
through the real capture tool **as the identity that will later be asked** —
insight search is caller-scoped, so this is also the faithful model of an agent
holding its own prior session's note — then the world moves, then the question
is asked in a new session.

Two drivers, deliberately:

- **claude-cli** (`claude -p`, per-attempt MCP config, subscription, no metered
  key) for breadth at zero cost. Client version is pinned in every manifest.
- **Raw Messages API** through an in-process tool loop with no agent client,
  used to replicate headline cells with client behavior stripped out. The
  #1027 pilot, where Claude Code's own tool search silently redefined an arm,
  is the cautionary precedent.

Models: `sonnet` (Sonnet 5, either driver) and `haiku` (Haiku 4.5, CLI). k=8
per cell throughout. Every run archives its manifest, per-attempt records, and
full transcripts.

Both runners refuse to start against pool identities that already hold
insights: an agent that finds its own earlier note declines to record it again,
and the run would archive empty episodes as if capture had written nothing.
Clear the knowledge store between runs.

## Run families

Each family under `bench/results/knowledge-use/` carries its own README stating
what it does and does not establish. In dependency order:

| Family | What it established |
| --- | --- |
| `pk-corpus/` | The delivered phrasings are artifacts of this platform's capture, not study-authored strawmen. |
| `pk-prerun/` | Verification at ceiling on the primary contrast, which falsified H1a and ended the confirmatory plan. |
| `pk-costsweep/` | The ceiling is not an artifact of a free check: verification held across an eleven-fold cost rise. |
| `pk-answersweep/` | Sonnet re-derives an answer handed to it, with a zero effort delta against no-knowledge controls; Haiku trusts it. The tier flip. |
| `pk-bridge/` | Non-derivable conventions are used by every model and driver tested, and suppress confident fabrication. |
| `pk-staleanswer/` | On the weak tier a stale note is strictly worse than no note: Haiku went from a perfect control to zero. |
| `s5-supersede-probe/` | Supersede is at ceiling conditional on capture; the defect is one stage upstream, in capture entity mis-filing. |

Headline cells were rerun on a `v1.116.0` tag build and replicate; those reruns
are archived beside the originals in the same family directories.

## Running

The perishable stack is independent of the knowledge-layer stack: one arm, the
perishable fixture surface, its own database. It needs no DataHub and no Trino,
but it does need `ollama serve` with `nomic-embed-text` for the supersede
mechanic. From the repository root:

```bash
make bench-pk-up                                  # Postgres + fixture + platform, fixture registered
make bench-pk-up BENCH_PK_WORLD=monitors-3        # start in a different world
make bench-pk-corpus REPLICATES=3 MODEL=sonnet    # capture-corpus episodes (claude-cli, no metered cost)
make bench-pk-run CELLS=prerun K=8 MODEL=sonnet   # a cell set
make bench-pk-down
```

`bench-pk-up` starts `apisvc -surface perishable`, whose world the harness
moves through the control plane during a run; the service holds world state in
memory, so a world set by a run persists until reset or restart.

Every run writes into its own timestamped directory under
`build/bench-results/`.

## Artifacts and drift

`make bench-api-gen` regenerates the committed fixture artifacts
(`bench/specs/pk.json`, `pk-world.json`, `pk-fixture.json`, `pk-seeds.json`)
from fixed seeds, and `TestCommittedPerishableArtifactsMatch` diffs them
against a fresh regeneration. A generator change that moves a cell's meaning, a
series' values, or a delivered belief's wording has to move the committed copy
in the same commit, where a reviewer sees it.

Report tables and figures recompute offline from the archived runs via
`bench/reports/knowledge-use/pk_tables.py` and `figures.py`, both runnable from
`report.ipynb` beside them; `make bench-report-knowledge-use-pdf` renders the
report.
