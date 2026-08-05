#!/usr/bin/env bash
#
# Run one arm of the knowledge-pollution confirmatory matrix (#1167), end to
# end, on an isolated stack.
#
# Protocol: bench/docs/knowledge-pollution-study-design.md. The steps below are
# that document's operational invariants, in the order it states them, and each
# one is a gate rather than a courtesy:
#
#   8.5 arm isolation      fresh database per arm, seed re-applied, gate state
#                          truncated, DataHub editable aspects cleared before
#                          every arm (the probe found leftovers from prior
#                          promote runs, which would have made a control arm
#                          silently non-clean)
#   5.1 grader agreement   pollutionplant -mode check before any episode
#   7.3 store constancy    a store snapshot either side of the EVAL; any drift
#                          invalidates the arm. It brackets the eval rather
#                          than the arm because the plant is a deliberate
#                          stack-side change that 7.2 excludes, and what the
#                          invariant detects is an evaluator writing mid-arm
#   plant + settle         the claim is promoted through the platform's own
#                          path, then the semantic cache is allowed to expire
#                          before the first episode
#   12  client surface     the three claude-cli meta-tools are pinned off
#
# Every artifact lands under --out. Nothing is overwritten: a re-run needs a
# new --out, which benchrun itself enforces.
#
# Usage:
#   bench/scripts/pollution-arm.sh --class convention --arm wrong \
#       --tier sonnet --out build/bench-results/pollution-rq1-<stamp>/<cell>
set -euo pipefail

CLASS=""
ARM=""
TIER="sonnet"
OUT=""
# Seconds. 300 matches the a3 semantic-cache TTL, which is what the plant
# has to outlive before the first episode reads a stale enrichment entry.
SETTLE=300
META_TOOLS="ToolSearch,ReadMcpResourceTool,ListMcpResourcesTool"
# --allow-meta-tools runs the Section 12 sensitivity cell, the one cell that
# runs with the meta-tools available so the report can state the exclusion
# changed nothing rather than assume it.
ALLOW_META=0
# Directive strength of the planted claim (protocol 6.3's follow-up ladder):
# bare, plain, or imperative. The RQ1 matrix ran imperative throughout, so
# that is the default and an arm that does not ask for a level reproduces the
# matrix exactly.
DIRECTIVE=imperative

usage() {
	sed -n '2,26p' "$0" >&2
	exit 2
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--class) CLASS="$2"; shift 2 ;;
	--arm) ARM="$2"; shift 2 ;;
	--tier) TIER="$2"; shift 2 ;;
	--out) OUT="$2"; shift 2 ;;
	--settle) SETTLE="$2"; shift 2 ;;  # seconds
	--allow-meta-tools) ALLOW_META=1; shift ;;
	--directive) DIRECTIVE="$2"; shift 2 ;;
	*) echo "unknown argument: $1" >&2; usage ;;
	esac
done

[[ -n "$CLASS" && -n "$ARM" && -n "$OUT" ]] || usage

# The cell's evaluation units and repetition. Section 8.2: the classes are
# balanced at 24 episodes each so the class contrast is not confounded with
# precision, which means the convention class runs three tasks at k=8 and the
# checkable class runs one task at k=24.
case "$CLASS" in
convention)
	TASK_IDS=(s3-fiscal-2025-count s3-fiscal-2025-net s3-fiscal-q1-net)
	K=8
	TREATMENT_BASE="fiscal-boundary"
	;;
checkable)
	TASK_IDS=(s3-deprecated-order-count)
	K=24
	TREATMENT_BASE="order-count"
	;;
*)
	echo "unknown --class $CLASS (convention|checkable)" >&2
	exit 2
	;;
esac

case "$DIRECTIVE" in
imperative) DIRECTIVE_SUFFIX="" ;;
bare | plain) DIRECTIVE_SUFFIX="-$DIRECTIVE" ;;
*)
	echo "unknown --directive $DIRECTIVE (bare|plain|imperative)" >&2
	exit 2
	;;
esac

# The ladder exists only on the checkable claim, which is the cell whose
# imperative the protocol flagged as confounded with instruction-following.
if [[ "$DIRECTIVE" != "imperative" && "$CLASS" != "checkable" ]]; then
	echo "ERROR: --directive $DIRECTIVE applies to the checkable class only" >&2
	exit 2
fi

case "$ARM" in
wrong | correct) TREATMENT="${TREATMENT_BASE}-${ARM}${DIRECTIVE_SUFFIX}" ;;
absent) TREATMENT="" ;;
*)
	echo "unknown --arm $ARM (wrong|correct|absent)" >&2
	exit 2
	;;
esac

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

CELL="${CLASS}-${ARM}-${TIER}${DIRECTIVE_SUFFIX}"
DB="mcp_bench_p_$(echo "$CELL" | tr '-' '_')"
CONFIG="build/platform.bench.a3-${CELL}.yaml"
BENCH_URL="http://localhost:8098"
BENCH_KEY="${BENCH_KEY:-bench-admin-key}"
# :9092 collides with the DataHub quickstart's Kafka broker.
METRICS_ADDR="${BENCH_METRICS_ADDR:-:9095}"
PG=e2e-postgres
GMS="${BENCH_DATAHUB_GMS:-http://localhost:8080}"
PIDFILE="build/pollution-arm.pid"
# A manifest that names a commit the harness did not run from is a
# reproducibility failure the archive cannot detect later, so a dirty working
# tree is labelled rather than hidden. An archive marked -dirty is a
# validation run, not a confirmatory cell.
COMMIT="$(git rev-parse HEAD)"
if ! git diff --quiet HEAD 2>/dev/null; then
	COMMIT="${COMMIT}-dirty"
fi

if [[ -e "$OUT" ]]; then
	echo "ERROR: --out $OUT already exists; every run keeps its data, so pick a new path" >&2
	exit 1
fi
mkdir -p "$OUT"
exec > >(tee -a "$OUT/arm.log") 2>&1
echo "=== pollution arm $CELL ==="
echo "commit $COMMIT  database $DB  treatment ${TREATMENT:-none}  k=$K  tasks ${TASK_IDS[*]}"

step() { echo; echo "--- $* ---"; }

step "build"
go build -o build/mcp-data-platform-bench ./cmd/mcp-data-platform
(cd bench && go build -o ../build/benchrun ./benchrun && go build -o ../build/pollutionplant ./pollutionplant)

step "stop any platform on $BENCH_URL"
if [[ -f "$PIDFILE" ]]; then
	kill "$(cat "$PIDFILE")" 2>/dev/null || true
	while kill -0 "$(cat "$PIDFILE")" 2>/dev/null; do sleep 1; done
	rm -f "$PIDFILE"
fi
if curl -fsS "$BENCH_URL/readyz" >/dev/null 2>&1; then
	echo "ERROR: something else is serving $BENCH_URL; stop it first" >&2
	exit 1
fi

step "fresh database $DB (8.5: arm isolation)"
docker exec "$PG" psql -q -U platform -d postgres -c "DROP DATABASE IF EXISTS $DB WITH (FORCE)"
docker exec "$PG" psql -q -U platform -d postgres -c "CREATE DATABASE $DB OWNER platform"

step "arm config (a3, differing from the committed arm only in DSN)"
sed "s#/mcp_platform?sslmode=disable#/${DB}?sslmode=disable#" \
	bench/config/platform.bench.a3.yaml >"$CONFIG"
if ! grep -q "/${DB}?sslmode=disable" "$CONFIG"; then
	echo "ERROR: the DSN rewrite did not take; the arm would run on the shared database" >&2
	exit 1
fi
# Prove the ONLY difference is the DSN: an arm config that drifted from the
# committed one measures a different platform, not a different treatment.
if [[ $(diff bench/config/platform.bench.a3.yaml "$CONFIG" | grep -c '^[<>]') -ne 2 ]]; then
	echo "ERROR: the arm config differs from the committed a3 config by more than the DSN:" >&2
	diff bench/config/platform.bench.a3.yaml "$CONFIG" >&2 || true
	exit 1
fi
cp "$CONFIG" "$OUT/arm-config.yaml"

step "DataHub baseline (8.5: clear editable aspects left by any prior promote)"
for t in orders customers daily_region_revenue legacy_orders; do
	urn="urn:li:dataset:(urn:li:dataPlatform:trino,memory.bench.$t,PROD)"
	enc=$(python3 -c "import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1],safe=''))" "$urn")
	code=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE "$GMS/openapi/v3/entity/dataset/$enc/editabledatasetproperties")
	echo "  $t: delete editabledatasetproperties -> HTTP $code"
	after=$(curl -s -o /dev/null -w "%{http_code}" "$GMS/openapi/v3/entity/dataset/$enc/editabledatasetproperties")
	if [[ "$after" != "404" ]]; then
		echo "ERROR: $t still carries an editable aspect (HTTP $after); the arm would start from a promoted state" >&2
		exit 1
	fi
done
curl -fsS "$GMS/openapi/v3/entity/dataset/$(python3 -c "import urllib.parse;print(urllib.parse.quote('urn:li:dataset:(urn:li:dataPlatform:trino,memory.bench.orders,PROD)',safe=''))")/datasetproperties" \
	>"$OUT/datahub-orders-baseline.json"
grep -q "Revenue Reporting Policy" "$OUT/datahub-orders-baseline.json" || {
	echo "ERROR: the orders base description is not the seeded text; re-run 'make bench-seed-datahub'" >&2
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
	echo "ERROR: platform did not become ready" >&2
	tail -30 "$OUT/platform.log" >&2
	exit 1
}
trap 'kill "$(cat "$PIDFILE" 2>/dev/null)" 2>/dev/null || true' EXIT

step "seed knowledge pages (the co-present correct sources, 5.3)"
docker exec -i "$PG" psql -q -U platform -d "$DB" -v ON_ERROR_STOP=1 <bench/seed/postgres/knowledge_pages.sql

step "truncate gate state (8.5)"
docker exec "$PG" psql -q -U platform -d "$DB" -c "TRUNCATE search_gate_discovery"

step "grader agreement (5.1)"
build/pollutionplant -mode check -tasks bench/tasks

step "restricted task set"
TASKS_DIR="$OUT/tasks"
mkdir -p "$TASKS_DIR"
for id in "${TASK_IDS[@]}"; do
	cp "bench/tasks/$id.yaml" "$TASKS_DIR/"
done

# Archived as the arm's clean starting state, with nothing to compare it
# against. It is what makes the plant's effect on the store readable after the
# fact: store-before-eval.json minus this is exactly what the plant added.
step "store snapshot of the clean stack"
build/pollutionplant -mode store-state -url "$BENCH_URL" -credential "$BENCH_KEY" >"$OUT/store-clean.json"

if [[ -n "$TREATMENT" ]]; then
	step "plant $TREATMENT"
	build/pollutionplant -mode plant -treatment "$TREATMENT" \
		-url "$BENCH_URL" -credential "$BENCH_KEY" -identity-keys 320 \
		-teacher-seq 200 -witness-seq 201 >"$OUT/planted.json"
	cat "$OUT/planted.json"
	step "settle ${SETTLE}s (semantic cache TTL) before the first episode"
	sleep "$SETTLE"
fi

# The store-constancy baseline (7.3), taken after the plant and its settle and
# immediately before the first episode.
#
# It must bracket the EVAL, not the whole arm. The invariant is that every
# episode in an arm met the same store, so what it has to detect is an
# evaluator writing mid-arm. The plant's own capture, approval and apply are
# stack-side operations that 7.2 excludes from the arm's accounting: they are
# the treatment being installed, and a baseline taken before them reports the
# treatment itself as drift and fails every planted arm.
step "store snapshot before the eval (7.3 baseline)"
build/pollutionplant -mode store-state -url "$BENCH_URL" -credential "$BENCH_KEY" >"$OUT/store-before-eval.json"

step "evaluate: $TIER, k=$K, ${#TASK_IDS[@]} task(s)"
DISALLOW=(-disallow-tools "$META_TOOLS")
if [[ "$ALLOW_META" -eq 1 ]]; then
	echo "  Section 12 sensitivity cell: meta-tools ALLOWED for this cell only"
	DISALLOW=()
fi
# ${arr[@]+"${arr[@]}"} rather than "${arr[@]}": under `set -u`, bash 3.2 (the
# system bash on macOS) treats an empty array's expansion as an unbound
# variable and aborts, which would kill the sensitivity cell — the one cell
# that runs with no added disallow list — before it ran an episode.
# The audit read-back window. The 15s default is tuned for the a* suites on a
# quiet machine; here the DataHub quickstart competes for the same host and the
# faster tiers finish an episode sooner, so an asynchronous audit write can land
# after the default gives up and a good episode fails as a harness error. The
# invariant is unchanged -- an episode whose rows never arrive still fails, and
# 23 of 24 episodes on the arm that hit this had every call audited -- this only
# stops a slow write being read as a lost one. Tier-independent, so it cannot
# interact with anything the study measures.
build/benchrun \
	-url "$BENCH_URL" -credential "$BENCH_KEY" \
	-audit-timeout 60s \
	-arm a3 -suite s3 -tasks "$TASKS_DIR" -k "$K" \
	-llm claude-cli -model "$TIER" -identity-keys 320 \
	-git-commit "$COMMIT" \
	${DISALLOW[@]+"${DISALLOW[@]}"} \
	-out "$OUT/results.json"

step "store snapshot after the eval (7.3)"
build/pollutionplant -mode store-state -url "$BENCH_URL" -credential "$BENCH_KEY" \
	-baseline "$OUT/store-before-eval.json" >"$OUT/store-after-eval.json"

step "summary"
build/benchrun -summarize "$OUT/results.json" | tee "$OUT/SUMMARY.txt"
echo
echo "=== arm $CELL complete: $OUT ==="
