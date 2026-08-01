# Gate probe summary (study-3 due diligence, 2026-07-31)

Question probed: does the search-first hard gate (`workflow.require_search`,
`SEARCH_REQUIRED`) cause discovery that would not happen without it? Decision
rule, stated before any run: gate-off spontaneous search at or above 7/8 under
the default scaffold on the tested tiers means the enforcement study is dead at
ceiling.

Design: pk fixture, `BridgeProbeCells` (question whose correct answer requires
a planted non-derivable convention note, plus a no-note control), k=8 per cell,
claude-cli. Gate OFF via `bench/config/platform.bench.pk-gateoff.yaml` (single
deviation from the pk arm). Scaffold `default` keeps the harness system-prompt
bullet "Use the search tool ..."; `no-discovery` removes it, leaving only the
platform's own channels (`platform_info` agent_instructions, tool
descriptions) — the realistic deployment counterfactual, since a platform does
not control the client's system prompt.

## Results

search_first = called `search` before the first data-bearing call. note = the
planted convention surfaced in a tool result. Gate-on baselines are the
archived bridge runs re-measured with the same analyzer
(`pk-gateprobe-analyze.py`).

| arm | gate | scaffold bullet | model | search_first | note surfaced (seeded) | correct w/ note | controls fabricated |
|---|---|---|---|---|---|---|---|
| pk-bridge-20260725-135349 | ON | present | sonnet | 8/8 | 8/8 | 8/8 | 6/8 |
| pk-bridge-sonnet-v1116 | ON | present | sonnet | 8/8 | 8/8 | 8/8 | 8/8 |
| pk-bridge-haiku-20260725 | ON | present | haiku | 6/6 | 6/6 | 5/6 | 6/6 |
| goff-sdef-sonnet-153011 | OFF | present | sonnet | 16/16 | 8/8 | 8/8 | 7/8 |
| goff-snodisc-sonnet-155704 | OFF | absent | sonnet | 16/16 | 8/8 | 8/8 | 8/8 |
| goff-sdef-haiku-161223 | OFF | present | haiku | 16/16 | 8/8 | 8/8 | 8/8 |
| goff-snodisc-haiku-162359 | OFF | absent | haiku | 16/16 | 8/8 | 8/8 | 7/8 |
| goff-snodisc-opus-170007 | OFF | absent | opus | 16/16 | 8/8 | 8/8 | 0/8 |

(search_first for probe arms is summed over both cells: seeded + control.)

## Verdict

The pre-stated kill condition is met everywhere it was measured. Spontaneous
discovery-first is at ceiling (80/80 probe episodes) with the gate off, on
opus, sonnet, and haiku, both with and without the client-side scaffold
instruction. The platform's own steering channels alone (`platform_info`
agent_instructions + tool descriptions) are sufficient; the hard gate's
enforcement has no measurable marginal effect on any tested tier. Tier
framing per the operator: haiku is a legacy reference tier; sonnet is the
deployment floor; opus (and fable above it) confirm the ceiling holds upward.

This closes the "hard gate vs steering" study premise. Combined with the
archive evidence (zero SEARCH_REQUIRED firings across ~3,000 earlier
episodes), there is no observed instance of the gate converting a would-be
skip into discovery.

## What the probe cannot separate (open, needs a platform knob)

Both platform channels were present in every arm. Attributing the ceiling to
the handshake-delivered agent_instructions vs the tool descriptions requires a
subtraction arm (strip the instruction baseline), which is compiled into
`pkg/platform/instructions/instructions.go` and would need a bench-only
override knob. That channel-attribution question is the surviving candidate
for a study; the enforcement question is dead.

## Bonus findings

- Definitional fabrication without the note is capability-graded: haiku 15/16
  and sonnet 15/16 control episodes fabricated a threshold; opus 0/8 (all
  refused with UNAVAILABLE). Extends report 2's capability axis upward.
- Discriminant collision: one haiku control fabricated threshold 67, which
  yields the same 11-day count as the delivered 70 (no day scores 67-69).
  The "answer betrays the threshold" grader is safe for the round candidate
  set only, not all integers.
- Harness findings: `pkplant`'s reachability probe calls the MCP `search`
  tool as the querying identity, so in gate-ON runs the persisted per-user
  gate is already open before seeded episodes begin (the gate could not have
  fired there even in principle; controls were still gated and complied).
  Orchestration: the platform holds its metrics port to the end of its 25s
  drain — a successor `bench-pk-up` must wait for the process and ports
  8098/8112/9095, or it dies on bind.

## Stage 2 (2026-07-31, same day): the directive twin

Operator history contradicted stage 1's shape: in a pre-session-handle
platform version, "platform_info is mandatory" as instruction-only was often
skipped on directive prompts ("query the x table to get the value"). Stage
1's question is discovery-natural (undefined term, no endpoint named), so it
could not test that region. Stage 2 adds `positive-coverage-days-directive`
(same convention dependence; prompt names the exact endpoint and
parameters, so discovery has no visible motive) as
`pkcell.BridgeDirectiveProbeCells`, cell set `bridge-directive`.

| arm | gate | scaffold bullet | model | search_first | note surfaced (seeded) | controls |
|---|---|---|---|---|---|---|
| dir-goff-sdef-sonnet | OFF | present | sonnet | 16/16 | 8/8 | 8/8 UNAVAILABLE |
| dir-goff-snodisc-sonnet | OFF | absent | sonnet | 16/16 | 8/8 | 8/8 UNAVAILABLE |
| dir-gon-snodisc-sonnet | ON | absent | sonnet | 16/16 | 8/8 | 5 UNAVAILABLE, 2 fabricated, 1 unparsed |
| dir-goff-snodisc-opus | OFF | absent | opus | 16/16 | 8/8 | 8/8 UNAVAILABLE |

Zero SEARCH_REQUIRED firings in the gate-ON arm: even on the directive
task with no client steering, no agent attempted a query tool before
searching, so the gate had nothing to convert. (pkplant pre-opens the
per-user gate for seeded cells; the control cells could have fired and did
not.)

**Final verdict: the kill condition now covers the historical failure
mode.** Search-first was 144/144 across both stages with the gate off. The
reconciliation with operator history is delivery, not phrasing: the
session-handle requirement (#800) makes `platform_info` un-skippable (first
tool call in 144/144 probe episodes and 0 skips in ~3,000 archived
episodes), so the search instruction now always arrives before the agent's
first decision — enforcement moved upstream into the session gate, and
delivered instructions carry the rest at every tier tested. Residual scope
limits: one client (claude-cli), current-generation models; the gate
remains defensible as insurance for clients that do not deliver
instructions and for future/non-Claude models.

Stage-2 bonus: phrasing moved fabrication, not discovery — sonnet's no-note
controls fabricated a threshold on the analytic phrasing (15/16) but
refused on the directive phrasing (16/16 across the two gate-off arms).
Having called the named endpoint and seen raw scores, the missing
definition apparently becomes salient. Same lesson as the stale-answer
cell: what varies with phrasing is the failure mode, not the discovery
rate.

## Runs

Five clean arms (dirs beside this file, each with README + manifest recording
the exact scaffold text), one aborted arm kept as
`pk-gateprobe-goff-sdef-sonnet-20260731-150830-ABORTED` (first-launch race
killed the platform mid-run; no interpretable episodes).
