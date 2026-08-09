# Graph-completion probe: design and pre-stated kill conditions (#1241)

Written before the fixture was rebuilt and before any episode ran. This is the
corrected instrument for the graph-traversal candidate after the
[lookup-shaped attempt](../graph-traversal-probe/README.md) measured search on
search's own ground. The candidate's question is unchanged; the condition it
turns on is finally varied.

## The question

A knowledge-page reference graph earns its keep in one situation: the task
cannot be completed from the page search surfaces, and the missing pieces
cannot be named by the asker, so they cannot be queried for directly. Lookup
questions never enter that situation. Completion tasks live in it: "write the
complete change plan", "write the incident-handling document" — tasks whose
ground truth is a set of constraints spread across pages the prompt does not
name, where the deliverable is judged on completeness the asker cannot check.

Two questions, one per test:

- **Necessity (no-search test).** When following references is the only route
  to the spread content, do agents take it, and how much of the constraint set
  do they recover?
- **Economics (search test).** When search exists, does the presence of the
  edges change what the final document covers, or what the coverage costs in
  queries and reads?

## The divergence mechanism, named

The mechanism that would make the arms differ: **an edge is a targeted,
zero-guess pointer supplied by context the agent already holds; a query for
content the asker cannot name must be guessed from the task's own vocabulary,
which by construction does not contain the constraint pages' names.** An agent
with edges can reach the tail of the constraint set by reading what it is
pointed at. An agent without edges must either enumerate (broad queries, wide
limits, many reads) or satisfice (ship the document without the tail).

Check that the design varies it: the graph/stripped contrast varies edge
presence while holding page meaning constant (a stripped body keeps a natural
prose mention where the token was, so the arm contrast is *machine-followable
edge versus prose mention* — exactly the authoring decision the platform's
split steering makes). The search/no-search contrast varies whether
enumeration is available at all, so the no-search test isolates traversal from
retrieval completely. The mechanism is varied on both axes; it is not held at
zero anywhere.

What could still collapse the arms, stated in advance:

- **Enumeration ceiling.** On a corpus of tens of pages, semantic search with
  a raised limit can return most of the corpus in one call (measured: an
  episode's `limit: 100` search returned all 42 pages). In the search test the
  stripped arm can therefore reach any coverage the graph arm can. That is not
  a defect; it is why cost (searches and reads per unit of coverage) is a
  primary dependent variable, not a tiebreaker. Coverage alone deciding
  nothing at this scale is itself the honest scale condition.
- **Semantic leakage.** A prose mention ("a central register of storage
  classes") is a workable query. The stripped arm is therefore not blind, only
  edge-less. The design accepts this deliberately: an unlinked wiki still
  mentions things; the platform's choice is whether to make mentions
  dereferenceable.

## Cells

Three completion cells on the operations-wiki corpus, one per subject area,
each with an entry page and a constraint set of 8-10 discrete facts spread
over 5-8 pages at reference depths 0-3 from the entry:

- **gc-billing-change** — plan an incompatible schema change to a
  regulated-tier stream (change class, notice, notice counting, freeze
  interaction, attestation, staged rollout, announcement channels, consumer
  contracts).
- **gc-incident** — handle a control job's third consecutive failure
  (severity band, notification, escalation route and rungs, per-rung clocks,
  unacknowledged-clock behavior, communication rules, closing evidence,
  postmortem record).
- **gc-export-onboarding** — stand up a new nightly export (naming, zone
  layout, storage class and lifetime, delivery expectations, re-run rules,
  egress).

Each constraint carries regex signatures: hard tokens (numbers, class names,
route names) a grounded document necessarily contains and a paraphrase cannot
avoid. Signature rules, enforced by the fixture validator:

- Each signature matches only its constraint's declared source pages' bodies,
  across the whole corpus.
- No signature matches any page title or summary (search renders title plus
  summary, so a signature there would be delivered without any read).
- No signature matches the prompt, the scaffold, or the gate queries.

Signature grading undercounts paraphrase. That error is identical in both
arms, so the contrasts are unbiased even where the absolute coverage is a
lower bound. Recorded, not hidden.

Constraints are additionally split by location: **entry constraints** (stated
on the entry page; a within-episode control both arms should cover equally)
and **off-entry constraints** (the spread mass; every kill condition is
written against these).

## Arms

Two crossed manipulations, four arms, run per model tier:

| Arm | Corpus | Search | What it measures |
| --- | --- | --- | --- |
| graph / search | edges planted | available | the deployed shape |
| stripped / search | prose mentions only | available | search-only counterfactual |
| graph / no-search | edges planted | client-disallowed | traversal when it is the only route |
| stripped / no-search | prose mentions only | client-disallowed | floor / instrument-leak check |

- The stripped corpus is the same fixture with every reference token rendered
  as its authored prose fallback; the platform's reference table is verified
  empty per page after the plant, so `fetch` returns no edges.
- No-search arms disallow the search tool at the client and hand the entry
  page's reference in the prompt ("The runbook for this job is
  mcp:knowledge_page:&lt;id&gt;."). The platform is unchanged: the search-first
  gate blocks only warehouse query tools (`DefaultQueryTools` in
  `pkg/middleware/session_workflow.go`), so `fetch` is reachable without a
  search. Handing the reference is symmetric across both no-search arms.
- stripped/no-search has a coverage ceiling of the entry constraints by
  construction. Any off-entry grounded coverage in that arm is an instrument
  leak and invalidates the run pair until explained.

Models: the two capability tiers the first attempt used (haiku, opus), k=3
replicates per cell per arm per model: 3 cells x 4 arms x 2 models x k3 = 72
episodes. Fresh identity per episode from the pool. Episodes through
claude-cli, so runs carry wall-clock cost only; the manifest records the
client version and the arm.

## Dependent variables

Per episode, computed from the archived transcript (offline, reproducible):

- **Off-entry grounded coverage** (primary): fraction of off-entry
  constraints whose signature appears in the final document AND whose source
  page was successfully fetched in the episode.
- **Unread coverage**: signatures present without any source page read.
  Given the gate below, unread coverage is confabulation or prior knowledge
  and is reported separately, never added to coverage.
- **Cost**: searches issued, fetches issued, distinct constraint-set pages
  read, off-set pages read; searches per grounded off-entry constraint.
- **Provenance** (carried over unchanged): for every fetch, whether its
  reference was first seen in a search result, a fetched page, or nowhere.

## Pre-stated kill conditions

Read per tier over cell means. "Coverage" always means off-entry grounded
coverage.

1. **Traversal floor — candidate killed.** In graph/no-search, no tier
   exceeds 0.25 mean coverage. Agents do not walk edges even when edges are
   the only route; the split steering (#705 suggestion text and the baseline
   cross-link guidance) is indicted directly, and the corrective action is to
   stop steering toward thin-index shapes or deliver linked content
   server-side with the fetch.
2. **Graph adds nothing over search — candidate killed.** In the search
   test, for every tier, the graph-minus-stripped coverage difference is
   inside ±0.10 AND searches-per-grounded-constraint differs by less than
   25%. At deployment scale the edges then change neither the document nor
   its cost, and the steering rests on nothing measurable.
3. **Premise holds — candidate proceeds to a density design.** Some tier
   shows graph/no-search coverage at or above 0.50 (agents traverse when
   required), AND in the search test some tier shows either a coverage gain
   of at least 0.15 or equal-coverage cost savings of at least 25% in
   searches per grounded constraint.
4. **Anything else** is a recorded boundary condition: the candidate stays
   open, the numbers go in the register row, and the next design decision is
   argued from them rather than from this probe alone.

Instrument kills, checked before any condition above is read:

- stripped/no-search shows off-entry grounded coverage above 0 anywhere.
- The fixture gate (below) fails.
- The stripped plant's reference table is not empty, or the graph plant's
  edges do not verify exactly.

## The fixture gate, corrected for the limit lesson

The first attempt's gate ran each cell's one query at the search tool's
default limit and was defeated by episodes on their first call, because the
agent chooses its own limit and phrasing (opus asked for 25 on 65 of 67
searches; one haiku search asked for 100 and got the whole corpus). The
rebuilt gate sweeps:

- **Queries**: each cell's prompt-derived query plus two authored broad
  phrasings an agent would plausibly try, per cell.
- **Limits**: 5, 25, 100.

Recorded per (query, limit): the rank of the entry page and of every
constraint-set page, and whether any hit's rendered text matches any
constraint signature. Pass requires: the entry page surfaces at limit 25 for
the prompt-derived query (episodes must be able to start), and **no
constraint signature appears in any hit text at any swept combination**
(otherwise search delivers constraint content without a read and grounded
coverage stops meaning anything). Constraint pages merely *surfacing* is
recorded as the enumeration profile, not failed on: their reachability is the
economics test's subject, not a leak.

## Rules carried forward from the register

- Non-disclosure: no page, prompt, or scaffold names the study or instructs
  the behavior measured. The completion prompts ask for a complete document;
  they do not say how to find its parts.
- The plant goes through the knowledge-page REST API (direct SQL leaves the
  reference table empty and the graph arm would have no graph).
- Embeddings arrive on the reconciler sweep; the gate polls the chunk table
  before reading.
- Every run writes a durable per-run directory; superseded runs are kept.
- Exploratory throughout: a premise probe is a decision input, not a
  published finding.
