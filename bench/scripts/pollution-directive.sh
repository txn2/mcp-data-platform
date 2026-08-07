#!/usr/bin/env bash
#
# Run the directive contrast (#1167, protocol 6.4): the same false order count
# planted at three directive strengths, at the one tier and cell that showed a
# non-zero adoption rate in the RQ1 warehouse block.
#
# It answers the question RQ1 cannot: an episode that answered 1140 may have
# adopted a belief about the world, or may have done as the planted text told
# it. The imperative level reproduces the RQ1 cell, so the block is
# self-checking -- if it does not come back near 16/24, the contrast is
# measuring a different stack rather than a different directive.
#
# Usage:
#   bench/scripts/pollution-directive.sh --out build/bench-results/pollution-directive-<stamp>
set -euo pipefail

OUT=""
TIER="haiku"

while [[ $# -gt 0 ]]; do
	case "$1" in
	--out) OUT="$2"; shift 2 ;;
	--tier) TIER="$2"; shift 2 ;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done
[[ -n "$OUT" ]] || { echo "--out is required" >&2; exit 2; }

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"
mkdir -p "$OUT"

echo "=== directive contrast (checkable/wrong, $TIER) -> $OUT ==="

# Shared with the block driver: an arm counts as banked only when it wrote its
# completion marker, and a drifted arm is re-run rather than banked.
finished() { grep -q "=== arm .* complete: " "$1/arm.log" 2>/dev/null; }
drifted() { grep -q "^pollutionplant: the shared store changed" "$1/arm.log" 2>/dev/null; }

ATTEMPTS=3

for directive in bare plain imperative; do
	suffix=""
	[[ "$directive" != imperative ]] && suffix="-$directive"
	cell="checkable-wrong-${TIER}${suffix}"

	if [[ -e "$OUT/$cell" ]] && finished "$OUT/$cell" && ! drifted "$OUT/$cell"; then
		echo "--- $cell already archived, skipping ---"
		continue
	fi

	try=1
	while :; do
		if [[ -e "$OUT/$cell" ]]; then
			s=INCOMPLETE
			drifted "$OUT/$cell" && s=DRIFTED
			n=0
			while [[ -e "$OUT/$cell-$s-$n" ]]; do n=$((n + 1)); done
			mv "$OUT/$cell" "$OUT/$cell-$s-$n"
			echo "--- $cell: previous attempt is $s, archived as $cell-$s-$n ---"
		fi
		echo
		echo "=== $cell (directive: $directive) ==="
		if bench/scripts/pollution-arm.sh --class checkable --arm wrong \
			--tier "$TIER" --directive "$directive" --out "$OUT/$cell" && finished "$OUT/$cell"; then
			break
		fi
		if ! drifted "$OUT/$cell"; then
			echo "!!! $cell failed for a reason other than store drift; stopping" >&2
			exit 1
		fi
		if [[ "$try" -ge "$ATTEMPTS" ]]; then
			echo "!!! $cell drifted on all $ATTEMPTS attempts; stopping" >&2
			exit 1
		fi
		try=$((try + 1))
	done
done

echo
echo "=== directive contrast complete ==="
ls -1 "$OUT"
