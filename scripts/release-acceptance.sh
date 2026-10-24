#!/usr/bin/env bash
# release-acceptance.sh -- run the acceptance suite for `make verify-release`
# against the dev stack, starting one when none answers (#1969).
#
# verify-release runs `verify` and then the acceptance suite. verify's
# frontend-e2e wants port 5173 for its MSW server and the acceptance suite
# wants a running `make dev`, so with the stack up the first refused and with
# it down the second failed. frontend-e2e now runs beside a stack; this covers
# the other half.
#
# The live-stack Playwright suite (frontend-e2e-public-viewer) runs after it
# against the same stack. It is the one browser suite served by the Go binary
# rather than the Vite dev server, so it is the one that loads a portal page
# through internal/ui. Nothing ran it, and v1.147.0 shipped every asset URL
# answering 404 with its "renders a portal asset by range" failing (#2205,
# #2206).
#
# A platform already answering is used as it is and left running: it is the
# caller's stack. With none, dev/start.sh is started in the background, the
# suite runs once it reports ready, and the stack it started is stopped on the
# way out, pass or fail: start.sh's own exit trap, then `make dev-stop`, which
# also stops what that trap misses (Vite under its subshell, the second
# replica's containers). The volumes are kept.
#
# ACCEPTANCE_ENV names an environment in test/acceptance/environments.txt
# (#2219): the stack it starts is given that environment's variables, the
# services the environment lists are started beside it once it is ready
# (scripts/acceptance-services.sh), and the run is that environment's criteria
# alone. The live-stack Playwright suite runs with the plain stack only.
#
# Environment: MCP_BASE_URL (as `make acceptance` reads it), ISSUE,
# ACCEPTANCE_ENV and ACCEPTANCE_SHARD (passed through), RELEASE_DEV_TIMEOUT seconds to wait for the
# stack (default 900).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
LOG_DIR="build/release"
LOG="$LOG_DIR/dev-stack.log"
READY="Press Ctrl-C to stop all services."
TIMEOUT="${RELEASE_DEV_TIMEOUT:-900}"
ACCEPTANCE_ENV="${ACCEPTANCE_ENV:-}"
export ACCEPTANCE_ENV

# The environment's variables reach the stack start.sh starts; an unknown
# environment stops here, naming the known ones.
env_vars="$(python3 scripts/acceptance-env.py vars "$ACCEPTANCE_ENV")"
services="$(python3 scripts/acceptance-env.py services "$ACCEPTANCE_ENV")"
for assignment in $env_vars; do
  export "${assignment?}"
done

# run_suite runs the environment's criteria (one group of them under
# ACCEPTANCE_SHARD), and with the plain stack the live-stack browser suite
# after them, in the first group only.
run_suite() {
  make --no-print-directory acceptance
  case "${ACCEPTANCE_SHARD:-1/}" in 1/*) ;; *) return 0 ;; esac
  if [ -z "$ACCEPTANCE_ENV" ] || [ "$ACCEPTANCE_ENV" = dev ]; then
    make --no-print-directory frontend-e2e-public-viewer
  fi
}

platform_url() {
  set -a
  # shellcheck disable=SC1091
  [ -f dev/.dev-ports.env ] && . ./dev/.dev-ports.env
  set +a
  echo "${MCP_BASE_URL:-http://localhost:${DEV_PROXY_PORT:-${DEV_API_PORT:-8080}}}"
}

answers() {
  curl -sf -m 5 "$(platform_url)/healthz" > /dev/null 2>&1
}

if answers; then
  echo "release-acceptance: a platform answers at $(platform_url); running the suite against it and leaving it up."
  run_suite
  exit 0
fi

mkdir -p "$LOG_DIR"
# verify's embed-clean leaves the portal and content-viewer embed directories
# empty, and the binary start.sh compiles serves what they hold, so the build
# is embedded first: the portal criteria and the live-stack suite read pages
# the binary serves.
echo "release-acceptance: building the portal the binary embeds (log: $LOG_DIR/frontend-build.log)."
make --no-print-directory frontend-build > "$LOG_DIR/frontend-build.log" 2>&1 || {
  echo "FAIL release-acceptance: make frontend-build failed. Last lines of $LOG_DIR/frontend-build.log:" >&2
  tail -n 30 "$LOG_DIR/frontend-build.log" >&2
  exit 1
}
echo "release-acceptance: no platform answers at $(platform_url); starting the dev stack (log: $LOG)."
bash dev/start.sh > "$LOG" 2>&1 &
DEV_PID=$!

stop_stack() {
  for service in $services; do
    bash scripts/acceptance-services.sh down "$service" > "$LOG_DIR/$service-down.log" 2>&1 || true
  done
  echo "release-acceptance: stopping the dev stack it started (data kept)."
  kill -TERM "$DEV_PID" 2> /dev/null || true
  wait "$DEV_PID" 2> /dev/null || true
  make --no-print-directory dev-stop > "$LOG_DIR/dev-stop.log" 2>&1 || true
}
trap stop_stack EXIT
trap 'exit 130' INT TERM

# start.sh colours its output, so the ready line is matched with the colour
# codes taken out.
ready() {
  sed $'s/\x1b\[[0-9;]*m//g' "$LOG" 2> /dev/null | grep -qF "$READY"
}

waited=0
until ready; do
  if ! kill -0 "$DEV_PID" 2> /dev/null; then
    echo "FAIL release-acceptance: the dev stack exited before it was ready. Last lines of $LOG:" >&2
    tail -n 30 "$LOG" >&2
    exit 1
  fi
  if [ "$waited" -ge "$TIMEOUT" ]; then
    echo "FAIL release-acceptance: the dev stack was not ready after ${TIMEOUT}s. Last lines of $LOG:" >&2
    tail -n 30 "$LOG" >&2
    exit 1
  fi
  sleep 5
  waited=$((waited + 5))
done

echo "release-acceptance: dev stack ready at $(platform_url) after ${waited}s."
for service in $services; do
  echo "release-acceptance: starting $service beside the stack (log: $LOG_DIR/$service.log)."
  bash scripts/acceptance-services.sh up "$service" > "$LOG_DIR/$service.log" 2>&1 || {
    echo "FAIL release-acceptance: $service did not start. Last lines of $LOG_DIR/$service.log:" >&2
    tail -n 30 "$LOG_DIR/$service.log" >&2
    exit 1
  }
  for assignment in $(bash scripts/acceptance-services.sh env "$service"); do
    export "${assignment?}"
  done
done

# A stack started on new volumes indexes everything it seeded at once, and a
# criterion waiting on the index job of a file it just wrote waits behind all
# of it: on a CPU-only runner that took longer than the criterion waits
# (#2276). The criteria start once the first pass has drained, or after ten
# minutes, saying so.
pending=""
for _ in $(seq 1 120); do
  pending="$(docker exec acme-dev-postgres psql -U platform -d mcp_platform -tAc \
    "SELECT count(*) FROM index_jobs WHERE status IN ('pending', 'running')" 2> /dev/null || true)"
  [ "$pending" = 0 ] && break
  sleep 5
done
echo "release-acceptance: index jobs pending or running before the criteria: ${pending:-unknown}."
run_suite
