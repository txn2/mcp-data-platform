# Graph-completion study, stage 3: separation design (#1250)

**Status: study concluded (2026-08-10). The confirmatory matrix ran in full
under #1251 — 99 episodes, no protocol amendment — and its pre-registered
instrument kill fired: stripped-arm episodes grounded the certified
discontinuity constraints at both certified scales through read-derived
queries, so the certified-scale pairs are invalid for the discontinuity DV
and no kill condition was read as a confirmatory finding (kill application
in `../results/graph-completion-confirmatory/graph-confirmatory-SUMMARY.md`).
Results are published as
[`docs/reference/benchmark-report-graph-completion.md`](../../docs/reference/benchmark-report-graph-completion.md)
(version 1.0, DOI 10.5281/zenodo.21881798), which claims cost and no-search
robustness, not completeness delivery. This document is frozen as the
pre-registration of record; published reports cite its section headings.**

This is the stage-3 design for the graph-traversal candidate
(`findings-register.md`, graph-traversal row), following the premise probe
(#1241, record in [`../results/graph-completion-probe/`](../results/graph-completion-probe/)).
The probe held: agents traverse a knowledge-page reference graph unprompted
when edges are the only route (grounded off-entry coverage 0.96 and 0.42
across two reading budgets, full depth, zero queries), and with search on the
outcome was governed by reading budget against corpus size — at 42 pages the
agent that could afford to read half the corpus hit ceiling with or without
edges. The probe also proved, via its own gate's enumeration profile, that it
never posed the two situations a reference graph structurally exists for.
This study poses them.

## Study question

When a completion task's constraints live across a knowledge-page graph, does
the graph deliver what retrieval structurally cannot:

1. **Semantic discontinuity.** A constraint whose connection to the task is
   institutional rather than topical — a finance close calendar governing
   when a schema change may ship — sits far from every task-derived query in
   embedding space. Search cannot rank what nothing in the task names or
   approximates; an authored edge crosses the gap in one hop. Do agents
   recover these constraints through edges, and does their absence in a
   stripped corpus surface as silent incompleteness?
2. **Completeness closure.** A ranked result list certifies nothing about
   coverage; a graph closure from an entry node is enumerable and
   terminating. Do agents with edges reach higher true coverage, and do they
   know when they are done — measured as claimed completeness against actual
   coverage (the overclaim rate)?

Both are read at corpus scales where exhaustive reading is unaffordable, so
the probe's enumeration ceiling becomes a measured boundary instead of a
confound.

## The divergence mechanisms, named

Following the register's instrument rule, the mechanism that would make the
arms differ is stated before the fixture, and the design is checked to vary
it.

- **Discontinuity.** In the graph arm the constraint's source page is one
  authored edge from a page the episode already holds. In the stripped arm
  the same page exists with the same content, mentioned in prose, but no
  task-derived query can rank it — that unreachability is *certified twice
  per scale* (below), not assumed. So the arm contrast varies the only
  discovery route the constraint has. This is the situation the probe could
  not produce: its gate showed every constraint page semantically adjacent to
  its task.
- **Closure.** In the graph arm the set "everything reachable from the entry"
  is enumerable and terminates; an agent can walk it and know it has walked
  it. In the stripped arm no affordance bounds the constraint set: however
  many searches return, more could exist. The arm contrast varies whether
  done-ness is decidable. The overclaim reading makes that decidability
  visible: an agent that cannot bound the set and claims completeness anyway
  is measurably overclaiming.
- **Scale** is the third axis and is what makes both mechanisms readable: at
  tens of pages a raised search limit enumerates the corpus and every
  discovery structure is equivalent (measured, #1241). The smallest scale is
  kept as the within-ceiling control where the design predicts *no*
  separation; the two larger scales are where the mechanisms are posed.

Everything is framed as reading budget against corpus size. Model-tier
framing is rejected: the probe showed the "tier contrast" was a budget
contrast, and budget-to-corpus ratio is the durable variable. The
confirmatory matrix fixes one agent configuration and moves the ratio two
orders of magnitude through the corpus.

## Corpus: deterministic generation at controlled scale

Generator: `bench/internal/graphgen` (`graphstudy` CLI). A corpus is a pure
function of a Spec (Scale, Seed, EdgeDensity); the frozen study parameters
are Seed 1250, EdgeDensity 3, Scales 50 / 500 / 5000. Archives record the
Spec, not the pages; any reader regenerates the exact corpus.

- **Fixed core, scaled haystack.** The three completion cells and their 27
  pages are hand-authored and byte-identical at every scale (proven by
  `TestCoreIsScaleInvariant`), so a difference across scales can never be
  attributed to the task changing. Filler is generated as per-system
  clusters in the same operations-wiki genre — runbooks, schema notes,
  tuning notes — so search faces real competition, with system names drawn
  off the core's compound names.
- **Cells.** `gs-change-plan` (wide and deep: schema change to a
  governed-tier stream), `gs-incident` (deep: clocks at reference distance
  three), `gs-feed-onboarding` (wide-shallow: stand up a new nightly feed).
  Each keeps the probe's structure — an entry-control constraint, a topical
  spread over the closure — and adds two discontinuity constraints.
- **Discontinuity constraints, authored.** Six pages in institutional
  vocabularies, each reachable by one authored edge from a page in the
  cell's closure: a finance close calendar (quiet window QW-4), a corporate
  records schedule (filing series RR-21), a workforce attendance ledger
  (code TL-19), a compliance filing calendar (form RN-26), a procurement
  commitment ledger (line PV-25), and a legal data-sharing register (SA-27).
  Each page is written entirely in its own department's register and never
  names the task's systems; each edge is written as the institution actually
  intrudes ("work on systems that feed the statutory accounts also observes
  the company close calendar"), because a discontinuity that reads as
  contrived is a pre-stated kill. The authoring already consumed one
  candidate: a site duty rota carrying out-of-hours cover was semantically
  inseparable from an incident task under every rewrite (embedding rank 4-9
  at scale 500) and was replaced — the certification doing before an episode
  what the kill condition would have done after one.
- **Closure as ground truth.** Per cell, the reference closure from the
  entry node (9 pages, every constraint source page inside it, no overlap
  between cells, no filler ever inside it — `validateClosures`). The closure
  is what "complete" is decided against.

### Signature uniqueness by construction

The probe validated signature uniqueness by exhaustive scan over 42 pages; a
scan does not certify the next generation and its failure mode is a
collision found after authoring. The generator instead guarantees uniqueness
structurally (`graphgen/mint.go`):

- every graded signature is a minted literal from a reserved namespace:
  class codes (`RB-7`), unique quantities (`17 business days`, every digit
  run minted at most once corpus-wide), or reserved name words (`garnet`);
- all non-minted prose — filler and core alike — is digit-free, free of
  class-code shapes, and free of reserved words (quantities in prose are
  spelled out), enforced by `validateReserved` over every rendered page and
  every prompt, intro and gate query;
- grading patterns guard digit boundaries, so `17` is never evidenced by
  `170`.

Under the construction a minted token cannot occur anywhere the mint did not
place it, at any scale. The graphfix battery's exhaustive scan still runs on
every generation as verification, and `TestValidatorRefusesASmuggledToken` /
`TestValidatorRefusesAFillerDigit` prove the verification has teeth. The
open question from the probe handoff — whether signature discipline holds at
thousands of pages — is settled: the battery passes at scale 5000 in the
package tests, in seconds.

Two entry-control patterns (`governed[- ]tier`, `collectors drain`) are
plain prose tokens rather than mints; entry controls ground no kill
condition, and the scan verifies them like everything else.

## Twofold discontinuity certification

A discontinuity constraint exists only if it is certified twice at the scale
in question, before any episode runs. A discontinuity search can rank is an
authoring failure, caught here.

1. **Authoring time (offline, `bench-gs-certify`).** Every corpus page and
   every task phrasing (the cell prompt plus its three gate queries) is
   embedded through the same local model the platform embeds with
   (`nomic-embed-text` via ollama, `pkg/embedding`'s endpoint and no
   instruction prefixes). Requirement: every discontinuity source page ranks
   **outside the exclusion horizon** by cosine similarity for **every**
   phrasing, while the cell's entry page ranks **inside the top 25** for the
   prompt phrasing. The horizon is two percent of the corpus, floored at 25
   (the modal limit episodes actually request) and capped at 100 (the widest
   limit ever observed): it models the largest result list an episode
   practically consumes, and it scales because a fixed count would be ten
   times stricter at 500 pages than at 5000, inverting the design's own
   scale logic. At the study scales the horizon is top-25 (500) and top-100
   (5000).
2. **Live sweep gate (`bench-gs-gate`).** The probe's limit-sweeping gate
   (three phrasings x limits 5/25/100 per cell, through the platform's own
   `search` as a pool identity), with the discontinuity reading flipped from
   recording to requirement: any discontinuity source page appearing in any
   hit list at any swept combination fails the gate
   (`GateResult.DiscontinuityHits`). The existing requirements stand: no
   signature leak in any hit text, and the entry page surfaces for the
   prompt-derived query at limit 25.

The two instruments share the statistic (rank of a page against a task
phrasing) and nothing else — raw cosine over whole pages offline; the
platform's chunked index and ranking live — so a certified discontinuity has
survived two different rankers.

**Scale 50 is certifiably unsatisfiable, by design.** At 50 pages the
horizon floor (25) covers half the corpus, and a limit-100 search returns
the whole store (measured at 42 pages in the probe and again at 50 in the
separation record). Both instruments record this explicitly
(`HorizonExceedsCorpus`; the gate's discontinuity hits at limits 25 and
100) rather than passing vacuously: within the enumeration ceiling,
discontinuity does not exist, which is the within-ceiling control reading,
not a failure. Consequence for the matrix: at scale 50 the discontinuity DV
is not read, and episodes there run the cells with the discontinuity
constraints graded as ordinary spread constraints — which, at that scale,
is what they are.

## Arms and matrix (frozen for #1251)

Corpus arm as in the probe: **graph** (edges planted, verified exact) vs
**stripped** (every reference rendered as its authored prose fallback,
reference table verified empty). Search is **on** in the primary matrix —
the deployed shape is the one under study; the probe already measured the
no-search quadrants.

| Scale | graph / search | stripped / search | Discontinuity DV read? |
| ---: | :---: | :---: | :--- |
| 50 | k=5 x 3 cells | k=5 x 3 cells | no (within ceiling, by construction) |
| 500 | k=5 x 3 cells | k=5 x 3 cells | yes (both certifications required) |
| 5000 | k=5 x 3 cells | k=5 x 3 cells | yes (both certifications required) |

Plus one auxiliary manipulation check: **graph / no-search at scale 5000**
(k=3 x 3 cells), reading whether full-depth traversal survives a
5000-page store around the closure — the walk itself is scale-invariant
(entry handed, edges the same), so a drop here would indict distraction, not
discovery.

Primary: 90 episodes; auxiliary: 9. One agent configuration (claude-cli,
model and client version recorded per manifest as in the probe; the probe's
stronger tier, so the reading-budget-to-corpus ratio is moved by the corpus
axis alone). Episodes run through the subscription client and carry
wall-clock cost only. Fresh pool identity per episode; every prompt carries
the completeness elicitation (below).

## Dependent variables

Per episode, computed offline from the archived transcript and final
document (instruments in `bench/internal/graphprobe`, unchanged where the
probe validated them):

- **Off-entry grounded coverage** (primary, unchanged): signature in the
  final document AND a source page actually fetched. Unread coverage is
  confabulation and is reported separately, never added (probe baseline: 19
  percent of unreachable slots).
- **Discontinuity grounded coverage** (new, certified scales only): the same
  reading restricted to the two discontinuity constraints per cell. In the
  stripped arm this is structurally zero — certification proves no search
  route exists and the stripped plant has no edges — so any nonzero stripped
  reading is an instrument leak that invalidates the run pair.
- **Claimed completeness and overclaim** (new): every prompt ends with the
  frozen elicitation suffix (`graphprobe.PromptCompleteness`): *"End the
  document with a section titled 'Open items': list each thing the document
  needs that you could not determine, or write 'None' if nothing is
  outstanding."* The claim is parsed (`ReadCompletenessClaim`: complete /
  gaps declared / no statement) and **overclaim** is a complete claim while
  at least one graded constraint is absent from the document (read against
  covered, not grounded, coverage: the claim is about what the document
  contains; confabulation is charged separately). The elicited channel
  biases against overclaim — declaring a gap costs one line — so measured
  overclaim is a lower bound, identically in both arms.
- **Cost**: searches, fetches, distinct closure pages read, off-closure
  reads, searches per grounded constraint.
- **Provenance** (unchanged): for every fetch, whether its reference was
  first seen in a search result, a fetched page, or nowhere.

## Pre-stated kill conditions (confirmatory, #1251)

Read over cell means at the certified scales (500 and 5000), graph vs
stripped, search on. "Coverage" is grounded unless said otherwise.

1. **Discontinuity mechanism killed.** Graph-arm discontinuity coverage
   stays below 0.25 at every certified scale. Agents do not follow the one
   authored edge to institutional content even though it is the only route;
   the platform's cross-link steering cannot be argued from completeness
   delivery, and the corrective story is server-side delivery of linked
   content, not authored edges.
2. **Closure mechanism killed.** At every certified scale the arms' overclaim
   rates differ by less than 0.10 AND off-entry coverage differs by less
   than 0.10. Edges then neither improve what the document contains nor
   what the agent knows about its own completeness, at scales where
   enumeration is unaffordable — the strongest available null for the
   candidate, and it retires the density direction entirely.
3. **Study proceeds to the report (#1252).** At some certified scale the
   graph arm shows a discontinuity coverage advantage of at least 0.30
   (against a certified-zero stripped baseline) OR an off-entry coverage
   advantage of at least 0.15 OR an overclaim reduction of at least 0.15.
4. **Anything else** is a recorded boundary condition: numbers to the
   register row, next step argued from them.

Instrument kills, checked before any condition is read: a certification
failure at a scale marked certified; any stripped-arm discontinuity
grounding; a sweep-gate signature leak; plant reference verification
failure; the scale-50 arms failing to replicate the probe's
ceiling-collapse (both arms near ceiling there would be expected; a large
scale-50 arm difference says the corpus, not the scale, moved and the
haystack construction is suspect).

## Estimator and power audit

Unit of analysis: episode (cell x replicate); per-arm-per-scale n = 15 (3
cells x k=5). From the probe's per-episode coverage spread (SD roughly 0.2
to 0.3 across its search arms), a two-sample comparison at n=15 per arm
resolves a coverage difference of about 0.25 at 80 percent power, which
brackets kill line 3's 0.15-0.30 thresholds: the 0.30 discontinuity line is
comfortably resolvable (against a structural zero baseline the one-sample
95 percent interval at n=15 is about +/-0.25 at worst-case variance), the
0.15 off-entry line is resolvable only if the probe's variance does not
recur, which is why kill line 3 is an OR over three readings rather than a
single threshold. Overclaim is a per-episode binary; at n=15 per arm the
detectable rate difference is roughly 0.35 — the 0.15 line in kill 3 is
therefore read descriptively unless pooled across scales (n=30), where
roughly 0.25 resolves. If the first certified scale's readings show SD
above 0.30, k rises to 8 (24 per arm) before the second scale runs; the
audit is re-read, not abandoned.

Wall-clock funding, from the probe's measured costs (72 episodes in ~2.5
hours; plant+embed for 42 pages in ~5 minutes): 99 episodes across six
plant-gate-run sequences (two arms x three scales, one store at a time),
with the 5000-page plants embedding a few thousand chunks on the reconciler
sweep (tens of minutes each, bounded by ollama throughput). Two overnight
driver sequences under `caffeinate -is`, subscription wall-clock only. This
is fundable; the third pre-stated stage kill (underpowered at fundable
episode counts) does not fire.

## Threats to validity

- **Filler monoculture.** Generated clusters share template families; an
  embedding model could rank them degenerately (all-near or all-far),
  making the haystack easier or harder than a real wiki. Mitigations:
  per-cluster vocabulary, shared-genre prose, and both certification
  instruments measuring the outcome that matters (entry findable, adjacent
  pages' enumeration profile recorded per scale). Residual risk stands and
  is why the separation validation is archived per scale rather than
  assumed from one.
- **Digit-free filler.** Real runbooks carry numbers; the corpus spells
  them out outside mints to make signature uniqueness constructive. This is
  a mild register shift, identical across arms and scales, and it touches
  ranking only diffusely.
- **Minted class codes are memorable.** A `RB-7` copied into the document
  is strong evidence of a read (arbitrary codes resist guessing better than
  the probe's plain tokens, which confabulated at 19 percent), but codes
  may also be *easier* to carry than prose facts, inflating absolute
  coverage. Identical in both arms; contrasts are unbiased.
- **Elicited claims.** The Open-items channel biases against overclaim
  (lower-bound reading, stated above) and could itself prompt more
  thoroughness; identical in both arms.
- **Discontinuity authoring realism** is the load-bearing risk and has its
  own kill: if a constraint strong enough to stay out of the top 100 reads
  as contrived, or drifts back into reach at some scale, that is recorded
  and the study falls back to closure alone or stops.
- **One agent configuration.** No tier claims are available or wanted; the
  scale axis moves the budget ratio. A second configuration is a
  replication decision for #1251's owner, not a requirement of this design.
- **Paraphrase undercount** (probe-inherited): signature grading is a lower
  bound on coverage, identical in both arms.

## Separation validation (this stage's deliverable)

Before #1251 is proposed, both certification gates are demonstrated against
a planted corpus at each scale and the readings archived under
[`../results/graph-completion-separation/`](../results/graph-completion-separation/):
per-scale embedding certification reports, live gate reports on the graph
arm, and the per-scale enumeration profiles showing where the corpus stops
being enumerable (entry rank and adjacent-page reachability at limits
5/25/100 as scale grows). Expected shape, pre-stated: scale 50
unsatisfiable-by-construction (recorded, both instruments); scales 500 and
5000 passing both gates with adjacent constraint pages still reachable.
Deviations are readings, not embarrassments: a discontinuity ranking inside
the top 100 at 500 pages is exactly the "drifts back into reach" kill
firing early, before an episode was spent.

## Ops (carried from the probe, unchanged where proven)

`make bench-gt-up` (ollama with `nomic-embed-text` required;
`BENCH_METRICS_ADDR=:9095` when the DataHub quickstart is up), then per
scale: `bench-gs-certify` -> `bench-gs-plant` -> wait for embeddings (live
pages joined to their chunks; deletes are soft, so a chunk count alone lies)
-> `bench-gs-gate` -> `bench-gs-reset`. The planter refuses a non-empty
store; long sequences run as one background driver under `caffeinate -is`
with per-run artifacts kept. Superseded runs are kept.

## Out of scope

The confirmatory episode matrix (#1251), the report and its series page
(#1252), and the authoring-decomposition probe (#1253) are their own
tickets. Density as a *varied* axis (EdgeDensity is a recorded parameter,
frozen at 3) is deferred until the mechanisms are read; a density sweep
before then would re-measure the ceiling the probe already measured.
