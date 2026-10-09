#!/usr/bin/env bash
# Upload seed asset content to SeaweedFS and the portal API.
# Called by start.sh after SQL seed completes.
#
# Only uploads content if the asset has no content yet (HTTP 404 on GET).
# This prevents clearing generated thumbnails on every dev restart.
set -euo pipefail

# DEV_API_PORT is exported by dev/start.sh (auto-relocated when 8080 is busy).
API="http://localhost:${DEV_API_PORT:-8080}"
API_KEY="acme-dev-key-2024"
CONTENT_DIR="dev/seed-content"

# Upload content via the portal API (handles S3 + version tracking)
# Bucket must already exist (created by start.sh during Docker startup).
# Skips upload if the asset already has content (preserves thumbnails).
upload() {
  local id="$1" file="$2"
  local status
  status=$(curl -s -o /dev/null -w "%{http_code}" \
    "$API/api/v1/portal/assets/$id/content" \
    -H "X-API-Key: $API_KEY")
  if [ "$status" = "200" ]; then
    return 0
  fi
  curl -sf -X PUT "$API/api/v1/portal/assets/$id/content" \
    -H "X-API-Key: $API_KEY" \
    --data-binary "@$file" > /dev/null
}

upload "asset-001" "$CONTENT_DIR/asset-001.html"
upload "asset-002" "$CONTENT_DIR/asset-002.csv"
upload "asset-003" "$CONTENT_DIR/asset-003.jsx"
upload "asset-004" "$CONTENT_DIR/asset-004.md"
upload "asset-005" "$CONTENT_DIR/asset-005.svg"
upload "asset-006" "$CONTENT_DIR/asset-006.html"
upload "asset-007" "$CONTENT_DIR/asset-007.html"
upload "asset-008" "$CONTENT_DIR/asset-008.html"
upload "asset-009" "$CONTENT_DIR/asset-009.html"

# The basemap region the seeded route map draws on (#2068): a San Francisco
# extract to zoom 12, put where an operator on a closed network puts one, in
# the managed-resources bucket under maps/uploads/. The maps fetch pass lists
# it as the ready region "san-francisco" with no fetch.
if [ -n "${DEV_S3_TLS_PORT:-}" ]; then
  AWS_ACCESS_KEY_ID=dev-access-key AWS_SECRET_ACCESS_KEY=dev-secret-key \
    aws --endpoint-url "https://localhost:$DEV_S3_TLS_PORT" s3 cp --only-show-errors \
    "$CONTENT_DIR/san-francisco.pmtiles" s3://managed-resources/maps/uploads/san-francisco.pmtiles
fi

# Content for the 120 generated demo assets (seed-0001..0120 from seed.sql).
# Reuse the five content files, cycled by the same (n % 5) index used in the
# SQL so each generated asset's bytes match its content_type. Uploaded in
# parallel batches so this stays a few seconds, not a minute of serial curls.
# The GET-404 skip in upload() makes reruns a no-op (preserves thumbnails).
GENERATED_CONTENT=(
  "$CONTENT_DIR/asset-001.html"  # n % 5 == 0  -> text/html
  "$CONTENT_DIR/asset-002.csv"   # n % 5 == 1  -> text/csv
  "$CONTENT_DIR/asset-003.jsx"   # n % 5 == 2  -> text/jsx
  "$CONTENT_DIR/asset-004.md"    # n % 5 == 3  -> text/markdown
  "$CONTENT_DIR/asset-005.svg"   # n % 5 == 4  -> image/svg+xml
)
for n in $(seq 1 120); do
  id=$(printf 'seed-%04d' "$n")
  upload "$id" "${GENERATED_CONTENT[$((n % 5))]}" &
  # Cap concurrency so we do not open 120 sockets at once.
  if [ "$((n % 16))" -eq 0 ]; then wait; fi
done
wait
