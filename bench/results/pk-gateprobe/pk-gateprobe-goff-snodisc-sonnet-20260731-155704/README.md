# Gate probe arm (sonnet, no-discovery (harness search bullet removed; platform channels only))

Study-3 due-diligence probe (2026-07-31): does the agent discover before
acting when the search-first gate does NOT force it?

- Platform config: bench/config/platform.bench.pk-gateoff.yaml - the pk arm's
  single-deviation copy with workflow.require_search: false (gate OFF).
- Cells: pkcell.BridgeProbeCells (positive-coverage-days; convention note
  planted vs no-note control), k=8, claude-cli (no metered cost).
- Scaffold: no-discovery (harness search bullet removed; platform channels only). The exact text is in the run manifest (results.json,
  manifest.scaffold).
- Analysis: ../pk-gateprobe-analyze.py <this dir>; summary and verdict in
  ../pk-gateprobe-SUMMARY.md.

Headline: search_first 8/8 both cells; note surfaced+used 8/8 (answer 11); controls fabricated threshold 8/8 (answer 15).

Gate-ON baselines for comparison: pk-bridge-20260725-135349 (sonnet),
pk-bridge-sonnet-v1116-20260726-150307, pk-bridge-haiku-20260725-141119 -
identical search_first and note rates, i.e. the gate's removal changed nothing.
