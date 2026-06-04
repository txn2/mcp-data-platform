#!/usr/bin/env bash
set -euo pipefail

# Load .env file if present (credentials for remote backends).
if [ -f .env ]; then
  set -a
  source .env
  set +a
fi

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

# PIDs to clean up on exit
PIDS=()
cleanup() {
  echo ""
  echo -e "${YELLOW}Shutting down...${NC}"
  for pid in "${PIDS[@]+"${PIDS[@]}"}"; do
    kill "$pid" 2>/dev/null || true
  done
  docker compose -f dev/docker-compose.yml stop 2>/dev/null || true
  wait 2>/dev/null || true
  echo -e "${GREEN}Done.${NC}"
}
trap cleanup EXIT INT TERM

fail() {
  echo -e "${RED}FAIL: $1${NC}" >&2
  exit 1
}

ok() {
  echo -e "  ${GREEN}✓${NC} $1"
}

info() {
  echo -e "  ${CYAN}…${NC} $1"
}

# ─── Pre-flight checks ──────────────────────────────────────────────

echo -e "${BOLD}Pre-flight checks${NC}"

# Docker
docker info > /dev/null 2>&1 || fail "Docker is not running. Start Docker Desktop and try again."
ok "Docker is running"

# Required tools
which air > /dev/null 2>&1 || fail "air not found. Install: go install github.com/air-verse/air@latest"
ok "air installed"

# python3 is used to build connection-registration JSON bodies safely
# (no shell interpolation of model-supplied secrets / spec text).
which python3 > /dev/null 2>&1 || fail "python3 not found. Required for fixture connection registration."
ok "python3 available"

# Node modules
if [ ! -d ui/node_modules ]; then
  info "Installing UI dependencies..."
  (cd ui && npm ci --silent)
fi
ok "UI dependencies ready"

# Port checks (8080 = platform, 5173 = vite, 5432 = postgres,
# 9000 = seaweedfs, 9090 = keycloak, 9091 = prometheus, 9180 = dev-mcp-mock
# OAuth, 9181 = dev-mcp-mock MCP, 9281 = mcp-test fixture, 9282 = api-test
# fixture, 9464 = platform /metrics scrape endpoint)
for port in 5432 8080 5173 9000 9090 9091 9180 9181 9281 9282 9464; do
  if lsof -i ":$port" -sTCP:LISTEN > /dev/null 2>&1; then
    fail "Port $port is already in use. Run 'make dev-down' or stop the conflicting process."
  fi
done
ok "Ports 5432, 8080, 5173, 9000, 9090, 9091, 9180, 9181, 9281, 9282, 9464 are free"

echo ""

# ─── Start Docker services ──────────────────────────────────────────

echo -e "${BOLD}Starting Docker services${NC}"
# Bring up Postgres first so we can ensure auxiliary databases (keycloak)
# exist before dependent services boot. The fixture init script handles
# this on a fresh volume, but reused volumes from prior `make dev` runs
# pre-date the keycloak database — create it idempotently here.
docker compose -f dev/docker-compose.yml up -d postgres 2>&1 | grep -v "^$" | sed 's/^/  /'

# Wait for PostgreSQL. We require a REAL connection via the final
# unix socket (/var/run/postgresql/.s.PGSQL.5432) with the `platform`
# role — not just `pg_isready`, which returns ready during the
# Postgres image's temporary-postmaster init phase before the final
# socket and roles are in place.
PG_CONTAINER="acme-dev-postgres"
info "Waiting for PostgreSQL..."
for i in $(seq 1 60); do
  if docker exec -u postgres "$PG_CONTAINER" psql -U platform -d mcp_platform -tAc 'SELECT 1' > /dev/null 2>&1; then
    break
  fi
  if [ "$i" -eq 60 ]; then
    fail "PostgreSQL did not become ready within 60s"
  fi
  sleep 1
done
ok "PostgreSQL ready on :5432"

# Idempotently ensure the auxiliary databases exist. The fixture init
# script creates these on first volume bring-up; this block handles
# upgrades where a pre-existing volume pre-dates a new database.
# psql runs as the in-container `postgres` superuser so it uses the
# image's local unix socket without needing to wait for TCP binding.
for db in mcp_test apitest keycloak; do
  exists=$(docker exec -u postgres "$PG_CONTAINER" psql -U platform -d mcp_platform -tAc \
    "SELECT 1 FROM pg_database WHERE datname='$db'" 2>/dev/null || true)
  if [ "$exists" != "1" ]; then
    info "Creating auxiliary database '$db'..."
    docker exec -u postgres "$PG_CONTAINER" psql -U platform -d mcp_platform \
      -c "CREATE DATABASE $db" > /dev/null
  fi
done
ok "Auxiliary databases present (mcp_test, apitest, keycloak)"

# Now bring up the remaining services. Keycloak depends_on postgres
# (already healthy), so this is a no-op for postgres and pulls/starts
# everything else in parallel.
docker compose -f dev/docker-compose.yml up -d 2>&1 | grep -v "^$" | sed 's/^/  /'

# Wait for SeaweedFS (S3 returns 403 on GET /, so check for any HTTP response)
info "Waiting for SeaweedFS..."
for i in $(seq 1 30); do
  HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:9000/ 2>/dev/null || echo "000")
  if [ "$HTTP_CODE" != "000" ]; then
    break
  fi
  if [ "$i" -eq 30 ]; then
    fail "SeaweedFS did not become ready within 30s"
  fi
  sleep 1
done
ok "SeaweedFS ready on :9000"

# Wait for Prometheus (powers the admin Dashboard's API Gateway tab via
# the platform's PromQL proxy). Scrapes the platform's /metrics on the
# host; data appears in the portal once gateway traffic is generated.
info "Waiting for Prometheus..."
for i in $(seq 1 30); do
  if curl -sf http://localhost:9091/-/healthy > /dev/null 2>&1; then
    break
  fi
  if [ "$i" -eq 30 ]; then
    fail "Prometheus did not become ready within 30s (check 'docker logs acme-dev-prometheus')"
  fi
  sleep 1
done
ok "Prometheus ready on :9091"

# Wait for Keycloak. First boot performs realm import + DB schema
# creation, which is slow (90s on a fresh volume); steady-state
# restarts come up in <10s. Realm endpoint is checked rather than
# / because Keycloak's root returns a redirect even before the
# realm is ready.
info "Waiting for Keycloak (first boot imports realm — up to 120s)..."
for i in $(seq 1 120); do
  if curl -sf http://localhost:9090/realms/mcp-platform/.well-known/openid-configuration > /dev/null 2>&1; then
    break
  fi
  if [ "$i" -eq 120 ]; then
    fail "Keycloak did not become ready within 120s (check 'docker logs acme-dev-keycloak')"
  fi
  sleep 1
done
ok "Keycloak ready on :9090 (realm mcp-platform)"

# Wait for the mcp-test fixture. First run pulls the image, which can
# take longer than the other waits — give it up to 60s before failing.
info "Waiting for mcp-test fixture..."
for i in $(seq 1 60); do
  if curl -sf http://localhost:9281/healthz > /dev/null 2>&1; then
    break
  fi
  if [ "$i" -eq 60 ]; then
    fail "mcp-test fixture did not become healthy within 60s (check 'docker logs acme-dev-mcp-test')"
  fi
  sleep 1
done
ok "mcp-test fixture ready on :9281"

# Wait for the api-test fixture.
info "Waiting for api-test fixture..."
for i in $(seq 1 60); do
  if curl -sf http://localhost:9282/healthz > /dev/null 2>&1; then
    break
  fi
  if [ "$i" -eq 60 ]; then
    fail "api-test fixture did not become healthy within 60s (check 'docker logs acme-dev-api-test')"
  fi
  sleep 1
done
ok "api-test fixture ready on :9282"

# Create the portal-assets S3 bucket (requires aws CLI)
if which aws > /dev/null 2>&1; then
  info "Creating S3 bucket..."
  for i in $(seq 1 30); do
    if AWS_ACCESS_KEY_ID=dev-access-key AWS_SECRET_ACCESS_KEY=dev-secret-key \
       aws --endpoint-url http://localhost:9000 s3 ls s3://portal-assets 2>/dev/null || \
       AWS_ACCESS_KEY_ID=dev-access-key AWS_SECRET_ACCESS_KEY=dev-secret-key \
       aws --endpoint-url http://localhost:9000 s3 mb s3://portal-assets 2>/dev/null; then
      break
    fi
    if [ "$i" -eq 30 ]; then
      fail "Could not create S3 bucket after 30s"
    fi
    sleep 1
  done
  ok "S3 bucket portal-assets ready"
else
  echo -e "  ${YELLOW}⚠${NC} aws CLI not found — S3 bucket not created. Install: brew install awscli"
fi

echo ""

# ─── Start dev-mcp-mock (mock upstream + OAuth provider) ────────────

echo -e "${BOLD}Starting dev-mcp-mock${NC}"
MOCK_LOG="/tmp/mcp-dev-mock.log"
go run ./cmd/dev-mcp-mock > "$MOCK_LOG" 2>&1 &
PIDS+=($!)
info "Compiling and starting mock (first run takes a moment)..."
for i in $(seq 1 30); do
  if curl -sf http://localhost:9181/.health > /dev/null 2>&1; then
    break
  fi
  if [ "$i" -eq 30 ]; then
    echo -e "  ${RED}dev-mcp-mock log (last 10 lines):${NC}"
    tail -10 "$MOCK_LOG" 2>/dev/null | sed 's/^/    /'
    fail "dev-mcp-mock did not become healthy within 30s"
  fi
  sleep 1
done
ok "dev-mcp-mock ready on :9180 (OAuth) and :9181 (MCP)"

echo ""

# ─── Start Go server with hot-reload ────────────────────────────────

echo -e "${BOLD}Starting Go server (air)${NC}"
AIR_LOG="/tmp/mcp-dev-air.log"
# Pin the /metrics scrape endpoint to :9464. The default (:9090) collides
# with Keycloak in this stack; the dev Prometheus scrapes this port.
export OTEL_METRICS_ADDR=":9464"
air -c dev/.air.toml > "$AIR_LOG" 2>&1 &
PIDS+=($!)

# Wait for server health
info "Building and starting server (this may take a moment on first run)..."
for i in $(seq 1 60); do
  if curl -sf http://localhost:8080/healthz > /dev/null 2>&1; then
    break
  fi
  if [ "$i" -eq 60 ]; then
    echo -e "  ${RED}Air log (last 20 lines):${NC}"
    tail -20 "$AIR_LOG" 2>/dev/null | sed 's/^/    /'
    fail "Go server did not become healthy within 60s"
  fi
  sleep 1
done
ok "Go server ready on :8080"

echo ""

# ─── Seed data ──────────────────────────────────────────────────────

echo -e "${BOLD}Seeding development data${NC}"
docker cp dev/seed.sql "$PG_CONTAINER":/tmp/seed.sql
docker exec "$PG_CONTAINER" psql -U platform -d mcp_platform -f /tmp/seed.sql > /dev/null 2>&1
ok "Database seeded"
bash dev/seed-s3.sh
ok "Asset content uploaded to S3"

# Register the dev-mock MCP gateway connection through the admin API.
# Going through the admin API (rather than just an INSERT in seed.sql)
# triggers the toolkit's AddConnection path, which discovers the
# upstream's tools and registers them on the live MCP server. A direct
# DB insert would only be picked up after a platform restart.
info "Registering dev-mock gateway connection..."
DEVMOCK_BODY='{"config":{"endpoint":"http://localhost:9181/mcp","auth_mode":"bearer","credential":"static-bearer-token","connection_name":"dev-mock","connect_timeout":"5s","call_timeout":"5s"},"description":"Dev fixture: cmd/dev-mcp-mock — echo, add, now"}'
DEVMOCK_HTTP=$(curl -s -o /dev/null -w "%{http_code}" -X PUT \
  -H "X-API-Key: acme-dev-key-2024" \
  -H "Content-Type: application/json" \
  -d "$DEVMOCK_BODY" \
  http://localhost:8080/api/v1/admin/connection-instances/mcp/dev-mock || echo "000")
if [ "$DEVMOCK_HTTP" = "200" ] || [ "$DEVMOCK_HTTP" = "201" ]; then
  ok "dev-mock gateway connection registered (tools: dev-mock__echo, dev-mock__add, dev-mock__now)"
else
  echo -e "  ${YELLOW}⚠${NC} dev-mock connection register returned HTTP $DEVMOCK_HTTP — admin API may not be ready"
fi

# Register the mcp-test fixture as an MCP-gateway connection. Same
# rationale as dev-mock: go through the admin API so AddConnection
# discovers the upstream's tools and registers them live. mcp-test
# serves streamable HTTP at "/", so the endpoint includes a trailing
# slash; api-key auth uses the X-API-Key header (matches the fixture's
# api_keys.file entry, default placement).
info "Registering mcp-test fixture connection..."
MCPTEST_DEV_KEY_VAL="${MCPTEST_DEV_KEY:-mcptest-dev-key-2024}"
MCPTEST_BODY=$(MCPTEST_DEV_KEY_VAL="$MCPTEST_DEV_KEY_VAL" python3 -c '
import json, os
key = os.environ.get("MCPTEST_DEV_KEY_VAL", "")
body = {
    "config": {
        "endpoint": "http://localhost:9281/",
        "auth_mode": "api_key",
        "credential": key,
        "connection_name": "mcp-test-fixture",
        "connect_timeout": "5s",
        "call_timeout": "10s",
    },
    "description": "Dev fixture: ghcr.io/plexara/mcp-test — identity, data, failure, streaming groups",
}
print(json.dumps(body))
')
MCPTEST_HTTP=$(curl -s -o /dev/null -w "%{http_code}" -X PUT \
  -H "X-API-Key: acme-dev-key-2024" \
  -H "Content-Type: application/json" \
  -d "$MCPTEST_BODY" \
  http://localhost:8080/api/v1/admin/connection-instances/mcp/mcp-test-fixture || echo "000")
if [ "$MCPTEST_HTTP" = "200" ] || [ "$MCPTEST_HTTP" = "201" ]; then
  ok "mcp-test fixture connection registered (tools: mcp-test-fixture__*)"
else
  echo -e "  ${YELLOW}⚠${NC} mcp-test connection register returned HTTP $MCPTEST_HTTP — admin API may not be ready"
fi

# Register the api-test fixture as an apigateway connection.
#
# api-test v1.1.0+ publishes its OpenAPI 3.1 spec at /openapi.yaml. We
# pull it and store it as the default component of an api-catalog
# named `api-test-fixture`, then point the connection at that catalog
# via config.catalog_id. The toolkit reads the spec from the catalog
# at request time so api_list_endpoints returns the 14 operations the
# fixture serves (whoami, headers, fixed/{key}, sized, lorem,
# status/{code}, slow, flaky, echo). Operations in the spec are
# pathed under /v1/..., so base_url omits the /v1 suffix; the gateway
# joins base_url + the operation path verbatim.
#
# The platform sends the api key as the X-API-Key header (default
# placement). If the OpenAPI fetch fails (older image, transient
# error), we still register the connection without a catalog so
# api_invoke_endpoint works against direct paths.
info "Fetching api-test OpenAPI spec..."
APITEST_OPENAPI=$(curl -sf http://localhost:9282/openapi.yaml || true)
if [ -n "$APITEST_OPENAPI" ]; then
  APITEST_OPENAPI_BYTES=${#APITEST_OPENAPI}
  ok "api-test OpenAPI spec fetched (${APITEST_OPENAPI_BYTES} bytes)"
else
  echo -e "  ${YELLOW}⚠${NC} api-test /openapi.yaml unavailable — registering without catalog (api_list_endpoints will be empty)"
fi

# Seed the api-test-fixture catalog. Idempotent: catalog create
# accepts a 409 conflict (already exists from a prior dev run), and
# the spec upsert is PUT so it replaces existing content. We wire
# the connection's config.catalog_id only when the upsert succeeds,
# so a transient catalog API failure falls back to the spec-less
# connection state (no api_list_endpoints, direct api_invoke_endpoint
# still works).
APITEST_CATALOG_ID="api-test-fixture"
APITEST_CATALOG_READY=0
if [ -n "$APITEST_OPENAPI" ]; then
  info "Creating api-test-fixture catalog (idempotent)..."
  APITEST_CATALOG_BODY=$(python3 -c '
import json
print(json.dumps({
    "id": "api-test-fixture",
    "name": "api-test-fixture",
    "display_name": "API Test Fixture",
    "description": "Dev fixture: deterministic /v1/* endpoints (whoami, headers, echo, fixed, sized, lorem, status, slow, flaky).",
}))
')
  CATALOG_HTTP=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
    -H "X-API-Key: acme-dev-key-2024" \
    -H "Content-Type: application/json" \
    -d "$APITEST_CATALOG_BODY" \
    http://localhost:8080/api/v1/admin/api-catalogs || echo "000")
  case "$CATALOG_HTTP" in
    200|201) ok "api-test-fixture catalog created" ;;
    409)     ok "api-test-fixture catalog already exists (reusing)" ;;
    *)       echo -e "  ${YELLOW}⚠${NC} catalog create returned HTTP $CATALOG_HTTP — falling back to no-catalog registration" ;;
  esac

  if [ "$CATALOG_HTTP" = "200" ] || [ "$CATALOG_HTTP" = "201" ] || [ "$CATALOG_HTTP" = "409" ]; then
    info "Upserting default spec into api-test-fixture catalog..."
    APITEST_SPEC_BODY=$(APITEST_OPENAPI="$APITEST_OPENAPI" python3 -c '
import json, os
print(json.dumps({
    "source_kind": "inline",
    "content": os.environ.get("APITEST_OPENAPI", ""),
}))
')
    SPEC_HTTP=$(curl -s -o /dev/null -w "%{http_code}" -X PUT \
      -H "X-API-Key: acme-dev-key-2024" \
      -H "Content-Type: application/json" \
      -d "$APITEST_SPEC_BODY" \
      "http://localhost:8080/api/v1/admin/api-catalogs/${APITEST_CATALOG_ID}/specs/default" || echo "000")
    case "$SPEC_HTTP" in
      200|201|204)
        ok "default spec upserted into api-test-fixture catalog"
        APITEST_CATALOG_READY=1
        ;;
      *)
        echo -e "  ${YELLOW}⚠${NC} spec upsert returned HTTP $SPEC_HTTP — falling back to no-catalog registration"
        ;;
    esac
  fi
fi

info "Registering api-test fixture connection..."
APITEST_DEV_KEY_VAL="${APITEST_DEV_KEY:-apitest-dev-key-2024}"
APITEST_BODY=$(APITEST_CATALOG_READY="$APITEST_CATALOG_READY" APITEST_CATALOG_ID="$APITEST_CATALOG_ID" APITEST_DEV_KEY_VAL="$APITEST_DEV_KEY_VAL" python3 -c '
import json, os
ready = os.environ.get("APITEST_CATALOG_READY", "0") == "1"
catalog_id = os.environ.get("APITEST_CATALOG_ID", "")
key = os.environ.get("APITEST_DEV_KEY_VAL", "")
config = {
    "base_url": "http://localhost:9282",
    "auth_mode": "api_key",
    "credential": key,
    "api_key_placement": "header",
    "api_key_header": "X-API-Key",
    "connection_name": "api-test-fixture",
    "connect_timeout": "5s",
    "call_timeout": "10s",
    "trust_level": "untrusted",
}
if ready:
    config["catalog_id"] = catalog_id
body = {
    "config": config,
    "description": "Dev fixture: ghcr.io/plexara/api-test — identity, data, failure, echo endpoints",
}
print(json.dumps(body))
')
APITEST_HTTP=$(curl -s -o /dev/null -w "%{http_code}" -X PUT \
  -H "X-API-Key: acme-dev-key-2024" \
  -H "Content-Type: application/json" \
  -d "$APITEST_BODY" \
  http://localhost:8080/api/v1/admin/connection-instances/api/api-test-fixture || echo "000")
if [ "$APITEST_HTTP" = "200" ] || [ "$APITEST_HTTP" = "201" ]; then
  if [ "$APITEST_CATALOG_READY" = "1" ]; then
    ok "api-test fixture connection registered with catalog ${APITEST_CATALOG_ID} (api_list_endpoints will resolve)"
  else
    ok "api-test fixture connection registered (no catalog)"
  fi
else
  echo -e "  ${YELLOW}⚠${NC} api-test connection register returned HTTP $APITEST_HTTP — admin API may not be ready"
fi

# Pre-seed two OAuth-authorization_code fixture connections so the
# unified OAuth flow is testable without manual config-form filling.
# Both point at dev-mcp-mock :9180 as the IdP (issues PKCE tokens, no
# client_id/secret validation, rotates refresh tokens). All the
# operator does is click Connect and complete the browser hop —
# everything else (auth URL, token URL, client credentials, callback)
# is already wired. dev-mcp-mock auto-grants on /authorize, so the
# "browser flow" is a single redirect through a new tab.
info "Registering oauth-mcp-dev connection (MCP gateway via authorization_code, Keycloak IdP)..."
OAUTH_MCP_BODY='{"config":{"endpoint":"http://localhost:9181/mcp","auth_mode":"oauth","oauth_grant":"authorization_code","oauth_authorization_url":"http://localhost:9090/realms/mcp-platform/protocol/openid-connect/auth","oauth_token_url":"http://localhost:9090/realms/mcp-platform/protocol/openid-connect/token","oauth_client_id":"oauth-mcp-dev","oauth_client_secret":"oauth-mcp-dev-secret","oauth_scope":"openid profile email","connection_name":"oauth-mcp-dev","connect_timeout":"5s","call_timeout":"10s"},"description":"Dev fixture: dev-mcp-mock (9181) via authorization_code OAuth against Keycloak (realm mcp-platform). Click Connect to authorize."}'
OAUTH_MCP_HTTP=$(curl -s -o /dev/null -w "%{http_code}" -X PUT \
  -H "X-API-Key: acme-dev-key-2024" \
  -H "Content-Type: application/json" \
  -d "$OAUTH_MCP_BODY" \
  http://localhost:8080/api/v1/admin/connection-instances/mcp/oauth-mcp-dev || echo "000")
if [ "$OAUTH_MCP_HTTP" = "200" ] || [ "$OAUTH_MCP_HTTP" = "201" ]; then
  ok "oauth-mcp-dev connection registered (kind=mcp, authorization_code, IdP :9180)"
else
  echo -e "  ${YELLOW}⚠${NC} oauth-mcp-dev connection register returned HTTP $OAUTH_MCP_HTTP"
fi

info "Registering oauth-api-dev connection (HTTP API gateway via authorization_code, Keycloak IdP)..."
OAUTH_API_BODY=$(APITEST_CATALOG_READY="$APITEST_CATALOG_READY" APITEST_CATALOG_ID="$APITEST_CATALOG_ID" python3 -c '
import json, os
ready = os.environ.get("APITEST_CATALOG_READY", "0") == "1"
catalog_id = os.environ.get("APITEST_CATALOG_ID", "")
config = {
    "base_url": "http://localhost:9282",
    "auth_mode": "oauth2_authorization_code",
    "oauth2_authorization_url": "http://localhost:9090/realms/mcp-platform/protocol/openid-connect/auth",
    "oauth2_token_url": "http://localhost:9090/realms/mcp-platform/protocol/openid-connect/token",
    "oauth2_client_id": "oauth-api-dev",
    "oauth2_client_secret": "oauth-api-dev-secret",
    "oauth2_scopes": ["openid", "profile", "email"],
    "oauth2_endpoint_auth_style": "header",
    "connection_name": "oauth-api-dev",
    "connect_timeout": "5s",
    "call_timeout": "10s",
    "trust_level": "untrusted",
}
if ready:
    config["catalog_id"] = catalog_id
body = {
    "config": config,
    "description": "Dev fixture: api-test (9282) via authorization_code OAuth against Keycloak (realm mcp-platform). Click Connect to authorize.",
}
print(json.dumps(body))
')
OAUTH_API_HTTP=$(curl -s -o /dev/null -w "%{http_code}" -X PUT \
  -H "X-API-Key: acme-dev-key-2024" \
  -H "Content-Type: application/json" \
  -d "$OAUTH_API_BODY" \
  http://localhost:8080/api/v1/admin/connection-instances/api/oauth-api-dev || echo "000")
if [ "$OAUTH_API_HTTP" = "200" ] || [ "$OAUTH_API_HTTP" = "201" ]; then
  ok "oauth-api-dev connection registered (kind=api, authorization_code, IdP :9180)"
else
  echo -e "  ${YELLOW}⚠${NC} oauth-api-dev connection register returned HTTP $OAUTH_API_HTTP"
fi

echo ""

# ─── Warm up API Gateway metrics ────────────────────────────────────
#
# The admin Dashboard's "API Gateway" tab reads apigateway_inbound_*
# metrics from Prometheus via the platform's PromQL proxy. With no
# traffic those metrics do not exist and the tab is empty, so the view
# cannot be exercised locally. The view breaks traffic down by
# connection, so a single connection is not a useful demo: register a few
# extra demo connections (all backed by the api-test fixture, sharing its
# catalog so operation_id resolves) and drive weighted traffic across all
# of them with varied endpoints, methods, and a slice of 4xx. Then keep a
# light trickle going so the request-rate timeseries stays live. Gateway
# calls are event_kind apigateway_invoke, so they do NOT pollute the MCP
# tab. Generic vendor names, not real integrations. Best-effort: failures
# never block startup.
echo -e "${BOLD}Warming up API Gateway metrics${NC}"

# Register extra demo api connections so the per-connection breakdown is
# meaningful. They reuse the api-test fixture's URL, key, and catalog.
reg_demo_api() {
  local name="$1" display="$2" body code
  body=$(APITEST_CATALOG_READY="$APITEST_CATALOG_READY" APITEST_CATALOG_ID="$APITEST_CATALOG_ID" \
    APITEST_DEV_KEY_VAL="$APITEST_DEV_KEY_VAL" NAME="$name" DISPLAY="$display" python3 -c '
import json, os
ready = os.environ.get("APITEST_CATALOG_READY", "0") == "1"
cfg = {
    "base_url": "http://localhost:9282",
    "auth_mode": "api_key",
    "credential": os.environ.get("APITEST_DEV_KEY_VAL", ""),
    "api_key_placement": "header",
    "api_key_header": "X-API-Key",
    "connection_name": os.environ["NAME"],
    "connect_timeout": "5s",
    "call_timeout": "10s",
    "trust_level": "untrusted",
}
if ready:
    cfg["catalog_id"] = os.environ.get("APITEST_CATALOG_ID", "")
print(json.dumps({"config": cfg, "description": "Dev demo: %s (api-test fixture) for API Gateway observability" % os.environ["DISPLAY"]}))
')
  code=$(curl -s -o /dev/null -w "%{http_code}" -X PUT \
    -H "X-API-Key: acme-dev-key-2024" -H "Content-Type: application/json" \
    -d "$body" "http://localhost:8080/api/v1/admin/connection-instances/api/$name" || echo "000")
  case "$code" in
    200|201) ok "demo connection '$name' registered" ;;
    *) echo -e "  ${YELLOW}⚠${NC} demo connection '$name' register returned HTTP $code" ;;
  esac
}
reg_demo_api salesforce Salesforce
reg_demo_api stripe Stripe
reg_demo_api github GitHub

GW_CONNS=(api-test-fixture salesforce stripe github)

gw() { # conn method path [bodyjson]
  curl -s --max-time 5 -o /dev/null -X POST \
    -H "X-API-Key: acme-dev-key-2024" -H "Content-Type: application/json" \
    -d "{\"method\":\"$2\",\"path\":\"$3\"${4:+,\"body\":$4}}" \
    "http://localhost:8080/api/v1/gateway/$1/invoke" 2>/dev/null || true
}
gw_bad() { # conn -> malformed body, shim returns 400 (status_class 4xx)
  curl -s --max-time 5 -o /dev/null -X POST \
    -H "X-API-Key: acme-dev-key-2024" -H "Content-Type: application/json" \
    -d 'not-json' "http://localhost:8080/api/v1/gateway/$1/invoke" 2>/dev/null || true
}
gw_burst() { # conn
  gw "$1" GET /v1/whoami;  gw "$1" GET /v1/headers;    gw "$1" GET /v1/fixed/demo
  gw "$1" GET /v1/lorem;   gw "$1" GET /v1/status/200; gw "$1" GET /v1/status/200
  gw "$1" GET /v1/slow;    gw "$1" GET /v1/flaky;      gw "$1" POST /v1/echo '{"hello":"world"}'
}

# HasConnection is a live lookup, but a connection added via the admin API
# is not in the metrics clamp set the instant the PUT returns. Probe each
# with a real invoke until it answers 200 so the burst is not dropped.
#
# The whole warm-up (probe + weighted bursts + error samples) runs in the
# background: a slow or unreachable fixture must never delay the "ready"
# banner that tells the operator how to log in. Tracked in PIDS so the
# exit trap stops it. Every curl is bounded by --max-time (see gw/gw_bad).
(
  for c in "${GW_CONNS[@]}"; do
    for _ in $(seq 1 10); do
      code=$(curl -s --max-time 5 -o /dev/null -w "%{http_code}" -X POST \
        -H "X-API-Key: acme-dev-key-2024" -H "Content-Type: application/json" \
        -d '{"method":"GET","path":"/v1/whoami"}' \
        "http://localhost:8080/api/v1/gateway/$c/invoke" 2>/dev/null || echo "000")
      [ "$code" = "200" ] && break
      sleep 1
    done
  done

  # Weighted volume (parallel) so "top connections" has a clear ranking.
  for _ in 1 2 3 4 5 6; do gw_burst salesforce; done &
  for _ in 1 2 3 4;       do gw_burst stripe; done &
  for _ in 1 2 3;         do gw_burst github; done &
  for _ in 1 2;           do gw_burst api-test-fixture; done &
  wait
  for c in "${GW_CONNS[@]}"; do for _ in 1 2 3; do gw_bad "$c"; done; done
) &
PIDS+=($!)
ok "API Gateway warm-up running in the background"

# Light continuous trickle across all connections so the request-rate
# timeseries stays live. Registered in PIDS so the exit trap stops it.
(
  while true; do
    sleep 12
    for c in "${GW_CONNS[@]}"; do
      gw "$c" GET /v1/whoami
      gw "$c" GET /v1/status/200
    done
    gw_bad salesforce
  done
) &
PIDS+=($!)
ok "API Gateway trickle running (every 12s); open Dashboard then API Gateway"

echo ""

# ─── Start Vite dev server ──────────────────────────────────────────

echo -e "${BOLD}Starting Vite UI${NC}"
(cd ui && npm run dev -- --clearScreen false 2>&1) &
PIDS+=($!)

# Wait for Vite
for i in $(seq 1 15); do
  if curl -sf http://localhost:5173/portal/ > /dev/null 2>&1; then
    break
  fi
  if [ "$i" -eq 15 ]; then
    fail "Vite dev server did not start within 15s"
  fi
  sleep 1
done
ok "Vite UI ready on :5173"

echo ""

# ─── Ready ──────────────────────────────────────────────────────────

echo -e "${BOLD}${GREEN}══════════════════════════════════════════${NC}"
echo -e "${BOLD}${GREEN}  Development environment ready${NC}"
echo -e "${BOLD}${GREEN}══════════════════════════════════════════${NC}"
echo ""
echo -e "  Portal UI:        ${CYAN}http://localhost:5173/portal/${NC}"
echo -e "  Go API:           ${CYAN}http://localhost:8080${NC}"
echo -e "  API Key:          ${CYAN}acme-dev-key-2024${NC}"
echo -e "  Metrics:          ${CYAN}http://localhost:9464/metrics${NC} (scraped by Prometheus)"
echo -e "  Prometheus:       ${CYAN}http://localhost:9091${NC}"
echo ""
echo -e "  ${BOLD}API Gateway activity tab${NC} (admin Dashboard)"
echo -e "  Invoke an ${BOLD}api_*${NC} tool against ${BOLD}api-test-fixture${NC}, then watch"
echo -e "  ${CYAN}Dashboard → API Gateway${NC} populate (allow a few seconds for the scrape)."
echo ""
echo -e "  ${BOLD}OIDC operator login (Keycloak)${NC}"
echo -e "  Issuer:           ${CYAN}http://localhost:9090/realms/mcp-platform${NC}"
echo -e "  Admin console:    ${CYAN}http://localhost:9090/admin${NC} (admin / admin)"
echo -e "  Test users:       ${CYAN}admin@example.com / admin-password${NC} (dp_admin)"
echo -e "                    ${CYAN}analyst@example.com / analyst-password${NC} (dp_analyst)"
echo ""
echo -e "  ${BOLD}Pre-wired upstream fixtures${NC}"
echo -e "  dev-mock (MCP):   ${CYAN}http://localhost:9181/mcp${NC} (echo, add, now)"
echo -e "                    ${CYAN}OAuth at http://localhost:9180${NC}"
echo -e "  mcp-test (MCP):   ${CYAN}http://localhost:9281/${NC}  portal: ${CYAN}http://localhost:9281/portal/${NC}"
echo -e "                    ${CYAN}API key: $MCPTEST_DEV_KEY_VAL${NC}"
echo -e "  api-test (HTTP):  ${CYAN}http://localhost:9282/v1${NC}  portal: ${CYAN}http://localhost:9282/portal/${NC}"
echo -e "                    ${CYAN}API key: $APITEST_DEV_KEY_VAL${NC}"
echo ""
echo -e "  ${BOLD}Pre-wired OAuth authorization_code fixtures${NC}"
echo -e "  ${CYAN}Portal → Settings → Connections${NC} — click ${BOLD}Connect${NC} on either to test the browser flow:"
echo -e "    • ${BOLD}oauth-mcp-dev${NC}  (kind=mcp, MCP fixture, IdP=Keycloak)"
echo -e "    • ${BOLD}oauth-api-dev${NC}  (kind=api, api-test fixture, IdP=Keycloak)"
echo ""
echo -e "  Go files  → air rebuilds automatically"
echo -e "  UI files  → Vite hot-reloads automatically"
echo ""
echo -e "  Scrolled away? Re-print the login any time with ${BOLD}make dev-info${NC}"
echo ""
echo -e "  Press ${BOLD}Ctrl-C${NC} to stop all services."
echo ""

# Keep running until interrupted
wait
