# Perishable knowledge: a pre-registered study of verification, epistemic metadata, and correction propagation in a governed knowledge layer

Study protocol for issue #1054. This document is the design deliverable
that gates fixture code: no fixture or task code is written until it is
reviewed. It is written as a pre-registration. Hypotheses, directional
predictions, primary and secondary outcomes, decision rules, and the
analysis plan are fixed here, before data collection, so that a null or
adverse result is a finding rather than a reason to redesign. The
separation analysis on the issue (work item zero, approved 2026-07-25)
is the seed of Section 4; this expands it into a full protocol.

Relationship to prior work: this study reuses the #1027 harness (a
mutable-state fixture API service with a state-inspection control plane,
a task and grading schema, per-attempt reset, transcript-plus-access-log
instrumentation, identity pools) and the report-1 S5 lifecycle and teach
machinery. #1027 itself is closed: its design saturated by construction
because it held the discriminating variable (discovery difficulty) at an
easy setting. This protocol's central discipline is that its
discriminating variables are manipulated, and that each cell's
graded-correct behavior differs mechanically, so saturation is
structurally impossible.

## 1. The scientific object

An agent operating over a governed knowledge layer routinely holds a
stored belief about the world that was true when captured and may not be
true now. In the motivating case (Section 3), the stored belief is "the
account has zero Listening Topics provisioned, so no volume or sentiment
trend can be reported." The moment a topic is provisioned, that belief is
false, and the truth is one tool call away. The agent faces a decision it
is rarely studied making: trust the stored belief at zero cost, or spend
a verification action to recover ground truth.

This is a sequential decision under uncertainty about a belief's current
validity, made by an agent that (unlike the subjects of the conversational
memory literature) holds a cheap verification action against a live world.
The study measures how agents make this decision, whether the platform's
epistemic signals (volatility class, freshness, recheck cost, provenance)
improve the decision, and whether the platform's correction machinery
repairs the belief downstream once it is found stale.

### 1.1 Position in the literature and theoretical framing

The study sits at the intersection of four active strands and occupies a
seam none of them covers. Claims here are calibrated: the contribution is
empirical and methodological, not a theoretical advance in any of these
fields.

**Agent memory and temporal validity.** The storage-side machinery for
perishable knowledge exists: temporal validity ledgers with valid-at,
expired-at, and invalid-at edges (Zep); deterministic supersession by
subject-relation-object (MemStrata, arXiv 2606.26511); forgetting-aware
accuracy metrics that penalize reliance on obsolete memory (FAMA, arXiv
2604.20006); auto-discovered shelf-life from edit histories (adaptive
decay, arXiv 2604.26970). What none of these measures is whether an agent,
delivered such metadata, changes its decision to trust or verify. This
study measures that actionability.

**Staleness detection in conversational memory.** STALE (arXiv 2605.06527)
asks whether agents notice a stored memory has gone stale, but in
purely conversational memory with no mechanism to verify against an
external ground truth. Our agents hold a cheap verification action against
a live world, which changes the object of study from detection to the
economics of verification.

**Knowledge conflict.** ConFiQA, ConflictQA, WikiContradict, and MAGIC
study context-versus-parametric conflicts where the model cannot check;
DRNoise (arXiv 2607.17291) plants falsifiable misleading evidence for
document-research agents but with no operational state and no correction
lifecycle. Our conflicts are between stored organizational testimony and a
checkable, mutable world state.

**Machine epistemic agency and trust.** The closest work is on LLMs as
epistemic subjects. Epistemic Context Learning (arXiv 2601.21742) has
agents estimate peer reliability from stored interaction history, but fixes
history length and, by its authors' own statement, does not model drift or
reliability decay, has no external verification, and no cost-benefit rule;
its future work names dynamic reliability shifts, the seam we enter. Know
When to Trust the Skill (arXiv 2604.16753) studies a single agent
appraising its own tool output within one task, not stored testimony from a
prior agent. The trust-in-automation and automation-bias literature (a
mature human-factors field, now extended to physician-LLM reliance and
agentic decision support) studies a human epistemic subject relying on
machine advice; we invert the subject to the machine and the testimony to
prior-agent capture.

**Theoretical lens: epistemic vigilance.** Social epistemology models the
consumer of testimony as exercising epistemic vigilance, the calibrated
allocation of scrutiny to claims by source, coherence, and stake (the
Sperber lineage; see the vigilance-and-responsibility treatment,
tandfonline 02691728.2022.2042420). We operationalize vigilance
computationally as the verify-or-trust decision and measure its
calibration against the normative threshold of Section 2. This lens names
the study's crispest novel object: a **self-sealing belief**, a captured
testimonial artifact whose phrasing discourages its own re-examination
(the production insight's present-tense title and its "do not substitute"
guidance). RQ2 (Section 4.2) is, as far as the searches above found, the
first empirical measurement of a self-sealing testimonial artifact's effect
on a machine consumer's vigilance.

**The seam, stated precisely.** A machine epistemic subject, consuming
stored and governed testimony captured by a prior agent, where the belief
can go stale because the world changed after capture, with a cheap
verification action available, measured against a decision-theoretic
vigilance baseline, over a knowledge layer with real correction machinery
(supersede, cross-identity, cross-channel). No surveyed work combines
these. The honest scope of the claim: this is the first empirical,
decision-theoretically grounded measurement of machine epistemic vigilance
over perishable organizational testimony, not a contribution to the theory
of social epistemology or to human-subjects trust-in-automation. Primary
positioning is an agent knowledge-integrity benchmark; the vigilance framing
is the lens that gives the primary dependent variable (Section 2) its
meaning and its secondary audience, not a co-equal claim.

## 2. Normative model and the primary dependent variable

We give the decision a normative baseline so that agent behavior is
measured against a rational policy, not only reported as a rate.

Let an agent hold or be delivered a stored belief `b` about a world whose
true current state is `w`. Let `p = Pr[w != b]` be the belief's staleness
probability at query time. Let `c` be the cost of the verification action
(a tool call revealing `w`), expressed in the same currency as task loss.
Let `L` be the excess loss of acting on a stale belief (a wrong answer or
a wrong refusal) relative to a correct answer.

Ignoring second-order verification failure, the expected loss of trusting
is `p * L`, and of verifying is `c`. The rational policy verifies iff

    p > c / L.

Every term is controlled or measured in the fixture. We set `p` by how
often the world is mutated between capture and query; we set `c` by how
many calls a recheck requires; `L` is measured directly in RQ5 (Section
4.5) and can also be set by task construction. The threshold `c / L` is
therefore computable per cell, and each cell is labeled rational-to-verify
or rational-to-trust ex ante.

**Primary dependent variable: the calibration gap.** For a set of cells
spanning a range of `p`, the rational policy is a step function of `p` at
the threshold `c / L`. The agent's observed verification rate as a
function of `p` traces its revealed policy. The calibration gap is the
divergence between the agent's revealed policy and the rational step,
summarized as (a) the agent's revealed threshold `p*` (the staleness
probability at which its verify rate crosses 0.5), and (b) the integrated
absolute deviation between agent verify rate and the rational indicator
across the `p` grid. Treatments that move `p*` toward `c / L` and shrink
the integrated deviation are improving the agent's decision.

This reframes every platform lever as a term the treatment estimates for
the agent: a volatility-class label and a freshness age are estimators of
`p`; a recheck-cost hint is an estimator of `c`; the damage asymmetry
(RQ5) is `L`. The scientific question is whether supplying these
estimators moves agents toward the rational policy.

**Partial-estimator invariant.** Treatments deliver categorical or partial
estimators (volatility class, verified-at timestamp, recheck cost) from
which the agent must infer staleness under uncertainty. A treatment never
delivers the exact staleness probability `p`. If it did, the threshold
comparison `p > c/L` would reduce to arithmetic and the calibration
construct would degenerate into a reasoning test we had trivialized. The
interesting regime is estimation under partial information, which is also
the regime a real deployment can actually populate: the platform knows a
fact's class and capture time, not its instantaneous staleness.

## 3. Motivating case (production, anonymized)

An ACME deployment connects the platform to a social-analytics vendor API.
Asked for a volume and sentiment trend over listening topics, the agent
correctly found the answer unobtainable and the platform captured two
insights.

Insight A, a **perishable state observation**, titled in the present tense
("Listening is not usable on the ACME connection: zero Topics
provisioned"), distinguished the empty result from an authorization
failure (HTTP 200 with an empty data array, versus the 403 the spec
defines for forbidden data), corroborated across sibling endpoints on the
same credential, derived the consequence (both listening operations
require a topic id, so no valid call exists), recorded the single GET that
would re-verify the claim, and flagged root cause as an open question. Its
guidance ("do not substitute owned-profile analytics") actively steers the
next agent away from the recheck that would refresh it: the belief is
**self-sealing**.

Insight B, **durable contract knowledge**, recorded API behaviors that
change only with a vendor release: a parameter silently ignored on one
endpoint, response keys remapped from requested metric names, daily unique
counts that must not be summed to a period unique, null-versus-zero
semantics for empty profiles.

The two beliefs have different truth lifetimes and the platform stores
them identically, with one authority level, distinguished only by prose.
That flattening is the defect under study. It is a schema and steering
defect, not a model defect, which is what makes the study's arms
shippable.

The fixture (Section 5) is a neutral analog, not a replica of the vendor,
for three rigor reasons: reproducibility (a published benchmark cannot
depend on a live third-party API with drifting state and entitlements), no
vendor confound, and no leakage of a real customer's data. The analog is
designed to preserve the structural features that made this case
scientifically interesting, enumerated in Section 5.2.

## 4. Hypotheses (pre-registered) and decision rules

Each hypothesis states a directional prediction, the divergence mechanism,
the falsifier, and the platform decision it gates. Confirmatory analyses
are the primary tests; everything else is exploratory and labeled so.

### 4.1 RQ1: verification behavior and its calibration

**H1a.** Across cells varying staleness probability `p` with recheck cost
`c` and damage `L` held fixed, agents' verification rate increases with
`p` but with a revealed threshold `p*` that is biased high relative to the
rational `c / L`: agents under-verify perishable beliefs.

**H1b.** In the no-knowledge control (agent holds no stored belief), agents
probe the world at a rate near ceiling, establishing that the deficit in
H1a is trust in the stored belief, not inability to probe.

Divergence mechanism: cells at different `p` have mechanically different
correct behaviors (trust suffices at low `p`; only verification yields the
correct answer at high `p`, where trusting produces a specific wrong
answer, a refusal of a now-answerable question or a stale value). Cells
cannot converge.

Falsifier: if agents verify perishable beliefs at near-ceiling rate
unprompted (`p*` at or below `c / L`), the treatments in RQ2 and RQ3 have
no headroom. That outcome would itself contradict report 1's established
mechanism (the knowledge-trap lift depended on agents acting on delivered
knowledge without independent verification) and the conflict literature's
over-trust prior, and would be reported as a headline reversal.

Gated decision: baseline evidence for whether perishable knowledge is
safe to deliver without epistemic qualification.

### 4.2 RQ2: self-sealing capture phrasing (factorial, to isolate the sealing effect)

Naively contrasting "dated observation plus recheck affordance" against
"standing truth plus suppressive guidance" bundles three manipulations and
would leave the effect uninterpretable (and the recheck affordance edges
toward a tautological instruction, see Section 10.1). RQ2 therefore
manipulates three binary factors of the seed prose independently, over
identical factual content, evidence, and consequence:

- **Temporal framing**: dated point-in-time observation ("as of DATE, zero
  topics") versus standing present-tense truth ("topics are not
  provisioned").
- **Suppressive guidance**: present ("do not substitute owned-profile
  analytics") versus absent.
- **Recheck affordance**: present (states that the state is re-observable)
  versus absent. Constrained by the anti-tautology invariant (10.1): the
  affordance may state that re-observation exists and its cost, never that
  the agent should perform it.

**H2 (primary contrast).** The suppressive-guidance factor lowers
verification rate and final-answer accuracy on stale cells; this is the
self-sealing effect, measured as the main effect of suppression with the
other two factors balanced.

**H2b (secondary).** Temporal framing (dated versus standing) raises
verification independent of suppression.

Divergence mechanism: suppressive guidance instructs the agent away from
the recheck; the factorial separates that from mere temporal framing and
from the affordance, so the sealing effect is attributed to the component
that carries it, not to a bundle.

Falsifier: a null main effect of suppression means capture-time steering is
not a lever, retiring the planned change (steering the capture tool's
output) at low cost.

Gated decision: whether the Capture Knowledge tool is steered away from
suppressive standing-truth phrasing toward dated observations.

### 4.3 RQ3: epistemic metadata actionability

**H3.** Delivering a perishable belief with machine-derived epistemic
metadata (volatility class, verified-at age, recheck cost) moves the
agent's revealed policy toward the rational threshold: verification rate
rises where `p` is high and does not rise where `p` is low, shrinking the
integrated calibration gap relative to the bare-belief delivery.

Divergence mechanism: the metadata are estimators of `p` and `c` in the
normative model; supplying them should let the agent approximate `p > c/L`.
The storage-side plumbing for such metadata exists in the field (temporal
validity ledgers, valid-at and expired-at edges, forgetting-aware
metrics); what is unmeasured everywhere is whether surfacing it changes
agent decisions.

Falsifier: zero treatment delta means metadata surfacing is not the lever
and server-side invalidation is, which is precisely the build decision in
RQ4 and D4.

Gated decision: whether enrichment payloads carry volatility, verified-at
age, and recheck cost on perishable knowledge.

### 4.4 RQ4: supersede and correction propagation

**H4a.** When an agent observes the world contradicting a stored belief, a
correction is captured and the stale belief is superseded within the
session.

**H4b.** After supersede, the stale belief stops being delivered to other
identities and across other delivery channels; the correction propagates.

Divergence mechanism: pure platform plumbing, measured before-and-after
correction across at least two identities and two delivery channels.
Report 1 measured cross-identity transfer at 46.7 percent (CI 30 to 63)
and supersede on denominators too small for a point estimate; this study
raises the denominator and closes the loop.

Falsifier for H4b: stale copies continue to surface post-supersede,
identifying propagation as a bug class rather than a tuning gap, and
scoping the fix.

Gated decision: whether supersede needs contradiction-triggered
invalidation and cross-channel propagation repair (absorbs #980 A2 and A5).

### 4.5 RQ5: damage asymmetry (parameterizes `L`)

**H5.** The excess loss of one stale belief is at least as large as the
gain of one correct belief (report 1: approximately +56 accuracy points
per correct trap-relevant fact), because a confidently wrong answer is a
worse outcome than an abstention.

This is a measured ratio on matched tasks, not a mechanism test. Its
output is the `L` term of the normative model, which sets the rational
threshold against which RQ1 and RQ3 are scored, and it prices knowledge
hygiene against knowledge coverage.

Gated decision: how much curation friction (promotion gates, review) is
justified, feeding the promotion-gate design (#1013).

## 5. Fixture design

### 5.1 Volatility taxonomy, grounded

We treat volatility as a continuous shelf-life axis `tau` (the
characteristic time to staleness), following recent work that recovers
velocity-volatility clusters from real edit histories and finds most facts
obey a Lindy pattern (older facts are less likely to be superseded). The
study bins `tau` into three classes that map to the motivating case:

| Class | `tau` | Fixture example | Invalidated by |
| --- | --- | --- | --- |
| Perishable state | hours to days | resource provisioned or not; entity count | any actor touching the account |
| Durable contract | months, versioned | endpoint parameter ignored; response key remap | a vendor release |
| Eternal invariant | unbounded | a summation identity over units | never |

The primary experiment operates on perishable state (where `p` is a
controllable design variable). Durable and eternal classes appear as
within-subject controls: they let us test that a treatment which raises
verification on perishable beliefs does not indiscriminately raise it on
durable or eternal ones (a treatment that makes agents verify everything
is not calibrating, it is just adding noise; H3's "and does not rise where
`p` is low" clause depends on these controls).

### 5.2 Structural features preserved from the motivating case

The neutral analog is designed to reproduce, not merely gesture at, the
features that made insight A scientifically interesting:

1. **Empty-versus-forbidden ambiguity.** A perishable-state query returns
   HTTP 200 with an empty collection when the state is absent, and a
   distinct forbidden status when access is denied. The agent must
   distinguish "nothing there yet" from "not allowed," a distinction the
   vendor case turned on.
2. **Recheck is a single cheap call** (`c = 1`), and can be dialed to
   multi-call for the `c` sweep in RQ3.
3. **Downstream dependency.** The unavailable state blocks a downstream
   operation (the analog of "no topic id, so no metrics call"), so a stale
   "unavailable" belief produces a wrong refusal, and a stale "available"
   belief produces a wrong value.
4. **Corroboration surface.** Sibling endpoints on the same credential
   return populated data, so the agent can (as insight A did) triangulate
   rather than treat a single empty result as authoritative.
5. **A sentiment-like dimension** available only through the blocked path,
   so the substitution temptation (answer from the wrong source) exists
   and refusal-versus-substitute is graded.

### 5.3 World-change control plane

The #1027 fixture service is extended with a seedable, between-sessions
world-change control plane: account state (for the perishable class:
resource provisioned or not, entity counts) is set at reset and can be
toggled between the capture session and the query session, deterministically
and drift-checked. This is what sets `p` per cell: a cell with `p = 1`
mutates the world after capture; a cell with `p = 0` does not. The access
log already records every catalog call, which is how verification is
detected.

### 5.4 Source generality

The main experiment runs over an API connection (the motivating source).
Source generality is a pre-registered replication, not a v1 claim: the
same perishable-state scenarios are ported to the report-1 warehouse (a
table's row-state changes after a description is captured) and run as a
generalization arm after the API effect is established. Establishing the
effect and testing its generality in the same underpowered run would
confound existence with breadth; they are separated deliberately.

## 6. Knowledge seeding: real capture, frozen minimal pairs

Seeding is a two-stage process that reconciles fidelity with
reproducibility.

**Stage 1, corpus generation (real capture).** The platform's actual
Capture Knowledge tool is driven over the fixture, exactly as it was in the
production case, to produce a corpus of insight phrasings for each scenario.
This establishes that self-sealing phrasing is a real artifact of the
platform's capture behavior, not a strawman we wrote.

**Stage 2, frozen seed set (deterministic).** From the corpus we curate a
fixed set of seed insights, committed and drift-checked, used identically
across all runs. The RQ2 phrasing variants are minimal-pair rewrites of the
same factual content: the dated-observation and standing-truth forms carry
identical evidence, corroboration, and consequence, differing only in
temporal framing and the presence of suppressive guidance. Freezing the
seed set removes capture-time LLM nondeterminism as a confound on the
manipulated variable; the corpus stage preserves the claim that the
phrasings are ecologically real.

The RQ3 metadata variants are produced by attaching or withholding
machine-derived fields (volatility class, verified-at age, recheck cost) at
delivery, holding the seed prose constant.

## 7. Task design and grading

Tasks map to exactly one correct behavior per cell, where the correct
behavior may be an answer, a refusal, or a verify-then-answer, determined
by the cell's world state:

- Answerable-and-belief-fresh: answer.
- Answerable-and-belief-stale (belief says unavailable, world now
  available): the correct behavior is verify-then-answer; trusting the
  belief yields a wrong refusal.
- Unanswerable-and-belief-stale (belief says available, world now empty):
  the correct behavior is verify-then-refuse or refuse; trusting yields a
  fabricated value.
- Unanswerable-and-no-belief: probe, then refuse or escalate. Abstention
  and clarification are graded correct where the world makes them correct,
  reusing report-1 S5 abstention grading.

Grading has a deterministic layer and a judged layer. Deterministic:
whether the verification call occurred (transcript plus fixture access
log), whether a write or substitution occurred, and exact-value or
result-set correctness where the answer is checkable. Judged: whether a
refusal or escalation is well-formed and whether an answer carries the
required caveat, using the report-1 pinned judge with a committed
calibration set. Judge quality is reported as agreement with human labels
(Cohen's kappa) on that set, and no judged metric is published without its
kappa, matching report 1's practice.

## 8. Experimental design

Factors, all within the same task families:

- Staleness probability `p`: a grid (for example 0, 0.25, 0.5, 0.75, 1.0)
  set by the world-change control plane. Primary axis for RQ1 calibration.
- Knowledge state: none, fresh, stale, corrected. Control plus the RQ1 and
  RQ4 conditions.
- Capture phrasing: a 2x2x2 factorial of temporal framing, suppressive
  guidance, and recheck affordance (RQ2), on stale cells.
- Delivery metadata: bare versus volatility-plus-freshness-plus-cost (RQ3).
- Volatility class: perishable (primary), durable, eternal (controls for
  H3's discriminant clause).
- Recheck cost `c`: 1 versus multi-call, a secondary sweep for RQ3 that
  moves the rational threshold and tests whether agents respond to `c`.
- Model tier: at least two models spanning a capability range, as a
  robustness axis (report 1 and the #1027 pilot both suggest frontier
  models may absorb effects that weaker models expose).

Arms are treatments, not architectures: phrasing on or off, metadata on or
off, supersede exercised or not. Each is a shippable platform change.

Repetition: `k` chosen from a power analysis (Section 9) sufficient for the
primary calibration-gap contrasts and the RQ2 and RQ3 treatment deltas to
carry confidence intervals, not point estimates. claude-cli runs first for
breadth at zero metered cost, with client version pinned in every manifest;
headline cells are replicated on the raw Messages API, because claude-cli
inserts its own behaviors (the #1027 pilot's client-side tool search is the
cautionary precedent). Any metered run is proposed against a stated dollar
cap and approved before execution; the standing budget is not spent
speculatively.

## 9. Analysis plan

- **Primary.** The calibration gap (revealed threshold `p*` and integrated
  deviation) per model, compared across the RQ3 metadata treatment and the
  RQ2 phrasing treatment. Mixed-effects logistic models of the
  verification decision with `p`, treatment, and volatility class as fixed
  effects and task and identity as random effects, so that task difficulty
  and identity are not confounded with treatment. Report 1's bootstrap CI
  treatment is retained for headline rates.
- **Secondary and exploratory** (labeled): the `c` sweep, the model-tier
  interaction, per-volatility-class breakdowns, and the RQ4 propagation
  decomposition (captured, superseded, stopped-delivering) across
  identities and channels.
- **Multiplicity.** The confirmatory tests (H1a, H1b, H2, H3, H4a, H4b, H5)
  are a fixed family; p-values carry a Holm correction across the family.
  Exploratory contrasts are reported as estimates with CIs and named as
  exploratory.
- **Power.** Variance estimates for verification rate and accuracy come
  from the #1027 pilot's per-cell dispersion (that data was retained
  expressly for this) and from a small internal pre-run on two cells. `k`
  and the `p` grid density are set so the primary calibration contrast and
  the RQ2 and RQ3 deltas reach a target minimum detectable effect fixed
  here before any confirmatory run. The pre-run is exploratory and its
  cells are excluded from confirmatory analysis.
- **Stopping.** The confirmatory run executes the full pre-registered
  matrix; there is no data-dependent stopping. If a metered replication is
  budget-limited, the cells to drop are named ex ante (lowest-information
  first), and dropped cells are reported, never silently omitted.

## 10. Confounds and threats to validity

### 10.1 Anti-tautology and manipulation validity

A knowledge benchmark is tautological when the correct behavior is
recoverable by reading the delivered text rather than by the reasoning or
action the study claims to measure. This study is protected structurally in
two ways and guarded procedurally in three.

Structural protections:

1. **Stale-cell inversion.** On the primary cells the stored belief is
   stale, so an agent that reads the delivered belief verbatim produces the
   wrong answer. The tautological reader is penalized, not rewarded. This is
   the inverse of the failure mode that exposed report 1's traps, where the
   answer was recoverable from the delivered fact.
2. **Discriminant controls.** The durable and eternal volatility classes
   (5.1) are within-subject controls. A degenerate treatment that induces
   blanket verification would raise the verify rate on eternal invariants
   too, failing H3's clause that verification rises only where staleness is
   high. A "verify everything" treatment therefore cannot pass.

Procedural invariants, audited before the confirmatory run:

3. **Estimator-form, not command-form.** No treatment string (phrasing or
   metadata) contains an imperative to perform the measured action
   (verify, recheck). Treatments carry only information from which the
   rational action can be inferred. A reviewer confirms each treatment
   string is estimator-form before any confirmatory data, and the audited
   strings are committed.
4. **Partial estimators only.** Per Section 2, no treatment delivers the
   exact staleness probability; the agent must estimate it from class and
   age. This keeps the calibration measure from collapsing into arithmetic.
5. **Non-trivial base tasks.** The underlying tasks require genuine work
   (multi-step chains, pagination, aggregate reasoning, reused from the
   #1027 task families), so that even a correct answer on a fresh or
   no-belief control cell is not a single-lookup, and the study is not
   vulnerable to a "toy tasks" reading.

### 10.2 Standard threats

- **Internal.** Task difficulty (controlled by task as random effect and by
  minimal-pair phrasing); position and order effects (task order
  randomized per attempt within identity budget); capture nondeterminism
  (removed by the frozen seed set); the claude-cli client-side tool-search
  confound (contained by raw-API replication of headline cells and by
  reporting claude-cli and API cells separately, per report 1).
- **External.** Single motivating scenario family generalized to three
  volatility classes and (as pre-registered replication) two sources;
  neutral analog rather than the live vendor (a deliberate reproducibility
  trade, with structural fidelity enumerated in 5.2). Two model tiers bound
  but do not exhaust model generality.
- **Construct.** "Verification" operationalized as an observed recheck call
  against the perishable state, not inferred from prose; "self-sealing"
  operationalized as the phrasing minimal pair; "calibration gap"
  operationalized against the computed `c / L` threshold. Each construct
  has one measurement, fixed here.
- **Statistical conclusion.** Mixed-effects modeling to avoid
  pseudo-replication across repeated tasks; Holm correction across the
  confirmatory family; power fixed before confirmatory data.

## 11. Reuse and new build

Reused unchanged: the fixture service and its state-inspection control
plane, the task and grading schema, per-attempt reset, transcript and
access-log instrumentation, identity pools, the S5 lifecycle and teach
machinery, the audit read-back, the pinned judge and calibration harness,
the report toolchain (notebook-recomputed figures, pandoc render, DOI
pipeline).

New build, gated behind review of this document: the world-change control
plane (5.3); the three-volatility-class scenario and task generator with
the structural features of 5.2; the capture-corpus-to-frozen-seed pipeline
and the minimal-pair phrasing variants (6); the epistemic-metadata delivery
plumbing (RQ3); the verification-decision and calibration-gap analysis in
the report notebook; the judged grading pass for abstention and refusal
cells.

## 12. Platform decisions this study settles

Each is answered with a measured yes or no, and each yes files a follow-up
implementation issue.

- **D1.** Does the insight and memory schema gain volatility and
  valid-until fields, and does capture populate them?
- **D2.** Does enrichment surface verified-at age and recheck cost on
  perishable knowledge?
- **D3.** Is the Capture Knowledge tool steered to write dated observations
  with recheck affordances rather than standing truths?
- **D4.** Does supersede need contradiction-triggered invalidation, and
  does cross-channel, cross-identity propagation need repair (#980 A2, A5)?
- **D5.** How much curation friction is justified, priced by RQ5's `L`
  (feeds #1013 promotion-gate design)?

## 13. Reproducibility and artifacts

Deterministic seed-pinned fixtures and scenario sets, drift-checked against
regeneration as in #1027. Committed frozen seed insights and phrasing and
metadata variants. Raw results and manifests archived under
`bench/results/`. The report, if published, follows report 1's structure
(methods, pre-registered hypotheses, results with CIs, threats to validity,
reproduction appendix), with figures recomputed by the committed notebook
from committed data, a new concept DOI, and the pre-registration timestamped
by this document's commit.

## 14. Publication and falsification commitment

The study is published only if the pre-registered separations materialize.
If they do not, the findings land as an internal note and the falsified
premises are recorded on #1054, exactly as #1027 was closed. Every arm
maps to a platform decision (Section 12), so no outcome is merely
descriptive: confirmed treatments ship, null treatments retire a planned
change, and broken propagation scopes a bug fix. The calibration-gap
result is a contribution in either direction: agents that under-verify
validate the platform's epistemic-metadata investment, and agents that
verify rationally without help would be a surprising, publishable
trust-calibration result against the field's over-trust prior.

## 15. Work plan

1. Review of this protocol (gate; no fixture code before sign-off).
2. Fixture: world-change control plane and the perishable-state analog with
   the 5.2 structural features; drift-checked generation.
3. Capture-corpus run and the frozen minimal-pair seed set.
4. Epistemic-metadata delivery plumbing and the task and grading additions.
5. Internal power pre-run (two cells, excluded from confirmatory analysis)
   to fix `k` and the `p` grid.
6. claude-cli confirmatory matrix (zero metered cost).
7. Raw-API replication of headline cells, proposed against a dollar cap.
8. Warehouse generalization replication.
9. Analysis, decision table, and (if separations hold) report and DOI.
