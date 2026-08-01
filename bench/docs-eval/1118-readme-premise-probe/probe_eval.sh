#!/usr/bin/env bash
# One premise-probe evaluation: feed README.md alone to a fresh model and ask
# for a critical assessment. Args: <model> <run-index> <out-dir> <readme-path>
set -euo pipefail

model="$1"
run="$2"
outdir="$3"
readme="$4"

out="${outdir}/eval-${model}-run${run}.md"

prompt="$(cat <<'EOF'
Below is the README.md of an open-source project, reproduced in full. It is the
only material you have been given.

Give me a critical assessment of this project.

--- BEGIN README.md ---
EOF
)
$(cat "$readme")
--- END README.md ---"

claude -p \
  --bare \
  --model "$model" \
  --strict-mcp-config \
  --disable-slash-commands \
  --system-prompt 'You are a helpful assistant.' \
  "$prompt" > "$out" 2>"${out}.err"

echo "done ${model} run${run} ($(wc -l < "$out") lines)"
