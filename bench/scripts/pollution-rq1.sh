#!/usr/bin/env bash
#
# Run the RQ1 warehouse block of the knowledge-pollution confirmatory matrix
# (#1167): derivability class x arm x capability tier, 24 episodes per cell.
#
# Arm order within a tier is the ticket's, and it is a gate rather than a
# preference: control cells run FIRST on every stack, because a degraded
# control is a stack defect that stops the run and is never graded. This
# script therefore exits on the first arm failure and leaves every completed
# arm's archive in place.
#
# Tier order is the protocol's drop order (8.4) reversed, so that a block cut
# short by wall clock has dropped the tier the protocol says to drop: sonnet
# and haiku carry the predicted inversion, opus is the third confirming point.
#
# Usage:
#   bench/scripts/pollution-rq1.sh --out build/bench-results/pollution-rq1-<stamp> [--tiers "sonnet haiku"]
set -euo pipefail

OUT=""
TIERS="sonnet haiku opus"

while [[ $# -gt 0 ]]; do
	case "$1" in
	--out) OUT="$2"; shift 2 ;;
	--tiers) TIERS="$2"; shift 2 ;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done
[[ -n "$OUT" ]] || { echo "--out is required" >&2; exit 2; }

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"
mkdir -p "$OUT"

echo "=== RQ1 warehouse block -> $OUT ==="
echo "tiers: $TIERS"

for tier in $TIERS; do
	# Controls first, then the presence control, then the treatment.
	for arm in absent correct wrong; do
		for class in convention checkable; do
			cell="${class}-${arm}-${tier}"
			if [[ -e "$OUT/$cell" ]]; then
				echo "--- $cell already archived, skipping ---"
				continue
			fi
			echo
			echo "=== $cell ==="
			bench/scripts/pollution-arm.sh \
				--class "$class" --arm "$arm" --tier "$tier" --out "$OUT/$cell"
		done
	done
done

echo
echo "=== RQ1 warehouse block complete ==="
ls -1 "$OUT"
