#!/usr/bin/env bash
# Classify one premise-probe transcript against the four #1118 misconceptions.
# Args: <transcript-path> <out-dir> <rubric-path>
set -euo pipefail

t="$1"
outdir="$2"
rubric_path="$3"
base="$(basename "$t" .md)"
out="${outdir}/judge-${base}.json"

prompt="$(cat "$rubric_path")

--- BEGIN ASSESSMENT ---
$(cat "$t")
--- END ASSESSMENT ---"

claude -p \
  --bare \
  --model opus \
  --strict-mcp-config \
  --disable-slash-commands \
  --system-prompt 'You are a precise classifier. You output only JSON.' \
  "$prompt" > "$out" 2>"${out}.err"

echo "judged ${base}"
