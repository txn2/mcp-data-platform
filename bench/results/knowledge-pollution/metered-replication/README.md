# Raw-API replication (protocol 13) — complete

Every other arm in this study runs through claude-cli on a subscription. That
leaves one objection open: that the effect is a property of one client harness
rather than of the platform and the model. This arm closes it, on the raw
Messages API with the harness's own agent loop and no agent framework.

Cells are the pre-registered ones: both derivability classes' wrong arms
across three tiers at k=8. They were deliberately **not** re-chosen in light of
the RQ1 result.

## Result

| Cell | claude-cli | raw API | agree? |
| --- | --- | --- | --- |
| checkable / haiku | 16–24 of 24 adopted | **8/8** [67.6, 100] | yes |
| checkable / sonnet | 0/24 | 0/8 [0, 32.4] | yes |
| checkable / opus | 0/24 | 0/8 — **arm invalidated**, see below | — |
| convention / haiku | **0/24** | **4/8** [21.5, 78.5] | **no** |
| convention / sonnet | 0/24 | 1/8 [2.2, 47.1] | **no** |
| convention / opus | 0/24 | 0/8 [0, 32.4] | yes |

Spend: $4.26 across 48 episodes plus one invalidated retry, against the $25
cap. Computed from the token counts in the results files, not estimated.

## What replicates: the headline

The checkable claim propagates to the weak tier and not to the strong ones,
with no agent framework in the path. This is the study's central finding and
it survives the client change intact.

## What does not replicate: the convention null

On claude-cli the false fiscal convention was adopted **zero times at every
tier**. On the raw API haiku adopts it **4 of 8**. Every answer in that arm was
inspected individually: it alternates cleanly between the planted 724 and the
correct 873, with no other reading.

**So the immunity of non-derivable conventions is a property of Claude Code's
scaffolding, not of the platform.** Strip the agent framework and conventions
propagate too. H1c, falsified on claude-cli, would have held here.

This is the correction the replication existed to find, and it bounds two
claims that would otherwise have been stated too broadly:

- **Robust across clients:** checkable claims propagate to weak models, by
  suppressing the verification those models would otherwise perform.
- **Client-specific:** the immunity of conventions. Reportable only for agents
  running inside a scaffold like Claude Code's, and not as a platform property.

The report must not state the convention null as a general result. Section 10's
"one client" threat was real and this is what it was hiding.

## The invalidated arm

`checkable-wrong-opus-api` drifted twice and the block stopped rather than
spend more. Opus evaluators promoted corrections into the shared applied tier
mid-arm, creating changesets other identities can read, which the store
invariant catches.

Note the difference from the claude-cli arms, where opus captured corrections
at **pending** status: on the raw-API path it applied them. Same corrective
behavior, carried further. Both attempts are archived; the cell is already
covered at 0/24 by the claude-cli matrix, so what is missing is a confirmation
rather than a finding.

## Provenance

These arms ran from a working tree, so their manifests record the commit with
a `-dirty` suffix. The arm script was committed immediately afterwards; the
suffix is the guard reporting honestly rather than a defect.
