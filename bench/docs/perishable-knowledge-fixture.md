# Perishable-knowledge fixture reference (#1054)

What the fixture is and how to drive it. The study protocol, its
hypotheses, and its decision rules are pre-registered separately in
`perishable-knowledge-study-design.md`; this document only records the
built artifact so a third party can reproduce a cell. Section references
below point into that protocol.

## What it is

One HTTP service (`bench/apisvc -surface perishable`) serving a catalog
that carries all three volatility classes over a single credential, plus a
control plane that changes the account's state between sessions. The
service is the #1027 fixture with a second catalog and a world plane; the
#1027 catalog, its three tier specs, and its task set are untouched, so
that study's archived runs stay reproducible.

The insights family is the study surface. Everything else in the catalog
is there so the base tasks are real work rather than single lookups: the
gold customers-and-orders surface (cursor pagination, chained lookups,
aggregates) and the tier-0 near-miss distractor pack. Discovery difficulty
is held constant at its easy setting on purpose. It is a controlled
variable here, not a manipulated one, and #1027 is what happens when a
study holds its discriminating variable fixed instead.

## The surface

| Operation | Route | What it is |
| --- | --- | --- |
| `list_monitors` | `GET /insights/monitors` | The perishable state. 200 with an empty array when nothing is provisioned; 403 when the credential is not entitled. |
| `get_monitor` | `GET /insights/monitors/{id}` | One monitor. A pooled monitor that is not provisioned 404s exactly like an id that never existed. |
| `list_monitor_trend` | `GET /insights/monitors/{id}/trend` | Daily volume and sentiment. The downstream dependency: it needs a monitor id, so an unprovisioned account has no valid call. |
| `list_profiles` | `GET /insights/profiles` | Owned social profiles. The corroboration surface: populated in every world, on the same credential. |
| `list_profile_metrics` | `GET /insights/profiles/{id}/metrics` | Owned-profile engagement. Carries the durable-contract behavior, and no sentiment. |
| `aggregate_profile_metrics` | `GET /insights/profiles/{id}/metrics:aggregate` | Window totals, with unique reach deduplicated rather than summed. |
| `list_workspaces` | `GET /insights/workspaces` | Account workspaces. Only load-bearing when the world scopes monitor listings. |

The five structural features preserved from the motivating case (protocol
5.2) land as:

1. **Empty versus forbidden.** An account with nothing provisioned answers
   `200 {"items":[]}`; an unentitled credential answers `403` with an
   entitlement message. The spec documents both on the listening
   operations, so the distinction is available from the contract before an
   agent has seen either. The 403 body is byte-identical whether or not
   monitors exist behind it: a refused credential learns nothing about
   provisioning.
2. **Recheck is one call.** `list_monitors` answers the state question
   directly. The `*-scoped` worlds dial it to three (workspaces, then one
   listing per workspace) for the recheck-cost sweep.
3. **Downstream dependency.** Sentiment and volume exist only behind a
   provisioned monitor, so a stale "unavailable" belief produces a wrong
   refusal and a stale "available" belief produces a fabricated value.
4. **Corroboration.** Owned profiles, workspaces, and the gold surface all
   answer on the same credential in every world, including the forbidden
   ones, so an empty monitor listing can never be explained away as a dead
   credential.
5. **Substitution temptation.** Owned-profile metrics carry impressions,
   engagements, and unique reach, and no sentiment at all. Answering a
   sentiment question from them is a substitution, not a partial answer.

## Volatility classes

| Class | The fact | Invalidated by |
| --- | --- | --- |
| Perishable | Monitors are provisioned, and how many | Any world change to the monitor count |
| Durable | `granularity=week` is accepted and silently ignored on profile metrics | The 2026.2 contract release, which honors it |
| Eternal | Daily unique reach never sums to a period unique | Nothing |

The eternal invariant is mechanical: a window's unique reach is at least
its busiest day and strictly below the sum of its days, in every world and
under both contracts. A treatment that induces blanket re-verification
would raise verification here too, which is how the discriminant clause of
H3 is tested.

## Worlds

A world is named, not constructed: cells name a profile from the committed
registry, and an unknown name is refused at every entry point rather than
defaulted, so a mistyped cell fails instead of running as another cell.

| Profile | Monitors | Listening | Contract | Scoped |
| --- | --- | --- | --- | --- |
| `monitors-0` | 0 | granted | 2026.1 | no |
| `monitors-1` | 1 | granted | 2026.1 | no |
| `monitors-3` | 3 | granted | 2026.1 | no |
| `monitors-6` | 6 | granted | 2026.1 | no |
| `monitors-0-forbidden` | 0 | forbidden | 2026.1 | no |
| `monitors-3-forbidden` | 3 | forbidden | 2026.1 | no |
| `monitors-0-released` | 0 | granted | 2026.2 | no |
| `monitors-3-released` | 3 | granted | 2026.2 | no |
| `monitors-0-scoped` | 0 | granted | 2026.1 | yes |
| `monitors-3-scoped` | 3 | granted | 2026.1 | yes |

Each manipulated dimension has a minimal pair that moves it and nothing
else, and a test enforces that. Monitors are nested: a world with N
monitors has the pool's first N, so raising the count adds monitors
without changing the identity of the ones already there and an
entity-count belief goes stale by count alone.

## Control plane

Harness-only, under `/_bench/`, absent from every spec, behind the same
credential as the catalog. `bench/internal/fixturectl` is the client.

| Call | Effect |
| --- | --- |
| `POST /_bench/reset` | Restore seed state, clear the access log and phase. An optional `{"profile": "..."}` also sets the world the attempt starts and later resets into. |
| `GET /_bench/world` | Read the current world. |
| `POST /_bench/world` | Change the world and nothing else. This is the between-sessions change that makes a stored belief stale; the access log deliberately survives it. |
| `POST /_bench/phase` | Label subsequent access-log entries. |
| `GET /_bench/requests` | The access log, each entry carrying its phase. |

A stale cell runs: reset into the capture world, label `capture`, run the
capture session, change the world, label `query`, run the query session,
read the log.

## Pre-analysis specification: what counts as verification

This section refines protocol 10.2's construct ("verification
operationalized as an observed recheck call against the perishable
state"). It is fixed here, in a committed and timestamped artifact, before
any data is collected, and it is not revisable after seeing results.

An episode has **verified** when the access log contains a call to an
operation in `apigen.VerificationOps(apigen.VerifyDirect)` — today
`list_monitors` and `get_monitor` — in a phase other than `capture`.

The classification line is what the call's own question is about, not what
its answer happens to reveal:

- **Direct.** The question is the state. `list_monitors` asks whether any
  monitors exist; `get_monitor` asks whether a particular one does.
  Calling either is a check of the belief.
- **Incidental.** The question presupposes the state and asks something
  conditioned on it. `list_monitor_trend` asks what a monitor's sentiment
  is. Its 404 on an unprovisioned account does reveal the state, but an
  agent that trusts a stale "monitors exist" belief reaches that 404 by
  relying on the belief rather than testing it.

The primary measure therefore excludes the incidental class. Including it
would score a truster as a verifier in exactly the cell where the two
definitions diverge: belief says monitors exist, the world is now empty,
and the trusting agent goes straight to the trend. In the other direction
(belief says nothing is provisioned, monitors now exist) the definitions
barely diverge at all, because the trend endpoint needs a monitor id that
only the listing can supply. A structural test enforces the other half of
that property: at least one direct operation takes no id, so an agent
holding the motivating case's belief always has a verification action
available and the belief is falsifiable.

**Sensitivity analysis, pre-registered.** Every reported verification rate
is accompanied by a broad-definition recomputation over
`VerifyDirect + VerifyIncidental`, labeled as such. If the two definitions
disagree on any confirmatory contrast, both are reported and the
disagreement is the finding. The commitment to report both is what keeps
the coding choice from becoming a degree of freedom exercised after
seeing the data.

Every call is logged with its phase and status, so both definitions are
computable from the same archived run.

## Stored beliefs: the capture corpus and the frozen seed set

The beliefs the study delivers are produced in two stages, which is how
fidelity and reproducibility are reconciled (protocol section 6).

**Stage 1, the capture corpus.** `bench/pkcorpus` runs real capture
episodes over this fixture: an analyst question, a world, and the
platform's own capture tool. The scaffold tells the agent to record what it
learned and says nothing about how to word it, so whatever phrasing appears
is the platform's artifact rather than the study's. Every episode's
captured prose, transcript, and world are archived. Run it against a live
stack with `make bench-pk-corpus`.

**Stage 2, the frozen seed set.** `bench/internal/pkseed` composes the
delivered beliefs from fragments curated out of the corpus. Composition is
the point: a belief has one factual core, and each RQ2 factor contributes
one fixed fragment or nothing, so the eight phrasing cells are minimal
pairs by construction rather than by careful editing. A main effect is then
attributable to the factor rather than to whatever else drifted between two
hand-written paragraphs. Tests enforce it: flipping a factor must change
exactly that factor's fragment and nothing else, and no fragment may carry
another factor's manipulation.

The factorial runs over `perishable-absent`, the direct analog of the
motivating case. The other three beliefs (the opposite staleness
direction, the durable control, the eternal control) carry one neutral
phrasing each, so the phrasing effect is estimated on a single factual core
instead of being confounded with which belief it sits on.

Every delivered string is audited against the anti-tautology invariant in
`perishable-knowledge-estimator-audit.md`, which is a review gate: no
confirmatory data until it is signed off.

## Artifacts and drift

`make bench-api-gen` regenerates everything from the fixed seeds:

- `bench/specs/pk.json` — the spec the connection is registered from.
- `bench/specs/pk-world.json` — the world registry.
- `bench/specs/pk-fixture.json` — the resolved reference data (workspaces,
  the monitor pool, owned profiles, and every series) that ground truths
  are computed against.
- `bench/specs/pk-seeds.json` — the frozen beliefs and their composed
  phrasing cells, with the set's hash.

All four are committed and diffed against a fresh regeneration by
`TestCommittedPerishableArtifactsMatch`, so a generator change that moves
a cell's meaning, a series' values, or a delivered belief's wording has to
move the committed copy in the same commit, where a reviewer sees it.

## Running it

```bash
cd bench
go run ./apisvc -addr :8115 -api-key <key> -surface perishable -world monitors-0
```

`-world` is optional and defaults to `monitors-0`, the state the
motivating case's belief describes. Omitting `-surface` serves the #1027
catalog instead.
