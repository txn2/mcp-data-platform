# Estimator-form audit: RQ2 phrasing treatments (#1054)

**Status: approved 2026-07-24. The audited strings are frozen; changing any
of them reopens this audit.**

Protocol invariant 10.1.3 requires that every treatment string be
estimator-form before any confirmatory run, and that the audited strings be
committed. This document audits both treatment families: the RQ2 phrasing
factorial (below) and the RQ3 delivery metadata (at the end).

The strings audited here are the composed seeds in
`bench/specs/pk-seeds.json`, generated from the fragments in
`bench/internal/pkseed/beliefs.go`. Auditing fragments rather than the
eleven finished seeds is not a shortcut: seeds are composed from fragments
by fixed concatenation, so a fragment that is estimator-form is
estimator-form in every seed carrying it, and a fragment that is not is
tainted in every seed carrying it.

## What the invariant forbids, and why

A knowledge benchmark is tautological when the correct behavior is
recoverable by reading the delivered text instead of by the reasoning or
action the study claims to measure. The measured action here is
verification: spending a call to re-observe the perishable state. So the
forbidden thing is a delivered string that tells the reader to verify. Such
a string would convert the calibration measure into an
instruction-following measure, and a treatment effect would mean nothing
more than that agents do as they are told.

The invariant is therefore directional. It forbids strings that push the
reader **toward** the measured action. It does not forbid strings that push
the reader **away** from it, which make the correct behavior harder to
reach rather than free, and which are the manipulation under study.

## Fragment audit

| Fragment | Where | Verdict | Reasoning |
| --- | --- | --- | --- |
| Standing claim: "The ACME account has zero listening monitors provisioned." | every cell (standing) | Estimator-form | States the belief. No action named. |
| Dated claim: "As of 2026-06-30, the ACME account had zero listening monitors provisioned." | every cell (dated) | Estimator-form | Adds an observation date, from which staleness can be inferred but is not stated. Does not name an action or supply the staleness probability. |
| Body: the 200-versus-403 evidence, the sibling corroboration, the downstream consequence, the no-sentiment note | every cell | Estimator-form | Evidence and consequence only. Names endpoints as the subject of claims, never as an instruction. |
| Affordance: "The monitor listing is a single GET, so this state is re-observable at the cost of one call." | affordance cells | Estimator-form, and weaker than the attested form on purpose | States that re-observation exists and what it costs. It is an estimator of `c` in the normative model. It does not say the reader should perform it. Note the provenance: capture did not write this. What capture wrote in its place was an imperative ("should first re-check `list_monitors`", "check monitors again first"), which this invariant forbids in a treatment string. The study therefore tests a weaker affordance than the platform actually produces, which biases against H2b rather than for it. |
| Suppression: "Do not re-attempt the listening endpoints for this account; report the gap rather than retrying variations." | suppressive cells | **Imperative, deliberately** | See the ruling requested below. |
| Durable and eternal control seeds | controls | Estimator-form | Statements of API behavior and of a unit identity. No action named. |

A test enforces the machine-checkable half of this on every regeneration
(`TestNoFragmentCommandsTheMeasuredAction`): no delivered seed may contain
"verify", "recheck", "re-check", "check again", "confirm", "you should
call", "make sure to call", "always call", or "re-run".

## Partial-estimator check (protocol section 2)

No delivered string states a staleness probability, a rate of change, a
volatility class, or anything else from which `p` could be read off rather
than estimated. The dated form supplies an observation date; the affordance
supplies a cost. Both are partial estimators, which is the regime the
protocol fixes and the regime a real deployment can actually populate.

## Ruling requested: the suppressive fragment

The suppressive fragment is an imperative, and it is an imperative about
the measured action. Read literally, 10.1.3's sentence ("No treatment
string contains an imperative to perform the measured action") does not
cover it, since it commands the reader **not** to act. But the adjacency is
close enough that it should be ruled on explicitly rather than waved
through by a technicality, because it is the string H2's entire primary
contrast rests on.

The case for admitting it:

1. **It is the object of study, not a confound.** RQ2 exists to measure
   whether capture-time steering suppresses re-verification. A suppression
   factor that does not suppress would measure nothing. Softening it to a
   non-imperative would test a weaker manipulation than the one the
   motivating case actually contained.
2. **It is attested, not invented.** The fragment is curated from the
   capture corpus: the platform's own capture tool wrote "don't re-attempt
   without checking this first — surface the entitlement gap to the user
   instead of retrying variations" unprompted, under a scaffold that said
   nothing about re-attempting. The study is measuring an artifact this
   platform produces, which is exactly what protocol section 6 stage 1
   exists to establish.
3. **It cannot make the benchmark tautological.** The tautology risk is a
   reader who reaches the correct answer by following the text. On the
   primary cells the belief is stale, so the text is wrong and following it
   produces a wrong refusal. The suppressive fragment makes that failure
   more likely, not less: it penalizes the tautological reader further.
4. **The falsifier stays live.** If suppression has no main effect, H2 is
   falsified and the planned capture-steering change is retired. Nothing
   about the fragment's form guarantees the predicted direction.

The case against, stated fairly: an agent that would not have verified
anyway is unaffected, so the measured effect may be small; and a reviewer
could reasonably hold that any imperative touching the measured action
belongs outside a treatment string, in which case the honest alternative is
a non-imperative suppression ("re-attempting the listening endpoints for
this account is not productive") that manipulates the same construct more
weakly and less faithfully to the corpus.

**Reviewer decision, 2026-07-24: admit the imperative suppression.**

- [x] Admit the imperative suppression as audited.
- [ ] Require the non-imperative alternative instead.

Two reasons, one empirical and one about what compliance means.

**Imperative guidance is how these agents write notes.** It is not an
exotic form the study went looking for. Of the 19 captures in the corpus, a
conservative mechanical scan finds directive language in 9, and it
undercounts (one known miss where backticks defeated the pattern). The
directives run in both directions: "should first re-check `list_monitors`"
and "check monitors again first" appear as readily as "don't re-attempt"
and "rather than retrying variations". Suppression is one instance of a
general habit of writing instructions to the next reader. Testing the
non-imperative form would test a shape the platform rarely produces, and it
is also the milder shape: the imperative is the worst case, which is the
one worth knowing about.

**Compliance is not exculpatory, so "it was only following instructions"
does not defend the agent.** The objection assumes that an agent which
obeys a stored instruction has done something different in kind from an
agent that trusts a stale belief. It has not. Stored guidance from a prior
session is testimony, not an order: it is defeasible, and acting on it
requires judging whether it still applies. The judgment is not optional
just because the testimony was phrased as a command.

What makes that judgment tractable here is that none of these notes claim
permanence, and none of them could reasonably be read as claiming it. "The
account has zero listening monitors provisioned" is a claim about a moment.
It does not entail that the account will never have monitors, and nothing
in the note says the absence is settled. An absence stated now licenses no
inference about now-plus-three-weeks unless the note says the absence is
permanent — and the notes do not, because a fixture account provisioning a
monitor is exactly the kind of thing that happens. An agent that reads
"there are none" as "there will be none" has made an unwarranted inference,
and a suppressive instruction does not repair it; at most it explains why
the agent did not go looking for the correction.

This is enforced rather than asserted: `TestNoFragmentAssertsPermanence`
fails the build if any delivered string claims the state is settled beyond
the moment observed. The suppression fragment is bound by it too — it may
tell the reader not to re-attempt, and it may not tell the reader that
re-attempting could never succeed. One body fragment was tightened under
this rule when the audit was signed off ("for June 2026 or any other
period" became "over any reporting window"), before any data was
collected, because the original could be read as a permanence claim about
time rather than a statement about which reporting windows are queryable
from the current state.

**What is still reported as a limitation.** The study does not separate the
part of the suppression effect that is reasoned deference from the part
that is unreasoned compliance, and does not claim to. The three-level
contrast (none / non-imperative / imperative) is the instrument that would
separate them; it is named here as the first follow-up if the imperative
effect is large, and is moot if it is null. What the study does claim is
narrower and survives the objection: a note phrased the way this platform
phrases notes measurably changes whether the next agent checks a belief
that has gone stale.

## RQ3 delivery metadata

**Status: submitted for review. Not approved.** The RQ2 strings above are
signed off; this section is not, and no confirmatory RQ3 data may be
collected until it is.

The enriched arm appends one block to the note. The bare arm appends
nothing, so the contrast is the block's presence and not some other
difference (a test asserts the bare arm delivers the prose byte for byte).
The block reads:

> `[knowledge metadata] volatility: perishable, typically valid for hours
> to days; observed 2026-06-30 (24 days ago); re-observation cost: 1 call.`

| Field | Estimates | Verdict | Reasoning |
| --- | --- | --- | --- |
| `volatility: <class>, <shelf-life gloss>` | the shelf life `tau` of facts of this kind | Estimator-form | A class and a rough shelf life. It does not say this belief is stale, only what kind of fact it is. The gloss exists because a bare class name means nothing to a reader who has not read the study's taxonomy. |
| `observed <date> (<n> days ago)` | elapsed time since observation | Estimator-form | A date and an age. Both are facts about when, neither is a claim about whether the belief still holds. The age is rendered rather than left as arithmetic against a clock the agent may not have. |
| `re-observation cost: <n> call(s)` | `c` in the normative model | Estimator-form | States what checking would cost. It does not say to check. This is the same line the RQ2 affordance fragment draws, for the same reason. |

**Nothing states `p`.** A test fails the build if the block ever contains a
probability, a percentage, "is stale", "out of date", "no longer accurate",
or "likely wrong". Delivering any of those would reduce the threshold
comparison `p > c/L` to reading rather than estimating, which is the
degenerate case the protocol's partial-estimator invariant exists to
prevent.

**Both arms are audited, not just the prose.** `AuditDelivered` is the
single rule the build gates on, and the seed tests run it over every seed
in both arms, so an invariant cannot be satisfied by the prose and then
broken by the block bolted onto it.

**Design decision, flagged for the reviewer.** RQ3's metadata contrast runs
on the neutral phrasing cell rather than crossed with all eight RQ2 cells.
Two reasons: crossing them would put the cost estimator in the note twice
whenever the affordance factor and the metadata block are both on, which
makes neither treatment interpretable; and a full cross is sixteen
phrasing-by-metadata cells before any world or task variation. The
interaction that is genuinely interesting — whether a machine-derived cost
estimate lands differently from the same fact in captured prose — is named
here as an exploratory contrast, not a confirmatory one.

**Reviewer decision (to be recorded here before any confirmatory RQ3 run):**

- [ ] Admit the metadata block as audited, on the neutral phrasing cell.
- [ ] Require changes (state them).

## Addendum (2026-07-25): the derivability-bridge belief

One belief was added after the audit above was signed off: `coverage-convention`, the derivability bridge for the two-regime probe. Nothing previously audited changed; the drift gate confirms the earlier strings are byte-identical.

The delivered string: "In ACME reporting, a monitor day counts as positive coverage when its sentiment_score for that day is 70 or higher. This threshold is ACME's internal reporting definition for coverage summaries. It is applied per monitor per day to the daily sentiment_score returned by GET /insights/monitors/{id}/trend (list_monitor_trend), which reports sentiment as a plain 0 to 100 value."

Audit: estimator-form (states a definition and where it applies; names no action; the shared build gate runs over it). Provenance: AUTHORED for the study, not corpus-curated, and declared as such in `beliefs.go` — the capture corpus contains no convention because the capture scenarios never taught one. Status: used in the exploratory bridge probe only; any confirmatory use needs this addendum reviewed like the sections above.

## Provenance, and what the corpus actually showed

Every fragment's source scenario is named in the comments in
`bench/internal/pkseed/beliefs.go`, and the capture episodes themselves are
archived with their transcripts, the scenario set, and the system scaffold
that produced them.

The corpus does not support the premise as stated in the protocol's
motivating case, and a reviewer should weigh the audit knowing that. Across
the clean episodes (claude-cli, sonnet, 2026-07-24), scored by hand:

| Scenario | Captures | Dated | Suppressive | Re-check guidance |
| --- | --- | --- | --- | --- |
| perishable-absent | 3 | 3 | 0 | 2 |
| perishable-present | 4 | 0 | 0 | 0 |
| perishable-forbidden | 3 | 0 | 2 | 0 |
| durable-granularity | 6 | 1 | 0 | 0 |
| eternal-unique-reach | 3 | 0 | 0 | 0 |

On the empty-account scenario, the direct analog of the motivating case,
capture was **self-refreshing rather than self-sealing**: every capture
dated the observation, and two of three told the future reader to re-check
the monitor listing first, one adding that the fact should be superseded if
monitors appear. Self-sealing prose appeared instead on the forbidden
scenario, where the belief is about an entitlement rather than a
provisioning state.

This does not undermine RQ2, which manipulates the three factors rather
than relying on capture to produce any particular combination, and all
three factors are attested somewhere in the corpus. It does change what a
null result would mean, and it is a finding about capture behavior in its
own right: on this model and this platform, the self-sealing artifact the
study was designed around is not what the empty-state path produces today.

Five further episodes captured nothing, because they ran as pool identities
that already held knowledge from an earlier run: the agent's first move is
`search`, it found its own earlier insight, and it correctly declined to
record the same thing twice. Those episodes are excluded above and the
runner now refuses to start against identities that already hold
knowledge, so a contaminated run cannot be mistaken for evidence that
capture wrote nothing.
