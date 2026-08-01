#!/usr/bin/env bash
# #1139 k=5 lifecycle run, as five independent k=1 passes merged afterward.
#
# Each pass gets a full clean reset (bench-down does `down -v`, so the platform
# starts from freshly seeded state). That reset is the premise the -merge path
# documents: passes are only genuinely independent if they do not share an
# accumulating knowledge store.
#
# Aborts on the first failed pass rather than continuing, so a broken stack
# cannot burn the remaining budget. Every pass writes its own timestamped dir
# under build/bench-results/; nothing is ever overwritten.
set -uo pipefail

PASSES=${PASSES:-5}
LOG_DIR="build/bench-results/1139-k5-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$LOG_DIR"
MANIFEST="$LOG_DIR/passes.txt"

echo "orchestrator: $PASSES passes, logs in $LOG_DIR"

# shellcheck disable=SC1090
source ~/.bench-key.env
if [ -z "${ANTHROPIC_API_KEY:-}" ]; then
  echo "FATAL: ANTHROPIC_API_KEY not set after sourcing ~/.bench-key.env" >&2
  exit 1
fi

for i in $(seq 1 "$PASSES"); do
  echo "=== pass $i/$PASSES: reset ==="
  make bench-down                                     > "$LOG_DIR/pass$i-down.log" 2>&1
  # SeaweedFS readiness is marginal against the 120s probe; a retry finds it healthy.
  up_ok=0
  for attempt in 1 2 3; do
    if make bench-up BENCH_ARM=a3 BENCH_METRICS_ADDR=:9095 > "$LOG_DIR/pass$i-up.log" 2>&1; then
      up_ok=1; break
    fi
    echo "  bench-up attempt $attempt failed, retrying"
  done
  if [ "$up_ok" -ne 1 ]; then
    echo "FATAL: pass $i could not bring the stack up; stopping before spending" >&2
    exit 1
  fi

  echo "=== pass $i/$PASSES: running (k=1, anthropic, claude-sonnet-5) ==="
  if ! make bench-lifecycle LLM=anthropic K=1 > "$LOG_DIR/pass$i-run.log" 2>&1; then
    echo "FATAL: pass $i run failed; stopping. See $LOG_DIR/pass$i-run.log" >&2
    exit 1
  fi

  # The target prints the results path on its last line; capture it for -merge.
  out=$(grep -aoE 'build/bench-results/lifecycle-a3-[^ ]+/lifecycle-a3\.json' "$LOG_DIR/pass$i-run.log" | tail -1)
  if [ -z "$out" ] || [ ! -f "$out" ]; then
    echo "FATAL: pass $i produced no results file; stopping" >&2
    exit 1
  fi
  echo "$out" >> "$MANIFEST"
  echo "=== pass $i/$PASSES complete -> $out ==="
  grep -aE "protocols [0-9]+|harness failures|tokens:" "$LOG_DIR/pass$i-run.log" | tail -2
done

echo "ALL $PASSES PASSES COMPLETE"
echo "merge with:"
echo "  ./build/benchrun -lifecycle -merge \"\$(paste -sd, $MANIFEST)\" -out $LOG_DIR/lifecycle-a3-k$PASSES.json"
