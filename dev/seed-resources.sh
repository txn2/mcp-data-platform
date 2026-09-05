#!/usr/bin/env bash
# Upload managed resources into every library the portal shows a tab for.
# Called by start.sh after the SQL seed completes.
#
# seed.sql generates 130 global rows for pagination, but it writes rows only:
# nothing puts an object behind their s3_key, so opening one fails to render,
# and every other scope is empty. That left the Resources page untestable
# against the dev stack -- "My Resources" and each persona tab showed the empty
# state, and a Global row opened onto a content error.
#
# These go in through the real upload endpoint rather than as SQL, so each one
# has a blob behind it and reads exactly as a person's upload does.
set -euo pipefail

API="http://localhost:${DEV_API_PORT:-8080}"
ADMIN_KEY="acme-dev-key-2024"
CONTENT_DIR="dev/seed-resources"

# The admin key's subject, which is what "My Resources" is keyed by. Read from
# the server rather than hardcoded: it is derived from the key's name, and a
# rename here would otherwise file the personal resources under a scope whose
# tab nobody can reach.
ADMIN_SUB=$(curl -sf "$API/api/v1/portal/me" -H "X-API-Key: $ADMIN_KEY" \
  | sed -n 's/.*"user_id":"\([^"]*\)".*/\1/p')
if [ -z "$ADMIN_SUB" ]; then
  echo "  seed-resources: could not resolve the admin subject; skipping" >&2
  exit 0
fi

# already_seeded reports whether a display name is present in a scope, so a
# restart adds nothing and a hand-deleted resource comes back.
already_seeded() {
  local scope="$1" scope_id="$2" name="$3"
  curl -sf "$API/api/v1/resources?scope=$scope&scope_id=$scope_id" \
    -H "X-API-Key: $ADMIN_KEY" | grep -qF "\"display_name\":\"$name\""
}

seed() {
  local scope="$1" scope_id="$2" category="$3" name="$4" desc="$5" file="$6"
  if already_seeded "$scope" "$scope_id" "$name"; then
    return 0
  fi
  local status
  # The file part goes last. The route streams it to blob storage where it
  # finds it and reads no part behind it (#1631), so every field has to be
  # named before -F file=@.
  status=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$API/api/v1/resources" \
    -H "X-API-Key: $ADMIN_KEY" \
    -F "scope=$scope" -F "scope_id=$scope_id" \
    -F "path=$category" -F "display_name=$name" -F "description=$desc" \
    -F "file=@$CONTENT_DIR/$file")
  # 409 is the file already being in that scope under that name, which the
  # already_seeded probe misses whenever the listing it reads is paged past the
  # row. It is the same no-op as the probe hitting, and treating it as a
  # storage failure aborted the whole run and skipped every seed after it.
  if [ "$status" = "409" ]; then
    return 0
  fi
  if [ "$status" != "200" ] && [ "$status" != "201" ]; then
    echo "  seed-resources: $scope/$scope_id \"$name\" failed (HTTP $status)" >&2
    # A storage backend that cannot take a write fails every one of these the
    # same way, and thirty seconds each. Stop at the first rather than spend
    # minutes proving it repeatedly.
    exit 0
  fi
}

# My Resources: the admin's own library, which is the first tab the portal opens
# on and was the one guaranteed to be empty.
seed user "$ADMIN_SUB" references "Metric Definitions" \
  "How weeks of supply, sell-through, shrink and comps are actually computed." \
  metric-definitions.md
seed user "$ADMIN_SUB" samples "Store Hierarchy" \
  "Region, district and store roster with opening dates and floor area." \
  store-hierarchy.csv

# The admin persona's library: what the persona tab on the user portal shows.
seed persona admin playbooks "Stockout Escalation Playbook" \
  "Steps to follow when a store reports a shortfall. Carried out, not summarized." \
  escalation-playbook.md
seed persona admin templates "Weekly Inventory Review" \
  "The layout the weekly review is produced in." \
  weekly-report-template.md

# A second persona, so a deployment with more than one persona tab is exercised.
seed persona data-engineer samples "Inventory API Error Sample" \
  "A stale-data problem response from the inventory position service." \
  api-error-sample.json

# The brand mark two seeded assets REFERENCE rather than embed (#1474), which
# is what dev/seed-asset-refs.sh declares and the public-viewer suite renders.
seed global "" brand "ACME Brand Mark" \
  "The mark a report names instead of carrying, as an asset reference." \
  acme-logo.svg

# Global, alongside the 130 generated rows -- these are the ones that open.
seed global "" references "Metric Definitions (Global)" \
  "The shared definitions every region reports against." \
  metric-definitions.md
seed global "" samples "Store Hierarchy (Global)" \
  "The store roster, published for everyone signed in." \
  store-hierarchy.csv

# The 130 rows seed.sql generates carry an s3_key nothing ever wrote to, so
# every one of them answered the portal with a content error the moment it was
# opened. Content goes in through the replace endpoint, which is the only door
# that writes a blob for a row that already exists.
#
# The bodies are cycled to match the mime type each row declares -- seed.sql
# picks it by (n % 4) from markdown, csv, json, text -- because replacing
# content re-detects the type from the bytes, and one body for all 130 would
# rewrite every row to the same type and flatten the variety the seed exists
# to provide.
GENERATED=(
  "metric-definitions.md"   # n % 4 == 0 -> text/markdown
  "store-hierarchy.csv"     # n % 4 == 1 -> text/csv
  "api-error-sample.json"   # n % 4 == 2 -> application/json
  "change-log.txt"          # n % 4 == 3 -> text/plain
)

# fill_generated puts a body behind one generated row, skipping a row that
# already serves content so a restart is a no-op.
fill_generated() {
  local id="$1" file="$2" status
  status=$(curl -s -o /dev/null -w "%{http_code}" \
    "$API/api/v1/resources/$id/content" -H "X-API-Key: $ADMIN_KEY")
  if [ "$status" = "200" ]; then
    return 0
  fi
  curl -sf -o /dev/null -X POST "$API/api/v1/resources/$id/content" \
    -H "X-API-Key: $ADMIN_KEY" -F "file=@$CONTENT_DIR/$file" || true
}

for n in $(seq 1 130); do
  id=$(printf 'res-seed-%04d' "$n")
  fill_generated "$id" "${GENERATED[$((n % 4))]}" &
  # Cap concurrency so this does not open 130 sockets at once.
  if [ "$((n % 16))" -eq 0 ]; then wait; fi
done
wait
