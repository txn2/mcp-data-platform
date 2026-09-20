//go:build integration

package acceptance

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Issue #1794: a PDF gets a thumbnail.
//
// A PDF was never offered for capture at all, so a library of them was a wall
// of identical red icons: the one place a tile is doing real work, and the one
// family that had none. The viewer hands a PDF to the browser's own plugin,
// which the platform's headless renderer does not ship; the tile page now
// rasterizes page one itself with pdf.js, which needs no plugin.
//
// These criteria run through the real surface on the local stack. A PDF is
// uploaded with the tool a person calls, the platform draws it on its own in
// the renderer beside it, and the stored PNG is read back from the route the
// portal reads and measured. Nothing here draws anything itself.
//
// They need the renderer the dev stack starts beside the platform. A platform
// with no renderer reachable draws nothing, and every criterion fails on its
// wait rather than skipping: a gate that skips is a gate that was not run.
//
// Wire forms. manage_resource's action, filename, display_name, path,
// description, content_base64 and content_type are typed strings in its schema
// (pkg/toolkits/portal/schemas.go), and api_export's connection, method, path,
// name and purpose are too, so each admits exactly one JSON form and each is
// sent below as a literal tools/call parameter of that form. save_asset's
// references is a typed array of strings and is sent as one. The tile routes
// take their variant, and the reference route its thumbnail and variant, as
// query-string parameters, which have no second form. The oversize upload goes
// through the resource content route as a multipart form, which is the form
// the portal's own upload posts and the only one that route takes.

// tileWait1794 bounds how long a criterion waits for the platform to draw a
// tile on its own. The worker polls on an interval, and a render that hangs is
// abandoned at the renderer's own deadline well inside this.
const tileWait1794 = 150 * time.Second

// oversize1794 is comfortably past thumbtypes.LargeSourceLimit (32 MB), which
// is the bound a PDF is held to. Every other family is still held to 1 MB.
const oversize1794 = 34 << 20

func unique1794() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
}

// pdfFixture1794 reads one of the two documents the criteria measure. Each
// fills the top of page one with a color nothing else on the page uses, and
// fills page TWO solid black, so "page one was drawn" and "this document was
// drawn, not some other" are both measurements rather than opinions.
func pdfFixture1794(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading the PDF fixture %s: %v", name, err)
	}
	return body
}

// uploadPDFResource1794 puts a PDF in the library through the tool a person
// calls, and returns its id.
func uploadPDFResource1794(t *testing.T, c *client, fixture string) string {
	t.Helper()
	name := "acceptance-1794-" + unique1794()
	out := c.call("manage_resource", map[string]any{
		"action":         "create",
		"filename":       name + ".pdf",
		"display_name":   name,
		"path":           "acceptance-1794",
		"description":    "Acceptance #1794: a PDF the platform draws a tile of.",
		"content_base64": base64.StdEncoding.EncodeToString(pdfFixture1794(t, fixture)),
		"content_type":   "application/pdf",
	})
	id, _ := out["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	return id
}

// resourceRow1794 reads a resource the way the library page does.
func resourceRow1794(t *testing.T, c *client, id string) map[string]any {
	t.Helper()
	status, row := c.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET resource %s: status %d: %v", id, status, row)
	}
	return row
}

// awaitResourceTile1794 waits until the platform has drawn the file as it
// stands, and fails with the recorded reason the moment it says it could not.
func awaitResourceTile1794(t *testing.T, c *client, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(tileWait1794)
	for {
		row := resourceRow1794(t, c, id)
		if failure, _ := row["thumbnail_failure"].(string); failure != "" {
			t.Fatalf("the platform could not draw resource %s: %s", id, failure)
		}
		if drawn, _ := row["thumbnail_captured_at"].(string); drawn != "" {
			return row
		}
		if time.Now().After(deadline) {
			t.Fatalf("no tile for resource %s after %s. Is the renderer running beside the platform?", id, tileWait1794)
		}
		time.Sleep(time.Second)
	}
}

// awaitAssetTile1794 is the same wait for an asset, which versions its tile
// rather than dating it.
func awaitAssetTile1794(t *testing.T, c *client, id string) {
	t.Helper()
	deadline := time.Now().Add(tileWait1794)
	for {
		status, row := c.rest(http.MethodGet, "/api/v1/portal/assets/"+id, http.NoBody)
		if status != http.StatusOK {
			t.Fatalf("GET asset %s: status %d: %v", id, status, row)
		}
		if failure, _ := row["thumbnail_failure"].(string); failure != "" {
			t.Fatalf("the platform could not draw asset %s: %s", id, failure)
		}
		version, _ := row["current_version"].(float64)
		drawn, _ := row["thumbnail_version"].(float64)
		if drawn >= version && drawn > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no tile for asset %s after %s (version %v, tile %v). Is the renderer running beside the platform?",
				id, tileWait1794, version, drawn)
		}
		time.Sleep(time.Second)
	}
}

// readPNG1794 fetches a URL as this client and returns the status and bytes.
func readPNG1794(t *testing.T, c *client, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, baseURL()+path, http.NoBody)
	if err != nil {
		t.Fatalf("building the read of %s: %v", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(res.Body)
	return res.StatusCode, buf.Bytes()
}

// tile1794 reads a stored tile and decodes it, failing unless it is a PNG.
func tile1794(t *testing.T, c *client, path string) image.Image {
	t.Helper()
	status, data := readPNG1794(t, c, path)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d: %s", path, status, data)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("the stored tile at %s is not a PNG: %v", path, err)
	}
	return img
}

// share1794 is the fraction of the tile within tolerance of target.
func share1794(img image.Image, target color.RGBA, tolerance int) float64 {
	b := img.Bounds()
	total, hit := 0, 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			total++
			r, g, bl, _ := img.At(x, y).RGBA()
			within := func(a uint32, want uint8) bool {
				d := int(a>>8) - int(want)
				if d < 0 {
					d = -d
				}
				return d <= tolerance
			}
			if within(r, target.R) && within(g, target.G) && within(bl, target.B) {
				hit++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(hit) / float64(total)
}

var (
	teal1794   = color.RGBA{R: 0x00, G: 0x8B, B: 0x8B}
	orange1794 = color.RGBA{R: 0xFF, G: 0x8C, B: 0x00}
	black1794  = color.RGBA{}
)

// TestIssue1794_APDFResourceShowsItsFirstPage is criterion 1: a PDF uploaded
// to the library gets a tile of its first page, drawn the way the document
// looks -- not a generic page icon, and not page two.
func TestIssue1794_APDFResourceShowsItsFirstPage(t *testing.T) {
	c := connectFor(t, 3*tileWait1794)
	id := uploadPDFResource1794(t, c, "issue_1794_teal.pdf")
	awaitResourceTile1794(t, c, id)

	img := tile1794(t, c, "/api/v1/resources/"+id+"/thumbnail")
	if got := share1794(img, teal1794, 24); got < 0.2 {
		t.Fatalf("the first page's fill covers %.1f%% of the tile; the page was not drawn", 100*got)
	}
	// Page two is solid black. A tile showing it is a tile of the wrong page.
	if got := share1794(img, black1794, 20); got > 0.5 {
		t.Fatalf("%.1f%% of the tile is the second page's fill; the tile must be page one", 100*got)
	}
}

// TestIssue1794_TheTileIsThisDocumentNotAGenericPage is criterion 2: two PDFs
// get two different tiles. A generic page icon would pass criterion 1 for any
// document that happened to be the right color; this is what says the tile is
// the document.
func TestIssue1794_TheTileIsThisDocumentNotAGenericPage(t *testing.T) {
	c := connectFor(t, 3*tileWait1794)
	tealID := uploadPDFResource1794(t, c, "issue_1794_teal.pdf")
	orangeID := uploadPDFResource1794(t, c, "issue_1794_orange.pdf")
	awaitResourceTile1794(t, c, tealID)
	awaitResourceTile1794(t, c, orangeID)

	tealTile := tile1794(t, c, "/api/v1/resources/"+tealID+"/thumbnail")
	orangeTile := tile1794(t, c, "/api/v1/resources/"+orangeID+"/thumbnail")
	if got := share1794(tealTile, orange1794, 24); got > 0.05 {
		t.Fatalf("the teal document's tile is %.1f%% the other document's color", 100*got)
	}
	if got := share1794(orangeTile, orange1794, 24); got < 0.2 {
		t.Fatalf("the orange document's tile is only %.1f%% its own color", 100*got)
	}
}

// TestIssue1794_ThereIsOneTileForBothColorSchemes is criterion 3: a PDF page
// is drawn as the document looks, so one image serves light and dark. A file
// owed a dark tile nothing draws is offered for capture forever.
func TestIssue1794_ThereIsOneTileForBothColorSchemes(t *testing.T) {
	c := connectFor(t, 3*tileWait1794)
	id := uploadPDFResource1794(t, c, "issue_1794_teal.pdf")
	row := awaitResourceTile1794(t, c, id)

	if dark, _ := row["thumbnail_dark_s3_key"].(string); dark != "" {
		t.Errorf("a PDF stored a dark capture (%s); it is drawn as the document looks and needs one", dark)
	}
	// The dark variant answers with the light capture, which is what a dark
	// portal asking for one must get rather than nothing.
	light := tile1794(t, c, "/api/v1/resources/"+id+"/thumbnail")
	darkAsked := tile1794(t, c, "/api/v1/resources/"+id+"/thumbnail?variant=dark")
	if got := share1794(darkAsked, teal1794, 24); got < 0.2 {
		t.Fatalf("the dark request answered a tile that is %.1f%% the page's fill; it must fall back to the light capture", 100*got)
	}
	if light.Bounds() != darkAsked.Bounds() {
		t.Errorf("the two answers are different images: %v and %v", light.Bounds(), darkAsked.Bounds())
	}
}

// TestIssue1794_APDFAssetShowsItsFirstPage is criterion 4: the Assets gallery
// shows a PDF asset's first page too.
//
// The asset is made the way the platform makes a PDF asset: an export writing
// an upstream's response, which is the one door that stores a type the string
// allowlist does not admit (portaldomain.ValidateContentTypeChange names it).
// The upstream is the platform's own admin REST API, through the platform-admin
// connection every deployment registers -- a real upstream, not a stand-in
// written for this test.
func TestIssue1794_APDFAssetShowsItsFirstPage(t *testing.T) {
	c := connectFor(t, 3*tileWait1794)
	resourceID := uploadPDFResource1794(t, c, "issue_1794_teal.pdf")

	out := c.call("api_export", map[string]any{
		"connection": "platform-admin",
		"method":     "GET",
		"path":       "/api/v1/resources/" + resourceID + "/content",
		"name":       "acceptance-1794-asset-" + unique1794(),
		"purpose":    "Acceptance #1794: producing a PDF asset the way an export produces one, to check the gallery draws its first page.",
	})
	assetID, _ := out["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("api_export returned no asset_id: %v", out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID})
	})
	if ct, _ := out["content_type"].(string); ct != "application/pdf" {
		t.Fatalf("the exported asset is %q, not a PDF; this criterion no longer tests what it names", ct)
	}

	awaitAssetTile1794(t, c, assetID)
	img := tile1794(t, c, "/api/v1/portal/assets/"+assetID+"/thumbnail")
	if got := share1794(img, teal1794, 24); got < 0.2 {
		t.Fatalf("the PDF asset's tile is %.1f%% the first page's fill; the page was not drawn", 100*got)
	}
}

// TestIssue1794_AReferencedPDFShowsItsTile is criterion 5: an asset's
// References panel shows a referenced PDF as the tile the platform drew for
// it. The panel reads it through the reference's own URL, which is the grant
// the asset makes -- a reader of a shared asset may have no access to the
// target's own route.
func TestIssue1794_AReferencedPDFShowsItsTile(t *testing.T) {
	c := connectFor(t, 3*tileWait1794)
	resourceID := uploadPDFResource1794(t, c, "issue_1794_teal.pdf")
	row := awaitResourceTile1794(t, c, resourceID)
	uri, _ := row["uri"].(string)
	if uri == "" {
		t.Fatalf("the uploaded resource carries no uri: %v", row)
	}

	saved := c.call("save_asset", map[string]any{
		"name":         "acceptance-1794-refs-" + unique1794(),
		"content":      "# Report\n\nThe source document is [attached](" + uri + ").\n",
		"content_type": "text/markdown",
		"description":  "Acceptance #1794: an asset that references a PDF.",
		"references":   []any{uri},
	})
	assetID, _ := saved["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("save_asset returned no asset_id: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID})
	})

	status, refs := c.rest(http.MethodGet, "/api/v1/portal/assets/"+assetID+"/references", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET the asset's references: status %d: %v", status, refs)
	}
	contentURL := refContentURL1794(t, refs)

	// The panel appends this query to the reference's own URL.
	tileStatus, data := readPNG1794(t, c, contentURL+"?thumbnail=1")
	if tileStatus != http.StatusOK {
		t.Fatalf("the reference's tile answered %d: %s", tileStatus, data)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("the reference's tile is not a PNG: %v", err)
	}
	if got := share1794(img, teal1794, 24); got < 0.2 {
		t.Fatalf("the referenced PDF's tile is %.1f%% the first page's fill; the panel is not showing its tile", 100*got)
	}
}

// refContentURL1794 pulls the one reference's serving URL out of the panel's
// own response, as a path this client can fetch.
func refContentURL1794(t *testing.T, body map[string]any) string {
	t.Helper()
	list, _ := body["data"].([]any)
	if len(list) != 1 {
		t.Fatalf("the asset carries %d references, want exactly one: %v", len(list), body)
	}
	ref, _ := list[0].(map[string]any)
	full, _ := ref["content_url"].(string)
	if full == "" {
		t.Fatalf("the reference carries no content_url: %v", ref)
	}
	// content_url is absolute; this client addresses the platform by its own
	// base, which the two-replica lane puts behind a proxy.
	if i := indexOfPath1794(full); i >= 0 {
		return full[i:]
	}
	t.Fatalf("the reference's content_url is not a URL this client can follow: %q", full)
	return ""
}

// indexOfPath1794 is where the path begins in an absolute http(s) URL.
func indexOfPath1794(u string) int {
	for _, prefix := range []string{"http://", "https://"} {
		if len(u) > len(prefix) && u[:len(prefix)] == prefix {
			for i := len(prefix); i < len(u); i++ {
				if u[i] == '/' {
					return i
				}
			}
			return -1
		}
	}
	if len(u) > 0 && u[0] == '/' {
		return 0
	}
	return -1
}

// TestIssue1794_APDFPastTheBoundKeepsItsIcon is criterion 6: the source bound
// rises for this family and still exists. A PDF past it is never offered for
// capture -- no tile and no recorded failure, because nothing was attempted --
// which is what leaves the card showing its content-type icon.
//
// The bytes go through the resource content route as the multipart form the
// portal's own upload posts; a 34 MB document base64'd into a tool argument is
// not how anyone uploads one.
func TestIssue1794_APDFPastTheBoundKeepsItsIcon(t *testing.T) {
	c := connectFor(t, 3*tileWait1794)
	id := uploadPDFResource1794(t, c, "issue_1794_teal.pdf")
	awaitResourceTile1794(t, c, id)

	// Replace the file with one past the bound. The tile it already has is of
	// the old file, so what is asserted is that no NEW one is drawn.
	before := resourceRow1794(t, c, id)
	oldTile, _ := before["thumbnail_captured_at"].(string)
	putOversizePDF1794(t, c, id)

	after := resourceRow1794(t, c, id)
	if size, _ := after["size_bytes"].(float64); int64(size) < oversize1794 {
		t.Fatalf("the replacement stored %v bytes, want at least %d; this criterion no longer tests the bound", size, oversize1794)
	}

	// Give the worker several polling rounds to prove it does NOT take the
	// work, rather than proving it has not taken it yet.
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		row := resourceRow1794(t, c, id)
		if drawn, _ := row["thumbnail_captured_at"].(string); drawn != oldTile {
			t.Fatalf("a PDF of %d bytes was drawn at %s; it is past the bound and must never be offered", oversize1794, drawn)
		}
		if failure, _ := row["thumbnail_failure"].(string); failure != "" {
			t.Fatalf("a PDF past the bound recorded the failure %q; it must never be offered at all", failure)
		}
		time.Sleep(3 * time.Second)
	}
}

// putOversizePDF1794 replaces a resource's file with a valid PDF past the
// bound, through the content route the portal's own upload posts to -- a
// multipart form with the file in it, which is the one form that route takes.
func putOversizePDF1794(t *testing.T, c *client, id string) {
	t.Helper()
	body := padPDF1794(pdfFixture1794(t, "issue_1794_teal.pdf"), oversize1794)
	form := new(bytes.Buffer)
	writer := multipart.NewWriter(form)
	part, err := writer.CreateFormFile("file", "oversize.pdf")
	if err != nil {
		t.Fatalf("building the oversize upload: %v", err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatalf("writing the oversize upload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("closing the oversize upload: %v", err)
	}
	req, err := http.NewRequestWithContext(c.ctx, http.MethodPost,
		baseURL()+"/api/v1/resources/"+id+"/content", form)
	if err != nil {
		t.Fatalf("building the oversize upload: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		t.Fatalf("uploading the oversize PDF: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusCreated {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(res.Body)
		t.Fatalf("uploading the oversize PDF: status %d: %s", res.StatusCode, buf.String())
	}
}

// padPDF1794 grows a PDF past size while leaving it a PDF: the padding is a
// comment line, which the format ignores, appended after %%EOF.
func padPDF1794(body []byte, size int) []byte {
	out := make([]byte, 0, size+64)
	out = append(out, body...)
	out = append(out, '\n', '%')
	filler := bytes.Repeat([]byte("0123456789abcdef"), 4096)
	for len(out) < size {
		out = append(out, filler...)
	}
	return out
}

// TestIssue1794_APDFThatCannotBeDrawnRecordsWhy is criterion 7: a document
// pdf.js cannot read records the reason and is not re-offered until the file
// changes, rather than being retried forever.
func TestIssue1794_APDFThatCannotBeDrawnRecordsWhy(t *testing.T) {
	c := connectFor(t, 3*tileWait1794)
	name := "acceptance-1794-broken-" + unique1794()
	out := c.call("manage_resource", map[string]any{
		"action":         "create",
		"filename":       name + ".pdf",
		"display_name":   name,
		"path":           "acceptance-1794",
		"description":    "Acceptance #1794: a PDF that cannot be decoded.",
		"content_base64": base64.StdEncoding.EncodeToString(brokenPDF1794()),
		"content_type":   "application/pdf",
	})
	id, _ := out["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })

	deadline := time.Now().Add(tileWait1794)
	var reason string
	for {
		row := resourceRow1794(t, c, id)
		if reason, _ = row["thumbnail_failure"].(string); reason != "" {
			if drawn, _ := row["thumbnail_captured_at"].(string); drawn != "" {
				t.Fatalf("a document that could not be read also stored a tile at %s", drawn)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a PDF that cannot be decoded recorded no reason after %s; it is being retried forever", tileWait1794)
		}
		time.Sleep(2 * time.Second)
	}
	t.Logf("the recorded reason is %q", reason)

	// Recorded against the file as it stands: the claim excludes a row whose
	// failure is not older than its updated_at, so the worker does not take it
	// again until the document changes.
	for i := 0; i < 5; i++ {
		time.Sleep(3 * time.Second)
		row := resourceRow1794(t, c, id)
		if drawn, _ := row["thumbnail_captured_at"].(string); drawn != "" {
			t.Fatalf("the document was drawn at %s after being refused; it must not be re-offered until it changes", drawn)
		}
	}
}

// brokenPDF1794 begins with the PDF signature -- so detection stores it as one
// and the worker offers it -- and holds nothing pdf.js can parse.
func brokenPDF1794() []byte {
	return []byte("%PDF-1.4\nthis file claims to be a PDF and has no objects, xref or trailer\n")
}
