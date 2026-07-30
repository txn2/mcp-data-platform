# Cold-start run, interrupted at the baseline checkpoint

Started 2026-07-16T18:55:50Z on the `a3` arm against an empty enrichment layer,
k=1, `claude-cli` driver, 25 S3 eval tasks per checkpoint, settle window 5m.
Interrupted after checkpoint 0: the archive holds one lesson (captured, not
promoted), one checkpoint, and three harness failures. The manifest predates the
run-metadata fields, so its `model` and `platform_version` are empty; the driver
is recorded as `claude-cli`.

Baseline accuracy at checkpoint 0: **47.8%**, enrichment coverage 36.2%.

## What this establishes

One of the five independent empty-layer baseline replicates the report uses
(Section 4.3): the floor an `a3` agent reaches with an undocumented DataHub and
no knowledge pages is reproducible, not a single measurement.

## What it does not establish

Anything about the learning curve. No promotion completed and there is no
post-baseline checkpoint. Only the checkpoint-0 figure is usable.
