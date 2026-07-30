# S5 lifecycle, shared knowledge store, k=3

The first of the three S5 lifecycle runs behind
[`docs/reference/benchmark-report.md`](../../../docs/reference/benchmark-report.md)
Section 5. Fifteen multi-episode protocols on the `a3` arm, k=3, 45 attempts,
zero harness failures, run 2026-07-14 on the Anthropic API adapter with
`claude-sonnet-5`, platform build `v1.102.0-9-gadfb9d90-dirty`, protocol-set
hash `0920c552...`.

One accumulating knowledge store across all three repeats: the platform is not
reset between them, so the k-repeats of a protocol are coupled. That coupling is
the reason the two isolated replicates beside this directory exist.

| Metric | Rate |
| --- | --- |
| capture | 36/45 (80.0%) |
| personal recall | 38/45 (84.4%) |
| unprompted surface | 36/36 (100%) |
| cross-identity transfer | 11/26 (42.3%) |
| update correctness | 10/10 (100%) |
| duplicate (lower is better) | 1/10 (10.0%) |
| abstention | 45/45 (100%) |
| pass^3 | 3/15 (20.0%) |

## What this establishes

Read together with the two isolated replicates: unprompted surface, update
correctness, and abstention are at or near ceiling, and cross-identity transfer
sits at a reproducible ceiling in the low forties. Every state transition was
verified through the admin insights and changesets APIs, not inferred from
transcripts.

## What it does not establish

Nothing on its own about the shared store as a confound. This run's numbers
cannot be separated from the accumulation across repeats; that question is what
the isolated replicates answer, and they show most metric-by-metric differences
at this scale are run-to-run noise rather than the confound.
