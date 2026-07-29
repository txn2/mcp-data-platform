# Benchmark report toolchain

This directory holds the reproducible supplement and the render toolchain for the
knowledge-layer benchmark report published at
[`docs/reference/benchmark-report.md`](../../docs/reference/benchmark-report.md).

| Path | What it is |
| --- | --- |
| `report.ipynb` | The notebook that recomputes every number and figure in the report from the committed run data under `bench/results/`. No API key, no network. |
| `requirements.txt` | Python dependencies for the notebook. |
| `figures/` | The generated figures the report embeds (also copied to `docs/reference/benchmark-figures/`). |
| `render-report.sh` | Renders the report to a citable PDF and a self-contained HTML. |
| `pandoc/` | Print-only pandoc config used by `render-report.sh` (table column widths, LaTeX header, HTML CSS). The committed markdown is never modified. |

## Reproduce the numbers and figures

```bash
python3 -m venv .venv && . .venv/bin/activate
pip install -r bench/reports/knowledge-layer/requirements.txt
jupyter nbconvert --to notebook --execute --inplace bench/reports/knowledge-layer/report.ipynb
```

The notebook reads only the `results.json` files under `bench/results/`, recomputes
every statistic, and regenerates the four figures. A mismatch between a number in
the report and the notebook's recomputed value is a factual-integrity defect to be
fixed in the report prose, never in the data.

## Render the report to PDF and HTML

```bash
make bench-report-pdf          # or: bash bench/reports/knowledge-layer/render-report.sh
```

Output lands in `build/report/` (gitignored): `benchmark-report.pdf` (figures
embedded, tables width-tuned) and `benchmark-report.html` (self-contained). This
needs `pandoc` and `tectonic` (`brew install pandoc tectonic`) and is an on-demand
tool, deliberately not part of `make verify`.

## Create a Zenodo deposit

The report is archived on Zenodo under the concept DOI
[10.5281/zenodo.21438044](https://doi.org/10.5281/zenodo.21438044), which always
resolves to the latest published version (the v1.0 snapshot carries version DOI
[10.5281/zenodo.21438045](https://doi.org/10.5281/zenodo.21438045)). To rebuild the
deposit artifacts (for example for a future report version):

```bash
make bench-report-pdf
# snapshot the raw data the report recomputes from:
( cd bench && zip -rqX ../build/report/bench-results.zip results -x '*.DS_Store' )
```

Upload `build/report/benchmark-report.pdf` and `build/report/bench-results.zip` to
the Zenodo record. Repo-level `CITATION.cff` and `.zenodo.json` carry the citation
metadata and the DOI.
