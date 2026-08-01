# Benchmark report toolchain

This directory holds the reproducible supplement and the render toolchain for the
knowledge-layer benchmark report published at
[`docs/reference/benchmark-report.md`](../../../docs/reference/benchmark-report.md).

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
make bench-report-knowledge-layer-pdf   # or: bash bench/reports/knowledge-layer/render-report.sh
```

Output lands in `build/report/` (gitignored), version-stamped from the report's
own `**Report version**` row: `benchmark-report-knowledge-layer-v<version>.pdf`
(figures embedded, tables width-tuned) and the matching `.html`. The study slug
is in the name because this is a report *series*: the sibling render emits
`benchmark-report-knowledge-use.pdf`. The source markdown keeps its slugless
legacy path (`docs/reference/benchmark-report.md`) because the deposited PDFs
cite it; only the rendered artifacts carry the slug. Do not rename these by hand — the stamp is what keeps the
rendered files, the data zip, and the Zenodo deposit on one name. This
needs `pandoc` and `tectonic` (`brew install pandoc tectonic`) and is an on-demand
tool, deliberately not part of `make verify`.

## Create a Zenodo deposit

The report is archived on Zenodo under the concept DOI
[10.5281/zenodo.21438044](https://doi.org/10.5281/zenodo.21438044), which always
resolves to the latest published version. Each version also carries its own DOI:
v2.0.1 is [10.5281/zenodo.21751635](https://doi.org/10.5281/zenodo.21751635), v2.0 is
[10.5281/zenodo.21751050](https://doi.org/10.5281/zenodo.21751050) and the
v1.0 snapshot is [10.5281/zenodo.21438045](https://doi.org/10.5281/zenodo.21438045).
There is no v1.1 DOI: v1.1 was a markdown-only revision that was never
deposited, so the concept DOI resolved to v1.0 until v2.0 was published.
**Record every new version DOI here and in `CITATION.cff` when you publish
one.** To rebuild the
deposit artifacts (for example for a future report version):

```bash
make bench-report-knowledge-layer-pdf
# snapshot the raw data the report recomputes from (same version stamp):
( cd bench && zip -rqX ../build/report/bench-results-all-v2.0.zip results -x '*.DS_Store' )
```

Upload `build/report/benchmark-report-knowledge-layer-v<version>.pdf` and
`build/report/bench-results-all-v<version>.zip` to the Zenodo record. The zip is
the entire `bench/results/` tree, shared by every study in the series, which is
why it carries no study slug — the PDF and
the HTML render the same content, but only the PDF is deposited. **Look at the
rendered PDF pages before uploading**, not just its text layer: report v2.0 was
deposited with a table whose cells overprinted, and grepping the text for the
numbers did not catch it. Repo-level `CITATION.cff` and `.zenodo.json` carry the citation
metadata and the DOI.
