# S5 lifecycle at k=5 (thirty-protocol set)

The statistical-scale re-run of the lifecycle suite (#1139). Five independent k=1 passes, merged into one k=5 scorecard. Supersedes `s5-anthropic-k3` as the lifecycle evidence of record; that family stays as the data the published report v1.1 recomputes from.

Ran 2026-08-01 against platform `v1.118.0-4-g445e3abc` (commit `445e3abc`), arm a3, `claude-sonnet-5` through the raw `anthropic` driver, seed 930, protocol-set hash `daeac8a49319`.

## Headline

| Metric | k=5 | Report v1.1 (k=3, fifteen protocols) |
| --- | --- | --- |
| Transfer rate | **98.9%** CI [96.8-100.0] (94/95) | 46.7% |
| Duplicate rate | **22.0%** CI [9.8-34.1] (9/41) | range only; CI [14, 86] on n=7 |
| Capture rate | 91.9% CI [87.2-96.0] (137/149) | |
| Personal recall | 95.3% CI [91.9-98.0] (142/149) | 84.4% |
| Update correctness | 100% (41/41) | |
| Abstention | 92.6% CI [87.9-96.6] (138/149) | |

**The transfer comparison is across-code, not across-scale.** #1129 made applied insights searchable and fetchable across identities, which is the exact path the published 46.7% measured. Reading the 52-point difference as a sample-size effect is wrong. #1141 likewise changed delivery for DataHub-sink facts. This run measures two product changes, not three: #1132 was retired without implementation.

## Why five passes rather than one k=5 run

`benchrun -lifecycle -merge` folds independent k=1 passes into a k=N scorecard. Each pass here got a full platform reset to clean seeded state (`make bench-down` does `down -v`), so a protocol's five attempts are genuinely independent. The within-run k-repeats of a single `benchrun` are not: they share one accumulating knowledge store. The merge refuses passes that disagree on arm, protocol set, model, or seed.

## Reproducing

```bash
./build/benchrun -lifecycle -merge "$(paste -sd, bench/results/s5-anthropic-k5/passes.txt)" -out /tmp/k5.json
```

Offline, no API key: the merge reads only the committed per-pass JSON. It reproduces `lifecycle-a3-k5.json` exactly. Re-running the passes themselves is a paid operation (~$20 total at Sonnet 5 introductory pricing) and needs the a3 stack plus an embedding provider. `orchestrator.sh` and `orchestrator.log` record how the passes were driven.

## What this establishes

- The supersede and transfer figures are point estimates with confidence intervals, which is what #1139 asked for. The duplicate-rate interval narrowed from 72 points wide to 24.
- Update correctness is at ceiling (41/41) across a denominator six times the published one.

## What it does not establish

- **Nothing about capture reliability from the 91.9% figure.** All twelve misses are `attempted_failed`, and they concentrate on `lc-anchor-region` (5/5 passes) and `lc-flagship-region` (4/5). The transcripts show `memory_capture` returning success: the model links the fact to `memory.bench.daily_region_revenue` instead of the protocol's canonical entity, so the harness's linked-insight check does not find it. This is the mis-filing already recorded in the findings register from the S5 supersede probe, reproducing deterministically as that entry predicted. It is a filing defect, not a capture defect.
- **A clean thirty-protocol denominator.** One protocol failed at the harness level and is excluded, so this is 149 attempts, not 150. See below.
- **Anything outside one model and one client.** Every episode ran `claude-sonnet-5` through the raw `anthropic` driver.

## The harness failure, and the validity threat behind it

Pass 5, `lc-holiday-total`: `approve insight ... status 409: invalid status transition from "superseded" to "approved"`.

`lc-holiday-total` has no `update` stage, so nothing in its own lifecycle can supersede its insight — yet the insight was in terminal `superseded` state when the harness tried to approve it. The plausible mechanism is cross-protocol: recall-first supersede matches restatements by vector similarity, all thirty protocols share one database within a pass, and the fixture contains families of near-identical facts. The four "total" protocols state the same sentence with a different period; six protocols are "-region" facts.

Consistent fingerprints across all five passes support this reading: `lc-primary-region` captures successfully and then fails recall in 5/5 passes, and `lc-closeout-total` fails recall in 2/5.

**The causal link is unproven.** Which protocol superseded which cannot be recovered from these archives — the between-pass reset wipes the database, and no run recorded per-insight supersede provenance. Establishing it needs an instrumented run. Until then this is a stated threat to validity, not a finding.

It bears on #1139 in particular because #1139 is the change that took the protocol set from fifteen to thirty. Denser semantic neighbourhoods make collision likelier, so this may be an artifact the published fifteen-protocol run never exposed.

## On the surfaced-versus-correct gap

Transfer correctness is 98.9% while the fact "surfaced" in only 68.4% of those attempts. That gap is **measurement conservatism, not unaided derivation.** `surfacedTarget` (`bench/internal/lifecycle/instrument.go:87`) requires the stored fact or page summary to appear as a normalized substring of a tool result, and its own comment states the signal "must stay a conservative 'the fact text is present', not a fuzzy paraphrase match that could over-report delivery."

All thirty correct-but-not-surfaced transfer episodes were checked against their transcripts: in 30 of 30 the model called `search` and a tool result contained the protocol's key term. There are no cases of the model answering correctly without the knowledge reaching it. The transfer figure is therefore not inflated by derivable facts.
