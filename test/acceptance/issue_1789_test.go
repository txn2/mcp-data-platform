//go:build integration

package acceptance

import (
	"image"
	"image/color"
	"testing"
	"time"
)

// Issue #1789: an HTML or JSX document that styles itself for a dark color
// scheme has a dark tile, and every tile is stored at twice the density a card
// shows it at.
//
// A document written with prefers-color-scheme rules opens dark in the viewer
// for a reader in dark mode, and its card was white in a dark grid: HTML and
// JSX were drawn once, in light. Every tile was also a 400x300 image of a page
// the browser had painted at a third of its size, so its text was a few
// pixels tall and a card on a high-density display stretched it twice over.
//
// These criteria are executed through the real surface: a document is saved
// with the tool a user calls, the platform draws it on its own in the renderer
// beside it, and the stored PNGs are read back from the route the portal reads.
//
// Wire forms: save_asset's name, content, content_type and description, and
// manage_asset's action, name, description and sections, are typed in their
// schemas (sections an array of objects), so each admits exactly one JSON form
// and each is sent as a literal tools/call parameter of that form. The
// thumbnail routes take their variant as a query-string parameter, which has
// no second form.

// schemeBackgrounds1789 are the page backgrounds the documents below paint in
// each color scheme.
var (
	lightBG1789 = color.RGBA{R: 0xf6, G: 0xf7, B: 0xf9}
	darkBG1789  = color.RGBA{R: 0x0d, G: 0x11, B: 0x17}
)

// schemeCSS1789 is a stylesheet that answers the reader's color scheme the way
// a dashboard written for both does.
const schemeCSS1789 = `body{margin:0;min-height:100vh;background:#f6f7f9;color:#12161c;font:15px/1.5 sans-serif}
@media (prefers-color-scheme: dark){body{background:#0d1117;color:#e7edf5}}
h1{margin:0;padding:28px 20px 4px;font-size:24px}p{margin:0;padding:0 20px}`

const html1789 = `<!DOCTYPE html><html><head><meta charset="utf-8"><style>` + schemeCSS1789 + `</style></head>
<body><h1>Data Center Weather Watch</h1><p>Forecasts for six locations, refreshed hourly.</p></body></html>`

const jsx1789 = "export default function Watch() {\n" +
	"  return (\n" +
	"    <div>\n" +
	"      <style>{`" + schemeCSS1789 + "`}</style>\n" +
	"      <h1>Data Center Weather Watch</h1>\n" +
	"      <p>Forecasts for six locations, refreshed hourly.</p>\n" +
	"    </div>\n" +
	"  );\n" +
	"}\n"

// assertSchemes1789 reads both stored tiles of an asset and holds each to the
// scheme it was drawn in and to the stored size.
func assertSchemes1789(t *testing.T, c *client, id string) {
	t.Helper()
	for _, v := range []struct {
		variant string
		bg      color.RGBA
	}{{"light", lightBG1789}, {"dark", darkBG1789}} {
		img := tile1787(t, c, "/api/v1/portal/assets/"+id, v.variant)
		b := img.Bounds()
		if b.Dx() != 800 || b.Dy() != 600 {
			t.Errorf("the %s tile is %dx%d, want 800x600", v.variant, b.Dx(), b.Dy())
		}
		// Below the heading the page is its background, in that scheme.
		below := image.Rect(0, b.Dy()/3, b.Dx(), b.Dy())
		if got := share1787(img, below, v.bg, 6); got < 0.95 {
			t.Errorf("the %s tile is %.1f%% the document's %s background, want nearly all of it", v.variant, 100*got, v.variant)
		}
	}
}

// TestIssue1789_AnHTMLDocumentHasATileInEachScheme is criterion 1: an HTML
// asset whose stylesheet has a prefers-color-scheme dark rule is drawn twice,
// and its dark tile is the document in its dark scheme.
func TestIssue1789_AnHTMLDocumentHasATileInEachScheme(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	id := saveAsset1789(t, c, "text/html", html1789)
	awaitAssetTile1787(t, c, id, true)
	assertSchemes1789(t, c, id)
}

// TestIssue1789_AJSXDocumentHasATileInEachScheme is criterion 2: a JSX asset
// is drawn in both schemes the same way.
func TestIssue1789_AJSXDocumentHasATileInEachScheme(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	id := saveAsset1789(t, c, "text/jsx", jsx1789)
	awaitAssetTile1787(t, c, id, true)
	assertSchemes1789(t, c, id)
}

// TestIssue1789_AnSVGKeepsOneTile is criterion 3: an SVG is drawn as stored,
// into one tile served in both schemes, and is never owed a dark one. Its type
// contains "xml", which is a themeable family; it is still an SVG.
func TestIssue1789_AnSVGKeepsOneTile(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	id := saveAsset1789(t, c, "image/svg+xml",
		`<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300"><rect width="400" height="300" fill="#2563eb"/></svg>`)
	awaitAssetTile1787(t, c, id, false)

	img := tile1787(t, c, "/api/v1/portal/assets/"+id, "")
	if b := img.Bounds(); b.Dx() != 800 || b.Dy() != 600 {
		t.Errorf("the tile is %dx%d, want 800x600", b.Dx(), b.Dy())
	}
	// The worker polls every few seconds and drains what it finds, so three
	// passes after the light tile landed a dark one would have been drawn if
	// the SVG were owed it.
	time.Sleep(15 * time.Second)
	row := assetRow1787(t, c, id)
	if key, _ := row["thumbnail_dark_s3_key"].(string); key != "" || intField1787(row, "thumbnail_dark_version") != 0 {
		t.Errorf("an SVG was drawn a dark tile: key %q, version %d", key, intField1787(row, "thumbnail_dark_version"))
	}
}

// TestIssue1789_ACollectionHasADarkMosaic is criterion 4: a collection's own
// tile has a dark mosaic composed from its members' dark tiles, served under
// variant=dark, beside the light one composed from their light tiles.
func TestIssue1789_ACollectionHasADarkMosaic(t *testing.T) {
	c := connectFor(t, 4*tileWait1787)
	first := saveAsset1789(t, c, "text/html", html1789)
	second := saveAsset1789(t, c, "text/html", html1789)
	awaitAssetTile1787(t, c, first, true)
	awaitAssetTile1787(t, c, second, true)

	out := c.call("manage_asset", map[string]any{
		"action":      "create_collection",
		"name":        "acceptance-1789-" + unique1787(),
		"description": "Acceptance #1789: a collection whose tile has a dark mosaic.",
		"sections": []any{map[string]any{
			"title": "Members",
			"items": []any{map[string]any{"asset_id": first}, map[string]any{"asset_id": second}},
		}},
	})
	coll, _ := out["collection_id"].(string)
	if coll == "" {
		t.Fatalf("create_collection returned no id: %v", out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete_collection", "collection_id": coll})
	})

	// The route answers variant=dark with the light mosaic until the dark one
	// is stored, so wait for a dark picture rather than for any picture.
	path := "/api/v1/portal/collections/" + coll
	deadline := time.Now().Add(tileWait1787)
	for {
		if status, _ := readTile1787(t, c, path, "dark"); status == 200 {
			if luminance1787(tile1787(t, c, path, "dark")) < 0.35 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no dark mosaic for collection %s after %s", coll, tileWait1787)
		}
		time.Sleep(time.Second)
	}
	if light := luminance1787(tile1787(t, c, path, "light")); light < 0.6 {
		t.Errorf("the light mosaic's mean luminance is %.2f; its members' light tiles are well above 0.6", light)
	}
}

// TestIssue1789_AnAssetNamesTheRendererThatDrewIt is criterion 5: the asset
// the portal reads carries the renderer generation that drew its tiles, which
// the portal puts in the tile's URL so a redraw is fetched rather than served
// from the browser's cache.
func TestIssue1789_AnAssetNamesTheRendererThatDrewIt(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	id := saveAsset1789(t, c, "text/markdown", "# Renderer\n\nDrawn by the current generation.\n")
	row := awaitAssetTile1787(t, c, id, true)
	if got := intField1787(row, "thumbnail_renderer"); got != 2 {
		t.Errorf("thumbnail_renderer = %d, want 2, the generation that draws at 800x600 with HTML in both schemes", got)
	}
}

// saveAsset1789 saves a document for these criteria and returns its id.
func saveAsset1789(t *testing.T, c *client, contentType, body string) string {
	t.Helper()
	out := c.call("save_asset", map[string]any{
		"name":         "acceptance-1789-" + unique1787(),
		"content":      body,
		"content_type": contentType,
		"description":  "Acceptance #1789: a document drawn once per color scheme at twice the density.",
	})
	id, _ := out["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id})
	})
	return id
}
