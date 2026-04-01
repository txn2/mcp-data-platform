#!/usr/bin/env bash
# Upload seed asset content to SeaweedFS and the portal API.
# Called by start.sh after SQL seed completes.
#
# Only uploads content if the asset has no content yet (HTTP 404 on GET).
# This prevents clearing generated thumbnails on every dev restart.
set -euo pipefail

API="http://localhost:8080"
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
