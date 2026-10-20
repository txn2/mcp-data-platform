#!/usr/bin/env bash
# acceptance-services.sh -- start or stop a service an acceptance environment
# runs beside the dev stack (test/acceptance/environments.txt, #2219).
#
#   up <service>    start it, against the dev stack already running
#   down <service>  stop it (its volumes are kept)
#   env <service>   the variables the criteria read it by, as NAME=value lines
#
# clickstack is the ClickStack bundle (deployments/observability/clickstack)
# with the substrate override, as #1900 and #1901 need it. Its settings file is
# written under build/acceptance with fresh secrets and ports clear of the dev
# stack's, so a bundle .env a developer keeps is never read or changed; the
# edge collector's OTLP port is the one environments.txt points the stack at.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# The host's native platform, as dev/start.sh chooses it: an exported
# DOCKER_DEFAULT_PLATFORM would force a multi-arch image's other variant.
export DOCKER_DEFAULT_PLATFORM=""

BUNDLE="deployments/observability/clickstack"
CLICKSTACK_ENV="$ROOT/build/acceptance/clickstack.env"

usage() {
  echo "usage: $0 up|down|env <service>; the services are: clickstack" >&2
  exit 2
}

clickstack_compose() {
  docker compose --env-file "$CLICKSTACK_ENV" -f "$BUNDLE/compose.yaml" -f "$BUNDLE/compose.substrate.yaml" "$@"
}

# clickstack_settings writes the bundle's settings for this run: the dev
# stack's ports from dev/.dev-ports.env (start.sh relocates them when the
# defaults are busy) and the dev database's monitoring role (dev/seed.sql).
clickstack_settings() {
  set -a
  # shellcheck disable=SC1091
  [ -f dev/.dev-ports.env ] && . ./dev/.dev-ports.env
  set +a
  # The secrets are made once and kept: the HyperDX user the seed creates
  # lives in the bundle's volume, so a later run signs in with the same
  # password.
  local key password
  key="$(sed -n 's/^CLICKSTACK_INGESTION_KEY=//p' "$CLICKSTACK_ENV" 2> /dev/null || true)"
  password="$(sed -n 's/^HYPERDX_SEED_PASSWORD=//p' "$CLICKSTACK_ENV" 2> /dev/null || true)"
  mkdir -p "$(dirname "$CLICKSTACK_ENV")"
  cat > "$CLICKSTACK_ENV" << EOF
CLICKSTACK_INGESTION_KEY=${key:-$(openssl rand -hex 24)}
HYPERDX_SEED_EMAIL=admin@example.com
HYPERDX_SEED_PASSWORD=${password:-Acc-$(openssl rand -hex 12)}
EDGE_DEPLOYMENT_ID=acme-dev
EDGE_ENVIRONMENT=dev
EDGE_TAIL_SAMPLE_PERCENT=100
CLICKSTACK_UI_PORT=18080
CLICKSTACK_API_PORT=18000
CLICKSTACK_CLICKHOUSE_PORT=18123
EDGE_OTLP_GRPC_PORT=14317
EDGE_OTLP_HTTP_PORT=14318
SUBSTRATE_PG_HOST=host.docker.internal
SUBSTRATE_PG_PORT=${DEV_PG_PORT:-5432}
SUBSTRATE_PG_USER=otel_monitor
SUBSTRATE_PG_PASSWORD=otel_monitor_dev
SUBSTRATE_PG_DATABASE=mcp_platform
SUBSTRATE_PG_SSLMODE=disable
SUBSTRATE_PG_TLS_INSECURE=true
SUBSTRATE_SEAWEEDFS_METRICS=host.docker.internal:${DEV_SEAWEEDFS_METRICS_PORT:-9327}
EOF
}

[ $# -eq 2 ] || usage
case "$2" in
  clickstack) ;;
  *) usage ;;
esac

case "$1" in
  up)
    clickstack_settings
    # --wait returns once every service with a healthcheck is healthy; the
    # seed then bootstraps the HyperDX user the criteria sign in as.
    clickstack_compose up -d --wait clickstack edge-collector
    clickstack_compose up --exit-code-from seed seed
    ;;
  down)
    [ -f "$CLICKSTACK_ENV" ] || exit 0
    clickstack_compose down
    ;;
  env)
    echo "CLICKSTACK_ENV_FILE=$CLICKSTACK_ENV"
    ;;
  *) usage ;;
esac
