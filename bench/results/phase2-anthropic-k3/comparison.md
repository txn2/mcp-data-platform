# Agent-effectiveness benchmark: arm comparison

## Manifest

- Model: `claude-sonnet-5` (anthropic)
- Repeats (k): 3
- Dataset seed: 930
- Task-set hash: `7e727f891ee5`
- Platform: v1.102.0-9-gadfb9d90-dirty @ commit `a373ff7f98e3`
- Arms: a0, a1, a2, a3

## Arms

- **a0** — baseline — raw toolkit tools only, no enrichment, no search
- **a1** — enrichment — A0 plus semantic cross-enrichment
- **a2** — knowledge — A1 plus search, the search-first gate, and knowledge pages
- **a3** — lifecycle — A2 plus memory/insight capture and apply_knowledge

## Overall accuracy

Accuracy is over graded attempts; the bracket is the 95% bootstrap CI.

| arm | graded | accuracy (95% CI) | pass^k | median calls | median wall (s) | harness fails |
| --- | ---: | --- | ---: | ---: | ---: | ---: |
| a0 | 261 | 83.1% [79–88] | 80% | 7 | 14.0 | 0 |
| a1 | 261 | 87.4% [83–91] | 86% | 7 | 13.9 | 0 |
| a2 | 261 | 98.5% [97–100] | 98% | 9 | 27.4 | 0 |
| a3 | 261 | 98.1% [96–100] | 97% | 9 | 27.0 | 0 |

## Accuracy by suite

| suite | a0 | a1 | a2 | a3 |
| --- | --- | --- | --- | --- |
| s1 | 98.0% [94–100] | 98.0% [94–100] | 100.0% [100–100] | 98.0% [94–100] |
| s2 | 100.0% [100–100] | 100.0% [100–100] | 97.8% [95–100] | 97.8% [95–100] |
| s3 | 42.7% [32–55] | 57.3% [45–68] | 98.7% [96–100] | 98.7% [96–100] |

## Median tool calls by suite

Fewer is better: efficiency is a first-class benchmark axis (BIRD's VES).

| suite | a0 | a1 | a2 | a3 |
| --- | --- | --- | --- | --- |
| s1 | 6 | 5 | 8 | 7 |
| s2 | 6 | 6 | 9 | 9 |
| s3 | 16 | 11 | 10 | 11 |

## S3 knowledge-trap accuracy by class

Each trap is answerable plausibly-but-wrongly without the knowledge layer.

| trap class | a0 | a1 | a2 | a3 |
| --- | --- | --- | --- | --- |
| deprecated_table | 95.2% [86–100] | 95.2% [86–100] | 100.0% [100–100] | 100.0% [100–100] |
| fiscal_calendar | 0.0% [0–0] | 0.0% [0–0] | 100.0% [100–100] | 100.0% [100–100] |
| freshness_cutoff | 91.7% [75–100] | 100.0% [100–100] | 100.0% [100–100] | 100.0% [100–100] |
| net_revenue | 21.2% [9–36] | 48.5% [30–67] | 100.0% [100–100] | 100.0% [100–100] |
| tier_boundary | 0.0% [0–0] | 0.0% [0–0] | 93.3% [80–100] | 100.0% [100–100] |
| units_cents | 61.9% [48–76] | 88.1% [79–98] | 97.6% [93–100] | 97.6% [93–100] |

## Accuracy delta vs a0 (the platform's effect)

Points are (arm − baseline) accuracy; the bracket is the 95% bootstrap CI on the difference.

| suite | arm | delta (points) | 95% CI |
| --- | --- | ---: | --- |
| s1 | a1 | +0.0 | -6 to +6 |
| s1 | a2 | +2.0 | +0 to +6 |
| s1 | a3 | +0.0 | -6 to +6 |
| s2 | a1 | +0.0 | +0 to +0 |
| s2 | a2 | -2.2 | -5 to +0 |
| s2 | a3 | -2.2 | -5 to +0 |
| s3 | a1 | +14.7 | -1 to +31 |
| s3 | a2 | +56.0 | +44 to +67 |
| s3 | a3 | +56.0 | +44 to +68 |

## Caveats

- Results are model-dependent; the headline is arm-vs-arm on a single pinned model, never model-vs-model.
- The seed dataset is small by design (fixed seed, airgapped); absolute accuracies are not real-world estimates.
- Judgment-call rubric items (required caveats) are scored separately by the pinned LLM judge; see the judge calibration report for its human-agreement rate.
- CIs are percentile bootstrap over graded attempts with a fixed resampling seed, so they are reproducible but do not model task-selection variance.
