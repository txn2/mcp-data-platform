#!/usr/bin/env bash
# Refresh the map style's glyphs and sprites under ui/vendor/maplibre (#2068).
#
# The Protomaps basemap style draws its labels from PBF glyph ranges and its
# icons from a spritesheet, and neither is published to npm: they live in the
# protomaps/basemaps-assets repository. The portal serves them under
# /portal/vendor/maplibre/ beside the npm-pinned runtime, so they are checked in
# here at a pinned commit rather than fetched by every build. This script is how
# that copy is replaced: change a SHA below, run it, and review the diff.
#
# It also copies the license texts the npm packages do not carry (pmtiles and
# @protomaps/basemaps publish no LICENSE file) so the served directory holds the
# license of everything in it.
set -euo pipefail

ASSETS_SHA="028c18f713baecad011301ff7a69acc39bcc2ae7"   # protomaps/basemaps-assets
BASEMAPS_SHA="42ffaaa4a85a41bfcb23e43cc0f5b492a5eca123" # protomaps/basemaps
PMTILES_SHA="aec8fa1341222fdddb3318e9ffa8e18e19b312f7"  # protomaps/PMTiles
ICONS_SHA="92510779634f4a006c61ea70e50cb8c52c765a81"    # tangrams/icons

FONTS=("Noto Sans Regular" "Noto Sans Medium" "Noto Sans Italic" "Noto Sans Devanagari Regular v1")
SPRITES=(light.json light.png light@2x.json light@2x.png dark.json dark.png dark@2x.json dark@2x.png)

root="$(cd "$(dirname "$0")/.." && pwd)"
dest="$root/ui/vendor/maplibre"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

raw() { curl -fsSL "https://raw.githubusercontent.com/$1/$2/$3"; }

curl -fsSL "https://codeload.github.com/protomaps/basemaps-assets/tar.gz/$ASSETS_SHA" | tar xz -C "$work"
src="$work/basemaps-assets-$ASSETS_SHA"

rm -rf "$dest/fonts" "$dest/sprites"
mkdir -p "$dest/fonts" "$dest/sprites/v4"
for f in "${FONTS[@]}"; do
	cp -R "$src/fonts/$f" "$dest/fonts/$f"
done
for s in "${SPRITES[@]}"; do
	cp "$src/sprites/v4/$s" "$dest/sprites/v4/$s"
done

cp "$src/fonts/OFL.txt" "$dest/LICENSE-fonts.txt"
raw tangrams/icons "$ICONS_SHA" LICENSE.md >"$dest/LICENSE-sprites.md"
raw protomaps/basemaps "$BASEMAPS_SHA" LICENSE.md >"$dest/LICENSE-basemaps.md"
raw protomaps/PMTiles "$PMTILES_SHA" LICENSE >"$dest/LICENSE-pmtiles.txt"

cat >"$dest/SOURCE.md" <<EOF
# Source of the files in this directory

Written by scripts/sync-map-assets.sh; do not edit by hand.

| Files | Repository | Commit |
|---|---|---|
| fonts/, sprites/v4/, LICENSE-fonts.txt | protomaps/basemaps-assets | $ASSETS_SHA |
| LICENSE-sprites.md | tangrams/icons | $ICONS_SHA |
| LICENSE-basemaps.md | protomaps/basemaps | $BASEMAPS_SHA |
| LICENSE-pmtiles.txt | protomaps/PMTiles | $PMTILES_SHA |
EOF

echo "synced $(find "$dest/fonts" "$dest/sprites" -type f | wc -l | tr -d ' ') files into ${dest#"$root"/}"
