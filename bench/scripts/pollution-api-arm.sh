#!/usr/bin/env bash
#
# Run one cross-fixture arm of the knowledge-pollution study (#1167, protocol
# 6.5) on the perishable-knowledge API fixture.
#
# This is the API counterpart of pollution-arm.sh. It differs in three ways
# that are properties of the fixture rather than choices:
#
#   - the stack is the pk stack (fixture service + pk platform config), not
#     the warehouse one;
#   - episodes run through pkrun, which asks a pkcell question, rather than
#     benchrun over committed task YAML;
#   - the claim applies to a knowledge page, because this fixture has no
#     catalog entity. The sink control in 6.5 is what makes that difference
#     interpretable.
#
# The committed pk config grants no identity `apply_knowledge`, so nothing can
# reach the applied tier on it and the plant would fail at its promotion step.
# This script generates a variant that adds it, the same way the warehouse arm
# script generates a per-arm DSN variant, and never edits the committed file.
# Adding it also makes the two fixtures MORE comparable: the warehouse arm
# config already grants apply_knowledge to its evaluators, so the tool surface
# is now held constant across the contrast rather than varying with it.
set -euo pipefail

ARM=""
TIER="haiku"
OUT=""
SETTLE=300
META_TOOLS="ToolSearch,ReadMcpResourceTool,ListMcpResourcesTool"
K=24

while [[ $# -gt 0 ]]; do
	case "$1" in
	--arm) ARM="$2"; shift 2 ;;
	--tier) TIER="$2"; shift 2 ;;
	--out) OUT="$2"; shift 2 ;;
	--settle) SETTLE="$2"; shift 2 ;;
	--k) K="$2"; shift 2 ;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done
[[ -n "$ARM" && -n "$OUT" ]] || { echo "--arm and --out are required" >&2; exit 2; }

case "$ARM" in
wrong | correct) TREATMENT="monitor-count-${ARM}" ;;
absent) TREATMENT="" ;;
*) echo "unknown --arm $ARM (wrong|correct|absent)" >&2; exit 2 ;;
esac

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

CELL="api-checkable-${ARM}-${TIER}"
DB="mcp_bench_pk_$(echo "$CELL" | tr '-' '_')"
CONFIG="build/platform.bench.pk-${CELL}.yaml"
BENCH_URL="http://localhost:8098"
BENCH_KEY="${BENCH_KEY:-bench-admin-key}"
METRICS_ADDR="${BENCH_METRICS_ADDR:-:9095}"
FIXTURE_ADDR="${BENCH_PK_APISVC_ADDR:-:8112}"
FIXTURE_URL="${BENCH_PK_APISVC_URL:-http://127.0.0.1:8112}"
FIXTURE_KEY="${BENCH_PK_APISVC_KEY:-bench-pk-fixture-key}"
# The world the study's API cells are asked in: it provisions monitors, so the
# question has an answer and the planted count has something to contradict.
WORLD="monitors-3"
PG=e2e-postgres
PIDFILE="build/pollution-api-arm.pid"
APISVC_PID="build/pollution-api-apisvc.pid"
COMMIT="$(git rev-parse HEAD)"
git diff --quiet HEAD 2>/dev/null || COMMIT="${COMMIT}-dirty"

[[ -e "$OUT" ]] && { echo "ERROR: --out $OUT exists; every run keeps its data" >&2; exit 1; }
mkdir -p "$OUT"
exec > >(tee -a "$OUT/arm.log") 2>&1
echo "=== pollution API arm $CELL ==="
echo "commit $COMMIT  database $DB  treatment ${TREATMENT:-none}  world $WORLD  k=$K"

step() { echo; echo "--- $* ---"; }

step "build"
go build -o build/mcp-data-platform-bench ./cmd/mcp-data-platform
(cd bench && go build -o ../build/bench-apisvc ./apisvc &&
	go build -o ../build/bench-apisetup ./apisetup &&
	go build -o ../build/bench-pkrun ./pkrun &&
	go build -o ../build/pollutionplant ./pollutionplant)

step "stop anything already serving"
for p in "$PIDFILE" "$APISVC_PID"; do
	if [[ -f "$p" ]]; then
		kill "$(cat "$p")" 2>/dev/null || true
		while kill -0 "$(cat "$p")" 2>/dev/null; do sleep 1; done
		rm -f "$p"
	fi
done
curl -fsS "$BENCH_URL/readyz" >/dev/null 2>&1 && {
	echo "ERROR: something else is serving $BENCH_URL" >&2
	exit 1
}

step "fresh database $DB"
docker exec "$PG" psql -q -U platform -d postgres -c "DROP DATABASE IF EXISTS $DB WITH (FORCE)"
docker exec "$PG" psql -q -U platform -d postgres -c "CREATE DATABASE $DB OWNER platform"

step "arm config: pk config, own DSN, plus apply_knowledge"
python3 - "$DB" "$CONFIG" <<'PY'
import re, sys
db, out = sys.argv[1], sys.argv[2]
src = "bench/config/platform.bench.pk.yaml"
s = open(src).read()
before = s
s = s.replace("/mcp_bench_pk?sslmode=disable", f"/{db}?sslmode=disable")
if s == before:
    s = re.sub(r"/mcp_platform\?sslmode=disable", f"/{db}?sslmode=disable", s)
# The committed persona grants no apply_knowledge, so no identity can promote
# an insight to the applied tier and the plant cannot build the condition the
# arm measures. Adding it matches the warehouse arm config, which already
# grants it, so the contrast holds the tool surface constant.
s = s.replace('"memory_*"]', '"memory_*", "apply_knowledge"]', 1)
open(out, "w").write(s)
PY
grep -q "/${DB}?sslmode=disable" "$CONFIG" || { echo "ERROR: DSN rewrite failed" >&2; exit 1; }
grep -q "apply_knowledge" "$CONFIG" || { echo "ERROR: apply_knowledge not granted" >&2; exit 1; }
# Exactly two lines differ from the committed config: the DSN and the persona
# allow-list. Anything more means the variant drifted and is measuring a
# different platform.
if [[ $(diff bench/config/platform.bench.pk.yaml "$CONFIG" | grep -c '^[<>]') -ne 4 ]]; then
	echo "ERROR: the arm config differs from the committed pk config by more than the DSN and the tool grant:" >&2
	diff bench/config/platform.bench.pk.yaml "$CONFIG" >&2 || true
	exit 1
fi
cp "$CONFIG" "$OUT/arm-config.yaml"

step "start the perishable fixture service (world $WORLD)"
build/bench-apisvc -addr "$FIXTURE_ADDR" -api-key "$FIXTURE_KEY" \
	-surface perishable -world "$WORLD" >"$OUT/apisvc.log" 2>&1 &
echo $! >"$APISVC_PID"
for _ in $(seq 1 20); do
	curl -fsS -H "X-API-Key: $FIXTURE_KEY" "$FIXTURE_URL/_bench/world" >/dev/null 2>&1 && break
	sleep 1
done
curl -fsS -H "X-API-Key: $FIXTURE_KEY" "$FIXTURE_URL/_bench/world" >"$OUT/fixture-world.json" || {
	echo "ERROR: fixture service not ready" >&2
	tail -20 "$OUT/apisvc.log" >&2
	exit 1
}
grep -q "$WORLD" "$OUT/fixture-world.json" || {
	echo "ERROR: fixture is not in world $WORLD" >&2
	cat "$OUT/fixture-world.json" >&2
	exit 1
}

step "start platform"
API_KEY_ADMIN="$BENCH_KEY" LOG_LEVEL=info OTEL_METRICS_ADDR="$METRICS_ADDR" \
	build/mcp-data-platform-bench --config "$CONFIG" --transport http --address :8098 \
	>"$OUT/platform.log" 2>&1 &
echo $! >"$PIDFILE"
for _ in $(seq 1 40); do
	curl -fsS "$BENCH_URL/readyz" >/dev/null 2>&1 && break
	sleep 1
done
curl -fsS "$BENCH_URL/readyz" >/dev/null 2>&1 || {
	echo "ERROR: platform not ready" >&2
	tail -30 "$OUT/platform.log" >&2
	exit 1
}
trap 'for p in "'"$PIDFILE"'" "'"$APISVC_PID"'"; do [ -f "$p" ] && kill "$(cat "$p")" 2>/dev/null; done' EXIT

step "register the fixture with the platform"
build/bench-apisetup -mode b1 -url "$BENCH_URL" -credential "$BENCH_KEY" \
	-spec bench/specs/pk.json -fixture "$FIXTURE_URL" -fixture-key "$FIXTURE_KEY"

step "truncate gate state"
docker exec "$PG" psql -q -U platform -d "$DB" -c "TRUNCATE search_gate_discovery"

step "store snapshot of the clean stack"
build/pollutionplant -mode store-state -url "$BENCH_URL" -credential "$BENCH_KEY" >"$OUT/store-clean.json"

if [[ -n "$TREATMENT" ]]; then
	step "plant $TREATMENT"
	build/pollutionplant -mode plant -treatment "$TREATMENT" \
		-url "$BENCH_URL" -credential "$BENCH_KEY" -identity-keys 150 \
		-teacher-seq 140 -witness-seq 141 >"$OUT/planted.json"
	cat "$OUT/planted.json"
	step "settle ${SETTLE}s before the first episode"
	sleep "$SETTLE"
fi

step "store snapshot before the eval (7.3 baseline)"
build/pollutionplant -mode store-state -url "$BENCH_URL" -credential "$BENCH_KEY" >"$OUT/store-before-eval.json"

step "evaluate: $TIER, k=$K, monitor-count"
build/bench-pkrun \
	-url "$BENCH_URL" -credential "$BENCH_KEY" \
	-fixture-url "$FIXTURE_URL" -fixture-key "$FIXTURE_KEY" \
	-cells pollution-monitor-count -k "$K" \
	-llm claude-cli -model "$TIER" -identity-keys 150 \
	-disallow-tools "$META_TOOLS" \
	-git-commit "$COMMIT" \
	-out "$OUT/results"

step "store snapshot after the eval (7.3)"
build/pollutionplant -mode store-state -url "$BENCH_URL" -credential "$BENCH_KEY" \
	-baseline "$OUT/store-before-eval.json" >"$OUT/store-after-eval.json"

echo
echo "=== arm $CELL complete: $OUT ==="
