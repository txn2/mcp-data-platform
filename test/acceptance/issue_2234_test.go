//go:build integration

package acceptance

import (
	"encoding/base64"
	"net/http"
	"os"
	"testing"
	"time"
)

// #2234: manage_asset update and patch created the new version, which makes
// the asset owed a tile, before they recorded the references the write
// declared, so the tile worker could draw the new version while a newly
// declared file was still refused, and keep that tile until the next content
// change. The references a write declares are now granted before its version
// exists.
//
// Each criterion starts from an asset drawn without the font, and writes the
// #2062 page that declares it: the tile of the new version is the page's
// font-loaded color.
//
// Wire forms: manage_asset update's content is a string and references a list
// of strings; manage_asset patch's edits is a list of objects (op, find,
// replace as strings) and references a list of strings. Each is sent once, as
// literal params.

// plainPage2234 is the page an asset starts as: one color, no reference.
const plainPage2234 = `export default function App() {
  return <div style={{ background: "rgb(0,0,255)", width: "100vw", height: "100vh" }} />;
}
`

// fontURI2234 uploads the #2062 font as a managed resource and saves an
// asset as the plain page, drawn; it returns the asset's id and the font's URI.
func fontURI2234(t *testing.T, c *client) (assetID, uri string) {
	t.Helper()
	font, err := os.ReadFile("testdata/issue_2062_font.woff2")
	if err != nil {
		t.Fatalf("reading the font fixture: %v", err)
	}
	name := "acceptance-2234-" + unique1794()
	out := c.call("manage_resource", map[string]any{
		"action": "create", "filename": name + ".woff2", "display_name": name, "path": "acceptance-2234",
		"description":    "Acceptance #2234: a brand font an update declares.",
		"content_base64": base64.StdEncoding.EncodeToString(font),
		"content_type":   "font/woff2",
	})
	resourceID, _ := out["resource_id"].(string)
	if resourceID == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+resourceID, http.NoBody) })
	status, row := c.rest(http.MethodGet, "/api/v1/resources/"+resourceID, http.NoBody)
	uri, _ = row["uri"].(string)
	if status != http.StatusOK || uri == "" {
		t.Fatalf("the font resource carries no uri: HTTP %d %v", status, row)
	}
	saved := c.call("save_asset", map[string]any{
		"name": name, "content": plainPage2234, "content_type": "text/jsx",
		"description": "Acceptance #2234: a page that gains a referenced font.",
	})
	assetID, _ = saved["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("save_asset returned no asset_id: %v", saved)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID}) })
	awaitAssetTile1794(t, c, assetID)
	return assetID, uri
}

// TestIssue2234_AnUpdateThatDeclaresAFontIsDrawnWithIt: manage_asset update
// writing the page that declares the font, with the reference, yields a tile
// of the new version drawn in the font.
func TestIssue2234_AnUpdateThatDeclaresAFontIsDrawnWithIt(t *testing.T) {
	c := connectFor(t, 3*tileWait1794+time.Minute)
	assetID, uri := fontURI2234(t, c)
	c.call("manage_asset", map[string]any{
		"action": "update", "asset_id": assetID, "content": fontPage2062(uri),
		"references": []any{uri}, "change_summary": "Draw the page in the brand font.",
	})
	awaitAssetTile1794(t, c, assetID)
	assertDrawnInTheFont2062(t, c, assetID)
}

// TestIssue2234_APatchThatDeclaresAFontIsDrawnWithIt: manage_asset patch
// replacing the page with the one that declares the font, with the
// reference, yields a tile of the new version drawn in the font.
func TestIssue2234_APatchThatDeclaresAFontIsDrawnWithIt(t *testing.T) {
	c := connectFor(t, 3*tileWait1794+time.Minute)
	assetID, uri := fontURI2234(t, c)
	c.call("manage_asset", map[string]any{
		"action": "patch", "asset_id": assetID,
		"edits":      []any{map[string]any{"op": "replace", "find": plainPage2234, "replace": fontPage2062(uri)}},
		"references": []any{uri}, "change_summary": "Draw the page in the brand font.",
	})
	awaitAssetTile1794(t, c, assetID)
	assertDrawnInTheFont2062(t, c, assetID)
}
