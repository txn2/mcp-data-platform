#!/usr/bin/env bash
# The cross-fixture block (protocol 6.5): does the RQ1 effect appear on a
# second, unrelated fixture? Controls first per tier, as the run ticket
# requires -- a degraded control is a stack defect and stops the block.
set -euo pipefail
OUT=""
while [[ $# -gt 0 ]]; do
	case "$1" in
	--out) OUT="$2"; shift 2 ;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done
[[ -n "$OUT" ]] || { echo "--out is required" >&2; exit 2; }
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"
mkdir -p "$OUT"

finished() { grep -q "=== arm .* complete: " "$1/arm.log" 2>/dev/null; }
drifted() { grep -q "^pollutionplant: the shared store changed" "$1/arm.log" 2>/dev/null; }

run_one() {
	local arm="$1" tier="$2"
	local cell="api-checkable-${arm}-${tier}"
	local try=1
	if [[ -e "$OUT/$cell" ]] && finished "$OUT/$cell" && ! drifted "$OUT/$cell"; then
		echo "--- $cell already archived, skipping ---"; return 0
	fi
	while :; do
		if [[ -e "$OUT/$cell" ]]; then
			s=INCOMPLETE; drifted "$OUT/$cell" && s=DRIFTED
			n=0; while [[ -e "$OUT/$cell-$s-$n" ]]; do n=$((n+1)); done
			mv "$OUT/$cell" "$OUT/$cell-$s-$n"
			echo "--- $cell: previous attempt $s, archived as $cell-$s-$n ---"
		fi
		echo; echo "=== $cell ==="
		if bench/scripts/pollution-api-arm.sh --arm "$arm" --tier "$tier" --out "$OUT/$cell" && finished "$OUT/$cell"; then
			return 0
		fi
		if ! drifted "$OUT/$cell"; then
			echo "!!! $cell failed for a reason other than store drift; stopping" >&2; return 1
		fi
		if [[ "$try" -ge 3 ]]; then
			echo "!!! $cell drifted on all 3 attempts; stopping" >&2; return 1
		fi
		try=$((try+1))
	done
}

for arm in absent correct wrong; do run_one "$arm" haiku || exit 1; done
run_one wrong sonnet || exit 1
echo; echo "=== cross-fixture block complete ==="; ls -1 "$OUT"
