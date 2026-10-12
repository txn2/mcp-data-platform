//go:build integration

package acceptance

// Issue #2087: a map asset's thumbnail was captured before the map had drawn.
// The capture fired 500 ms after load, and a map draws its tiles after load,
// so the stored tile showed the basemap at its lowest zooms and only the data
// tiles that happened to land: a point layer of many points came out with a
// few, and the light and dark tiles showed different amounts.
//
// This saves, with save_asset, a map of 30 points spread over San Francisco
// on the dev stack's san-francisco region, fitted to the points in its load
// handler as the maps knowledge page's skeleton does, and reads both tiles
// back from the route the portal reads. Each must show the basemap's detail
// and every point.
//
// Wire forms: save_asset's name, content, content_type and description are
// typed strings and admit that one JSON form; each is sent as a literal
// tools/call param. The thumbnail route takes its variant as a query-string
// parameter, which has no second form.

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"os"
	"strings"
	"testing"
)

// issue2087Points is the number of points the map draws.
const issue2087Points = 30

// issue2087Doc is a map of issue2087Points blue points on a 6x5 grid over the
// region, fitted to them without animation in the load handler.
func issue2087Doc() string {
	var pts []string
	for i := range issue2087Points {
		lon := -122.47 + float64(i%6)*0.012
		lat := 37.735 + float64(i/6)*0.011
		pts = append(pts, fmt.Sprintf("[%.4f,%.4f]", lon, lat))
	}
	return `<!DOCTYPE html>
<html><head><meta charset="utf-8">
<link rel="stylesheet" href="/portal/vendor/maplibre/maplibre-gl.css">
<script src="/portal/vendor/maplibre/maplibre-gl.js"></script>
<script src="/portal/vendor/maplibre/pmtiles.js"></script>
<script src="/portal/vendor/maplibre/basemaps.js"></script>
<style>html,body{margin:0;height:100%}#map{height:100%}</style>
</head><body><div id="map"></div>
<script>
const origin = new URL(document.baseURI).origin;
const flavor = matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
const protocol = new pmtiles.Protocol();
maplibregl.addProtocol("pmtiles", protocol.tile);
const coords = [` + strings.Join(pts, ",") + `];
const points = { type: "FeatureCollection", features: coords.map((c) => ({ type: "Feature", properties: {}, geometry: { type: "Point", coordinates: c } })) };
const map = new maplibregl.Map({
  container: "map",
  style: {
    version: 8,
    glyphs: origin + "/portal/vendor/maplibre/fonts/{fontstack}/{range}.pbf",
    sprite: origin + "/portal/vendor/maplibre/sprites/v4/" + flavor,
    sources: { protomaps: { type: "vector", url: "pmtiles://" + origin + "/portal/maps/san-francisco.pmtiles" } },
    layers: basemaps.layers("protomaps", basemaps.namedFlavor(flavor), { lang: "en" }),
  },
});
map.on("load", () => {
  map.addSource("points", { type: "geojson", data: points });
  map.addLayer({ id: "points", type: "circle", source: "points", paint: { "circle-radius": 8, "circle-color": "#1d4ed8" } });
  const lons = coords.map((c) => c[0]), lats = coords.map((c) => c[1]);
  map.fitBounds([[Math.min(...lons), Math.min(...lats)], [Math.max(...lons), Math.max(...lats)]], { padding: 40, animate: false });
});
</script></body></html>`
}

// issue2087Discs counts the separate discs of the points' color in a tile:
// 4-connected regions of pixels within tol of it, larger than a few pixels so
// a label's stray antialiased pixel is not counted.
func issue2087Discs(img image.Image, tol int) int {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	on := make([]bool, w*h)
	for y := range h {
		for x := range w {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			on[y*w+x] = issue2068Abs(int(r>>8)-0x1d) <= tol && issue2068Abs(int(g>>8)-0x4e) <= tol && issue2068Abs(int(bl>>8)-0xd8) <= tol
		}
	}
	discs := 0
	for start := range on {
		if !on[start] {
			continue
		}
		size, stack := 0, []int{start}
		on[start] = false
		for len(stack) > 0 {
			p := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			size++
			x := p % w
			for _, q := range []int{p - 1, p + 1, p - w, p + w} {
				if q < 0 || q >= len(on) || !on[q] || (q == p-1 && x == 0) || (q == p+1 && x == w-1) {
					continue
				}
				on[q] = false
				stack = append(stack, q)
			}
		}
		if size >= 10 {
			discs++
		}
	}
	return discs
}

// TestIssue2087_AMapsTileShowsEveryPointAndTheBasemap: both tiles show the
// basemap's detail and all 30 points as 30 separate discs; a tile taken
// before the map drew shows a handful.
func TestIssue2087_AMapsTileShowsEveryPointAndTheBasemap(t *testing.T) {
	issue2068Restore(t)
	issue2068Settings(t, baseURL(), true, 14, "")
	c := connect(t)
	out := c.call("save_asset", map[string]any{
		"name": issue2068Name("Acceptance #2087 points"), "content_type": "text/html", "content": issue2087Doc(),
		"description": "Acceptance #2087: a map of 30 points fitted in its load handler.",
	})
	assetID, _ := out["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("save_asset returned no asset_id: %v", out)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/portal/assets/"+assetID, http.NoBody) })

	for _, variant := range []string{"light", "dark"} {
		img := issue2068Tile(t, c, assetID, variant)
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err == nil {
			_ = os.WriteFile(fmt.Sprintf("../../build/2087/tile-%s.png", variant), buf.Bytes(), 0o600)
		}
		_, distinct := issue2068Count(img, 0x1d, 0x4e, 0xd8, 28)
		discs := issue2087Discs(img, 28)
		t.Logf("%s tile: %d point discs, %d distinct colors", variant, discs, distinct)
		if discs != issue2087Points {
			t.Errorf("the %s tile shows %d of the map's %d points", variant, discs, issue2087Points)
		}
		if distinct < 40 {
			t.Errorf("the %s tile holds %d distinct colors; a basemap drawn past its lowest zooms holds far more", variant, distinct)
		}
	}
}
