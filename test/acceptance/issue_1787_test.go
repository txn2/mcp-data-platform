//go:build integration

package acceptance

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Issue #1787: a tile is a picture of the document, drawn by the browser.
//
// A thumbnail used to be a second rendering of the document by html2canvas, a
// JavaScript reimplementation of CSS painting, run in whichever portal tab
// happened to be open. Wherever the reimplementation fell short the tile was
// wrong and nothing said so: a slide deck, which lays its slides out with a CSS
// transform, came out as an empty field with the deck's corner furniture in it
// (#1784), and a report that marks its best cells with a pale wash and a 2px
// inset rule came out with those cells filled solid in the rule's color.
//
// The platform now renders every tile itself, in a headless Chrome beside it,
// through the same renderers the viewer uses. These criteria are executed
// through the real surface: a document is saved with the tools a user calls,
// the platform renders it on its own, and the stored PNG is read back from the
// route the portal reads and measured.
//
// They need the renderer the dev stack starts beside the platform. A platform
// with no renderer reachable renders nothing, and every criterion here fails on
// its wait rather than skipping: a gate that skips is a gate that was not run.
//
// Wire forms: save_asset's name, content, content_type and description,
// manage_asset's action, asset_id, content, name and sections, and
// manage_resource's action, filename, display_name, path, description, content
// and content_type are typed strings (sections a typed array of objects) in
// their schemas, so each admits exactly one JSON form and each is sent below as
// a literal tools/call parameter of that form. The thumbnail routes take their
// variant as a query-string parameter, which has no second form.

// tileWait1787 bounds how long a criterion waits for the platform to render a
// tile on its own. The worker polls on an interval and a deck loads its runtime
// before it is drawn; a render that hangs is abandoned at the renderer's own
// deadline well inside this.
const tileWait1787 = 150 * time.Second

func unique1787() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
}

// saveAsset1787 saves a document and returns its id.
func saveAsset1787(t *testing.T, c *client, contentType, body string) string {
	t.Helper()
	out := c.call("save_asset", map[string]any{
		"name":         "acceptance-1787-" + unique1787(),
		"content":      body,
		"content_type": contentType,
		"description":  "Acceptance #1787: a document the platform renders its own tile for.",
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

// assetRow1787 reads an asset the way the portal does.
func assetRow1787(t *testing.T, c *client, id string) map[string]any {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/portal/assets/"+id, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET asset %s: status %d: %v", id, status, body)
	}
	return body
}

func intField1787(row map[string]any, key string) int {
	v, _ := row[key].(float64)
	return int(v)
}

// awaitAssetTile1787 waits until the platform has rendered the asset's current
// version -- both variants when dark is true -- and fails with the recorded
// reason the moment the platform says it could not.
func awaitAssetTile1787(t *testing.T, c *client, id string, dark bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(tileWait1787)
	for {
		row := assetRow1787(t, c, id)
		if failure, _ := row["thumbnail_failure"].(string); failure != "" {
			t.Fatalf("the platform could not render asset %s: %s", id, failure)
		}
		current := intField1787(row, "current_version")
		done := intField1787(row, "thumbnail_version") >= current
		if dark {
			done = done && intField1787(row, "thumbnail_dark_version") >= current
		}
		if done {
			return row
		}
		if time.Now().After(deadline) {
			t.Fatalf("no tile for asset %s version %d after %s (light v%d, dark v%d). Is the renderer running beside the platform?",
				id, current, tileWait1787, intField1787(row, "thumbnail_version"), intField1787(row, "thumbnail_dark_version"))
		}
		time.Sleep(time.Second)
	}
}

// awaitResourceTile1787 waits until the platform has drawn a resource's tile
// of the file as it stands -- both variants when dark is true -- and fails with
// the recorded reason the moment the platform says it could not.
func awaitResourceTile1787(t *testing.T, c *client, id string, dark bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(tileWait1787)
	for {
		status, row := c.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody)
		if status != http.StatusOK {
			t.Fatalf("GET resource %s: status %d: %v", id, status, row)
		}
		if failure, _ := row["thumbnail_failure"].(string); failure != "" {
			t.Fatalf("the platform could not render resource %s: %s", id, failure)
		}
		updated, _ := row["updated_at"].(string)
		light, _ := row["thumbnail_captured_at"].(string)
		darkAt, _ := row["thumbnail_dark_captured_at"].(string)
		done := drawnSince1787(light, updated) && (!dark || drawnSince1787(darkAt, updated))
		if done {
			return row
		}
		if time.Now().After(deadline) {
			t.Fatalf("no tiles for resource %s after %s (updated %q, light %q, dark %q). Is the renderer running beside the platform?",
				id, tileWait1787, updated, light, darkAt)
		}
		time.Sleep(time.Second)
	}
}

// drawnSince1787 reports whether a tile stamped at drawn is of the file as it
// was at updated. A resource's tile is stamped with the updated_at of the file
// it was drawn from, so equal is current.
func drawnSince1787(drawn, updated string) bool {
	if drawn == "" {
		return false
	}
	d, err1 := time.Parse(time.RFC3339Nano, drawn)
	u, err2 := time.Parse(time.RFC3339Nano, updated)
	if err1 != nil || err2 != nil {
		return false
	}
	return !d.Before(u)
}

// readTile1787 reads a stored tile from the route the portal reads, returning
// the status and the raw bytes.
func readTile1787(t *testing.T, c *client, path, variant string) (int, []byte) {
	t.Helper()
	target := baseURL() + path + "/thumbnail"
	if variant != "" {
		target += "?variant=" + variant
	}
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		t.Fatalf("building the tile read: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		t.Fatalf("reading the tile: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(res.Body)
	return res.StatusCode, buf.Bytes()
}

// tile1787 reads and decodes a stored tile, failing unless it is a PNG.
func tile1787(t *testing.T, c *client, path, variant string) image.Image {
	t.Helper()
	status, data := readTile1787(t, c, path, variant)
	if status != http.StatusOK {
		t.Fatalf("GET %s/thumbnail?variant=%s: status %d: %s", path, variant, status, data)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("the stored tile for %s is not a PNG: %v", path, err)
	}
	return img
}

func near1787(c color.Color, target color.RGBA, tolerance int) bool {
	r, g, b, _ := c.RGBA()
	d := func(a uint32, want uint8) bool { return int(math.Abs(float64(int(a>>8)-int(want)))) <= tolerance }
	return d(r, target.R) && d(g, target.G) && d(b, target.B)
}

// share1787 is the fraction of the pixels inside r that are within tolerance
// of target.
func share1787(img image.Image, r image.Rectangle, target color.RGBA, tolerance int) float64 {
	r = r.Intersect(img.Bounds())
	total, hit := 0, 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			total++
			if near1787(img.At(x, y), target, tolerance) {
				hit++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(hit) / float64(total)
}

// luminance1787 is the mean relative luminance of the whole tile, 0 to 1.
func luminance1787(img image.Image) float64 {
	b := img.Bounds()
	sum, n := 0.0, 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			sum += (0.2126*float64(r>>8) + 0.7152*float64(g>>8) + 0.0722*float64(bl>>8)) / 255
			n++
		}
	}
	return sum / float64(n)
}

// deck1787 is a presentation on the runtime the platform serves, on the dark
// theme, whose title is a color nothing else on the slide uses so the slide's
// presence can be measured.
const deck1787 = `<!DOCTYPE html><html><head><meta charset="utf-8">
<link rel="stylesheet" href="/portal/vendor/reveal/reset.css">
<link rel="stylesheet" href="/portal/vendor/reveal/reveal.css">
<link rel="stylesheet" href="/portal/vendor/reveal/theme/black.css">
<style>.reveal h1{color:#ffcc00;font-size:120px}</style>
</head><body>
<div class="reveal"><div class="slides">
<section><h1>Quarterly Review</h1><p>Acceptance #1787</p></section>
<section><h2>Second slide</h2></section>
</div></div>
<script src="/portal/vendor/reveal/reveal.js"></script>
<script>Reveal.initialize({hash:false});</script>
</body></html>`

// TestIssue1787_ADeckTileShowsItsFirstSlide is criterion 1: a deck's tile is
// its first slide, drawn where the slide is -- not an empty field with the
// deck's corner furniture in it.
func TestIssue1787_ADeckTileShowsItsFirstSlide(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	id := saveAsset1787(t, c, "text/html", deck1787)
	awaitAssetTile1787(t, c, id, false)

	img := tile1787(t, c, "/api/v1/portal/assets/"+id, "")
	b := img.Bounds()
	title := color.RGBA{R: 0xff, G: 0xcc, B: 0x00}
	middle := image.Rect(b.Dx()/6, b.Dy()/6, b.Dx()*5/6, b.Dy()*5/6)
	if got := share1787(img, middle, title, 40); got < 0.04 {
		t.Fatalf("the slide title covers %.1f%% of the middle of the tile; a drawn title slide covers far more. The slide was not drawn", 100*got)
	}
	background := color.RGBA{R: 0x19, G: 0x19, B: 0x19}
	if got := share1787(img, b, background, 12); got < 0.5 {
		t.Fatalf("the deck's dark theme covers %.1f%% of the tile, want most of it", 100*got)
	}
}

// insetShadow1787 marks one wide block the way a report marks its best cell:
// a barely-tinted wash and a 2px inset rule under it, in a strong color. The
// block fills the top half of the page so its interior is easy to measure.
const insetShadow1787 = `<!DOCTYPE html><html><head><meta charset="utf-8"><style>
html,body{margin:0;background:#ffffff}
.best{height:480px;background:#FBE7F3;box-shadow:inset 0 -2px 0 #DC108A}
</style></head><body><div class="best"></div></body></html>`

// TestIssue1787_AnInsetShadowIsAHairlineNotAFill is criterion 2: a block
// carrying a pale background and an inset rule is drawn as the pale background
// with the rule, not filled solid in the rule's color.
func TestIssue1787_AnInsetShadowIsAHairlineNotAFill(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	id := saveAsset1787(t, c, "text/html", insetShadow1787)
	awaitAssetTile1787(t, c, id, false)

	img := tile1787(t, c, "/api/v1/portal/assets/"+id, "")
	b := img.Bounds()
	// The block is the top half of the page; stay clear of its bottom edge,
	// where the rule is, and of the scaling seam.
	interior := image.Rect(0, b.Dy()/20, b.Dx(), b.Dy()*9/20)
	wash := color.RGBA{R: 0xFB, G: 0xE7, B: 0xF3}
	rule := color.RGBA{R: 0xDC, G: 0x10, B: 0x8A}
	if got := share1787(img, interior, rule, 40); got > 0.01 {
		t.Fatalf("%.1f%% of the block's interior is the rule's color: the inset shadow was painted as a fill", 100*got)
	}
	if got := share1787(img, interior, wash, 8); got < 0.95 {
		t.Fatalf("the pale wash covers %.1f%% of the block's interior, want nearly all of it", 100*got)
	}
}

// markdown1787 is a document the viewer lays out in the reader's color
// scheme, so it has a tile for each.
const markdown1787 = "# Weekly Highlights\n\nThe portal lays this out in the reader's color scheme, so it is drawn twice.\n\n- one\n- two\n- three\n"

// TestIssue1787_AThemeableFileGetsATileInEachScheme is criterion 3: a document
// drawn on the portal's own surface gets a light tile and a dark tile, each in
// its own scheme.
func TestIssue1787_AThemeableFileGetsATileInEachScheme(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	id := saveAsset1787(t, c, "text/markdown", markdown1787)
	awaitAssetTile1787(t, c, id, true)

	light := luminance1787(tile1787(t, c, "/api/v1/portal/assets/"+id, "light"))
	dark := luminance1787(tile1787(t, c, "/api/v1/portal/assets/"+id, "dark"))
	if light < 0.6 {
		t.Errorf("the light tile's mean luminance is %.2f; a light surface is well above 0.6", light)
	}
	if dark > 0.35 {
		t.Errorf("the dark tile's mean luminance is %.2f; a dark surface is well below 0.35", dark)
	}
}

// TestIssue1787_AResourceIsRenderedToo is criterion 4: a file in the resource
// library gets its tile the same way an asset does, without anyone opening the
// library.
func TestIssue1787_AResourceIsRenderedToo(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	name := "acceptance-1787-" + unique1787()
	out := c.call("manage_resource", map[string]any{
		"action":       "create",
		"filename":     name + ".csv",
		"display_name": name,
		"path":         "acceptance-1787",
		"description":  "Acceptance #1787: a file the platform renders its own tile for.",
		"content":      "region,revenue\nwest,41208\neast,38112\nnorth,29911\n",
		"content_type": "text/csv",
	})
	id, _ := out["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })

	awaitResourceTile1787(t, c, id, true)
	for _, variant := range []string{"light", "dark"} {
		img := tile1787(t, c, "/api/v1/resources/"+id, variant)
		if b := img.Bounds(); b.Dx() != 800 || b.Dy() != 600 {
			t.Errorf("the %s tile is %dx%d, want 800x600 (#1789)", variant, b.Dx(), b.Dy())
		}
	}
}

// TestIssue1787_ARewrittenDocumentIsRenderedAgain is criterion 5: writing a new
// version is enough to get a tile of the new version.
func TestIssue1787_ARewrittenDocumentIsRenderedAgain(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	solid := func(hex string) string {
		return `<!DOCTYPE html><html><body style="margin:0;background:` + hex + `;height:100vh"></body></html>`
	}
	id := saveAsset1787(t, c, "text/html", solid("#16a34a"))
	awaitAssetTile1787(t, c, id, false)
	green := color.RGBA{R: 0x16, G: 0xa3, B: 0x4a}
	first := tile1787(t, c, "/api/v1/portal/assets/"+id, "")
	if got := share1787(first, first.Bounds(), green, 12); got < 0.9 {
		t.Fatalf("the first version's tile is %.1f%% green, want nearly all of it", 100*got)
	}

	c.call("manage_asset", map[string]any{"action": "update", "asset_id": id, "content": solid("#7c3aed")})
	awaitAssetTile1787(t, c, id, false)
	violet := color.RGBA{R: 0x7c, G: 0x3a, B: 0xed}
	img := tile1787(t, c, "/api/v1/portal/assets/"+id, "")
	if got := share1787(img, img.Bounds(), violet, 12); got < 0.9 {
		t.Fatalf("after the rewrite the tile is %.1f%% the new color: the new version was not rendered", 100*got)
	}
}

// probe1787 records every request that reaches it. It stands in for an
// internal service a document names: the renderer runs inside the network
// perimeter, and a document must not be able to reach anything there.
type probe1787 struct {
	mu   sync.Mutex
	hits []string
	addr string
}

// rendererHostAlias is how the renderer reaches the machine the suite runs on:
// the dev stack runs the renderer in a container. It is the address an
// unguarded renderer WOULD reach the probe at.
func rendererHostAlias() string {
	if v := os.Getenv("ACCEPTANCE_RENDERER_HOST_ALIAS"); v != "" {
		return v
	}
	return "host.docker.internal"
}

func startProbe1787(t *testing.T) *probe1787 {
	t.Helper()
	// Every interface, so a renderer in a container could reach it if nothing
	// stopped it: that is the point of the probe.
	ln, err := net.Listen("tcp", "0.0.0.0:0") // #nosec G102 -- the probe must be reachable from the renderer's container to prove it is not reached
	if err != nil {
		t.Fatalf("starting the probe: %v", err)
	}
	p := &probe1787{addr: ln.Addr().String()}
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.hits = append(p.hits, r.Method+" "+r.URL.Path)
		p.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return p
}

func (p *probe1787) port() string {
	_, port, _ := net.SplitHostPort(p.addr)
	return port
}

func (p *probe1787) received(except string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, h := range p.hits {
		if !strings.Contains(h, except) {
			out = append(out, h)
		}
	}
	return out
}

// TestIssue1787_ADocumentNamingAnInternalAddressNeverReachesIt is criterion 6:
// a document can name an address inside the perimeter in every way a page can
// make a request -- an image, fetch, a WebSocket, a worker, a beacon -- and the
// tile is still drawn, and the address is never contacted.
//
// That each of the platform's layers is what stops those requests, rather than
// the browser's own policy, is proven against a renderer with that policy
// switched off in internal/headless's integration tests; this criterion is the
// property a user relies on, executed through the real surface.
func TestIssue1787_ADocumentNamingAnInternalAddressNeverReachesIt(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	probe := startProbe1787(t)

	// The probe answers, so a silence below means it was not contacted, not
	// that it was not listening.
	self, err := http.Get("http://127.0.0.1:" + probe.port() + "/self-check") // #nosec G107 -- the suite's own probe
	if err != nil {
		t.Fatalf("the probe does not answer the suite itself: %v", err)
	}
	_ = self.Body.Close() //nolint:errcheck // status is irrelevant

	at := "http://" + rendererHostAlias() + ":" + probe.port()
	doc := `<!DOCTYPE html><html><body style="margin:0;background:#1d4ed8;height:100vh">
<img src="` + at + `/1787-img">
<script>
fetch("` + at + `/1787-fetch").catch(function(){});
try { new WebSocket("` + strings.Replace(at, "http://", "ws://", 1) + `/1787-ws"); } catch (e) {}
try { new Worker(URL.createObjectURL(new Blob(["fetch('` + at + `/1787-worker').catch(function(){})"]))); } catch (e) {}
try { navigator.sendBeacon("` + at + `/1787-beacon"); } catch (e) {}
</script></body></html>`
	id := saveAsset1787(t, c, "text/html", doc)
	awaitAssetTile1787(t, c, id, false)

	blue := color.RGBA{R: 0x1d, G: 0x4e, B: 0xd8}
	img := tile1787(t, c, "/api/v1/portal/assets/"+id, "")
	if got := share1787(img, img.Bounds(), blue, 12); got < 0.9 {
		t.Fatalf("the document's own page covers %.1f%% of the tile: it was not drawn", 100*got)
	}
	if hits := probe.received("/self-check"); len(hits) != 0 {
		t.Fatalf("rendering the document reached an address inside the perimeter: %v", hits)
	}
}

// neverReady1787 is a document whose script never lets the page settle.
const neverReady1787 = `<!DOCTYPE html><html><body><h1>never</h1><script>while (true) {}</script></body></html>`

// TestIssue1787_ADocumentThatCannotBeDrawnSaysWhy is criterion 7: a document
// the renderer cannot draw is recorded as not drawn, with the reason, rather
// than stored as a wrong picture or retried forever; and asking for the tile
// again clears the record.
func TestIssue1787_ADocumentThatCannotBeDrawnSaysWhy(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	id := saveAsset1787(t, c, "text/html", neverReady1787)

	deadline := time.Now().Add(tileWait1787)
	var row map[string]any
	for {
		row = assetRow1787(t, c, id)
		if f, _ := row["thumbnail_failure"].(string); f != "" {
			break
		}
		if intField1787(row, "thumbnail_version") >= intField1787(row, "current_version") {
			t.Fatalf("a document whose script never settles was stored as a tile: %v", row)
		}
		if time.Now().After(deadline) {
			t.Fatalf("no failure recorded for a document that cannot be drawn after %s: %v", tileWait1787, row)
		}
		time.Sleep(time.Second)
	}
	if got := intField1787(row, "thumbnail_failed_version"); got != intField1787(row, "current_version") {
		t.Errorf("the failure is dated version %d, want the current version %d", got, intField1787(row, "current_version"))
	}
	if status, _ := readTile1787(t, c, "/api/v1/portal/assets/"+id, ""); status != http.StatusNotFound {
		t.Errorf("GET the tile of a document that was not drawn: status %d, want 404", status)
	}

	if status, body := c.rest(http.MethodDelete, "/api/v1/portal/assets/"+id+"/thumbnail", http.NoBody); status != http.StatusOK {
		t.Fatalf("asking for the tile again: status %d: %v", status, body)
	}
	// A new attempt at this document takes the renderer's whole deadline to
	// fail, so right after the request the old record must be gone rather than
	// standing in for an attempt that has not happened.
	row = assetRow1787(t, c, id)
	if f, _ := row["thumbnail_failure"].(string); f != "" {
		t.Errorf("asking for the tile again left the old failure in place: %q", f)
	}
	if got := intField1787(row, "thumbnail_failed_version"); got != 0 {
		t.Errorf("asking for the tile again left the failure dated version %d", got)
	}
}

// TestIssue1787_ACollectionTileIsAMosaicOfItsMembers is criterion 8: a
// collection's tile is composed by the platform from its members' tiles.
func TestIssue1787_ACollectionTileIsAMosaicOfItsMembers(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	solid := func(hex string) string {
		return `<!DOCTYPE html><html><body style="margin:0;background:` + hex + `;height:100vh"></body></html>`
	}
	red := saveAsset1787(t, c, "text/html", solid("#dc2626"))
	blue := saveAsset1787(t, c, "text/html", solid("#2563eb"))
	awaitAssetTile1787(t, c, red, false)
	awaitAssetTile1787(t, c, blue, false)

	out := c.call("manage_asset", map[string]any{
		"action":      "create_collection",
		"name":        "acceptance-1787-" + unique1787(),
		"description": "Acceptance #1787: a collection whose tile is composed from its members'.",
		"sections": []any{map[string]any{
			"title": "Members",
			"items": []any{map[string]any{"asset_id": red}, map[string]any{"asset_id": blue}},
		}},
	})
	coll, _ := out["collection_id"].(string)
	if coll == "" {
		t.Fatalf("create_collection returned no id: %v", out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete_collection", "collection_id": coll})
	})

	path := "/api/v1/portal/collections/" + coll
	deadline := time.Now().Add(tileWait1787)
	for {
		if status, _ := readTile1787(t, c, path, ""); status == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no tile for collection %s after %s", coll, tileWait1787)
		}
		time.Sleep(time.Second)
	}
	img := tile1787(t, c, path, "")
	for name, hue := range map[string]color.RGBA{
		"red":  {R: 0xdc, G: 0x26, B: 0x26},
		"blue": {R: 0x25, G: 0x63, B: 0xeb},
	} {
		if got := share1787(img, img.Bounds(), hue, 16); got < 0.2 {
			t.Errorf("the %s member covers %.1f%% of the collection's tile, want a share of it", name, 100*got)
		}
	}
}

// TestIssue1787_TheBrowserCaptureRoutesAreGone is criterion 9: nothing a tab
// used to capture and upload with is served any more. A tile is the
// platform's to render; a route that let a caller store any PNG as a
// document's picture is not a way to fix one.
func TestIssue1787_TheBrowserCaptureRoutesAreGone(t *testing.T) {
	c := connect(t)
	id := saveAsset1787(t, c, "text/html", "<!DOCTYPE html><p>routes</p>")
	for _, probe := range []struct{ method, path string }{
		{http.MethodPut, "/api/v1/portal/assets/" + id + "/thumbnail"},
		{http.MethodGet, "/api/v1/portal/thumbnails/pending"},
		{http.MethodGet, "/api/v1/resources/thumbnails/pending"},
		{http.MethodPut, "/api/v1/resources/" + id + "/thumbnail"},
		{http.MethodPut, "/api/v1/portal/collections/" + id + "/thumbnail"},
	} {
		status, body := c.rest(probe.method, probe.path, bytes.NewReader([]byte{0x89, 'P', 'N', 'G'}))
		if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
			t.Errorf("%s %s answered %d (%v); the route should not exist", probe.method, probe.path, status, body)
		}
	}
}
