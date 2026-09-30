//go:build integration

package acceptance

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"testing"
	"time"

	_ "github.com/lib/pq" // postgres driver for the dev database
)

// Issue #1991: a white SVG with a transparent background showed as a blank
// white rectangle on its tile, because the tile was captured on the tile
// page's own background. SVG and raster images are now captured with their
// transparent areas left transparent, and every surface that shows a tile puts
// the image viewer's checkerboard behind it.
//
// These criteria read the tiles the running platform draws, through the route
// the portal reads them from, drawn by the renderer beside the dev stack. The
// checkerboard itself is drawn by the browser on each surface and is checked in
// the portal (build/1991/acceptance.md records it).
//
// Wire forms: manage_resource's content and content_base64 are typed strings,
// so an SVG is sent as content and a PNG as content_base64, each once; its
// content_type is a string. save_asset's content and content_type are strings.
// The tile read takes its variant as a query-string parameter.

// whiteLogo1991 is white artwork on nothing: a white square in the middle of a
// transparent canvas.
const whiteLogo1991 = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100" width="100%" height="100%">` +
	`<rect x="20" y="20" width="60" height="60" fill="#ffffff"/></svg>`

// transparentAt reports whether the tile is fully transparent at (x, y).
func transparentAt(img image.Image, x, y int) bool {
	_, _, _, a := img.At(x, y).RGBA()
	return a == 0
}

// opaqueNear reports whether the tile at (x, y) is opaque and within tolerance
// of want.
func opaqueNear(img image.Image, x, y int, want color.RGBA) bool {
	_, _, _, a := img.At(x, y).RGBA()
	return a == 0xffff && near1787(img.At(x, y), want, 12)
}

// createResource1991 files a resource through manage_resource and returns its id.
func createResource1991(t *testing.T, c *client, args map[string]any) string {
	t.Helper()
	args["action"] = "create"
	args["path"] = "acceptance-1991"
	// A run that ends early leaves its file behind, so each name is new.
	args["filename"] = unique1787() + "-" + args["filename"].(string)
	args["display_name"] = "acceptance-1991-" + unique1787()
	args["description"] = "Acceptance #1991: a file whose tile keeps its transparency."
	out := c.call("manage_resource", args)
	id, _ := out["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	return id
}

// assertWhiteLogoTile1991 is criteria 1 and 3's picture: the white artwork is
// white and opaque, and the canvas around it is transparent rather than the
// page's white.
func assertWhiteLogoTile1991(t *testing.T, img image.Image) {
	t.Helper()
	b := img.Bounds()
	if !transparentAt(img, b.Min.X+2, b.Min.Y+2) || !transparentAt(img, b.Max.X-3, b.Max.Y-3) {
		t.Errorf("the SVG's transparent canvas was stored opaque: corner pixels %v and %v",
			img.At(b.Min.X+2, b.Min.Y+2), img.At(b.Max.X-3, b.Max.Y-3))
	}
	cx, cy := (b.Min.X+b.Max.X)/2, (b.Min.Y+b.Max.Y)/2
	if !opaqueNear(img, cx, cy, color.RGBA{255, 255, 255, 255}) {
		t.Errorf("the white artwork is not in the tile: centre pixel %v", img.At(cx, cy))
	}
}

// TestIssue1991_AWhiteSVGResourceTileKeepsItsTransparency is criterion 1's
// stored half, and 3 in the black case: the tile a resource's preview pane and
// card show is the artwork on a transparent canvas, so the surface's
// checkerboard shows through around it in either scheme.
func TestIssue1991_AWhiteSVGResourceTileKeepsItsTransparency(t *testing.T) {
	c := connect(t)
	white := createResource1991(t, c, map[string]any{
		"filename": "white-logo.svg", "content": whiteLogo1991, "content_type": "image/svg+xml",
	})
	awaitResourceTile1787(t, c, white, false)
	assertWhiteLogoTile1991(t, tile1787(t, c, "/api/v1/resources/"+white, "light"))

	black := createResource1991(t, c, map[string]any{
		"filename": "black-logo.svg", "content_type": "image/svg+xml",
		"content": `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100" width="100%" height="100%">` +
			`<circle cx="50" cy="50" r="30" fill="#000000"/></svg>`,
	})
	awaitResourceTile1787(t, c, black, false)
	img := tile1787(t, c, "/api/v1/resources/"+black, "light")
	b := img.Bounds()
	if !transparentAt(img, b.Min.X+2, b.Min.Y+2) {
		t.Errorf("a black SVG's canvas was stored opaque: %v", img.At(b.Min.X+2, b.Min.Y+2))
	}
	if cx, cy := (b.Min.X+b.Max.X)/2, (b.Min.Y+b.Max.Y)/2; !opaqueNear(img, cx, cy, color.RGBA{0, 0, 0, 255}) {
		t.Errorf("the black artwork is not in the tile: centre pixel %v", img.At(cx, cy))
	}
}

// TestIssue1991_AnSVGAssetTileKeepsItsTransparency holds an asset's tile to
// the same rule: the cards and collection viewer show the one stored image in
// both schemes.
func TestIssue1991_AnSVGAssetTileKeepsItsTransparency(t *testing.T) {
	c := connect(t)
	id := saveAsset1787(t, c, "image/svg+xml", whiteLogo1991)
	awaitAssetTile1787(t, c, id, false)
	assertWhiteLogoTile1991(t, tile1787(t, c, "/api/v1/portal/assets/"+id, "light"))
}

// TestIssue1991_ATransparentPNGTileKeepsItsTransparency is criterion 4: a
// raster image's tile keeps the image's alpha. The PNG is transparent on its
// left half and opaque black on its right, and the tile covers the card with
// it the way the card shows it.
func TestIssue1991_ATransparentPNGTileKeepsItsTransparency(t *testing.T) {
	c := connect(t)
	src := image.NewNRGBA(image.Rect(0, 0, 80, 60))
	for x := 40; x < 80; x++ {
		for y := 0; y < 60; y++ {
			src.Set(x, y, color.NRGBA{0, 0, 0, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatalf("encoding the fixture: %v", err)
	}
	id := createResource1991(t, c, map[string]any{
		"filename": "half-transparent.png", "content_type": "image/png",
		"content_base64": base64.StdEncoding.EncodeToString(buf.Bytes()),
	})
	awaitResourceTile1787(t, c, id, false)
	img := tile1787(t, c, "/api/v1/resources/"+id, "light")
	b := img.Bounds()
	midY := (b.Min.Y + b.Max.Y) / 2
	if !transparentAt(img, b.Min.X+10, midY) {
		t.Errorf("the PNG's transparent half was stored opaque: %v", img.At(b.Min.X+10, midY))
	}
	if !opaqueNear(img, b.Max.X-10, midY, color.RGBA{0, 0, 0, 255}) {
		t.Errorf("the PNG's opaque half is not in the tile: %v", img.At(b.Max.X-10, midY))
	}
}

// TestIssue1991_AnSVGDrawnBeforeTheChangeIsDrawnAgain is criterion 5: a tile
// an earlier renderer generation drew is drawn again with no new upload. The
// stamp of the generation that drew the tile is set back to 2, which is what
// every SVG tile stored before the upgrade carries after migration 000172;
// the running platform's tile worker finds it owed, draws it transparent and
// stamps it with the current generation.
func TestIssue1991_AnSVGDrawnBeforeTheChangeIsDrawnAgain(t *testing.T) {
	// Two draws are waited on: the upload's and the redraw.
	c := connectFor(t, 2*tileWait1787+time.Minute)
	id := createResource1991(t, c, map[string]any{
		"filename": "old-logo.svg", "content": whiteLogo1991, "content_type": "image/svg+xml",
	})
	awaitResourceTile1787(t, c, id, false)

	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the dev database: %v", err)
	}
	defer func() { _ = db.Close() }()
	// The generation stamp is written only by the tile worker, when it records
	// a tile it drew; a resource's captured_at is the file's updated_at, so it
	// does not move on a redraw of the same file.
	if _, err := db.ExecContext(c.ctx, `UPDATE resources SET thumbnail_renderer = 2 WHERE id = $1`, id); err != nil {
		t.Fatalf("stamping the tile with the previous generation: %v", err)
	}

	deadline := time.Now().Add(tileWait1787)
	for {
		var renderer int
		if err := db.QueryRowContext(c.ctx, `SELECT thumbnail_renderer FROM resources WHERE id = $1`, id).Scan(&renderer); err != nil {
			t.Fatalf("reading the tile's stamp: %v", err)
		}
		if renderer == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the tile drawn by generation 2 was not drawn again after %s (renderer %d)", tileWait1787, renderer)
		}
		time.Sleep(time.Second)
	}
	assertWhiteLogoTile1991(t, tile1787(t, c, "/api/v1/resources/"+id, "light"))
}
