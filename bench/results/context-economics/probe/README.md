# Platform-tax probe (archival re-analysis, 2026-08-01)

The premise probe for the context-economics study (epic #1164): what does the
semantic-platform machinery cost per turn on tasks that do not need it, and is
that cost attributable to something a deployment can act on?

No episodes were run for this probe and no run budget was spent on it. It is a
re-analysis of run archives that belong to the published knowledge-layer study,
performed under the cross-study re-analysis convention in
[`bench/README.md`](../../../README.md). `SUMMARY.md` and `decomposition.json`
are archived exactly as they were produced on 2026-08-01 and are not edited
again; where a later check contradicts the frozen text, the correction is
recorded here and the frozen file is left alone.

## Provenance

| | |
| --- | --- |
| Source study | Knowledge-layer effectiveness |
| Source page | [`docs/reference/benchmark-report.md`](../../../../docs/reference/benchmark-report.md) |
| DOI | [10.5281/zenodo.21438044](https://doi.org/10.5281/zenodo.21438044) (concept) |
| Consuming study | Context economics (`context-economics`), epic #1164 |
| Role of the re-analysis | Motivating evidence only, never a confirmatory result |

Archives consumed, read in place and unmodified:

| Path | What was read | n |
| --- | --- | --- |
| [`bench/results/phase2-anthropic-k3/full-a0/`](../../phase2-anthropic-k3/full-a0/) | `results.json` per-attempt token fields; `transcripts/` tool calls and result payloads | 261 attempts |
| [`bench/results/phase2-anthropic-k3/full-a1/`](../../phase2-anthropic-k3/full-a1/) | same | 261 attempts |
| [`bench/results/phase2-anthropic-k3/full-a2/`](../../phase2-anthropic-k3/full-a2/) | same | 261 attempts |
| [`bench/results/phase2-anthropic-k3/full-a3/`](../../phase2-anthropic-k3/full-a3/) | same | 261 attempts |

Not consumed: `phase2-anthropic-k3/_probe/` (a three-task a3 smoke run before
the matrix) and the top-level `comparison.md`, `comparison.txt`, and
`orchestrator.log` files. The four `full-*` directories above are the whole
input.

## Metric definitions

The recompute that lands with #1170 has to reproduce `decomposition.json`
exactly, so the two places where the field names do not speak for themselves are
recorded here rather than left to be rediscovered.

`median_cache_creation`, `median_cache_read`, `median_input`, `median_output`,
and `median_total_tokens` are medians over attempts of the per-attempt
`cache_creation_tokens`, `cache_read_tokens`, `input_tokens`, and
`output_tokens` fields in `results.json`. A missing key counts as zero, which
matters: 18 of 261 attempts in `full-a0`, 18 in `full-a1`, 2 in `full-a2`, and 1
in `full-a3` carry no `cache_creation_tokens` key at all. Coercing those to zero
is what reproduces the archived medians; excluding them instead moves a0 from
1,683 to 1,898 and its median total from 23,696 to 24,288.

`median_ctx_per_turn` is the median over attempts of `cache_read_tokens`
divided by that attempt's `tool_calls`. A turn here is a tool call, not an
assistant message. Computing the denominator from the transcripts instead
(assistant messages, one more than the tool-call count on a completed episode)
gives roughly 2,939 for a0 against the archived 3,359.67.

## Regenerability

`decomposition.json` is a frozen snapshot. The script that produced it ran
inline in the session that produced the file and was not committed, so nothing
in this directory regenerates today. `SUMMARY.md` calls the numbers
"regenerable from the archived results.json + transcripts" and names no
committed script; read that as a statement about the inputs, not as a claim
that the repository contains the procedure. It does not.

The rerunnable recompute lands with the protocol and decomposition toolchain
(#1170), whose acceptance condition is that it reproduces this file byte for
byte from the same four archives, under the metric definitions above. Until
then, treat the numbers here as a record of what was computed, not as a result a
reader can independently reproduce from this repository.

## What it establishes

That the cost question is measurable and worth measuring. The metrics are
continuous with large observed deltas across the four archived arms — median
total tokens 23.7k at a0 to 148.1k at a3, and cache read per tool call 3.4k to
15.8k, a 4.7x growth against a median tool-call count that only moves 7 to 9 —
so there is no ceiling failure mode waiting for the study.

It also separates two quantities that a cost study has to keep apart, because
they are billed differently. Cache creation is written when a session's prefix
is established; cache read is paid again on every turn. Both grow across the
arms (cache creation 1,683 to 5,906, a 3.5x span; cache read per tool call
3,359.7 to 15,830.6, a 4.7x span), so neither is negligible, but only the second
is multiplied by turn count, and the largest single component behind it is
search-result payload, which varies by federated group (a3 median search result
3,393 chars against a2 601). That is what makes a store-population arm worth
running before a tool-count arm, not a finding that the static surface is free.

## What it does not establish

Nothing causal, and nothing about current code.

The federation-breadth gap between a2 and a3 has at least two candidate
explanations and these archives separate neither. `SUMMARY.md` states that the
a2/a3 configuration difference is only the persona allow-list; that is wrong,
and the correction matters. Diffing the arm configs at the commit every one of
these manifests pins (`a373ff7f`), `bench/config/platform.bench.a3.yaml` differs
from `a2` in three places, not one: the persona allow-list gains `memory_*`,
`apply_knowledge`, and `manage_feedback`, and a3 additionally declares a
`memory.embedding` block (Ollama, `nomic-embed-text`) and a `knowledge` block
(`enabled: true`, `apply.enabled: true`, `datahub_connection: primary`) that a2
does not have at all. So the gap may reflect what was populated in the shared
platform between the two runs, or it may reflect those configuration blocks, or
both. Anything that separates them needs new runs that hold configuration
genuinely constant, and a run that varies only store population has to be built
from a single config file rather than from this arm pair.

Every episode in all four archives ran with `fetch` denied by the arm personas,
the instrument defect recorded in issue #1176. These transcripts show it
directly: 3 `fetch` calls in `full-a3`, all refused with `not authorized: tool
not allowed for persona: admin`, and none in the other three arms, against
993-1,061 `search` calls per arm. With no way to dereference a search
reference, every body an agent read had to arrive inside a search result, which
is the payload this probe measures. The per-turn growth reported here is
therefore an upper bound on a search-only delivery path, not a measurement of
the delivery architecture the platform ships.

These archives were produced on 2026-07-14 against platform build
`v1.102.0-9-gadfb9d90-dirty`. Every enrichment, search, and federation path they
exercised has changed since, `fetch` denial among them. No number here may be
cited as a property of the platform as it stands; each one is a reason to go and
measure.

The accuracy figures quoted in `SUMMARY.md` are published results of the
knowledge-layer study, reproduced for context. This study treats them as
manipulation checks and does not re-litigate them.

## The open decision in `SUMMARY.md`

`SUMMARY.md` closes with an open question to the user: whether a study built on
re-analysis of another study's archives should instead run its own arms, or
whether the series should adopt a stated convention. That question is resolved.
The series adopted the convention, it is recorded in
[`bench/README.md`](../../../README.md) under "Re-analyzing another study's
published archives", and this directory is its first application. The frozen
file is left with its question intact rather than edited to point here.
