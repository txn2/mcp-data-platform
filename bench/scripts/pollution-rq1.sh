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

# An arm that fails the store-constancy check is re-run on a fresh database,
# which is exactly the remedy protocol 7.3 prescribes. The failed attempt is
# archived rather than deleted -- its episodes are real data, and the evaluator
# write that invalidated it is itself an observation the report states -- so a
# retry moves it aside under a numbered suffix and starts the cell clean.
#
# Retries are bounded. An arm that drifts ATTEMPTS times running is not a
# coincidence, and the block should stop and say so rather than loop.
ATTEMPTS=3

# drifted reports whether an arm's archive shows a store-constancy failure, as
# opposed to any other error. Only this failure is worth retrying: a stack
# defect or a bad flag would fail identically every time.
drifted() {
	grep -q "^pollutionplant: the shared store changed" "$1/arm.log" 2>/dev/null
}

# finished reports whether an arm ran to completion, matched on the marker the
# arm script writes as its last line.
#
# The existence of the directory is NOT the test. An arm interrupted part-way
# leaves a directory holding a partial results file and no failure of any kind,
# so an existence check silently accepts a cell that is short of its k and the
# block reports a full matrix it never ran. That is worse than a crash: it is a
# denominator nobody can see is wrong.
finished() {
	grep -q "=== arm .* complete: " "$1/arm.log" 2>/dev/null
}

run_cell() {
	local class="$1" arm="$2" tier="$3" cell="$4" try=1
	while :; do
		if bench/scripts/pollution-arm.sh --class "$class" --arm "$arm" --tier "$tier" --out "$OUT/$cell"; then
			# A zero exit is not on its own proof the arm ran to completion.
			# Check the marker, so a cell short of its k can never be banked.
			if finished "$OUT/$cell"; then
				return 0
			fi
			echo "!!! $cell exited cleanly without completing; stopping the block" >&2
			return 1
		fi
		if ! drifted "$OUT/$cell"; then
			echo "!!! $cell failed for a reason other than store drift; stopping the block" >&2
			return 1
		fi
		if [[ "$try" -ge "$ATTEMPTS" ]]; then
			echo "!!! $cell drifted on all $ATTEMPTS attempts; stopping the block" >&2
			return 1
		fi
		mv "$OUT/$cell" "$OUT/$cell-DRIFTED-$try"
		echo "--- $cell: an evaluator wrote to the shared store; attempt $try archived as $cell-DRIFTED-$try, re-running on a fresh database ---"
		try=$((try + 1))
	done
}

for tier in $TIERS; do
	# Controls first, then the presence control, then the treatment.
	for arm in absent correct wrong; do
		for class in convention checkable; do
			cell="${class}-${arm}-${tier}"
			if [[ -e "$OUT/$cell" ]] && finished "$OUT/$cell" && ! drifted "$OUT/$cell"; then
				echo "--- $cell already archived, skipping ---"
				continue
			fi
			if [[ -e "$OUT/$cell" ]]; then
				# Either it drifted or it never finished. Both are re-runs, and
				# both keep what they produced: a partial arm's episodes are
				# real, and the reason it stopped belongs in the archive.
				suffix=INCOMPLETE
				drifted "$OUT/$cell" && suffix=DRIFTED
				n=0
				while [[ -e "$OUT/$cell-$suffix-$n" ]]; do n=$((n + 1)); done
				mv "$OUT/$cell" "$OUT/$cell-$suffix-$n"
				echo "--- $cell: a previous attempt is $suffix, archived as $cell-$suffix-$n ---"
			fi
			echo
			echo "=== $cell ==="
			run_cell "$class" "$arm" "$tier" "$cell" || exit 1
		done
	done
done

echo
echo "=== RQ1 warehouse block complete ==="
ls -1 "$OUT"
