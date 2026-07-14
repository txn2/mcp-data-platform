# Agent-effectiveness benchmark: arm comparison

## Manifest

- Model: `sonnet` (claude-cli)
- Repeats (k): 1
- Dataset seed: 930
- Task-set hash: `7e727f891ee5`
- Platform: v1.102.0-9-gadfb9d90-dirty @ commit `62f6fe68db93`
- Arms: a1, a3

## Arms

- **a1** — enrichment — A0 plus semantic cross-enrichment
- **a3** — lifecycle — A2 plus memory/insight capture and apply_knowledge

## Overall accuracy

Accuracy is over graded attempts; the bracket is the 95% bootstrap CI.

| arm | graded | accuracy (95% CI) | pass^k | median calls | median wall (s) | harness fails |
| --- | ---: | --- | ---: | ---: | ---: | ---: |
| a1 | 87 | 87.4% [80–94] | 87% | 8 | 42.7 | 0 |
| a3 | 87 | 100.0% [100–100] | 100% | 8 | 40.3 | 0 |

## Accuracy by suite

| suite | a1 | a3 |
| --- | --- | --- |
| s1 | 100.0% [100–100] | 100.0% [100–100] |
| s2 | 100.0% [100–100] | 100.0% [100–100] |
| s3 | 56.0% [36–76] | 100.0% [100–100] |

## Median tool calls by suite

Fewer is better: efficiency is a first-class benchmark axis (BIRD's VES).

| suite | a1 | a3 |
| --- | --- | --- |
| s1 | 7 | 6 |
| s2 | 7 | 7 |
| s3 | 12 | 10 |

## S3 knowledge-trap accuracy by class

Each trap is answerable plausibly-but-wrongly without the knowledge layer.

| trap class | a1 | a3 |
| --- | --- | --- |
| deprecated_table | 100.0% [100–100] | 100.0% [100–100] |
| fiscal_calendar | 0.0% [0–0] | 100.0% [100–100] |
| freshness_cutoff | 100.0% [100–100] | 100.0% [100–100] |
| net_revenue | 45.5% [18–73] | 100.0% [100–100] |
| tier_boundary | 0.0% [0–0] | 100.0% [100–100] |
| units_cents | 85.7% [64–100] | 100.0% [100–100] |

## Accuracy delta vs a1 (the platform's effect)

Points are (arm − baseline) accuracy; the bracket is the 95% bootstrap CI on the difference.

| suite | arm | delta (points) | 95% CI |
| --- | --- | ---: | --- |
| s1 | a3 | +0.0 | +0 to +0 |
| s2 | a3 | +0.0 | +0 to +0 |
| s3 | a3 | +44.0 | +24 to +64 |

## Caveats

- Results are model-dependent; the headline is arm-vs-arm on a single pinned model, never model-vs-model.
- The seed dataset is small by design (fixed seed, airgapped); absolute accuracies are not real-world estimates.
- Judgment-call rubric items (required caveats) are scored separately by the pinned LLM judge; see the judge calibration report for its human-agreement rate.
- CIs are percentile bootstrap over graded attempts with a fixed resampling seed, so they are reproducible but do not model task-selection variance.
