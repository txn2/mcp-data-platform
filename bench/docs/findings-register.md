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
| A wrong claim promoted through the platform's own curation gate propagates only in the checkable class and only to the weak tier (16/24 Haiku 4.5 vs 0/24 Sonnet 5 and Opus 5), by displacing the verification the model would otherwise perform (exact observation-based separation, 120 episodes, no exception); the effect is adoption-driven (bare == imperative), not sink-bound, not fixture-bound, and replicates on the raw API — while the convention class's immunity is client-scaffold-bound (raw-API haiku adopted it 4/8) and the strongest tier's convention null is confounded by the plant-disclosure defect below | `results/knowledge-pollution/` (4 confirmatory families + probe, 664 analyzed episodes plus archived invalidated attempts) | `docs/reference/benchmark-report-knowledge-pollution.md` (2026-08-07, DOI 10.5281/zenodo.21834813) |
| Agent reliance on stored knowledge is governed by derivability (strong tier) and inverted by capability (weak tier); delivered conventions are used and fabrication-suppressing; stale world-state notes are strictly worse than nothing on the weak tier | `results/knowledge-use/` (7 run families, headline cells replicated on a v1.116.0 tag build) | `docs/reference/benchmark-report-knowledge-use.md` (2026-07-26, DOI 10.5281/zenodo.21614059) |
| The knowledge layer lifts trap accuracy +56 points where business context is required and is neutral elsewhere; cold-start teaching unlocks each trap class at its own promotion checkpoint | `results/` top-level families (paths frozen by the deposited PDF) | `docs/reference/benchmark-report.md` (2026-07-19, DOI 10.5281/zenodo.21438044) |

## Instrument defects

Defects in the harness or its configurations that shaped what a published
study could measure. Each row states the defect, the evidence, and what was
done about the published record — which is not always a re-run.

| Defect | Evidence | Disposition |
| --- | --- | --- |
| The knowledge-pollution plant's reviewer note named it as a plant: the promotion path records `review_notes` as `knowledge-pollution study plant: <treatment-id>` on the applied insight, and `fetch` of `mcp:insight:<id>` returns that field, so any episode that dereferenced the planted insight read an explicit disclosure. Exposure was capability- and class-graded (convention cells: haiku 9/24, sonnet 18/24, opus 24/24 on claude-cli; 7-8/8 on every raw-API convention arm; 0-4 per checkable arm) | Per-episode exposure and conditional-adoption tables in `bench/reports/knowledge-pollution/pollution_tables.py` over `results/knowledge-pollution/` transcripts | Reported as a threats entry in the knowledge-pollution report (its section 7): the opus convention null is confounded and stated as such; the haiku/sonnet convention nulls hold among unexposed episodes; the checkable headline is essentially untouched (the few exposed weak-tier episodes adopted anyway). No re-run: the disclosure rides the provenance surface the study measures, and conditioning is possible per episode. Instrument rule for future studies: a plant's reviewer note must not name it as a plant |
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

## The study lifecycle these entries follow

Probe before protocol: no study is proposed without a cheap empirical
probe of its headline effect, and the probe result travels with the
proposal. Probe fails → one register row, an afternoon spent, done. Probe
holds → pre-registered protocol (`docs/`), runs (`results/<slug>/`),
report (`docs/reference/`), and a register row either way. Decision rules
are stated before each run, and archives are kept for every
data-producing run including the ones that killed their own hypothesis.
