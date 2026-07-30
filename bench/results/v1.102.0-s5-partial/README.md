# Phase-3 S5 pilot, k=1, partial (#944)

The first S5 lifecycle run: fifteen protocols at k=1 on the `a3` arm, run
2026-07-13 on the Anthropic API adapter with `claude-sonnet-5`, platform build
`v1.102.0-9-gadfb9d90-dirty`, protocol-set hash `0920c552...`.

**Partial by outcome, and kept as such.** Two of the fifteen attempts failed at
the harness level and are excluded from every rate, which is why the
denominators below are 13 rather than 15.

| Metric | Rate |
| --- | --- |
| capture | 11/13 (84.6%) |
| personal recall | 8/13 (61.5%) |
| unprompted surface | 11/11 (100%) |
| cross-identity transfer | 4/6 (66.7%) |
| update correctness | 5/5 (100%) |
| duplicate (lower is better) | 2/5 (40.0%) |
| abstention | 13/13 (100%) |
| pass^1 | 5/15 (33.3%) |

## What this establishes

That the lifecycle protocols, the reviewer-promotion path, and the admin
insights and changesets read-back work against a live platform. It is the run
that sized the k=3 lifecycle work.

## What it does not establish

Any published lifecycle rate. k=1 on denominators as small as five, with two
harness failures; the transfer figure here (66.7% on six applicable attempts) is
well above every later run and is a small-sample artifact, not a result. The
published scorecard comes from the three k=3 runs.
