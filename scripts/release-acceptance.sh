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
# A platform already answering is used as it is and left running: it is the
# caller's stack. With none, dev/start.sh is started in the background, the
# suite runs once it reports ready, and the stack it started is stopped on the
# way out, pass or fail: start.sh's own exit trap, then `make dev-stop`, which
# also stops what that trap misses (Vite under its subshell, the second
# replica's containers). The volumes are kept.
#
# Environment: MCP_BASE_URL (as `make acceptance` reads it), ISSUE (passed
# through), RELEASE_DEV_TIMEOUT seconds to wait for the stack (default 900).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
LOG_DIR="build/release"
LOG="$LOG_DIR/dev-stack.log"
READY="Press Ctrl-C to stop all services."
TIMEOUT="${RELEASE_DEV_TIMEOUT:-900}"

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
  exec make --no-print-directory acceptance
fi

mkdir -p "$LOG_DIR"
echo "release-acceptance: no platform answers at $(platform_url); starting the dev stack (log: $LOG)."
bash dev/start.sh > "$LOG" 2>&1 &
DEV_PID=$!

stop_stack() {
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
make --no-print-directory acceptance
