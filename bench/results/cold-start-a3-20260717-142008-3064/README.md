# Cold-start run, k=3 (the headline learning curve)

The headline cold-start run behind
[`docs/reference/benchmark-report.md`](../../../docs/reference/benchmark-report.md)
Section 4. Six lessons on the `a3` arm against an empty enrichment layer,
2026-07-17T21:20:08Z to 2026-07-18T10:50:34Z, k=3, `claude-cli` with `sonnet`,
platform build `v1.102.1-5-g96169337`, settle window 5m, 25 S3 eval tasks per
checkpoint. **Zero harness failures**; two audit read-backs were lost and are
recorded as `audit_read_failures`, which understates enrichment coverage
slightly rather than corrupting accuracy.

Five of six lessons captured and all five promoted.

| Checkpoint | 0 | 1 | 2 | 3 | 4 | 5 | 6 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Accuracy | 48.0% | 41.3% | 50.7% | 70.7% | 70.7% | 96.0% | 90.7% |

## What this establishes

The report's headline longitudinal result: an empty-layer platform taught one
fact at a time climbs from a 48.0% baseline to 90.7%, with the per-trap-class
trajectories showing each taught fact moving at or shortly after its own
promotion checkpoint. That mechanism, not the endpoint, is the result. Three
repeats per checkpoint give the reported confidence band.

## What it does not establish

Comparability with the ablation families: this run used the `claude-cli` driver
and those used the Anthropic API adapter, and the report never places the two on
one axis. The dip at checkpoint 1 and the drop from checkpoint 5 to 6 are within
the reported band and are not read as effects.
