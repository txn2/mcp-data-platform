# RQ1 warehouse block (in progress)

Confirmatory arms for the knowledge-pollution study's RQ1, run per
`bench/docs/knowledge-pollution-study-design.md` and ticket #1167. Driver:
`bench/scripts/pollution-rq1.sh`, one arm at a time through
`bench/scripts/pollution-arm.sh`.

The full block is derivability class (convention, checkable) x arm (absent,
correct, wrong) x tier (sonnet, haiku, opus), 24 episodes per cell, 432
episodes. Arms land here as they complete; this README is updated with them.

Every arm runs on its own fresh database with the seed re-applied, with the
DataHub editable aspects cleared and verified absent beforehand, with gate
state truncated, and with the three claude-cli meta-tools pinned off
(protocol section 12 — the effective disallow list is on each manifest).
`platform.log` is not archived; everything else the arm produced is.

## Completed arms

| Arm | Accuracy | Store across the eval | Commit |
| --- | --- | --- | --- |
| warehouse / convention / absent, sonnet | 23/24 | constant | 8558b1e6 |
| warehouse / checkable / absent, sonnet | 24/24 | constant | 8558b1e6 |

Both controls are at or near ceiling, so the treatment arms' adoption is
measured against a clean floor rather than a noisy one.

The one convention miss is not a wrong reading: the episode answered
317090.50 on `s3-fiscal-q1-net` against a correct value of 317090.52 at
tolerance 0.01, a two-cent precision difference on a figure of roughly
317,000. It classifies as "other" rather than as any planted or trap value.
Protocol 5.1 already names that task as the matrix's tightest discriminant
pair and 8.4 names it as the first convention task to drop; the tolerance is
the committed task's and was not touched after seeing this.

## Retained attempts that are not confirmatory data

Kept because their episodes are real and because what invalidated each one is
itself worth stating. None is used in the analysis.

**`convention-correct-sonnet-BASELINE-MISPLACED`** (23/24). Ran to completion,
then failed its store-constancy check because the arm script took the section
7.3 baseline before the plant rather than after it. The two records reported
as drift are the plant's own — insight `ab10c6cb...` captured by the teacher
identity `bench-agent-200`, and changeset `c1854878...` on the orders entity,
both matching `planted.json` — and no evaluator identity appears in the drift
at all, so the store was in fact constant across the 24 episodes. Section 7.2
excludes the plant's capture, approval and apply from the arm's accounting.
Fixed in `cb61f690`: the snapshots now bracket the eval, with a third
`store-clean.json` taken before the plant so the plant's own effect on the
store stays readable.

**`convention-correct-sonnet-DRIFTED-0`** (23/24). A genuine store-constancy
failure and the first one the check caught for the right reason: evaluator
identity `bench-agent-015` captured a pending insight
(`eb0e10f82eb30aef4a2022482313baee`) mid-arm. Section 7.3's remedy is to
invalidate the arm and re-run it on a fresh database, which is what happened.

The rule was applied as pre-registered rather than narrowed. A pending insight
is readable only by its own capturer — `provider_insights.readableBy` admits an
insight to a non-capturer only once applied — so no later episode in that arm
could have seen it, and scoping the invariant to cross-identity-visible state
would have been defensible. It would also have been an amendment made after
seeing the data, which costs more in a pre-registered study than the wall clock
it saves. The observed evaluator-write rate is reported as a finding in its own
right rather than absorbed into a caveat.

**`convention-correct-sonnet-INTERRUPTED-bells`** (16/17, incomplete). Stopped
at 17 of 24 episodes to restart the block detached from the operator's
terminal: each headless `claude -p` child inherited the controlling TTY and was
ringing a terminal bell roughly every thirty seconds. Not a measurement
failure; the episodes it did produce are intact.
