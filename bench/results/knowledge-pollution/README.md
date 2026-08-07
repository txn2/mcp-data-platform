# Knowledge-pollution run data

Run families for the knowledge-pollution study (issue #1163): when an agent
captures a plausible-but-wrong insight and it is promoted to the shared applied
tier, what governs whether other identities adopt it over a co-present correct
source?

| Family | What it established |
| --- | --- |
| [`probe/`](probe/) | The premise probe. A wrong fiscal convention, planted and promoted through the product's own path, was adopted in 2/24 episodes against a co-present correct source. |
| [`rq1-warehouse/`](rq1-warehouse/) | The pre-registered confirmatory matrix: 18 arms, 432 episodes. The only claim adopted anywhere is the checkable one, on the weak tier only (16/24 haiku vs 0/24 sonnet and opus), by displacing the refuting query; conventions were adopted nowhere on the agent client. |
| [`directive-contrast/`](directive-contrast/) | The same false count at three directive strengths, 72 episodes: a bare statement adopts like an imperative (18-17-18 of 24), so the effect is adoption of a claim, not compliance with an instruction. |
| [`generalization/`](generalization/) | The sink control (page sink 24/24, at least as contagious as the entity description) and the cross-fixture arms (API monitor count: 24/24 wrong vs 0/24 controls at haiku, 0/24 at sonnet). Not sink-bound, not warehouse-bound. |
| [`metered-replication/`](metered-replication/) | Raw Messages API, no agent client, k=8: the checkable headline replicates where analyzed (8/8 haiku, 0/8 sonnet; the opus cell was invalidated twice by store drift and stays covered at 0/24 by the claude-cli matrix); the convention null does not replicate (haiku 4/8), so convention immunity is a client-scaffold property. |

The published report is
[`docs/reference/benchmark-report-knowledge-pollution.md`](../../../docs/reference/benchmark-report-knowledge-pollution.md);
every number in it recomputes from these directories via
`bench/reports/knowledge-pollution/pollution_tables.py`.

## The probe

`probe/` is archived as it ran, including its pre-stated design and decision
rules (`README.md`, written before the polluted arm ran) and its result
(`SUMMARY.md`). Both files are kept unchanged.

Two figures in the probe's attribution table were computed by hand and are
superseded by the harness, which computes every attribution value from the
seeded fixture (`bench/internal/pollutionplant`, `-mode table`): fiscal-year net
revenue under the planted April boundary is 989,550.68 (the probe stated
989,550.70) and fiscal-Q1 net is 323,455.09 (the probe stated 323,455.10). The
probe's adopted episodes were both on the count task, whose values (correct 873,
adopted 724, calendar 948) the harness reproduces exactly, so the probe's
verdict is unaffected. Later families grade against the computed table, never a
transcribed one.
