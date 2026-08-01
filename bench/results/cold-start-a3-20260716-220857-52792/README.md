# Cold-start run, interrupted at the baseline checkpoint

Started 2026-07-17T05:08:57Z on the `a3` arm against an empty enrichment layer,
k=1, `claude-cli` driver, 25 S3 eval tasks per checkpoint, settle window 5m.
Interrupted after checkpoint 0: one lesson (captured, not promoted), one
checkpoint, one harness failure. As with the other early run, the manifest
predates the model and build fields and leaves them empty.

Baseline accuracy at checkpoint 0: **44.0%**.

## What this establishes

One of the five empty-layer baseline replicates behind the report's
reproducible-floor claim (Section 4.3).

## What it does not establish

Anything about the learning curve: no promotion completed and no post-baseline
checkpoint exists.
