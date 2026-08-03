# Knowledge-pollution run data

Run families for the knowledge-pollution study (issue #1163): when an agent
captures a plausible-but-wrong insight and it is promoted to the shared applied
tier, what governs whether other identities adopt it over a co-present correct
source?

| Family | What it established |
| --- | --- |
| [`probe/`](probe/) | The premise probe. A wrong fiscal convention, planted and promoted through the product's own path, was adopted in 2/24 episodes against a co-present correct source. |

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
