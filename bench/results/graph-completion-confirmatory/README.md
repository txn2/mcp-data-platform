# Graph-completion confirmatory matrix (#1251)

**Status: published** as
[`docs/reference/benchmark-report-graph-completion.md`](../../../docs/reference/benchmark-report-graph-completion.md)
(version 1.0, 2026-08-10; DOI 10.5281/zenodo.21881798).

The run record and the kill application are in
[`graph-confirmatory-SUMMARY.md`](graph-confirmatory-SUMMARY.md); the design
this matrix froze is
[`../../docs/graph-completion-study-design.md`](../../docs/graph-completion-study-design.md)
(#1250). 99 episodes as pre-registered (98 graded, 1 recorded client
failure), seven runs: graph vs stripped with search on at scales 50/500/5000
(k=5 x 3 cells), plus the graph/no-search auxiliary at 5000 (k=3 x 3 cells).
One agent configuration, commit-pinned build, corpora regenerated from Spec
and fingerprinted in every manifest.

## What it establishes

- **The pre-registered instrument kill, which is the published headline**:
  stripped-arm episodes grounded the twice-certified discontinuity
  constraints at both certified scales (1.00 at 500, 0.93 at 5000) through
  read-derived queries — the certified-scale pairs are invalid for the
  discontinuity DV, and "unreachable by search" must hold against
  read-derived queries, which meaning-constant authored prose cannot
  provide.
- Coverage at ceiling in every cell (~11 fetches ground every constraint
  against 5000 pages); the cost separation (searches per grounded constraint
  0.63 graph vs 1.44 stripped at 5000, graph flat across two orders of
  magnitude); and the full-depth no-search walk at 5000 (1.00 grounded, zero
  searches).

## What it does not establish

- Completeness delivery by authored edges (retired with the discontinuity
  construct: the arms did not differ in what documents contained at any
  scale), anything about closure awareness (0 of 98 episodes claimed
  completeness — the elicited channel is inert at this configuration), or
  any tier claim (one agent configuration).

## Reproducing

```bash
python3 graph-confirmatory-analyze.py
```

Offline, stdlib only — and it **exits non-zero on these archives by
design**: the instrument kill is present in the data, and the analyzer
refuses to read kill conditions from an invalid pair. The published report's
toolchain (`bench/reports/graph-completion/graph_tables.py`) pins that exit
code along with the headline numbers.
`cd bench && go run ./graphstudy -mode reread -out results/graph-completion-confirmatory/<run>`
re-derives any run's readings from its transcripts, regenerating the exact
corpus from the manifest's Spec and refusing on fingerprint mismatch.

Reading the archives: the per-scale `graph-study-gate-*.json`,
`graph-study-plant-*.json`, and `graph-study-cert-*.json` files at this
directory's root are the **last arm's** at each scale (the driver overwrites
them per arm); every run's `results.json` embeds the gate reading and plant
record it actually ran under, which is what the analyzer, the reread tool,
and the report toolchain consume.
