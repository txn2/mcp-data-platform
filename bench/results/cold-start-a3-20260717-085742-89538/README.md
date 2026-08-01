# Cold-start run, k=1 (the K=1 curve)

A full six-lesson run on the `a3` arm against an empty enrichment layer,
2026-07-17T15:57:43Z to 20:13:42Z, k=1, `claude-cli` with `sonnet`, platform
build `v1.102.1-3-g1fac7772`, settle window 5m, 25 S3 eval tasks per checkpoint,
one harness failure. Five of six lessons captured and all five promoted to their
sinks (three knowledge pages, two DataHub descriptions).

| Checkpoint | 0 | 1 | 2 | 3 | 4 | 5 | 6 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Accuracy | 44.0% | 48.0% | 62.5% | 76.0% | 68.0% | 100% | 100% |

## What this establishes

The K=1 curve the report overlays on the headline (Section 4.1): from a 44.0%
empty-layer baseline to 100% after five promotions, with each trap class moving
at or shortly after its own promotion checkpoint. Every evaluator is a fresh,
never-taught identity, so the climb is delivery of promoted knowledge rather
than an evaluator's own memory.

## What it does not establish

Confidence intervals: k=1 means one attempt per eval task per checkpoint, so
per-checkpoint accuracy carries no repeat-based uncertainty. The k=3 run is the
headline for that reason. The sixth lesson never captured, so the curve covers
five promotions, not six.
