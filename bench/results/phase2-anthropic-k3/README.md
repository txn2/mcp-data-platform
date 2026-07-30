# Four-arm S1-S3 ablation, k=3 (the knowledge-layer headline)

The single-shot ablation behind
[`docs/reference/benchmark-report.md`](../../../docs/reference/benchmark-report.md)
Section 3. Four arms over the same 87-task phase-2 suite, three repeats each,
261 graded attempts per arm, run 2026-07-14 on the Anthropic API adapter with
`claude-sonnet-5`, platform build `v1.102.0-9-gadfb9d90-dirty`, task-set hash
`7e727f89...`. Every manifest pins its own commit, seed, and hash.

| Directory | Arm | S1 | S2 | S3 |
| --- | --- | --- | --- | --- |
| `full-a0/` | raw tools | 0.980 | 1.000 | 0.427 |
| `full-a1/` | enrichment | 0.980 | 1.000 | 0.573 |
| `full-a2/` | knowledge and search | 1.000 | 0.978 | 0.987 |
| `full-a3/` | lifecycle | 0.980 | 0.978 | 0.987 |

`_probe/` is a three-task a3 smoke (`s3-deprecated-completed-usd`,
`s3-deprecated-order-count`, `s3-fiscal-2025-count`) run before the full matrix
to confirm the stack was serving enrichment. `comparison.md` and
`comparison.txt` are the generated cross-arm tables; `orchestrator.log` and the
per-arm `run.log` files are the drivers' own logs.

## What this establishes

The knowledge layer's effect is specific to knowledge-gated questions. S3 trap
accuracy rises 42.7 to 98.7 percent between raw tools and the full platform,
while S1 discovery and S2 analytical accuracy are near ceiling for every arm, so
the gain is not a general accuracy uplift. Confidence intervals, the trap-class
breakdown, and the tool-call efficiency numbers are computed from these files by
the report notebook.

## What it does not establish

Nothing about the lifecycle. a3 ties a2 here because these are single-session
tasks that exercise only the surfacing half; the capture-and-propagation half is
measured by the S5 families and the cold-start runs, not by this one. Nothing
about other models: one pinned model, arm versus arm. Nothing about absolute
accuracy on real warehouses; the dataset is generated and its traps are planted.
