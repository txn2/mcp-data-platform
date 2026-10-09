//go:build integration

package acceptance

import (
	"database/sql"
	"encoding/base64"
	"image/color"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// #2062: a JSX asset that declares a managed-resource font in an @font-face
// rule loads it, in the viewer's frame and in the frame its tile is drawn in,
// which share one policy (buildCSP). The policy left the reference route out
// of font-src, so the font was refused, and the refusal was counted as a
// reference that failed and the tile recorded as not drawable.
//
// The page draws itself teal only once document.fonts reports the declared
// face loaded, and red when it did not, so the tile's pixels say whether the
// font reached the frame. The font is KaTeX's Main Regular (MIT), copied from
// the UI's own dependencies.
//
// Wire forms: manage_resource's content_base64 and content_type are strings;
// save_asset's content and content_type are strings and references is a list
// of strings. Each is sent once, as literal params.

var (
	teal2062 = color.RGBA{R: 0, G: 128, B: 128, A: 255}
	red2062  = color.RGBA{R: 200, G: 0, B: 0, A: 255}
)

// fontPage2062 is the JSX asset: it declares the font at uri and colors the
// page by whether the font loaded.
func fontPage2062(uri string) string {
	return `import { useEffect, useState } from "react";

const css = ` + "`" + `@font-face { font-family: "Brand2062"; src: url("` + uri + `") format("woff2"); }
body { margin: 0; font-family: "Brand2062", Arial, sans-serif; }` + "`" + `;

export default function App() {
  const [loaded, setLoaded] = useState(null);
  useEffect(() => {
    document.fonts.load('48px "Brand2062"').then((faces) => setLoaded(faces.length > 0), () => setLoaded(false));
  }, []);
  const background = loaded === null ? "rgb(255,255,255)" : loaded ? "rgb(0,128,128)" : "rgb(200,0,0)";
  return (
    <>
      <style>{css}</style>
      <div style={{ background, width: "100vw", height: "100vh" }}>
        <h1 style={{ margin: 0, fontSize: 48, color: "white" }}>Brand</h1>
      </div>
    </>
  );
}
`
}

// fontAsset2062 uploads the font as a managed resource and saves the JSX
// asset that declares it, returning the asset's id.
func fontAsset2062(t *testing.T, c *client) string {
	t.Helper()
	font, err := os.ReadFile("testdata/issue_2062_font.woff2")
	if err != nil {
		t.Fatalf("reading the font fixture: %v", err)
	}
	name := "acceptance-2062-" + unique1794()
	out := c.call("manage_resource", map[string]any{
		"action": "create", "filename": name + ".woff2", "display_name": name, "path": "acceptance-2062",
		"description":    "Acceptance #2062: a brand font a JSX asset declares.",
		"content_base64": base64.StdEncoding.EncodeToString(font),
		"content_type":   "font/woff2",
	})
	resourceID, _ := out["resource_id"].(string)
	if resourceID == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+resourceID, http.NoBody) })
	status, row := c.rest(http.MethodGet, "/api/v1/resources/"+resourceID, http.NoBody)
	uri, _ := row["uri"].(string)
	if status != http.StatusOK || uri == "" {
		t.Fatalf("the font resource carries no uri: HTTP %d %v", status, row)
	}

	saved := c.call("save_asset", map[string]any{
		"name":         name,
		"content":      fontPage2062(uri),
		"content_type": "text/jsx",
		"description":  "Acceptance #2062: a JSX page drawn in a referenced font.",
		"references":   []any{uri},
	})
	assetID, _ := saved["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("save_asset returned no asset_id: %v", saved)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID}) })
	return assetID
}

// assertDrawnInTheFont2062 reads the asset's tile and holds it to the page's
// loaded branch.
func assertDrawnInTheFont2062(t *testing.T, c *client, assetID string) {
	t.Helper()
	img := tile1794(t, c, "/api/v1/portal/assets/"+assetID+"/thumbnail")
	if red := share1794(img, red2062, 24); red > 0.2 {
		t.Fatalf("the tile is %.0f%% the page's font-failed color: the font did not load in the frame", 100*red)
	}
	if teal := share1794(img, teal2062, 24); teal < 0.5 {
		t.Fatalf("the tile is %.0f%% the page's font-loaded color; the page was not drawn with the font", 100*teal)
	}
}

func TestIssue2062_AJSXAssetLoadsItsReferencedFontAndGetsATile(t *testing.T) {
	c := connectFor(t, tileWait1794+time.Minute)
	assetID := fontAsset2062(t, c)
	awaitAssetTile1794(t, c, assetID)
	assertDrawnInTheFont2062(t, c, assetID)
}

// TestIssue2062_ATileTheBugWithheldIsDrawnAgain writes onto an asset the
// failure this bug recorded, then applies migration 000182 as an upgrade
// does, and holds the running tile worker to drawing the asset again with no
// action by its owner.
func TestIssue2062_ATileTheBugWithheldIsDrawnAgain(t *testing.T) {
	c := connectFor(t, 2*tileWait1794+time.Minute)
	assetID := fontAsset2062(t, c)
	awaitAssetTile1794(t, c, assetID)

	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the dev database: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(c.ctx, `UPDATE portal_assets
		SET thumbnail_failure = '1 file(s) this document links to could not be loaded',
		    thumbnail_failed_version = current_version, thumbnail_version = 0, thumbnail_attempts = 3
		WHERE id = $1`, assetID); err != nil {
		t.Fatalf("recording the failure the bug recorded: %v", err)
	}
	up, err := os.ReadFile("../../pkg/database/migrate/migrations/000182_ref_failure_tiles_redrawn.up.sql")
	if err != nil {
		t.Fatalf("reading migration 000182: %v", err)
	}
	if _, err := db.ExecContext(c.ctx, string(up)); err != nil {
		t.Fatalf("applying migration 000182: %v", err)
	}
	var failure string
	if err := db.QueryRowContext(c.ctx, `SELECT thumbnail_failure FROM portal_assets WHERE id = $1`, assetID).Scan(&failure); err != nil {
		t.Fatalf("reading the asset back: %v", err)
	}
	if strings.TrimSpace(failure) != "" {
		t.Fatalf("migration 000182 left the failure in place: %q", failure)
	}
	awaitAssetTile1794(t, c, assetID)
	assertDrawnInTheFont2062(t, c, assetID)
}
