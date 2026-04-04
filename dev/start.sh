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

# Node modules
if [ ! -d ui/node_modules ]; then
  info "Installing UI dependencies..."
  (cd ui && npm ci --silent)
fi
ok "UI dependencies ready"

# Port checks
for port in 5432 8080 5173 9000; do
  if lsof -i ":$port" -sTCP:LISTEN > /dev/null 2>&1; then
    fail "Port $port is already in use. Run 'make dev-down' or stop the conflicting process."
  fi
done
ok "Ports 5432, 8080, 5173, 9000 are free"

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
echo -e "  Portal UI:  ${CYAN}http://localhost:5173/portal/${NC}"
echo -e "  Go API:     ${CYAN}http://localhost:8080${NC}"
echo -e "  API Key:    ${CYAN}acme-dev-key-2024${NC}"
echo ""
echo -e "  Go files  → air rebuilds automatically"
echo -e "  UI files  → Vite hot-reloads automatically"
echo ""
echo -e "  Press ${BOLD}Ctrl-C${NC} to stop all services."
echo ""

# Keep running until interrupted
wait
