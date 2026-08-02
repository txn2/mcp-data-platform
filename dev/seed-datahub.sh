#!/usr/bin/env bash
# Seed the dev DataHub with the datasets the seeded knowledge pages cite.
#
# dev/seed.sql writes knowledge pages whose bodies cite catalog datasets by URN
# (iceberg.retail.daily_sales and friends). Nothing else in the dev stack creates
# those datasets, so with a real DataHub attached every one of those citations
# dangles: the catalog view and the knowledge graph show an entity the catalog
# has never heard of. This ingests them, so the prose and the catalog agree.
#
# SAFETY: this only ever writes to a LOOPBACK DataHub. DATAHUB_ENDPOINT is a
# developer's own setting and may point at a shared or production catalog;
# injecting fixture datasets into one of those on a `make dev` would be a real
# incident, not an inconvenience. A non-local endpoint is refused by name unless
# DEV_DATAHUB_SEED_FORCE=1 says otherwise.
set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
info() { echo -e "  ${YELLOW}…${NC} $1"; }
ok()   { echo -e "  ${GREEN}✓${NC} $1"; }
warn() { echo -e "  ${YELLOW}!${NC} $1"; }
fail() { echo -e "${RED}FAIL: $1${NC}" >&2; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DATASETS="$SCRIPT_DIR/datahub-datasets.json"
ENDPOINT="${DATAHUB_ENDPOINT:-}"

[ -f "$DATASETS" ] || fail "missing $DATASETS"
if [ -z "$ENDPOINT" ]; then
  warn "DATAHUB_ENDPOINT is not set; skipping catalog seed."
  exit 0
fi

# The platform is configured with DataHub's GraphQL URL, but ingestion is a REST
# call on the same host, so reduce the endpoint to its origin.
ORIGIN="$(printf '%s' "$ENDPOINT" | sed -E 's#^(https?://[^/]+).*#\1#')"

case "$ORIGIN" in
  # The IPv6 brackets are escaped: unescaped, [::1] is a character class
  # matching one of ":" or "1", so the IPv6 loopback would never match.
  http://localhost:*|http://127.0.0.1:*|http://localhost|http://127.0.0.1|http://\[::1\]:*)
    ;;
  *)
    if [ "${DEV_DATAHUB_SEED_FORCE:-0}" != "1" ]; then
      warn "DataHub at ${ORIGIN} is not local — not seeding it."
      warn "The seeded knowledge pages cite iceberg.* datasets that this catalog"
      warn "will not have, so catalog citations there will not resolve."
      warn "To seed it anyway: DEV_DATAHUB_SEED_FORCE=1 bash dev/seed-datahub.sh"
      exit 0
    fi
    warn "DEV_DATAHUB_SEED_FORCE=1 — writing fixture datasets to ${ORIGIN}"
    ;;
esac

# Expanded as ${AUTH[@]+"${AUTH[@]}"} everywhere below: under `set -u` an empty
# array is an unbound variable on the bash that ships with macOS.
AUTH=()
if [ -n "${DATAHUB_TOKEN:-}" ]; then
  AUTH=(-H "Authorization: Bearer ${DATAHUB_TOKEN}")
fi

if ! curl -sf --max-time 5 ${AUTH[@]+"${AUTH[@]}"} "${ORIGIN}/config" > /dev/null 2>&1; then
  warn "No DataHub responding at ${ORIGIN}; skipping catalog seed."
  exit 0
fi

info "Seeding DataHub at ${ORIGIN}..."
COUNT="$(python3 -c 'import json,sys;print(len(json.load(open(sys.argv[1]))))' "$DATASETS")"

# async=false so the aspects are searchable by the time the platform starts and
# the portal's first catalog read happens.
STATUS="$(curl -s -o /tmp/dev-datahub-seed.out -w '%{http_code}' --max-time 60 \
  -X POST "${ORIGIN}/openapi/v3/entity/dataset?async=false" \
  -H 'Content-Type: application/json' ${AUTH[@]+"${AUTH[@]}"} \
  --data-binary "@$DATASETS")"

if [ "$STATUS" != "200" ]; then
  warn "DataHub ingest returned HTTP ${STATUS}; catalog citations may not resolve."
  head -c 400 /tmp/dev-datahub-seed.out >&2 || true
  echo >&2
  exit 0
fi
ok "Seeded ${COUNT} datasets into DataHub (${ORIGIN})"
