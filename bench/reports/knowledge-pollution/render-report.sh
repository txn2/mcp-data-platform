#!/usr/bin/env bash
# render-report.sh — Render docs/reference/benchmark-report-knowledge-pollution.md
# to a citable PDF and a self-contained HTML, with the report's tables given
# print-legible column widths.
#
# Run from anywhere in the repo:  bash bench/reports/knowledge-pollution/render-report.sh
#   or:  make bench-report-knowledge-pollution-pdf
#
# Output goes to build/report-knowledge-pollution/ (gitignored). The committed
# markdown is never modified; all print-only formatting lives in
# bench/reports/knowledge-pollution/pandoc/.
#
# Requires: pandoc + a PDF engine (tectonic). Install on macOS:
#   brew install pandoc tectonic
# This is an on-demand tool and is deliberately NOT part of `make verify`.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

REPORT="docs/reference/benchmark-report-knowledge-pollution.md"
PANDOC_DIR="bench/reports/knowledge-pollution/pandoc"
OUT="build/report-knowledge-pollution"
TITLE="Knowledge pollution"
SUBTITLE="Verification displacement, capability, and the price of a curation gate"
AUTHOR="Craig Johnston (cj@imti.co), Deasil Works, Inc. / txn2"

command -v pandoc > /dev/null 2>&1 || { echo "error: pandoc not found (brew install pandoc)"; exit 1; }
command -v tectonic > /dev/null 2>&1 || { echo "error: tectonic not found (brew install tectonic)"; exit 1; }
[ -f "$REPORT" ] || { echo "error: $REPORT not found"; exit 1; }

mkdir -p "$OUT"
ROOT="$PWD"

echo "1/2  PDF (pandoc + tectonic) -> $OUT/benchmark-report-knowledge-pollution.pdf"
( cd "$(dirname "$REPORT")" && pandoc "$(basename "$REPORT")" \
    --pdf-engine=tectonic --toc --toc-depth=2 \
    --lua-filter="$ROOT/$PANDOC_DIR/table-widths.lua" \
    --include-in-header="$ROOT/$PANDOC_DIR/header.tex" \
    -V geometry:margin=1in -V fontsize=11pt -V colorlinks=true -V linkcolor=teal -V urlcolor=teal \
    -V title="$TITLE" -V subtitle="$SUBTITLE" -V author="$AUTHOR" \
    -o "$ROOT/$OUT/benchmark-report-knowledge-pollution.pdf" )

echo "2/2  self-contained HTML -> $OUT/benchmark-report-knowledge-pollution.html"
( cd "$(dirname "$REPORT")" && pandoc "$(basename "$REPORT")" \
    -f gfm -t html5 --standalone --embed-resources --toc --toc-depth=2 \
    --lua-filter="$ROOT/$PANDOC_DIR/table-widths.lua" \
    --metadata title="$TITLE. $SUBTITLE" \
    -c "$ROOT/$PANDOC_DIR/report.css" \
    -o "$ROOT/$OUT/benchmark-report-knowledge-pollution.html" )

echo "done:"
ls -la "$OUT/benchmark-report-knowledge-pollution.pdf" "$OUT/benchmark-report-knowledge-pollution.html"
echo
echo "For a Zenodo deposit, also snapshot the raw data the report recomputes from:"
echo "  ( cd bench && zip -rqX ../$OUT/bench-results-knowledge-pollution.zip results/knowledge-pollution -x '*.DS_Store' )"
