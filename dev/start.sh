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
# 9000 = seaweedfs, 9180 = dev-mcp-mock OAuth, 9181 = dev-mcp-mock MCP,
# 9281 = mcp-test fixture, 9282 = api-test fixture)
for port in 5432 8080 5173 9000 9180 9181 9281 9282; do
  if lsof -i ":$port" -sTCP:LISTEN > /dev/null 2>&1; then
    fail "Port $port is already in use. Run 'make dev-down' or stop the conflicting process."
  fi
done
ok "Ports 5432, 8080, 5173, 9000, 9180, 9181, 9281, 9282 are free"

echo ""

# ─── Start Docker services ──────────────────────────────────────────

echo -e "${BOLD}Starting Docker services${NC}"
docker compose -f dev/docker-compose.yml up -d 2>&1 | grep -v "^$" | sed 's/^/  /'

# Wait for PostgreSQL using docker exec (no psql dependency required)
PG_CONTAINER="acme-dev-postgres"
info "Waiting for PostgreSQL..."
for i in $(seq 1 30); do
  if docker exec "$PG_CONTAINER" pg_isready -U platform -d mcp_platform > /dev/null 2>&1; then
    break
  fi
  if [ "$i" -eq 30 ]; then
    fail "PostgreSQL did not become ready within 30s"
  fi
  sleep 1
done
ok "PostgreSQL ready on :5432"

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
# pull it and inline it into the connection's openapi_spec field so
# api_list_endpoints returns the 14 operations the fixture serves
# (whoami, headers, fixed/{key}, sized, lorem, status/{code}, slow,
# flaky, echo). Operations in the spec are pathed under /v1/..., so
# base_url omits the /v1 suffix — the gateway joins base_url + the
# operation path verbatim.
#
# The platform sends the api key as the X-API-Key header (default
# placement). If the OpenAPI fetch fails (older image, transient
# error), we still register the connection without a spec so
# api_invoke_endpoint works against direct paths.
info "Fetching api-test OpenAPI spec..."
APITEST_OPENAPI=$(curl -sf http://localhost:9282/openapi.yaml || true)
if [ -n "$APITEST_OPENAPI" ]; then
  APITEST_OPENAPI_BYTES=${#APITEST_OPENAPI}
  ok "api-test OpenAPI spec fetched (${APITEST_OPENAPI_BYTES} bytes)"
else
  echo -e "  ${YELLOW}⚠${NC} api-test /openapi.yaml unavailable — registering without spec (api_list_endpoints will be empty)"
fi

info "Registering api-test fixture connection..."
APITEST_DEV_KEY_VAL="${APITEST_DEV_KEY:-apitest-dev-key-2024}"
APITEST_BODY=$(APITEST_OPENAPI="$APITEST_OPENAPI" APITEST_DEV_KEY_VAL="$APITEST_DEV_KEY_VAL" python3 -c '
import json, os
spec = os.environ.get("APITEST_OPENAPI", "")
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
if spec:
    config["openapi_spec"] = spec
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
  if [ -n "$APITEST_OPENAPI" ]; then
    ok "api-test fixture connection registered with OpenAPI spec (api_list_endpoints will resolve)"
  else
    ok "api-test fixture connection registered (no OpenAPI spec)"
  fi
else
  echo -e "  ${YELLOW}⚠${NC} api-test connection register returned HTTP $APITEST_HTTP — admin API may not be ready"
fi

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
echo ""
echo -e "  ${BOLD}Pre-wired upstream fixtures${NC}"
echo -e "  dev-mock (MCP):   ${CYAN}http://localhost:9181/mcp${NC} (echo, add, now)"
echo -e "                    ${CYAN}OAuth at http://localhost:9180${NC}"
echo -e "  mcp-test (MCP):   ${CYAN}http://localhost:9281/${NC}  portal: ${CYAN}http://localhost:9281/portal/${NC}"
echo -e "                    ${CYAN}API key: $MCPTEST_DEV_KEY_VAL${NC}"
echo -e "  api-test (HTTP):  ${CYAN}http://localhost:9282/v1${NC}  portal: ${CYAN}http://localhost:9282/portal/${NC}"
echo -e "                    ${CYAN}API key: $APITEST_DEV_KEY_VAL${NC}"
echo ""
echo -e "  Go files  → air rebuilds automatically"
echo -e "  UI files  → Vite hot-reloads automatically"
echo ""
echo -e "  Press ${BOLD}Ctrl-C${NC} to stop all services."
echo ""

# Keep running until interrupted
wait
