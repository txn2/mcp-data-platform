#!/usr/bin/env bash
# render-report.sh — Render docs/reference/benchmark-report.md to a citable PDF
# and a self-contained HTML, with the figures embedded and the report's tables
# given print-legible column widths.
#
# Run from anywhere in the repo:  bash bench/reports/knowledge-layer/render-report.sh
#   or:  make bench-report-knowledge-layer-pdf
#
# Output goes to build/report/ (gitignored). The committed markdown is never
# modified; all print-only formatting lives in bench/reports/knowledge-layer/pandoc/.
#
# Requires: pandoc + a PDF engine (tectonic). Install on macOS:
#   brew install pandoc tectonic
# This is an on-demand tool and is deliberately NOT part of `make verify`.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

REPORT="docs/reference/benchmark-report.md"
PANDOC_DIR="bench/reports/knowledge-layer/pandoc"
OUT="build/report"
TITLE="Does a semantic knowledge layer make an agent measurably better?"
SUBTITLE="A reproducible benchmark of the mcp-data-platform knowledge layer"
AUTHOR="Craig Johnston (cj@imti.co), Deasil Works, Inc. / txn2"

# Every artifact is version-stamped from the report's own header row, so the
# rendered files, the zip, and the Zenodo deposit all carry one name and cannot
# drift from the report they came from. Hand-renaming these after the fact is
# how v2.0 ended up with two naming schemes in one directory.
VERSION="$(sed -n 's/^| \*\*Report version\*\* | \(.*\) |$/\1/p' "$REPORT" | head -1 | tr -d ' ')"
[ -n "$VERSION" ] || { echo "error: could not read '| **Report version** |' from $REPORT"; exit 1; }
PDF="benchmark-report-knowledge-layer-v$VERSION.pdf"
HTML="benchmark-report-knowledge-layer-v$VERSION.html"
ZIP="bench-results-all-v$VERSION.zip"

command -v pandoc > /dev/null 2>&1 || { echo "error: pandoc not found (brew install pandoc)"; exit 1; }
command -v tectonic > /dev/null 2>&1 || { echo "error: tectonic not found (brew install tectonic)"; exit 1; }
[ -f "$REPORT" ] || { echo "error: $REPORT not found"; exit 1; }

mkdir -p "$OUT"
ROOT="$PWD"

echo "1/2  PDF (pandoc + tectonic) -> $OUT/$PDF"
( cd "$(dirname "$REPORT")" && pandoc "$(basename "$REPORT")" \
    --pdf-engine=tectonic --toc --toc-depth=2 \
    --lua-filter="$ROOT/$PANDOC_DIR/table-widths.lua" \
    --include-in-header="$ROOT/$PANDOC_DIR/header.tex" \
    -V geometry:margin=1in -V fontsize=11pt -V colorlinks=true -V linkcolor=teal -V urlcolor=teal \
    -V title="$TITLE" -V subtitle="$SUBTITLE" -V author="$AUTHOR" \
    -o "$ROOT/$OUT/$PDF" )

echo "2/2  self-contained HTML (figures embedded) -> $OUT/$HTML"
( cd "$(dirname "$REPORT")" && pandoc "$(basename "$REPORT")" \
    -f gfm -t html5 --standalone --embed-resources --toc --toc-depth=2 \
    --lua-filter="$ROOT/$PANDOC_DIR/table-widths.lua" \
    --metadata title="$TITLE. $SUBTITLE" \
    -c "$ROOT/$PANDOC_DIR/report.css" \
    -o "$ROOT/$OUT/$HTML" )

echo "done:"
ls -la "$OUT/$PDF" "$OUT/$HTML"
echo
echo "For a Zenodo deposit, also snapshot the raw data the report recomputes from:"
echo "  ( cd bench && zip -rqX ../$OUT/$ZIP results -x '*.DS_Store' )"
