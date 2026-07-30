# Phase-1 pilot (#942)

The first end-to-end run of the harness against a live stack: a 10-task slice,
two arms, k=3, 30 attempts each, run 2026-07-13 on the Anthropic API adapter
with `claude-sonnet-5`, platform build `v1.102.0-5-g1984dd2e`. Its task-set hash
(`f36a2420...`) is an earlier set than the phase-2 suite's, so these numbers are
not comparable to `phase2-anthropic-k3/` and were never merged with it.

| File | Arm | S1 | S3 |
| --- | --- | --- | --- |
| `results-a0.json` | raw tools | 1.000 | 0.600 |
| `results-a2.json` | knowledge and search | 1.000 | 1.000 |

## What this establishes

That the pipeline works end to end against a real platform: seed data, ground
truths, handle threading, audit read-back, and both graders. It sized the
phase-2 run and showed the S3 trap direction was worth measuring at scale.

## What it does not establish

Any published result. Ten tasks and two arms, superseded in full by
`phase2-anthropic-k3/`. No figure or statistic in either report comes from here.
