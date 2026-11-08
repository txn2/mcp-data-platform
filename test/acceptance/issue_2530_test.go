//go:build integration

package acceptance

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Issue #2530: Download of an HTML or JSX asset is one self-contained HTML
// file that opens from file:// with the network off and renders as it does in
// the portal. The platform's served runtimes, the asset's declared images and
// fonts, and files it names on other hosts are inlined; a JSX asset is
// compiled and bundled with React, Recharts and lucide-react. An asset that
// reads the platform's data while it renders gets a dialog offering the stored
// source instead. Every other type downloads as before.
//
// The browser half is ui/e2e/standalone-live/standalone-live.spec.ts, run with
// SA_BASE_URL, SA_API_KEY, SA_CASES and SA_OUT: it clicks Download on the
// asset page and on the shares, and opens each file from disk in Chrome with
// the network emulated offline.
//
// Wire forms: save_asset's content, name, content_type and purpose are
// strings and references a list of strings; manage_resource's content_base64,
// filename and content_type are strings; manage_asset's action, asset_id,
// access_mode and expires_in are strings; manage_script's command, name and
// source and run_script's name are strings, run_script's args an object and
// wait_seconds a number. The standalone route takes no parameter beyond the
// asset in its path; the content route's source parameter is the string
// "stored".

const issue2530Purpose = "Acceptance for #2530: assets download as self-contained files."

// issue2530Out is where the browser suite keeps every downloaded file and the
// dialog screenshots.
func issue2530Out() string {
	root, _ := filepath.Abs("../..")
	return filepath.Join(root, "build", "2530", "files")
}

// saCase is one asset the browser suite downloads; its JSON names are the
// suite's.
type saCase struct {
	Check       string   `json:"check"`
	Label       string   `json:"label"`
	Name        string   `json:"name"`
	Asset       string   `json:"asset"`
	AuthShare   string   `json:"authShare,omitempty"`
	PublicShare string   `json:"publicShare,omitempty"`
	Surfaces    []string `json:"surfaces"`
	Slides      int      `json:"slides,omitempty"`
	Font        string   `json:"font,omitempty"`
	StoredB64   string   `json:"storedB64,omitempty"`
	ContentB64  string   `json:"contentB64,omitempty"`
	Screenshots bool     `json:"screenshots,omitempty"`
}

func issue2530Name(label, ext string) string {
	return fmt.Sprintf("Acceptance 2530 %s %d.%s", label, time.Now().UnixNano(), ext)
}

// issue2530Save saves an asset and returns its id.
func issue2530Save(t *testing.T, c *client, name, content, contentType string, refs []string) string {
	t.Helper()
	args := map[string]any{"name": name, "content": content, "content_type": contentType, "purpose": issue2530Purpose}
	if len(refs) > 0 {
		list := make([]any, len(refs))
		for i, r := range refs {
			list[i] = r
		}
		args["references"] = list
	}
	saved := c.call("save_asset", args)
	id, _ := saved["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id, "purpose": issue2530Purpose})
	})
	return id
}

// issue2530Share makes a link share of the asset and returns its viewer path.
func issue2530Share(t *testing.T, c *client, assetID, mode string) string {
	t.Helper()
	args := map[string]any{"action": "share", "asset_id": assetID, "access_mode": mode, "purpose": issue2530Purpose}
	if mode == "public" {
		args["expires_in"] = "1h"
	}
	share := c.call("manage_asset", args)
	u, _ := share["share_url"].(string)
	i := strings.Index(u, "/portal/")
	if i < 0 {
		t.Fatalf("share returned no viewer URL: %v", share)
	}
	return u[i:]
}

// issue2530Resource uploads a managed resource and returns its URI.
func issue2530Resource(t *testing.T, c *client, filename, contentType string, body []byte) string {
	t.Helper()
	out := c.call("manage_resource", map[string]any{
		"action": "create", "filename": filename, "display_name": filename, "path": "acceptance-2530",
		"description":    "Acceptance #2530: a file an asset declares.",
		"content_base64": base64.StdEncoding.EncodeToString(body),
		"content_type":   contentType,
	})
	id, _ := out["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	status, row := c.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody)
	uri, _ := row["uri"].(string)
	if status != http.StatusOK || uri == "" {
		t.Fatalf("the resource carries no uri: HTTP %d %v", status, row)
	}
	return uri
}

// issue2530Logo is a 32x32 teal PNG.
func issue2530Logo(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := range 32 {
		for x := range 32 {
			img.Set(x, y, color.RGBA{R: 0, G: 128, B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// runStandaloneSuite downloads every case in Chrome and opens each file
// offline.
func runStandaloneSuite(t *testing.T, cases []saCase) {
	t.Helper()
	raw, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.Abs("../..")
	cmd := exec.Command("npx", "playwright", "test", "--config", "e2e/standalone-live/playwright.config.ts")
	cmd.Dir = filepath.Join(root, "ui")
	cmd.Env = append(os.Environ(), "SA_BASE_URL="+baseURL(), "SA_API_KEY="+devAPIKey(), "SA_CASES="+string(raw), "SA_OUT="+issue2530Out(),
		"SA_SIGN_IN_EMAIL="+issue2373SignInEmail, "SA_SIGN_IN_PASSWORD="+issue2373SignInPassword)
	out, err := cmd.CombinedOutput()
	t.Logf("playwright:\n%s", out)
	if err != nil {
		t.Fatalf("a download did not behave as the issue says: %v", err)
	}
}

// standaloneGet reads an asset's standalone route with the API key, and a
// cookie the file must not repeat.
func standaloneGet(t *testing.T, assetID string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL()+"/api/v1/portal/assets/"+url.PathEscape(assetID)+"/standalone", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-API-Key", devAPIKey())
	req.AddCookie(&http.Cookie{Name: "mcp_portal_session", Value: issue2530Cookie})
	res, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		t.Fatalf("GET standalone: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	return res, body
}

const issue2530Cookie = "acceptance-2530-session-cookie-value"

// issue2530Deck is a reveal.js deck on the served runtime: the black theme
// and the markdown plugin, four slides, one written in markdown.
const issue2530Deck = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Deck</title>
<link rel="stylesheet" href="/portal/vendor/reveal/reset.css">
<link rel="stylesheet" href="/portal/vendor/reveal/reveal.css">
<link rel="stylesheet" href="/portal/vendor/reveal/theme/black.css">
</head><body>
<div class="reveal"><div class="slides">
<section><h1>Quarterly review</h1></section>
<section><h2>Revenue</h2><p>Up 12%</p></section>
<section data-markdown><textarea data-template>## Written in markdown
- one
- two</textarea></section>
<section><h2>Questions</h2></section>
</div></div>
<script src="/portal/vendor/reveal/reveal.js"></script>
<script src="/portal/vendor/reveal/plugin/markdown.js"></script>
<script>Reveal.initialize({ hash: false, plugins: [RevealMarkdown] });</script>
</body></html>`

// TestIssue2530_ARevealDeckDownloadsAsAFileThatRunsOffline: downloaded from
// the asset page, an authenticated share link and a public share link, the
// deck opens from disk offline with its theme, its slide count, and the right
// arrow advancing a slide, with no request made and nothing failed.
func TestIssue2530_ARevealDeckDownloadsAsAFileThatRunsOffline(t *testing.T) {
	c := connect(t)
	id := issue2530Save(t, c, issue2530Name("deck", "html"), issue2530Deck, "text/html", nil)
	runStandaloneSuite(t, []saCase{{
		Check: "deck", Label: "deck", Asset: id, Slides: 4,
		AuthShare: issue2530Share(t, c, id, "authenticated"), PublicShare: issue2530Share(t, c, id, "public"),
		Surfaces: []string{"asset", "auth", "public"},
	}})
}

// TestIssue2530_AJSXChartWithItsDeclaredLogoRunsOffline: Recharts and
// lucide-react bundled, the declared logo inlined, no request made.
func TestIssue2530_AJSXChartWithItsDeclaredLogoRunsOffline(t *testing.T) {
	c := connect(t)
	logo := issue2530Resource(t, c, fmt.Sprintf("logo-2530-%d.png", time.Now().UnixNano()), "image/png", issue2530Logo(t))
	src := `import { LineChart, Line, XAxis, YAxis } from "recharts";
import { TrendingUp } from "lucide-react";

const data = [{ q: "Q1", v: 10 }, { q: "Q2", v: 14 }, { q: "Q3", v: 12 }, { q: "Q4", v: 18 }];

export default function Revenue() {
  return (
    <div>
      <img id="logo" src="` + logo + `" width="32" height="32" alt="logo" />
      <h1><TrendingUp size={20} /> Revenue</h1>
      <LineChart width={480} height={240} data={data}>
        <XAxis dataKey="q" />
        <YAxis />
        <Line dataKey="v" isAnimationActive={false} />
      </LineChart>
    </div>
  );
}
`
	id := issue2530Save(t, c, issue2530Name("jsx", "jsx"), src, "text/jsx", []string{logo})
	runStandaloneSuite(t, []saCase{{
		Check: "jsx-chart", Label: "jsx", Asset: id,
		PublicShare: issue2530Share(t, c, id, "public"), Surfaces: []string{"asset", "public"},
	}})
}

// TestIssue2530_AMapOnTheBasemapArchiveGetsTheDialogAndAStaticMapRunsOffline.
func TestIssue2530_AMapOnTheBasemapArchiveGetsTheDialogAndAStaticMapRunsOffline(t *testing.T) {
	c := connect(t)
	archive := `<!DOCTYPE html><html><head><link rel="stylesheet" href="/portal/vendor/maplibre/maplibre-gl.css"></head><body>
<div id="map" style="height:400px"></div>
<script src="/portal/vendor/maplibre/maplibre-gl.js"></script><script src="/portal/vendor/maplibre/pmtiles.js"></script>
<script>
const origin = new URL(document.baseURI).origin;
const REGION = "/portal/maps/us-west.pmtiles";
const protocol = new pmtiles.Protocol();
maplibregl.addProtocol("pmtiles", protocol.tile);
new maplibregl.Map({ container: "map", style: { version: 8, sources: { p: { type: "vector", url: "pmtiles://" + origin + REGION } }, layers: [] } });
</script></body></html>`
	archiveID := issue2530Save(t, c, issue2530Name("map-archive", "html"), archive, "text/html", nil)

	tile := issue2530Resource(t, c, fmt.Sprintf("tile-2530-%d.png", time.Now().UnixNano()), "image/png", issue2530Logo(t))
	static := `<!DOCTYPE html><html><head><link rel="stylesheet" href="/portal/vendor/maplibre/maplibre-gl.css"></head><body>
<div id="map" style="height:400px;width:600px"></div>
<script src="/portal/vendor/maplibre/maplibre-gl.js"></script>
<script>
const map = new maplibregl.Map({
  container: "map", center: [0, 0], zoom: 1,
  style: { version: 8,
    sources: { tile: { type: "image", url: "` + tile + `", coordinates: [[-40, 40], [40, 40], [40, -40], [-40, -40]] } },
    layers: [ { id: "bg", type: "background", paint: { "background-color": "#ffffff" } },
              { id: "tile", type: "raster", source: "tile" } ] },
});
map.on("load", () => { window.__mapLoaded = true; });
</script></body></html>`
	staticID := issue2530Save(t, c, issue2530Name("map-static", "html"), static, "text/html", []string{tile})

	runStandaloneSuite(t, []saCase{
		{Check: "dialog", Label: "map-archive", Asset: archiveID, Surfaces: []string{"asset"}, StoredB64: base64.StdEncoding.EncodeToString([]byte(archive))},
		{Check: "static-map", Label: "map-static", Asset: staticID, Surfaces: []string{"asset"}},
	})
}

// issue2530PublicImage is an image on a public host: this repository's logo.
const issue2530PublicImage = "https://raw.githubusercontent.com/txn2/mcp-data-platform/main/docs/images/logo.png"

// TestIssue2530_AGoogleFontAndAPublicImageAreInlined: a JSX asset loading a
// Google Fonts stylesheet shows the font offline, and an HTML asset naming an
// image on a public host shows the image offline.
func TestIssue2530_AGoogleFontAndAPublicImageAreInlined(t *testing.T) {
	c := connect(t)
	font := `export default function Lettering() {
  return (
    <div>
      <link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Pacifico&display=swap" />
      <p style={{ fontFamily: "Pacifico", fontSize: 32 }}>Quarterly review</p>
    </div>
  );
}
`
	fontID := issue2530Save(t, c, issue2530Name("font", "jsx"), font, "text/jsx", nil)
	pic := `<!DOCTYPE html><html><body><img id="pic" src="` + issue2530PublicImage + `" width="120"></body></html>`
	picID := issue2530Save(t, c, issue2530Name("image", "html"), pic, "text/html", nil)
	runStandaloneSuite(t, []saCase{
		{Check: "font", Label: "font", Asset: fontID, Font: "Pacifico", Surfaces: []string{"asset"}},
		{Check: "image", Label: "image", Asset: picID, Surfaces: []string{"asset"}},
	})
}

// TestIssue2530_LoopbackAndMetadataAddressesAreRefusedAndNamed: both are left
// as their URLs and named in the notice, and the platform makes no request to
// either. A listener on loopback counts the connections it is offered.
func TestIssue2530_LoopbackAndMetadataAddressesAreRefusedAndNamed(t *testing.T) {
	c := connect(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	var accepted atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			_ = conn.Close()
		}
	}()
	loopback := fmt.Sprintf("http://%s/secret.png", ln.Addr())
	metadata := "http://169.254.169.254/latest/meta-data/iam/security-credentials/"
	doc := `<!DOCTYPE html><html><body><img src="` + loopback + `"><img src="` + metadata + `"></body></html>`
	id := issue2530Save(t, c, issue2530Name("internal", "html"), doc, "text/html", nil)

	res, body := standaloneGet(t, id)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("standalone: HTTP %d %s", res.StatusCode, body)
	}
	raw, err := url.QueryUnescape(res.Header.Get("X-Standalone-Notices"))
	if err != nil {
		t.Fatal(err)
	}
	var notices []struct{ URL, Reason string }
	if err := json.Unmarshal([]byte(raw), &notices); err != nil {
		t.Fatalf("notices %q: %v", raw, err)
	}
	named := map[string]string{}
	for _, n := range notices {
		named[n.URL] = n.Reason
	}
	for _, u := range []string{loopback, metadata} {
		if !strings.Contains(string(body), u) {
			t.Errorf("%s is not left as its URL", u)
		}
		if _, ok := named[u]; !ok {
			t.Errorf("%s is not named in the notice: %v", u, notices)
		}
	}
	t.Logf("notices: %v", notices)
	if n := accepted.Load(); n != 0 {
		t.Errorf("the platform connected to the loopback address %d time(s)", n)
	}
}

// TestIssue2530_ADashboardReadingACSVAndAJSXEmbeddingAFormGetTheDialog: on
// the asset page and on a public share, Download shows the dialog, Cancel
// saves nothing, and Download source saves the stored source byte for byte.
func TestIssue2530_ADashboardReadingACSVAndAJSXEmbeddingAFormGetTheDialog(t *testing.T) {
	c := connect(t)
	csv := issue2530Resource(t, c, fmt.Sprintf("sales-2530-%d.csv", time.Now().UnixNano()), "text/csv", []byte("region,total\nnorth,10\nsouth,7\n"))
	dashboard := `<!DOCTYPE html><html><body><table id="t"></table>
<script>fetch("` + csv + `").then(r => r.text()).then(t => { document.getElementById("t").textContent = t; });</script>
</body></html>`
	dashID := issue2530Save(t, c, issue2530Name("csv-dashboard", "html"), dashboard, "text/html", []string{csv})

	form, _ := bindForm(t, c, "acc-2530", formFields, map[string]any{"auth_mode": "public"})
	jsx := `export default function Survey() {
  return <iframe src="mcp:form:` + form + `" style={{ width: "100%", height: 480, border: 0 }} />;
}
`
	formID := issue2530Save(t, c, issue2530Name("form", "jsx"), jsx, "text/jsx", []string{"mcp:form:" + form})

	runStandaloneSuite(t, []saCase{
		{
			Check: "dialog", Label: "csv-dashboard", Asset: dashID, PublicShare: issue2530Share(t, c, dashID, "public"),
			Surfaces: []string{"asset", "public"}, StoredB64: base64.StdEncoding.EncodeToString([]byte(dashboard)), Screenshots: true,
		},
		{
			Check: "dialog", Label: "form", Asset: formID, PublicShare: issue2530Share(t, c, formID, "public"),
			Surfaces: []string{"asset", "public"}, StoredB64: base64.StdEncoding.EncodeToString([]byte(jsx)),
		},
	})
}

// issue2530Publisher publishes a dashboard on its first run and splices its
// numbers into the data region with platform.publish_data on the second: an
// output is written once per run.
const issue2530Publisher = `NAME = "Acceptance 2530 published"

DOC = """<!DOCTYPE html><html><body><p id="total"></p>
<script type="application/json" id="data">{"total": 0}</script>
<script>document.getElementById("total").textContent = JSON.parse(document.getElementById("data").textContent).total;</script>
</body></html>"""

def main():
    """Publishes the dashboard on its first run, and splices its numbers into the data region on the next."""
    if run.state.get("exported"):
        platform.publish_data(NAME, {"total": 42})
    else:
        platform.export(NAME, DOC, format = "html")
        platform.save_state({"exported": True})
`

// TestIssue2530_APublishedDataRegionDownloadsStandalone with no dialog and
// shows its numbers offline.
func TestIssue2530_APublishedDataRegionDownloadsStandalone(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acceptance-2530-publish-%d", time.Now().UnixNano())
	created := c.saveScript(map[string]any{
		"command": "create", "name": name, "source": issue2530Publisher,
		"description": "Acceptance #2530: a dashboard whose data region publish_data fills.",
	}, nil)
	if status, _ := created["status"].(string); status == "invalid" {
		t.Fatalf("manage_script create refused: %v", created["findings"])
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	run := c.call("run_script", map[string]any{"name": name, "args": map[string]any{}, "wait_seconds": 120})
	if status, _ := run["status"].(string); status != "succeeded" {
		t.Fatalf("the publisher's first run did not succeed: %v", run)
	}
	if spliced := c.call("run_script", map[string]any{"name": name, "args": map[string]any{}, "wait_seconds": 120}); spliced["status"] != "succeeded" {
		t.Fatalf("publish_data did not succeed: %v", spliced)
	}
	outputs, _ := run["outputs"].([]any)
	var assetID string
	for _, o := range outputs {
		if out, ok := o.(map[string]any); ok {
			if id, _ := out["asset_id"].(string); id != "" {
				assetID = id
			}
		}
	}
	if assetID == "" {
		t.Fatalf("the run names no asset: %v", run["outputs"])
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID, "purpose": issue2530Purpose})
	})
	if status, body := c.rest(http.MethodGet, "/api/v1/portal/assets/"+assetID, http.NoBody); status != http.StatusOK {
		t.Fatalf("the published asset: HTTP %d %v", status, body)
	}
	runStandaloneSuite(t, []saCase{{Check: "published", Label: "published", Asset: assetID, Surfaces: []string{"asset"}}})
}

// TestIssue2530_ACSVAndAPDFDownloadAsBefore: the bytes the content route
// serves, under the asset's own name.
func TestIssue2530_ACSVAndAPDFDownloadAsBefore(t *testing.T) {
	c := connect(t)
	csvName := issue2530Name("table", "csv")
	csv := "region,total\nnorth,10\nsouth,7\n"
	csvID := issue2530Save(t, c, csvName, csv, "text/csv", nil)

	pdf, err := os.ReadFile("../../internal/docread/testdata/objstream.pdf")
	if err != nil {
		t.Fatalf("reading the PDF fixture: %v", err)
	}
	pdfName := issue2530Name("report", "pdf")
	status, created := c.rest(http.MethodPost, "/api/v1/portal/assets", jsonBody(t, map[string]any{
		"name": pdfName, "content_type": "application/octet-stream", "content": "placeholder",
	}))
	if status != http.StatusCreated {
		t.Fatalf("portal create: HTTP %d %v", status, created)
	}
	pdfID, _ := created["id"].(string)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": pdfID}) })
	if status, body := c.rest(http.MethodPut, "/api/v1/portal/assets/"+pdfID+"/content", bytes.NewReader(pdf)); status != http.StatusOK {
		t.Fatalf("PDF content: HTTP %d %v", status, body)
	}
	if status, row := c.rest(http.MethodGet, "/api/v1/portal/assets/"+pdfID, http.NoBody); status != http.StatusOK || row["content_type"] != "application/pdf" {
		t.Fatalf("the PDF asset is not application/pdf: HTTP %d %v", status, row["content_type"])
	}
	runStandaloneSuite(t, []saCase{
		{Check: "plain", Label: "csv", Name: csvName, Asset: csvID, Surfaces: []string{"asset"}, ContentB64: base64.StdEncoding.EncodeToString([]byte(csv))},
		{Check: "plain", Label: "pdf", Name: pdfName, Asset: pdfID, Surfaces: []string{"asset"}, ContentB64: base64.StdEncoding.EncodeToString(pdf)},
	})
}

// TestIssue2530_NoDownloadedFileCarriesTheReadersCredentials: the deck and a
// JSX asset read through the standalone route with the API key and a session
// cookie, searched for both as written and base64. (The browser suite makes
// the same search in every file it downloads.)
func TestIssue2530_NoDownloadedFileCarriesTheReadersCredentials(t *testing.T) {
	c := connect(t)
	deck := issue2530Save(t, c, issue2530Name("creds-deck", "html"), issue2530Deck, "text/html", nil)
	jsx := issue2530Save(t, c, issue2530Name("creds-jsx", "jsx"),
		`import { Star } from "lucide-react"; export default function A() { return <p><Star /> ok</p>; }`, "text/jsx", nil)
	for _, id := range []string{deck, jsx} {
		res, body := standaloneGet(t, id)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("standalone %s: HTTP %d %s", id, res.StatusCode, body)
		}
		t.Logf("standalone %s: %d bytes", id, len(body))
		for _, secret := range []string{devAPIKey(), issue2530Cookie} {
			for _, form := range []string{secret, base64.StdEncoding.EncodeToString([]byte(secret)), url.QueryEscape(secret)} {
				if bytes.Contains(body, []byte(form)) {
					t.Errorf("the file for %s carries a credential (%q)", id, form)
				}
			}
		}
	}
}
