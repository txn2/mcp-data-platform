# S5 lifecycle, isolated replicate 1, k=3 by merge

The first isolated S5 replicate: three independent k=1 passes with a full clean
platform reset between them, merged into one k=3 scorecard. Run 2026-07-14 on
the Anthropic API adapter with `claude-sonnet-5`, platform build
`v1.102.0-10-g32d61254-dirty`, same fifteen protocols and protocol-set hash
`0920c552...` as the shared-store run beside it. 45 attempts, zero harness
failures.

`pass1.json`, `pass2.json`, and `pass3.json` are the raw per-pass results;
`lifecycle-a3.json` is the merged scorecard the report reads.

| Metric | Rate |
| --- | --- |
| capture | 38/45 (84.4%) |
| personal recall | 40/45 (88.9%) |
| unprompted surface | 38/38 (100%) |
| cross-identity transfer | 13/30 (43.3%) |
| update correctness | 8/8 (100%) |
| duplicate (lower is better) | 0/8 (0%) |
| abstention | 45/45 (100%) |
| pass^3 | 4/15 (26.7%) |

## What this establishes

That the platform's application code is unchanged from the shared-store run:
the two build strings differ only by benchmark-harness commits, verified by
diff, so a difference between the runs is not a platform difference.

## What it does not establish

That isolation lifts the lifecycle metrics. This replicate reads as a small
improvement over the shared-store run, and replicate 2 — identical in
configuration — does not reproduce it. Neither replicate should be read alone;
the pair is the evidence.
