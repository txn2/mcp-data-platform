#!/usr/bin/env bash
# Declare the asset references the seeded content names (#1474, #1488, #1494).
# Called by start.sh after seed-s3.sh (asset content) and seed-resources.sh
# (the files that content points at).
#
# Two of the seeded assets carry the brand mark as a reference rather than as
# embedded bytes: asset-001 (HTML) and asset-003 (JSX). Only a DECLARED
# reference is rewritten as the content is served, so without this step both
# render an unresolved mcp:// URI and the public-viewer suite's reference cases
# would pass on an asset that never referenced anything.
set -euo pipefail

API="http://localhost:${DEV_API_PORT:-8080}"
API_KEY="acme-dev-key-2024"

# The global brand resource seed-resources.sh uploads. Its id is generated at
# upload, so it is looked up by the URI its scope, category and filename
# determine. The listing is narrowed to the brand category rather than paging
# the whole global library, which holds the 130 generated rows seed.sql writes
# and would otherwise sit against the server's 200-row listing ceiling.
# Objects are split on "{" so the id read belongs to the record the URI matched
# rather than to a neighbor.
LOGO_URI="mcp://global/brand/acme-logo.svg"
# The lookup must never take the stack down: it runs under set -e, and a miss
# is a grep with no match rather than an error worth aborting a dev startup for.
LOGO_ID=$(curl -sf "$API/api/v1/resources?scope=global&category=brand&limit=200" \
  -H "X-API-Key: $API_KEY" \
  | tr '{' '\n' | grep -F "\"uri\":\"$LOGO_URI\"" \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p' | head -1 || true)
if [ -z "$LOGO_ID" ]; then
  # Reported as the failure it is: the reference cases in
  # ui/e2e/public-viewer read an asset that now declares nothing.
  echo "  seed-asset-refs: FAILED to find $LOGO_URI in the global library." >&2
  echo "  seed-asset-refs: the seeded HTML and JSX assets will render no reference." >&2
  exit 0
fi

# ensure_content re-uploads an asset's seed file when its stored content names
# no reference. seed-s3.sh uploads only into an asset with no content at all, to
# preserve generated thumbnails, so a stack seeded before the referencing
# content existed keeps the old bytes and the declaration below would point at a
# URI nothing in the markup writes.
#
# The read is a VIEWING path, so a URI that is already declared comes back
# rewritten to the reference route rather than as itself: both forms count as
# the content naming the file, and matching only the mcp:// form would re-upload
# on every startup and add an asset version each time.
ensure_content() {
  local asset="$1" file="$2" body
  # Read into a variable rather than piping into `grep -q`: under pipefail, a
  # grep that exits at its first match closes the pipe, curl fails on the
  # write, and the pipeline reports failure for content that DID match --
  # re-uploading, and adding an asset version, on every start.
  body=$(curl -sf "$API/api/v1/portal/assets/$asset/content" -H "X-API-Key: $API_KEY" || true)
  case "$body" in
    *"$LOGO_URI"* | *"/portal/refs/"*) return 0 ;;
  esac
  curl -sf -o /dev/null -X PUT "$API/api/v1/portal/assets/$asset/content" \
    -H "X-API-Key: $API_KEY" --data-binary "@dev/seed-content/$file" || true
}

declare_ref() {
  local asset="$1" status
  # `|| true` because a refused connection exits curl non-zero with an empty
  # body, which under set -e would end the script before the case below can
  # report it.
  status=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
    "$API/api/v1/portal/assets/$asset/references" \
    -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
    -d "{\"target_kind\":\"resource\",\"target_id\":\"$LOGO_ID\"}" || true)
  # 409 is the reference already being declared, which a restart produces.
  case "$status" in
    200 | 201 | 409) return 0 ;;
    *) echo "  seed-asset-refs: $asset -> $LOGO_ID failed (HTTP $status)" >&2 ;;
  esac
}

ensure_content asset-001 asset-001.html
ensure_content asset-003 asset-003.jsx

declare_ref asset-001
declare_ref asset-003
