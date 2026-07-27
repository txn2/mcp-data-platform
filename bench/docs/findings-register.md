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
| Agent reliance on stored knowledge is governed by derivability (strong tier) and inverted by capability (weak tier); delivered conventions are used and fabrication-suppressing; stale world-state notes are strictly worse than nothing on the weak tier | `results/knowledge-use/` (7 run families, headline cells replicated on a v1.116.0 tag build) | `docs/reference/benchmark-report-knowledge-use.md` (2026-07-26, DOI 10.5281/zenodo.21614059) |
| The knowledge layer lifts trap accuracy +56 points where business context is required and is neutral elsewhere; cold-start teaching unlocks each trap class at its own promotion checkpoint | `results/` top-level families (paths frozen by the deposited PDF) | `docs/reference/benchmark-report.md` (2026-07-19, DOI 10.5281/zenodo.21438044) |

## Retired study candidates (negative results — concluded, not abandoned)

| Candidate | Why it died | Evidence | Recorded |
| --- | --- | --- | --- |
| Perishable-knowledge study as pre-registered (H1a: agents under-verify stored beliefs; RQ3: epistemic metadata raises verification) | H1a falsified at first empirical contact: verification at ceiling on the strong tier, insensitive to an 11x cost sweep, both belief directions, and delivery itself; RQ3's target rate was already 1.0, leaving no headroom | `results/knowledge-use/pk-prerun/`, `pk-costsweep/`, `pk-answersweep/` | Results record on issue #1054; protocol banner in `perishable-knowledge-study-design.md`; the falsification became the spine of the knowledge-use report |
| Supersede-reliability report (from the knowledge-layer report's wide supersede CIs, duplicate rate CI [14, 86] on n=7) | Premise probe at 3x the pilot denominator found the mechanism at ceiling: supersede 8/8 clean, duplicates 0/8, update correctness 8/8 — the wide CIs were small-n noise, not a defect | `results/knowledge-use/s5-supersede-probe/` (the probe surfaced capture mis-filing instead, which entered the knowledge-use report and motivated #1057/#1060) | This register; the probe archive's README |
| API-connection architecture study (#1027: per-endpoint tools vs lexical vs hybrid search+invoke vs code mode) | Saturated by construction: the design held its discriminating variable (discovery difficulty) at an easy setting, so every arm converged and the comparison measured a foregone conclusion | `results/api-study-pilot/` | Postmortem on issue #1027; banner in `api-connection-study-design.md` |

## Platform decisions taken on benchmark evidence

| Decision | Direction | Evidence |
| --- | --- | --- |
| Volatility / valid-until schema fields on insights | Do not build | Strong tier re-derives world-state notes regardless of metadata; weak tier ignores metadata in the other direction (knowledge-use report, sections 3 and 5) |
| Freshness + recheck-cost enrichment on perishable knowledge | Do not build | The verification rate the metadata would raise is already 1.0 at every cost (knowledge-use report, section 3) |
| Steer capture toward dated observations | Do not build — already the behavior | The capture corpus shows dated, self-refreshing notes on the empty-state path (`results/knowledge-use/pk-corpus/`) |
| Strict tool-argument schemas (reject unknown fields) | Built — #1060 | An agent burned calls on a silently-ignored misnamed field (capture corpus); the S5 probe's mis-filing decomposition reinforced the filing-reliability theme |
| Capture-guidance priority: conventions and definitions over world-state observations | Adopt in curation guidance | Conventions are the only knowledge class used by every tier and the only class that suppresses fabrication (knowledge-use report, sections 4, 5, 8) |
| Supersede invalidation / propagation repair (#980 A2/A5) | Still open — unmeasured | RQ4 never ran; A2/A5 reverted to open on #980 |

## The study lifecycle these entries follow

Probe before protocol: no study is proposed without a cheap empirical
probe of its headline effect, and the probe result travels with the
proposal. Probe fails → one register row, an afternoon spent, done. Probe
holds → pre-registered protocol (`docs/`), runs (`results/<slug>/`),
report (`docs/reference/`), and a register row either way. Decision rules
are stated before each run, and archives are kept for every
data-producing run including the ones that killed their own hypothesis.
