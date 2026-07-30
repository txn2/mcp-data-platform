# S5 lifecycle, isolated replicate 2, k=3 by merge

The second isolated S5 replicate, identical in configuration to replicate 1:
three independent k=1 passes with a full clean platform reset between them,
merged to k=3. Run 2026-07-14 on the Anthropic API adapter with
`claude-sonnet-5`, platform build `v1.102.0-10-g32d61254-dirty`, protocol-set
hash `0920c552...`. 45 attempts, zero harness failures.

`pass1/`, `pass2/`, and `pass3/` hold the raw per-pass results and transcripts;
`lifecycle-a3.json` is the merged scorecard the report reads, and it is the
source of the report's headline lifecycle figures.

| Metric | Rate |
| --- | --- |
| capture | 37/45 (82.2%) |
| personal recall | 38/45 (84.4%) |
| unprompted surface | 37/37 (100%) |
| cross-identity transfer | 14/30 (46.7%) |
| update correctness | 7/7 (100%) |
| duplicate (lower is better) | 3/7 (42.9%) |
| abstention | 43/45 (95.6%) |
| pass^3 | 3/15 (20.0%) |

## What this establishes

Read against replicate 1, that most metric-by-metric differences at this scale
are sampling noise, not signal. Two runs with identical configuration disagree
on duplicate rate by the full width of its range (0% against 42.9%, on
denominators of 8 and 7) and on pass^3 by nearly seven points. What survives
across all three S5 runs is what the report treats as firm: unprompted surface
at 100%, update correctness at 100%, abstention 96 to 100%, and cross-identity
transfer stuck in the low-to-mid forties.

## What it does not establish

Anything about supersede reliability from its duplicate rate alone: the
denominator is seven. The isolated supersede sub-benchmark, and later the
knowledge-use lifecycle probe, are what address that with a real denominator.
