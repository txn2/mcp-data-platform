#!/bin/zsh
# Gate probe (study-3 due diligence): does the agent discover spontaneously
# when the search-first gate does not force it?
#
# Four runs, all CELLS=bridge K=8, claude-cli (no metered cost), gate OFF
# (bench/config/platform.bench.pk-gateoff.yaml, the pk arm's single-deviation
# copy), crossing model {sonnet, haiku} x scaffold {default, no-discovery}.
# The no-discovery scaffold drops the harness's own "use the search tool"
# bullet, leaving only the platform's steering channels (platform_info
# agent_instructions, tool descriptions). Gate-ON baselines are the archived
# bridge runs (pk-bridge-20260725-135349, pk-bridge-sonnet-v1116, pk-bridge-haiku).
#
# Each run starts from a dropped mcp_bench_pk database: pkrun's contamination
# preflight requires clean identities, and sequential runs reuse the same pool
# seq numbers.
set -euo pipefail
cd "$(dirname "$0")/../.."

BASEVARS=(BENCH_PG=skip BENCH_PG_CONTAINER=acme-dev-postgres BENCH_METRICS_ADDR=:9095)
CONFIG_GATEOFF=bench/config/platform.bench.pk-gateoff.yaml
CONFIG_GATEON=bench/config/platform.bench.pk.yaml
GIT_COMMIT=$(git rev-parse HEAD)

(cd bench && go build -o ../build/bench-pkrun ./pkrun)

run_arm() {
  local name=$1 model=$2 scaffold=$3 cells=${4:-bridge} config=${5:-$CONFIG_GATEOFF}
  local MAKEVARS=("${BASEVARS[@]}" "BENCH_PK_CONFIG=${config}")
  local dir="build/bench-results/pk-gateprobe-${name}-$(date +%Y%m%d-%H%M%S)"
  echo "=== arm ${name}: model=${model} scaffold=${scaffold} cells=${cells} config=${config} -> ${dir}"
  make bench-pk-down "${MAKEVARS[@]}" || true
  # bench-pk-down returns before the platform finishes its 25s drain and
  # deletes the pid files bench-pk-up would otherwise wait on. The draining
  # process holds the METRICS port (:9095) to the very end, so wait for the
  # process itself and every port it binds (8098 MCP, 8112 fixture, 9095
  # metrics) or the next platform fails with "bind: address already in use".
  for i in {1..40}; do
    if ! pgrep -f 'mcp-data-platform-bench --config' >/dev/null 2>&1 \
       && ! lsof -ti :8098 -ti :8112 -ti :9095 >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  if pgrep -f 'mcp-data-platform-bench --config' >/dev/null 2>&1 \
     || lsof -ti :8098 -ti :8112 -ti :9095 >/dev/null 2>&1; then
    echo "ERROR: old platform or its ports (8098/8112/9095) still held after 40s" >&2
    exit 1
  fi
  docker exec acme-dev-postgres psql -U platform -d postgres \
    -c "DROP DATABASE IF EXISTS mcp_bench_pk WITH (FORCE)"
  make bench-pk-up "${MAKEVARS[@]}"
  mkdir -p "${dir}"
  build/bench-pkrun \
    -url http://localhost:8098 \
    -credential bench-admin-key \
    -fixture-url http://127.0.0.1:8112 \
    -fixture-key bench-pk-fixture-key \
    -identity-keys 150 \
    -git-commit "${GIT_COMMIT}" \
    -cells "${cells}" -k 8 \
    -model "${model}" -scaffold "${scaffold}" \
    -out "${dir}"
  echo "=== arm ${name} done"
}

# Arms may be passed as names on the command line to resume a partial
# sweep; no arguments runs the full original crossing. dir-* arms run the
# directive-twin cells (gate probe stage 2): same convention dependence,
# prompt names the exact endpoint, so discovery has no visible motive.
declare -A ARM_MODEL=( [goff-sdef-sonnet]=sonnet [goff-snodisc-sonnet]=sonnet
                       [goff-sdef-haiku]=haiku   [goff-snodisc-haiku]=haiku
                       [goff-snodisc-opus]=opus
                       [dir-goff-sdef-sonnet]=sonnet [dir-goff-snodisc-sonnet]=sonnet
                       [dir-gon-snodisc-sonnet]=sonnet [dir-goff-snodisc-opus]=opus )
declare -A ARM_SCAFFOLD=( [goff-sdef-sonnet]=default [goff-snodisc-sonnet]=no-discovery
                          [goff-sdef-haiku]=default  [goff-snodisc-haiku]=no-discovery
                          [goff-snodisc-opus]=no-discovery
                          [dir-goff-sdef-sonnet]=default [dir-goff-snodisc-sonnet]=no-discovery
                          [dir-gon-snodisc-sonnet]=no-discovery [dir-goff-snodisc-opus]=no-discovery )
declare -A ARM_CELLS=( [dir-goff-sdef-sonnet]=bridge-directive [dir-goff-snodisc-sonnet]=bridge-directive
                       [dir-gon-snodisc-sonnet]=bridge-directive [dir-goff-snodisc-opus]=bridge-directive )
declare -A ARM_CONFIG=( [dir-gon-snodisc-sonnet]="${CONFIG_GATEON}" )
ARMS=(goff-sdef-sonnet goff-snodisc-sonnet goff-sdef-haiku goff-snodisc-haiku)
if (( $# > 0 )); then ARMS=("$@"); fi

for arm in "${ARMS[@]}"; do
  run_arm "${arm}" "${ARM_MODEL[$arm]}" "${ARM_SCAFFOLD[$arm]}" \
    "${ARM_CELLS[$arm]:-bridge}" "${ARM_CONFIG[$arm]:-$CONFIG_GATEOFF}"
done

make bench-pk-down "${MAKEVARS[@]}" || true
echo "=== gate probe complete"
