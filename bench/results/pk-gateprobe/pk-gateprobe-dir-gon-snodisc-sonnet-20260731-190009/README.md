# Gate probe stage 2, directive twin (sonnet, gate ON (platform.bench.pk.yaml), no-discovery (platform channels only))

Study-3 due-diligence probe (2026-07-31), stage 2: the directive-phrased
twin question (`positive-coverage-days-directive`) names the exact endpoint
and parameters ("Call GET /insights/monitors/501/trend?start_date=...")
so the task presents no visible motive to discover. This is the phrasing
under which instruction-only steering of platform_info was historically
skipped ("query the x table to get the value"); the arm measures whether
search still happens under it.

- Cells: pkcell.BridgeDirectiveProbeCells (note planted vs no-note
  control), k=8, claude-cli.
- Gate: ON (platform.bench.pk.yaml). Scaffold: no-discovery (platform channels only); exact text in results.json manifest.
- Analysis: ../pk-gateprobe-analyze.py <this dir>; stage-2 verdict in
  ../pk-gateprobe-SUMMARY.md.

Headline: search_first 8/8 both cells; note surfaced+used 8/8; controls 5/8 UNAVAILABLE, 2/8 fabricated (15), 1/8 no FINAL ANSWER line. ZERO SEARCH_REQUIRED firings: the gate had nothing to convert even here (note: pkplant pre-opens the per-user gate for seeded cells; only control cells could fire, and none did).

Cross-arm observation: the directive phrasing flipped sonnet's no-note
controls from fabricating a threshold (15/16 on the analytic phrasing) to
refusing (UNAVAILABLE) - phrasing moved fabrication, not discovery.
