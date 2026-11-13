#!/usr/bin/env bash
# dev-embed.sh: build the portal the server embeds when the embed is empty (#2548).
#
# make verify's embed-clean empties internal/ui/dist and
# internal/contentviewer/dist, as a clean CI checkout has them. A dev server
# compiled after that embeds no tile page, so it draws no tile, and
# dev-smoke's tile check waited 45 seconds and failed without naming why.
# make dev runs this before it compiles the server, and air runs it before each
# rebuild, so the stack is never built from an empty embed. With both builds in
# place it does nothing.
set -euo pipefail

ui=internal/ui/dist/index.html
viewer=internal/contentviewer/dist/.vite/manifest.json
missing=()
[ -f "$ui" ] || missing+=("$ui")
[ -f "$viewer" ] || missing+=("$viewer")
[ ${#missing[@]} -eq 0 ] && exit 0

log=build/dev-embed.log
mkdir -p build
echo "dev-embed: ${missing[*]} missing (make verify's embed-clean empties them); building the portal and the tile page (make frontend-build, log: $log)..."
if ! "${MAKE:-make}" --no-print-directory frontend-build > "$log" 2>&1; then
  echo "dev-embed: make frontend-build failed; the last lines of $log:" >&2
  tail -n 30 "$log" >&2
  exit 1
fi
echo "dev-embed: built"
