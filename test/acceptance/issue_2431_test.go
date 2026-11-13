//go:build integration

package acceptance

import (
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Issue #2431: an HTML or JSX asset embedding a form (mcp:form:<endpoint>)
// gets a tile, light and dark, and a PDF that show a card naming the form in
// the frame, not the renderer's "not found" page, and drawing them runs none
// of the form's script.
//
// Wire forms: save_asset's name, content and content_type are strings and
// references a list of strings, each sent as a literal tools/call parameter;
// manage_script create and handler_set kind=endpoint as in #2178; the tile and PDF routes
// are plain GETs.
//
// Stack: `make dev`, with the renderer beside the platform and the form
// stack #2178 uses (Trino and the scratch catalog its source writes to).

const issue2431Purpose = "Acceptance #2431: an embedded form in a tile and a PDF."

// The card's colors (internal/endpoint/epform/card.go): its background and
// the button's accent, in each scheme.
var (
	issue2431LightBG     = color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	issue2431LightAccent = color.RGBA{R: 0x25, G: 0x63, B: 0xeb, A: 0xff}
	issue2431DarkBG      = color.RGBA{R: 0x11, G: 0x18, B: 0x27, A: 0xff}
	issue2431DarkAccent  = color.RGBA{R: 0x60, G: 0xa5, B: 0xfa, A: 0xff}
)

// issue2431Accent matches the light accent as a PDF fill: 0x25 0x63 0xeb as
// fractions of 255, written by Chrome without the leading zero.
var issue2431Accent = regexp.MustCompile(`0?\.145\d* 0?\.388\d* 0?\.92[12]\d* (rg|sc|scn)`)

// saveFormAsset2431 saves an asset of contentType whose whole page is the
// form's frame, and returns its id.
func saveFormAsset2431(t *testing.T, c *client, contentType, content, form string) string {
	t.Helper()
	saved := c.call("save_asset", map[string]any{
		"name": "Acceptance 2431 " + contentType + " " + time.Now().Format("150405.000"), "content": content,
		"content_type": contentType, "references": []any{"mcp:form:" + form}, "purpose": issue2431Purpose,
	})
	id, _ := saved["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id, "purpose": issue2431Purpose})
	})
	return id
}

// endpointRequests2431 is endpoint_requests_total for one endpoint, summed
// over every replica: each request a script bound to it answered.
func endpointRequests2431(t *testing.T, endpoint string) float64 {
	t.Helper()
	var total float64
	for _, target := range metricsURLs() {
		total += metricSeries(scrapeOne(t, target), "endpoint_requests_total", map[string]string{"endpoint": endpoint})
	}
	return total
}

// assertCard2431 fails unless the tile shows the card: its background over
// most of the tile and its button's accent somewhere in it.
func assertCard2431(t *testing.T, img image.Image, what string, bg, accent color.RGBA) {
	t.Helper()
	saveTile2431(t, img, what)
	b := img.Bounds()
	if got := share1787(img, b, bg, 12); got < 0.5 {
		t.Errorf("%s: %.0f%% of the tile is the card's background, want most of it", what, got*100)
	}
	if got := share1787(img, b, accent, 24); got <= 0 {
		t.Errorf("%s: no pixel of the card's button; the frame did not draw the card", what)
	}
}

// saveTile2431 keeps the tile under build/2431/ beside the run's transcript.
func saveTile2431(t *testing.T, img image.Image, what string) {
	t.Helper()
	dir := filepath.Join("..", "..", "build", "2431")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Logf("keeping the tile: %v", err)
		return
	}
	f, err := os.Create(filepath.Join(dir, strings.ReplaceAll(what, " ", "-")+".png")) // #nosec G304 -- a fixed directory and a name this test writes
	if err != nil {
		t.Logf("keeping the tile: %v", err)
		return
	}
	defer f.Close() //nolint:errcheck // best-effort close of evidence
	_ = png.Encode(f, img)
}

// TestIssue2431_AnEmbeddedFormIsDrawnAsACardAndRunsNoScript: the light and
// dark tiles of an HTML asset and of a JSX asset whose page is a form's
// frame show the form's card, the HTML asset's PDF paints the card, and none
// of it reaches the form's endpoint.
func TestIssue2431_AnEmbeddedFormIsDrawnAsACardAndRunsNoScript(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	form, _ := bindForm(t, c, "acc-2431", formFields, map[string]any{"auth_mode": "public"})
	before := endpointRequests2431(t, form)

	frame := `position:fixed;top:0;left:0;width:100%;height:100%;border:0`
	htmlID := saveFormAsset2431(t, c, "text/html",
		`<!doctype html><html><body style="margin:0;background:#ff00ff"><iframe src="mcp:form:`+form+`" style="`+frame+`"></iframe></body></html>`, form)
	jsxID := saveFormAsset2431(t, c, "text/jsx",
		`export default function App() { return <iframe src="mcp:form:`+form+`" style={{position: "fixed", top: 0, left: 0, width: "100%", height: "100%", border: 0}} />; }`, form)

	for name, id := range map[string]string{"html": htmlID, "jsx": jsxID} {
		awaitAssetTile1787(t, c, id, true)
		path := "/api/v1/portal/assets/" + id
		assertCard2431(t, tile1787(t, c, path, ""), name+" light tile", issue2431LightBG, issue2431LightAccent)
		assertCard2431(t, tile1787(t, c, path, "dark"), name+" dark tile", issue2431DarkBG, issue2431DarkAccent)
	}

	status, _, pdf := issue1983Get(t, "/api/v1/portal/assets/"+htmlID+"/pdf", c.apiKey)
	if status != http.StatusOK {
		t.Fatalf("GET pdf: HTTP %d: %s", status, pdf)
	}
	if !issue2431Accent.MatchString(issue1983Streams(pdf)) {
		t.Error("the PDF paints no fill of the card's button; the frame did not print the card")
	}

	if after := endpointRequests2431(t, form); after != before {
		t.Errorf("endpoint_requests_total{endpoint=%q} went from %v to %v: drawing the asset ran the form's script", form, before, after)
	}
}
