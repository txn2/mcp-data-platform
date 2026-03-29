#!/usr/bin/env bash
# Upload seed asset content to SeaweedFS and the portal API.
# Called by start.sh after SQL seed completes.
set -euo pipefail

API="http://localhost:8080"
API_KEY="acme-dev-key-2024"
CONTENT_DIR="dev/seed-content"

# Upload content via the portal API (handles S3 + version tracking)
# Bucket must already exist (created by start.sh during Docker startup).
upload() {
  local id="$1" file="$2"
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
