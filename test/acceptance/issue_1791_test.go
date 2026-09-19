//go:build integration

package acceptance

import (
	"fmt"
	"image/color"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// Issue #1791: a document's references reach the tile it is drawn into, and a
// reader, however many it declares.
//
// The tile worker loads a document's declared references from the platform's
// reference route in-process, and that route is behind a per-client rate
// limiter. Every in-process request presented the same loopback address, so
// every reference of every tile the worker drew came out of one bucket, and a
// document drawn after that bucket ran dry was recorded as not drawable: its
// light tile had loaded its files and its dark tile had loaded none. The
// limiter was also never scaled to a page's references on a deployment that
// sets no portal.rate_limit, so a reader opening a report that declares more
// than ten files had the rest refused.
//
// These criteria are executed through the real surface on a platform with no
// portal.rate_limit block (the dev stack's): files are created and an asset
// declaring them is saved with the tools a user calls, the platform draws it
// on its own, and the references are fetched from the route a reader's browser
// fetches them from.
//
// Wire forms: manage_resource's action, filename, display_name, path,
// description, content and content_type, and save_asset's name, content,
// content_type and description, are typed strings in their schemas, and
// save_asset's references is a typed array of strings; each admits exactly one
// JSON form and each is sent below as a literal tools/call parameter of that
// form. The reference route takes no parameters beyond its path.

// maxRefs1791 is how many references one asset may declare
// (assetrefs.MaxRefs), the most one page load fetches.
const maxRefs1791 = 20

// swatch1791 is the color every referenced file is painted, which nothing
// else on the page uses, so a tile shows whether its references loaded.
var swatch1791 = color.RGBA{R: 0xff, G: 0x00, B: 0xaa}

// swatchSVG1791 is one referenced file.
const swatchSVG1791 = `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><rect width="100" height="100" fill="#ff00aa"/></svg>`

// createSwatches1791 files maxRefs1791 images and returns their URIs.
func createSwatches1791(t *testing.T, c *client) []string {
	t.Helper()
	run := unique1787()
	uris := make([]string, 0, maxRefs1791)
	for i := range maxRefs1791 {
		name := fmt.Sprintf("acceptance-1791-%s-%02d", run, i)
		out := c.call("manage_resource", map[string]any{
			"action":       "create",
			"filename":     name + ".svg",
			"display_name": name,
			"path":         "acceptance-1791",
			"description":  "Acceptance #1791: one of the files a document declares.",
			"content":      swatchSVG1791,
			"content_type": "image/svg+xml",
		})
		id, _ := out["resource_id"].(string)
		uri, _ := out["uri"].(string)
		if id == "" || uri == "" {
			t.Fatalf("manage_resource create returned no resource: %v", out)
		}
		t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
		uris = append(uris, uri)
	}
	return uris
}

// saveReferencing1791 saves an HTML asset that shows every file in uris,
// declares them, and returns its id. Its page is a grid of the files on a
// background that answers the reader's color scheme.
func saveReferencing1791(t *testing.T, c *client, uris []string) string {
	t.Helper()
	var imgs strings.Builder
	for _, u := range uris {
		imgs.WriteString(`<img src="` + u + `" width="220" height="220">`)
	}
	doc := `<!DOCTYPE html><html><head><meta charset="utf-8"><style>
body{margin:0;background:#f6f7f9}@media (prefers-color-scheme: dark){body{background:#0d1117}}
img{display:block;float:left;margin:0 36px 20px 0}
</style></head><body>` + imgs.String() + `</body></html>`
	out := c.call("save_asset", map[string]any{
		"name":         "acceptance-1791-" + unique1787(),
		"content":      doc,
		"content_type": "text/html",
		"description":  "Acceptance #1791: a document declaring as many files as an asset may.",
		"references":   uris,
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

// assertSwatchesDrawn1791 holds both of an asset's tiles to showing the files
// it declares: twenty swatches cover a large share of the page.
func assertSwatchesDrawn1791(t *testing.T, c *client, id string) {
	t.Helper()
	for _, variant := range []string{"light", "dark"} {
		img := tile1787(t, c, "/api/v1/portal/assets/"+id, variant)
		if got := share1787(img, img.Bounds(), swatch1791, 24); got < 0.6 {
			t.Errorf("the %s tile is %.1f%% the referenced files' color; with every file loaded it is far more", variant, 100*got)
		}
	}
}

// TestIssue1791_ADocumentDeclaringTheMostFilesIsDrawnInBothSchemes is
// criterion 1: an HTML asset that declares as many files as an asset may gets
// a light tile and a dark tile, each showing the files, and no failure.
func TestIssue1791_ADocumentDeclaringTheMostFilesIsDrawnInBothSchemes(t *testing.T) {
	c := connectFor(t, 4*tileWait1787)
	id := saveReferencing1791(t, c, createSwatches1791(t, c))
	awaitAssetTile1787(t, c, id, true)
	assertSwatchesDrawn1791(t, c, id)
}

// TestIssue1791_ABacklogOfSuchDocumentsIsDrawnInFull is criterion 2: the same
// holds while the worker draws several such documents back to back, which is
// what it does after an upgrade redraws the library.
func TestIssue1791_ABacklogOfSuchDocumentsIsDrawnInFull(t *testing.T) {
	c := connectFor(t, 6*tileWait1787)
	uris := createSwatches1791(t, c)
	ids := []string{saveReferencing1791(t, c, uris), saveReferencing1791(t, c, uris), saveReferencing1791(t, c, uris)}
	for _, id := range ids {
		awaitAssetTile1787(t, c, id, true)
		assertSwatchesDrawn1791(t, c, id)
	}
}

// TestIssue1791_AReaderLoadsEveryFileAtOnce is criterion 3: a reader's page
// load of that asset fetches all of its files. They are fetched together and
// without credentials, the way a browser loads the images of a page it opens.
func TestIssue1791_AReaderLoadsEveryFileAtOnce(t *testing.T) {
	c := connect(t)
	id := saveReferencing1791(t, c, createSwatches1791(t, c))

	listed := listRefs1584(t, c, id)
	if len(listed) != maxRefs1791 {
		t.Fatalf("the asset lists %d references, want %d", len(listed), maxRefs1791)
	}
	statuses := make([]int, len(listed))
	var wg sync.WaitGroup
	for i, ref := range listed {
		url, _ := ref["content_url"].(string)
		if strings.HasPrefix(url, "/") {
			url = baseURL() + url
		}
		wg.Go(func() { statuses[i] = anonymousGet1791(t, url) })
	}
	wg.Wait()
	for i, status := range statuses {
		if status != http.StatusOK {
			t.Errorf("file %d of %d answered %d, want 200: a page load is refused part of what it declared", i+1, len(statuses), status)
		}
	}
}

// anonymousGet1791 fetches a URL with no credentials and returns the status.
func anonymousGet1791(t *testing.T, url string) int {
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Errorf("building %s: %v", url, err)
		return 0
	}
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		t.Errorf("GET %s: %v", url, err)
		return 0
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode
}

// darkNeverSettles1791 draws at once in a light scheme and never settles in a
// dark one.
const darkNeverSettles1791 = `<!DOCTYPE html><html><body style="margin:0;background:#16a34a;height:100vh">
<script>if (matchMedia("(prefers-color-scheme: dark)").matches) { while (true) {} }</script></body></html>`

// TestIssue1791_ADarkTileThatFailsKeepsTheLightOne is criterion 4, the record
// the thumbnail panel reads: when the light tile draws and the dark one
// cannot, the light tile of this version is kept and served, and the failure
// stands against this version with its reason. The panel tells the two apart
// by the light tile's version, which is why the light tile is drawn first.
func TestIssue1791_ADarkTileThatFailsKeepsTheLightOne(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	id := saveAsset1787(t, c, "text/html", darkNeverSettles1791)

	deadline := time.Now().Add(tileWait1787)
	var row map[string]any
	for {
		row = assetRow1787(t, c, id)
		if f, _ := row["thumbnail_failure"].(string); f != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no failure recorded for a document whose dark scheme never settles after %s: %v", tileWait1787, row)
		}
		time.Sleep(time.Second)
	}
	current := intField1787(row, "current_version")
	if got := intField1787(row, "thumbnail_version"); got != current {
		t.Errorf("the light tile is of version %d, want the current version %d", got, current)
	}
	if got := intField1787(row, "thumbnail_dark_version"); got != 0 {
		t.Errorf("a dark tile was recorded, version %d, for a document whose dark scheme never settles", got)
	}
	if got := intField1787(row, "thumbnail_failed_version"); got != current {
		t.Errorf("the failure is dated version %d, want the current version %d", got, current)
	}
	img := tile1787(t, c, "/api/v1/portal/assets/"+id, "light")
	green := color.RGBA{R: 0x16, G: 0xa3, B: 0x4a}
	if got := share1787(img, img.Bounds(), green, 12); got < 0.9 {
		t.Errorf("the light tile is %.1f%% the document's color, want nearly all of it", 100*got)
	}
}
