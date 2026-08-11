# Findings register

The single index of what the benchmark program has established, what it
has retired, and which platform decisions rest on which evidence. A
finding that says something is not worth a report is still a finding;
this file is where it lives so the work is never repeated unknowingly.
One row per entry, newest first within each section. Update this file in
the same PR as the finding it records.

## Published reports

| Finding | Evidence | Where published |
| --- | --- | --- |
| Authored knowledge-page edges buy cost and robustness, not coverage: grounded coverage sat at ceiling in every cell of a graph-vs-stripped matrix at 50/500/5000 pages (~11 fetches ground every constraint against 5000 pages — discovery is not enumeration), the only separation is search cost, growing with scale (searches per grounded constraint 0.63 graph vs 1.44 stripped at 5000, graph flat across two orders of magnitude), and with search removed the graph arm walks every closure at full depth (1.00 grounded, zero searches) where the pilot's stripped floors read 0.00. The study's pre-registered instrument kill is the headline: constraints certified unreachable twice per scale for every task-derived phrasing were recovered by stripped-arm agents at 1.00/0.93 through read-derived queries (read the prose mention, re-search in the named institution's vocabulary), so "unreachable by search" must hold against read-derived queries — which meaning-constant authored prose cannot provide. The elicited completeness claim was uniformly conservative (0/98 claimed complete), leaving closure awareness unmeasured, not null | `results/graph-completion-probe/` (72-episode pilot), `results/graph-completion-separation/` (per-scale twofold certification), `results/graph-completion-confirmatory/` (99 episodes, kill application in its SUMMARY) | `docs/reference/benchmark-report-graph-completion.md` (2026-08-10, DOI 10.5281/zenodo.21881798) |
| A wrong claim promoted through the platform's own curation gate propagates only in the checkable class and only to the weak tier (16/24 Haiku 4.5 vs 0/24 Sonnet 5 and Opus 5), by displacing the verification the model would otherwise perform (exact observation-based separation, 120 episodes, no exception); the effect is adoption-driven (bare == imperative), not sink-bound, not fixture-bound, and replicates on the raw API — while the convention class's immunity is client-scaffold-bound (raw-API haiku adopted it 4/8) and the strongest tier's convention null is confounded by the plant-disclosure defect below | `results/knowledge-pollution/` (4 confirmatory families + probe, 664 analyzed episodes plus archived invalidated attempts) | `docs/reference/benchmark-report-knowledge-pollution.md` (2026-08-07, DOI 10.5281/zenodo.21834813) |
| Agent reliance on stored knowledge is governed by derivability (strong tier) and inverted by capability (weak tier); delivered conventions are used and fabrication-suppressing; stale world-state notes are strictly worse than nothing on the weak tier | `results/knowledge-use/` (7 run families, headline cells replicated on a v1.116.0 tag build) | `docs/reference/benchmark-report-knowledge-use.md` (2026-07-26, DOI 10.5281/zenodo.21614059) |
| The knowledge layer lifts trap accuracy +56 points where business context is required and is neutral elsewhere; cold-start teaching unlocks each trap class at its own promotion checkpoint | `results/` top-level families (paths frozen by the deposited PDF) | `docs/reference/benchmark-report.md` (2026-07-19, DOI 10.5281/zenodo.21438044) |

## Instrument defects

Defects in the harness or its configurations that shaped what a published
study could measure. Each row states the defect, the evidence, and what was
done about the published record — which is not always a re-run.

| Defect | Evidence | Disposition |
| --- | --- | --- |
| A meaning-constant stripped arm cannot pose semantic discontinuity. The graph-completion study certified its discontinuity pages unreachable twice per scale — offline embedding rank and a live sweep gate, both sampling task-derived phrasings (the cell prompt and its authored gate queries) — and stripped-arm episodes recovered them anyway at 1.00 (500) and 0.93 (5000): the stripped rendering's meaning-preserving prose fallback names the institution, the agent reads it and re-searches in the institution's own vocabulary, and the read-derived query ranks the page instantly. The certifications measured the wrong query distribution; the construct, not the harness, failed (gates passed, plants verified, the scale-50 control replicated with delta 0.00) | 28 leak episodes recomputed by `bench/reports/graph-completion/graph_tables.py` over `results/graph-completion-confirmatory/`; the archived analyzer beside the runs exits non-zero on them by design | Applied as the pre-registered instrument kill on #1251 before any kill condition was read: the certified-scale pairs are invalid for the discontinuity DV, and the report presents the kill itself as the finding (its section 3). Instrument rule for future studies: an "unreachable by search" certification must hold against read-derived queries, and authored meaning-preserving prose can never provide that, because preserving meaning preserves the vocabulary trail |
| The knowledge-pollution plant's reviewer note named it as a plant: the promotion path records `review_notes` as `knowledge-pollution study plant: <treatment-id>` on the applied insight, and `fetch` of `mcp:insight:<id>` returns that field, so any episode that dereferenced the planted insight read an explicit disclosure. Exposure was capability- and class-graded (convention cells: haiku 9/24, sonnet 18/24, opus 24/24 on claude-cli; 7-8/8 on every raw-API convention arm; 0-4 per checkable arm) | Per-episode exposure and conditional-adoption tables in `bench/reports/knowledge-pollution/pollution_tables.py` over `results/knowledge-pollution/` transcripts | Reported as a threats entry in the knowledge-pollution report (its section 7): the opus convention null is confounded and stated as such; the haiku/sonnet convention nulls hold among unexposed episodes; the checkable headline is essentially untouched (the few exposed weak-tier episodes adopted anyway). No re-run: the disclosure rides the provenance surface the study measures, and conditioning is possible per episode. Instrument rule for future studies: a plant's reviewer note must not name it as a plant |
| The graph-traversal probe's fixture could not have shown traversal (#1241). Every cell posed a single-fact lookup question, so the design held its discriminating condition at zero: a reference graph is the cheaper route, or the only one, when a task cannot be completed from the page search returns and its missing pieces cannot be named by the asker and therefore cannot be queried for. Lookup questions have no such missing pieces, so search and the graph competed on search's own ground and the outcome followed from the design rather than from agent behavior | `results/graph-traversal-probe/`: 160 of 162 dereferences across 76 episodes used a reference `search` had already returned, and the page holding the ground truth came back to the episode's own search in 75 of 76 episodes. A separate reading of one production deployment's audit log records 31 `fetch` calls on knowledge pages across 23 sessions, 6 of them reading two or more pages in one session, so the appetite the fixture failed to elicit is present in real use (the audit row does not record where a reference came from, so it evidences dereference, not traversal) | The candidate stays in the ledger; the probe result is archived as a design postmortem rather than a finding, and the runs are cited only for the narrower claim they support (for lookup-shaped questions at this corpus size, search reaches the answer page directly and reference following adds nothing). Instrument rules carried forward: state the divergence mechanism before building a probe fixture and confirm the design varies it; run a fixture gate at the limits and phrasings the agent will choose, not at the tool default |
| Every arm persona of both published studies denied `fetch`: the enumerated allow-lists in `config/platform.bench.a2.yaml`, `a3.yaml`, and `pk.yaml` omitted it (the a-arms also omitted `list_connections`), so both reports measured search-only, single-hop delivery and neither said so (#1176) | 19 `fetch` attempts across all 4,173 archived transcripts, 19 denials with `not authorized: tool not allowed for persona: admin`, zero successes — attempted unprompted in 18 episodes even though the harness instructions never named the tool | Configs corrected and guarded (`config/config_test.go`); erratum on the knowledge-layer report; threats entry on the knowledge-use report. No re-run: the denial was uniform across arms, so every published contrast stands as a contrast under search-only delivery; whether full-document delivery moves any result is a study question, not an erratum |

## Retired study candidates (negative results — concluded, not abandoned)

| Candidate | Why it died | Evidence | Recorded |
| --- | --- | --- | --- |
| Search-first gate enforcement study (#1145: does the hard gate cause discovery that steering alone would not) | Pre-stated kill condition met at ceiling: with the gate off, search-first was 128/128 across the eight clean gate-off arms (opus/sonnet/haiku, both scaffolds, both question phrasings), and the gate-on control was 16/16 with zero SEARCH_REQUIRED conversions; the session-handle requirement (#800) delivers agent_instructions before the first decision, so enforcement has no measurable marginal effect on any tested tier | `results/pk-gateprobe/` (nine clean arms plus one aborted, summary and analyzer beside them) | This register; the probe archive's README and `pk-gateprobe-SUMMARY.md`. Channel attribution (agent_instructions vs tool descriptions) survives as a study candidate needing a bench-only instruction-baseline knob |
| Perishable-knowledge study as pre-registered (H1a: agents under-verify stored beliefs; RQ3: epistemic metadata raises verification) | H1a falsified at first empirical contact: verification at ceiling on the strong tier, insensitive to an 11x cost sweep, both belief directions, and delivery itself; RQ3's target rate was already 1.0, leaving no headroom | `results/knowledge-use/pk-prerun/`, `pk-costsweep/`, `pk-answersweep/` | Results record on issue #1054; protocol banner in `perishable-knowledge-study-design.md`; the falsification became the spine of the knowledge-use report |
| Supersede-reliability report (from the knowledge-layer report's wide supersede CIs, duplicate rate CI [14, 86] on n=7) | Premise probe at 3x the pilot denominator found the mechanism at ceiling: supersede 8/8 clean, duplicates 0/8, update correctness 8/8 — the wide CIs were small-n noise, not a defect | `results/knowledge-use/s5-supersede-probe/` (the probe surfaced capture mis-filing instead, which entered the knowledge-use report and motivated #1057/#1060) | This register; the probe archive's README |
| API-connection architecture study (#1027: per-endpoint tools vs lexical vs hybrid search+invoke vs code mode) | Saturated by construction: the design held its discriminating variable (discovery difficulty) at an easy setting, so every arm converged and the comparison measured a foregone conclusion | `results/api-study-pilot/` | Postmortem on issue #1027; banner in `api-connection-study-design.md` |

## Deferred study extensions (proposed, never probed)

Extensions written down as follow-up work, then deferred without a probe; all
but the last are extensions of the published knowledge-layer study. They differ
from the retired candidates above: nothing was measured and nothing was
falsified, so none of them is concluded. They are recorded here rather than left open because each
would cost a full run and none has a product question waiting on its answer,
and an open ticket nobody can act on reads as work in flight.

Reviving one means the same gate as any other study: a cheap probe of its
headline effect first, and a stated decision on whether its result revises the
published report (a v2 of `benchmark-report.md`, whose v1.1 DOI is frozen) or
forms a new study in the series with its own protocol.

| Extension | Deferred because | What would revive it |
| --- | --- | --- |
| Teaching-order invariance (#980 A1): run the six cold-start lessons shuffled and reversed | The per-class trajectories (knowledge-layer report, Figure 2) already suggest order-invariance; a run that confirms the expected answer buys a stronger claim, not a decision | A cold-start result that only makes sense if order matters, or a reviewer challenging the single-order design |
| Adversarial and conflicting curricula (#980 A2): correction taught mid-curve | No product question waits on it. Was once folded into #1054's RQ4, which never ran when H1a was falsified | Evidence that mid-curve corrections do not propagate in a real deployment, which would make this a defect hunt rather than a completeness run |
| Multi-model cold-start climb (#980 A3): repeat the curve on three or more models | The largest spend in the set. Single-model generalization is an acknowledged limit of the published report (Section 7), stated as such rather than hidden | A decision to publish a generalization claim, or a second model showing a materially different climb by accident |
| Forgetting and decay after supersede (#980 A5) | Depends on the A2 harness, and the supersede mechanism already probed at ceiling (see the supersede-reliability row above) | The A2 harness existing for another reason, or a stale value resurfacing in production |
| Channel ablation, sink x discovery mode (#980 A6) | Existed to arbitrate the two #1131 levers. #1131 now takes the platform-controlled lever on the report's own evidence that platform search delivers, so the matrix no longer gates a decision | Wanting to close the sink-delivery confound the report flagged (Section 6) as a publication goal in its own right |
| Standalone memory suite (#982 section 3): generalize S5 out of the data-analysis framing into long multi-session arcs with no warehouse dependency | The mechanisms it would measure — capture, personal recall, cross-identity transfer, supersede correctness and duplicate rate, abstention — are the ones S5 already measures inside the knowledge-layer study, so the delta is external validity for memory-primary deployments rather than a decision nobody can otherwise make. The cost is the whole of it: a second seeded, airgapped ground-truth corpus with no warehouse in it. Its retention-and-decay axis depends on the A2 harness, deferred in its own row above | A memory-primary deployment needing a citable number that the warehouse framing would not support, or an S5 metric that turns out to be an artifact of the warehouse dataset rather than a property of the memory layer |

## Platform decisions taken on benchmark evidence

| Decision | Direction | Evidence |
| --- | --- | --- |
| Volatility / valid-until schema fields on insights | Do not build | Strong tier re-derives world-state notes regardless of metadata; weak tier ignores metadata in the other direction (knowledge-use report, sections 3 and 5) |
| Freshness + recheck-cost enrichment on perishable knowledge | Do not build | The verification rate the metadata would raise is already 1.0 at every cost (knowledge-use report, section 3) |
| Steer capture toward dated observations | Do not build — already the behavior | The capture corpus shows dated, self-refreshing notes on the empty-state path (`results/knowledge-use/pk-corpus/`) |
| Strict tool-argument schemas (reject unknown fields) | Built — #1060 | An agent burned calls on a silently-ignored misnamed field (capture corpus); the S5 probe's mis-filing decomposition reinforced the filing-reliability theme |
| Capture-guidance priority: conventions and definitions over world-state observations | Adopt in curation guidance | Conventions are the only knowledge class used by every tier and the only class that suppresses fabrication (knowledge-use report, sections 4, 5, 8) |
| Supersede invalidation / propagation repair (#980 A2/A5) | Unmeasured — deferred, not concluded | RQ4 never ran; the measurement that would settle it is deferred (see the deferred-extensions section above) |
| Cross-identity reach of applied insights (#980 B2, #1130) | Built — applied insights are searchable across identities | The knowledge-layer report's 46.7% transfer rate against 84.4% personal recall (Section 5 of report v1.1); measured by #1139 and published in report v2.0 — transfer is 98.9% CI [96.8-100.0] on v1.118.0, an across-code comparison, not a scale effect (`results/s5-anthropic-k5/`) |

## Candidate ledger (open questions, not yet studies)

A candidate enters this table only in the three-line form the lifecycle
below requires: the question in plain English, the platform mechanism it
exercises cited to code, and who acts on the answer. A candidate that
cannot be stated this way is not deferred — it does not exist yet.
Newest first; a candidate leaves this table for the retired section (probe
killed it) or for a protocol under `docs/` (probe held).

| Candidate | Platform mechanism (cited) | Who acts on the answer | Probe status |
| --- | --- | --- | --- |
| Authoring decomposition: when an agent captures substantial, heterogeneous knowledge, what makes it write several focused, cross-referenced pages instead of one monolith — the platform's split steering, the size of the content, or the shape of the corpus it can already see? The reading-side complement measured the value of edges to a reader; this asks whether agents create them | The `apply_knowledge` page sink and the split steering it activates: the baseline cross-link guidance (`pkg/platform/instructions/instructions.go`) and the oversize-page suggestion (#705); authored references recorded via `pkg/portal/knowledgepage/entity_ref_scan.go`, so decomposition is measurable as pages created, edges written, and edge resolution | Capture-guidance wording and the #705 thresholds; whether split steering needs to be stronger than a suggestion; downstream, whether agent-authored structure serves later readers (which the graph-completion instruments can grade) | Not probed. Cheap probe shape: capture tasks over large heterogeneous source material, steering present vs absent as the arms, decomposition and edge validity as the readings |
| Delivery depth: do any published search-only contrasts move when the agent can read whole documents instead of result snippets? | `fetch` now allowed in the corrected bench personas, guarded by `config/config_test.go` (#1176) | Whether either published report needs a v2 qualifier; delivery guidance for deployments | Not probed. Named a study question, not an erratum, by the #1176 defect row |
| Capture and sink choice: when an agent learns something worth keeping, does it capture it, and does it file it into the right sink (memory vs insight vs page)? | `memory_capture` / `memory_manage` and the `apply_knowledge` promotion path; mis-filing already observed once in `results/knowledge-use/s5-supersede-probe/` (motivated #1057/#1060) | Capture-guidance wording; sink schema design | Gated on the #1136 measurement (decompose a capture miss into attempted / succeeded / budget-starved); the decomposition picks the question, not the other way around. Landscape: no external benchmark measures discretionary capture during real work or sink selection ([`study-landscape.md`](study-landscape.md) gaps 2–3) |

## The study lifecycle these entries follow

Probe before protocol: no study is proposed without a cheap empirical
probe of its headline effect, and the probe result travels with the
proposal. Probe fails → one register row, an afternoon spent, done. Probe
holds → pre-registered protocol (`docs/`), runs (`results/<slug>/`),
report (`docs/reference/`), and a register row either way. Decision rules
are stated before each run, and archives are kept for every
data-producing run including the ones that killed their own hypothesis.

The full lifecycle, in order. Every stage is a gate the candidate can
fail, each stage is bounded so failing is cheap, and no stage may be
entered before the one before it. The rows above are the receipts: #1027
died for want of stage 3, #1054 for want of stage 2, and the
search-first-gate candidate is what stage 2 looks like when it works —
killed at probe cost by its own pre-stated condition.

**Stage 0 — landscape check.** [`study-landscape.md`](study-landscape.md)
is the standing survey of external benchmarks and studies adjacent to
this program. A new candidate gets a targeted pass over it (and a search
for anything newer), appended to that file with citations. A literature
gap is motivation, never evidence: an unmeasured effect is equally
consistent with "nobody looked" and "absent or at ceiling", and two
retired rows above adopted a premise from plausibility alone.

**Stage 1 — ledger entry.** Three lines in the candidate ledger above:
(a) the question in one plain-English sentence containing no method
vocabulary — if the plain form is self-evidently true, false, or not a
question, the candidate dies here at zero cost; (b) the platform
mechanism the question exercises, cited to code — a question about a
mechanism the platform does not have is a product idea, not a study;
(c) who acts on the answer — a platform decision, a revision to a
published report, or a claim the field can use. Method vocabulary is
deferred to stage 4 deliberately: dressing a thin question in estimator
language makes it read as rigorous before anything has been measured.

**Stage 2 — premise probe.** Before the probe runs, write down its kill
condition and its timebox (a half-day; k of 2–4 hand-driven episodes of
the primary contrast). Then run it. The probe result travels with any
proposal; argument from the literature does not substitute. Probe killed
→ one row in the retired section, and stop. Ambiguity is a kill, not a
license to extend the probe. A probe result is a decision input, never a
published finding: confirmatory claims come only from stage-4 runs.

**Stage 3 — separation analysis and capability placement.** One page,
signed off before any build: the concrete mechanism by which the arms
diverge; the design variable that produces the divergence, with
confirmation the design varies it (never held at its easy setting in the
name of confound control); what result falsifies the premise; and which
model tier the effect is expected on and why — published results here
invert across tiers, so a design that does not place itself on the
capability curve has left its discriminating variable at an unknown
setting. Design choices are audited jointly, not one at a time.

**Stage 4 — protocol, build, run, report.** Pre-registered protocol under
`docs/`, decision rules stated before each run, runs under
`results/<slug>/` with a README stating what the family does and does not
establish, archives kept for every data-producing run including the ones
that kill their own hypothesis, and a register row either way.
