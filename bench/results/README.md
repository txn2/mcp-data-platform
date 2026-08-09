# Archived run data

Every benchmark run that produced output is kept here, verbatim. Runs are never
overwritten, relabeled, or deleted: a run whose result was uninteresting, whose
harness had a defect, or which was interrupted is still real data and is kept
with a note saying so. Each directory carries a README stating what it is and
what it does and does not establish.

Directory naming is the one place the series convention is not uniform. The
knowledge-layer families sit at the top level rather than under a slug
directory, because the deposited report PDF cites those paths; that exception is
permanent and is recorded in [`../README.md`](../README.md).

## Knowledge-layer effectiveness

Report: [`docs/reference/benchmark-report.md`](../../docs/reference/benchmark-report.md).
Protocol: [`docs/knowledge-layer-protocol.md`](../docs/knowledge-layer-protocol.md).

| Directory | What it is |
| --- | --- |
| [`phase2-anthropic-k3/`](phase2-anthropic-k3/) | The four-arm S1-S3 ablation, k=3. The report's headline. |
| [`s5-anthropic-k5/`](s5-anthropic-k5/) | S5 lifecycle at k=5 over the thirty-protocol set (#1139), five independent passes merged. Point estimates with CIs for transfer and supersede; the lifecycle evidence of record from report v2 onward. |
| [`s5-anthropic-k3/`](s5-anthropic-k3/) | S5 lifecycle, shared knowledge store, k=3. |
| [`s5-anthropic-k3-isolated/`](s5-anthropic-k3-isolated/) | S5 lifecycle, isolated replicate 1. |
| [`s5-anthropic-k3-isolated-v2/`](s5-anthropic-k3-isolated-v2/) | S5 lifecycle, isolated replicate 2. Source of the published lifecycle scorecard. |
| [`cold-start-a3-20260717-142008-3064/`](cold-start-a3-20260717-142008-3064/) | Cold-start learning curve, k=3. The headline curve. |
| [`cold-start-a3-20260717-085742-89538/`](cold-start-a3-20260717-085742-89538/) | Cold-start learning curve, k=1. |
| [`cold-start-a3-20260716-234306-5181/`](cold-start-a3-20260716-234306-5181/) | Cold-start, capture-only: every sink write failed and the curve still rose. |
| [`cold-start-a3-20260716-115550-399/`](cold-start-a3-20260716-115550-399/) | Cold-start, interrupted at the baseline checkpoint. |
| [`cold-start-a3-20260716-220857-52792/`](cold-start-a3-20260716-220857-52792/) | Cold-start, interrupted at the baseline checkpoint. |
| [`cold-start-a3-20260716-203207-21080/`](cold-start-a3-20260716-203207-21080/) | Cold-start, interrupted before its first checkpoint closed; transcripts only. |
| [`claude-cli-949/`](claude-cli-949/) | The `claude-cli` adapter's own validation runs (#949). |
| [`v1.102.0-pilot/`](v1.102.0-pilot/) | Phase-1 pilot on an earlier 10-task set. |
| [`v1.102.0-s5-partial/`](v1.102.0-s5-partial/) | Phase-3 S5 pilot, k=1, two harness failures. |

## Knowledge use

Report: [`docs/reference/benchmark-report-knowledge-use.md`](../../docs/reference/benchmark-report-knowledge-use.md).
Protocol: [`docs/knowledge-use-protocol.md`](../docs/knowledge-use-protocol.md).

All seven families are under [`knowledge-use/`](knowledge-use/), indexed by
[`knowledge-use/README.md`](knowledge-use/README.md).

## Knowledge pollution (published)

Epic: issue #1163. Published as
[`docs/reference/benchmark-report-knowledge-pollution.md`](../../docs/reference/benchmark-report-knowledge-pollution.md)
(version 1.0, 2026-08-07; DOI 10.5281/zenodo.21834813). The protocol is
pre-registered in
[`docs/knowledge-pollution-study-design.md`](../docs/knowledge-pollution-study-design.md)
and the confirmatory matrix ran in full; every table recomputes offline via
`bench/reports/knowledge-pollution/pollution_tables.py`.

All families are under [`knowledge-pollution/`](knowledge-pollution/), indexed
by [`knowledge-pollution/README.md`](knowledge-pollution/README.md).

## Search-first gate probe (closed)

Summary: [`pk-gateprobe/pk-gateprobe-SUMMARY.md`](pk-gateprobe/pk-gateprobe-SUMMARY.md).
Not a study; the probe killed the enforcement-study premise (#1145).

| Directory | What it is |
| --- | --- |
| [`pk-gateprobe/`](pk-gateprobe/) | Gate-off search-first probe: 128/128 search-first across eight clean gate-off arms, 16/16 in the gate-on control, plus one aborted arm, with summary, analyzer, and orchestrator logs. |

## Graph-traversal probe (design postmortem; candidate still open)

Summary: [`graph-traversal-probe/graph-traversal-SUMMARY.md`](graph-traversal-probe/graph-traversal-SUMMARY.md).
Not a study. The probe ran in full and its fixture could not have shown
traversal, so it settled nothing about the candidate (#1241); it is kept as an
instrument-defect record and for the boundary condition it does establish.

| Directory | What it is |
| --- | --- |
| [`graph-traversal-probe/`](graph-traversal-probe/) | Depth 0-3 cells over a 42-page planted page corpus, two capability tiers: 162 dereferences across 76 episodes, of which 160 used a reference search had already returned. The cells posed single-fact lookup questions, which search answers directly, so the design never varied the condition a reference graph exists for. Seven runs over three fixture generations, with summary, offline analyzer, fixture-gate reading and plant record. |

## Graph-completion probe (premise held; candidate proceeds)

Summary: [`graph-completion-probe/graph-completion-SUMMARY.md`](graph-completion-probe/graph-completion-SUMMARY.md).
Not a study; the corrected premise probe for the same candidate (#1241),
pre-registered on the ticket before any episode ran. No kill condition fired:
agents traverse the reference graph when it is the only route (0.96 of the
spread constraint set for the strong tier, 0.42 for the weak, by pure
edge-following), and with search available the graph's value concentrates in
the weaker tier. The candidate proceeds toward a density design.

| Directory | What it is |
| --- | --- |
| [`graph-completion-probe/`](graph-completion-probe/) | Three completion cells over the 42-page corpus, 2x2 of corpus arm (graph vs stripped-to-prose) and search condition (on vs client-disallowed), two capability tiers, k=3: 72 episodes, 8 runs, zero failures, with design doc, pre-stated kill conditions, per-arm plant and sweep-gate records, offline analyzer and driver log. |

## API-connection architecture (closed)

Design: [`docs/api-connection-study-design.md`](../docs/api-connection-study-design.md).
Not published; the postmortem is on issue #1027.

| Directory | What it is |
| --- | --- |
| [`api-study-pilot/`](api-study-pilot/) | The complete k=1 matrix: 4 arms x 3 catalog tiers x 50 tasks, 600 episodes. |
