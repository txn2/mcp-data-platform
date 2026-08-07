#!/usr/bin/env bash
# The raw-API replication (protocol 13). METERED: this spends real money.
#
# Every other arm in this study runs through claude-cli on the subscription.
# That leaves one objection open -- that the effect is a property of one client
# harness rather than of the platform and model -- and this arm exists to close
# it. The knowledge-use report set the precedent, replicating its headline on a
# raw Messages API loop with no agent client.
#
# Cells are the pre-registered ones: both derivability classes' wrong arms
# across three tiers at k=8, 48 episodes. They are NOT re-chosen in light of
# the RQ1 result; replicating the convention null on a second client is
# informative too, and this protocol has been amended enough.
set -euo pipefail
OUT=""
while [[ $# -gt 0 ]]; do
	case "$1" in
	--out) OUT="$2"; shift 2 ;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done
[[ -n "$OUT" ]] || { echo "--out is required" >&2; exit 2; }
[[ -n "${ANTHROPIC_API_KEY:-}" ]] || { echo "ERROR: metered run needs ANTHROPIC_API_KEY" >&2; exit 1; }
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"
mkdir -p "$OUT"

finished() { grep -q "=== arm .* complete: " "$1/arm.log" 2>/dev/null; }
drifted() { grep -q "^pollutionplant: the shared store changed" "$1/arm.log" 2>/dev/null; }

echo "=== raw-API replication -> $OUT ==="
echo "48 episodes: 2 classes x 3 tiers x k=8. Estimated \$4-7 against the \$25 cap."

for class in convention checkable; do
	for tier in haiku sonnet opus; do
		cell="${class}-wrong-${tier}-api"
		if [[ -e "$OUT/$cell" ]] && finished "$OUT/$cell" && ! drifted "$OUT/$cell"; then
			echo "--- $cell already archived, skipping ---"; continue
		fi
		try=1
		while :; do
			if [[ -e "$OUT/$cell" ]]; then
				s=INCOMPLETE; drifted "$OUT/$cell" && s=DRIFTED
				n=0; while [[ -e "$OUT/$cell-$s-$n" ]]; do n=$((n+1)); done
				mv "$OUT/$cell" "$OUT/$cell-$s-$n"
				echo "--- $cell: previous attempt $s, archived ---"
			fi
			echo; echo "=== $cell ==="
			# k=8 per tier for both classes, which is what 13 authorized. The
			# convention class normally runs three tasks at k=8; here the
			# authorized budget is per-cell, so K is forced to 8.
			tasks="s3-deprecated-order-count"
			[[ "$class" == convention ]] && tasks="s3-fiscal-2025-count"
			if K_OVERRIDE=8 TASKS_OVERRIDE="$tasks" bench/scripts/pollution-arm.sh \
				--class "$class" --arm wrong --tier "$tier" --driver anthropic \
				--out "$OUT/$cell" && finished "$OUT/$cell"; then
				break
			fi
			if ! drifted "$OUT/$cell"; then
				echo "!!! $cell failed for a reason other than store drift; stopping" >&2; exit 1
			fi
			if [[ "$try" -ge 2 ]]; then
				echo "!!! $cell drifted twice; stopping rather than spending more" >&2; exit 1
			fi
			try=$((try+1))
		done
	done
done
echo; echo "=== raw-API replication complete ==="; ls -1 "$OUT"
