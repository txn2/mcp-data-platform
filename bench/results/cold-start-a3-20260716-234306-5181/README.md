# Cold-start run, capture-only (every sink write failed)

A full six-lesson run on the `a3` arm against an empty enrichment layer,
2026-07-17T06:43:06Z to 11:47:21Z, k=1, `claude-cli` with `sonnet`, platform
build `v1.102.1-3-g1fac7772`, settle window 5m, 25 S3 eval tasks per checkpoint,
five harness failures.

Five of six lessons captured; **none promoted**. Every promotion failed on the
same MCP transport error (`promote: apply transport: ... session not found`),
recorded per lesson in `results.json`. The accuracy curve still rose:

| Checkpoint | 0 | 1 | 2 | 3 | 4 | 5 | 6 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Accuracy | 52.0% | 40.0% | 52.0% | 60.0% | 72.0% | 96.0% | 96.0% |

## What this establishes

An unplanned but legitimate ablation, reported as such (Section 4.4): with
promotion disabled, captured insights still reach later evaluators through the
persona-scoped captured-memory channel, so the curve climbs with no DataHub
aspect or knowledge page ever written. That makes captured memory a delivery
channel in its own right, and it is the concrete reason a rising cold-start
curve is not by itself evidence that promotion to a durable sink occurred.

Its checkpoint-0 figure is also one of the five baseline replicates.

## What it does not establish

Nothing about promotion, which never succeeded here, and nothing about the
delivery of promoted knowledge. The headline learning curve is the k=3 run.
