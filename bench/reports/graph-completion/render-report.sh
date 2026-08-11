#!/usr/bin/env bash
# render-report.sh — Render docs/reference/benchmark-report-graph-completion.md
# to a citable PDF and a self-contained HTML, with the report's tables given
# print-legible column widths.
#
# Run from anywhere in the repo:  bash bench/reports/graph-completion/render-report.sh
#   or:  make bench-report-graph-completion-pdf
#
# Output goes to build/report-graph-completion/ (gitignored). The committed
# markdown is never modified; all print-only formatting lives in
# bench/reports/graph-completion/pandoc/.
#
# Requires: pandoc + a PDF engine (tectonic). Install on macOS:
#   brew install pandoc tectonic
# This is an on-demand tool and is deliberately NOT part of `make verify`.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

REPORT="docs/reference/benchmark-report-graph-completion.md"
PANDOC_DIR="bench/reports/graph-completion/pandoc"
OUT="build/report-graph-completion"
TITLE="Do cross-references help LLM agents complete documents?"
SUBTITLE="Search cost, robustness, and unreachable content on a wiki-style corpus"
AUTHOR="Craig Johnston (cj@imti.co), Deasil Works, Inc. / txn2"

command -v pandoc > /dev/null 2>&1 || { echo "error: pandoc not found (brew install pandoc)"; exit 1; }
command -v tectonic > /dev/null 2>&1 || { echo "error: tectonic not found (brew install tectonic)"; exit 1; }
[ -f "$REPORT" ] || { echo "error: $REPORT not found"; exit 1; }

mkdir -p "$OUT"
ROOT="$PWD"

echo "1/2  PDF (pandoc + tectonic) -> $OUT/benchmark-report-graph-completion.pdf"
( cd "$(dirname "$REPORT")" && pandoc "$(basename "$REPORT")" \
    --pdf-engine=tectonic --toc --toc-depth=2 \
    --lua-filter="$ROOT/$PANDOC_DIR/table-widths.lua" \
    --include-in-header="$ROOT/$PANDOC_DIR/header.tex" \
    -V geometry:margin=1in -V fontsize=11pt -V colorlinks=true -V linkcolor=teal -V urlcolor=teal \
    -V title="$TITLE" -V subtitle="$SUBTITLE" -V author="$AUTHOR" \
    -o "$ROOT/$OUT/benchmark-report-graph-completion.pdf" )

echo "2/2  self-contained HTML -> $OUT/benchmark-report-graph-completion.html"
( cd "$(dirname "$REPORT")" && pandoc "$(basename "$REPORT")" \
    -f gfm -t html5 --standalone --embed-resources --toc --toc-depth=2 \
    --lua-filter="$ROOT/$PANDOC_DIR/table-widths.lua" \
    --metadata title="$TITLE. $SUBTITLE" \
    -c "$ROOT/$PANDOC_DIR/report.css" \
    -o "$ROOT/$OUT/benchmark-report-graph-completion.html" )

echo "done:"
ls -la "$OUT/benchmark-report-graph-completion.pdf" "$OUT/benchmark-report-graph-completion.html"
echo
echo "For a Zenodo deposit, also snapshot the raw data the report recomputes from"
echo "(this study's three run families sit at the top of bench/results/):"
echo "  ( cd bench && zip -rqX ../$OUT/bench-results-graph-completion.zip results/graph-completion-probe results/graph-completion-separation results/graph-completion-confirmatory -x '*.DS_Store' )"
