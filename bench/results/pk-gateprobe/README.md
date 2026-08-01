# Search-first gate probe (study-3 premise probe, #1145)

Exploratory. The premise probe for a proposed enforcement study: does the search-first hard gate (`workflow.require_search`, `SEARCH_REQUIRED`) cause discovery that would not otherwise happen, or does the platform's own steering already produce it? Decision rule, stated before any run: gate-off spontaneous search at or above 7/8 under the default scaffold means the enforcement study is dead at ceiling.

It is dead. Recomputed from the transcripts in this directory by `pk-gateprobe-analyze.py`, the agent called `search` before its first data-bearing call in **128/128 episodes across the eight clean gate-off arms**, on opus, sonnet, and haiku, with and without the client-side scaffold instruction, and on both an analytic and a directive phrasing of the question. The gate-on control arm was **16/16** on the same measure, with zero `SEARCH_REQUIRED` conversions, so the gate had nothing to convert.

The instrument (gate-off config, `-scaffold` flag, directive twin cells) landed in commit `2cb2cf59`. The register row is in [`../../docs/findings-register.md`](../../docs/findings-register.md) under retired study candidates.

## Counting, precisely

`pk-gateprobe-SUMMARY.md` is the run's own record and is archived verbatim, including one imprecision worth knowing before citing it. Its final verdict reads "Search-first was 144/144 across both stages with the gate off". 144/144 is the correct combined figure, but 16 of those episodes are the gate-on control arm. The gate-off figure is 128/128. Both numbers are right and the conclusion is unchanged; only that sentence conflates them. Cite 128/128 for the gate-off claim, or 144/144 for the combined one, and not the two together.

| | episodes | search-first |
| --- | ---: | ---: |
| Gate off, eight clean arms | 128 | 128 |
| Gate on, control arm | 16 | 16 |
| Combined | 144 | 144 |

## What it establishes

- The hard gate's enforcement has no measurable marginal effect on discovery-first behavior at any tier tested. The session-handle requirement (#800) makes `platform_info` un-skippable, so agent instructions arrive before the agent's first decision, and delivered instructions hold the ceiling on their own.
- Discovery-first survives removing the harness's own "use the search tool" bullet, which is the realistic deployment counterfactual: a platform does not control the client's system prompt.
- It survives a directive phrasing that names the exact endpoint and parameters, which is the region operator history said had failed on a pre-session-handle build. That is what makes the kill condition cover the historical failure mode rather than only the discovery-natural case.

## What it does not establish

- **Which channel does the work.** Both platform channels, handshake-delivered agent instructions and tool descriptions, were present in every arm. Separating them needs a subtraction arm that strips the instruction baseline compiled into `pkg/platform/instructions/instructions.go`, which requires a bench-only override knob that does not exist. Channel attribution is the surviving study candidate; enforcement is not.
- **Anything outside one client and one model generation.** Every episode ran through claude-cli on current Claude models. The gate remains defensible as insurance for clients that do not deliver instructions and for future or non-Claude models.
- **A fabrication rate.** The control-cell fabrication counts here are a by-product of the probe, on eight episodes per cell, not a measurement it was designed for.

## Runs

Eight clean gate-off arms, one gate-on control arm, and one aborted arm kept rather than deleted. Each run directory carries its own README, `results.json`, full `transcripts/`, and a manifest recording the exact scaffold text used.

| Directory | Gate | Scaffold bullet | Model | Phrasing |
| --- | --- | --- | --- | --- |
| `pk-gateprobe-goff-sdef-sonnet-20260731-153011` | off | present | sonnet | analytic |
| `pk-gateprobe-goff-snodisc-sonnet-20260731-155704` | off | absent | sonnet | analytic |
| `pk-gateprobe-goff-sdef-haiku-20260731-161223` | off | present | haiku | analytic |
| `pk-gateprobe-goff-snodisc-haiku-20260731-162359` | off | absent | haiku | analytic |
| `pk-gateprobe-goff-snodisc-opus-20260731-170007` | off | absent | opus | analytic |
| `pk-gateprobe-dir-goff-sdef-sonnet-20260731-184148` | off | present | sonnet | directive |
| `pk-gateprobe-dir-goff-snodisc-sonnet-20260731-185055` | off | absent | sonnet | directive |
| `pk-gateprobe-dir-goff-snodisc-opus-20260731-191003` | off | absent | opus | directive |
| `pk-gateprobe-dir-gon-snodisc-sonnet-20260731-190009` | **on** | absent | sonnet | directive |
| `pk-gateprobe-goff-sdef-sonnet-20260731-150830-ABORTED` | off | present | sonnet | analytic |

The aborted arm died to a first-launch race that killed the platform mid-run and has no interpretable episodes. It is kept because a paid run is evidence of what was attempted, and it is excluded from every count above.

## Reproducing

```bash
cd bench/results/pk-gateprobe
python3 pk-gateprobe-analyze.py pk-gateprobe-goff-*-2026* pk-gateprobe-dir-*-2026*
```

Offline, no API key, no network: the analyzer reads only the transcripts committed here. It reports, per cell, whether the agent searched at all, whether the first search preceded the first data-bearing call, whether the planted note surfaced in a tool result, the final answer, and a Wilson 95% interval on the search-first rate.

`pk-gateprobe-orchestrator.sh` and its logs record how the arms were launched. Re-running the arms themselves is a paid operation and needs the pk stack plus `bench/config/platform.bench.pk-gateoff.yaml`.

## Environment

pk fixture, `BridgeProbeCells` and `BridgeDirectiveProbeCells`, k=8 per cell, claude-cli. Gate off through `bench/config/platform.bench.pk-gateoff.yaml`, a single-deviation copy of the pk arm. Gate-on baselines in the summary's stage-1 table are the archived `pk-bridge` runs re-measured with this same analyzer, not new runs; they live under [`../knowledge-use/pk-bridge/`](../knowledge-use/pk-bridge/).

One harness caveat recorded during the probe: `pkplant`'s reachability check calls the MCP `search` tool as the querying identity, so in gate-on runs the persisted per-user gate is already open before seeded episodes begin. The gate could not have fired there even in principle. Control cells were still gated and complied.

## Provenance

These runs were executed 2026-07-31 from the #1027 worktree and archived under `build/bench-results/` (gitignored). They were moved here on 2026-08-01, when the gap between the commit message's claim and the repository's archival policy was noticed. Between those dates the result had no committed evidence behind it.
