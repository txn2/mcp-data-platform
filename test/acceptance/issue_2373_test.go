//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"image"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Issue #2373: agents are taught chart, diagram and animation libraries loaded
// from a public CDN, with a skeleton for each, instead of drawing SVG by hand.
// What is held here, through the surface an agent and a reader meet:
//
//   - platform_info and save_asset's description name the page;
//   - the page carries a skeleton per library, and every CDN file it names
//     answers;
//   - every skeleton, saved exactly as the page prints it, is drawn by the
//     platform's renderer into its tile, light and dark;
//   - every skeleton draws on the asset page, an authenticated share and a
//     public share signed out, in light and dark, with nothing refused by the
//     page's policy (ui/e2e/visual-libraries);
//   - the JSX form draws in a JSX asset;
//   - a deck with three of the libraries on its slides exports to PDF with
//     each figure drawn;
//   - the share page's policy compiles WebAssembly and still evaluates no
//     source.
//
// Wire forms: platform_info is registered through the typed mcp.AddTool form
// over an empty input struct, so its params admit an empty object and an absent
// object; both are sent. fetch's `reference` and `purpose`, save_asset's
// `name`, `content`, `content_type` and `purpose`, and manage_asset's
// `action`, `asset_id`, `access_mode` and `expires_in` are
// `{"type":"string"}` in their schemas, so each admits exactly one JSON form
// and is sent as a JSON string.

const issue2373Ref = "mcp:knowledge_page:platform-visual-libraries"

// issue2373SignInEmail and issue2373SignInPassword are the dev realm's
// administrator, who signs the browser in through the identity provider: an
// authenticated share reads the session cookie that sign-in sets.
const (
	issue2373SignInEmail    = "admin@example.com"
	issue2373SignInPassword = "admin-password"
)

const issue2373Purpose = "Acceptance for #2373: proving every library the visual-libraries page teaches draws where a document is read."

// issue2373Block is one fenced block of the page: its language and the
// heading of the section it sits in.
type issue2373Block struct {
	section, lang, body string
}

var (
	issue2373Fence   = regexp.MustCompile("(?s)```(html|jsx)\n(.*?)\n```")
	issue2373Heading = regexp.MustCompile(`(?m)^## (.+)$`)
	issue2373CDN     = regexp.MustCompile(`https://cdn\.jsdelivr\.net/npm/[A-Za-z0-9@._/-]+\.(?:js|css|mjs)`)
)

// issue2373Page fetches the page and returns its body.
func issue2373Page(t *testing.T, c *client) string {
	t.Helper()
	res, text, err := c.callRaw("fetch", map[string]any{"reference": issue2373Ref, "purpose": issue2373Purpose})
	if err != nil || res.IsError {
		t.Fatalf("fetch %s: %v %s", issue2373Ref, err, text)
	}
	var out struct {
		Found    bool `json:"found"`
		Document struct {
			Body string `json:"body"`
		} `json:"document"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil || !out.Found || out.Document.Body == "" {
		t.Fatalf("fetch %s did not return the page: %v\n%s", issue2373Ref, err, text)
	}
	return out.Document.Body
}

// issue2373Blocks is every skeleton of the page, named by section: each jsx
// block and each html block that is a whole document. A fragment (a slide's
// markup) is not a document to save; the deck criterion covers it.
func issue2373Blocks(page string) []issue2373Block {
	var out []issue2373Block
	for _, m := range issue2373Fence.FindAllStringSubmatchIndex(page, -1) {
		if page[m[2]:m[3]] == "html" && !strings.HasPrefix(page[m[4]:m[5]], "<!DOCTYPE html>") {
			continue
		}
		section := ""
		for _, h := range issue2373Heading.FindAllStringSubmatchIndex(page[:m[0]], -1) {
			section = page[h[2]:h[3]]
		}
		out = append(out, issue2373Block{section: section, lang: page[m[2]:m[3]], body: page[m[4]:m[5]]})
	}
	return out
}

// issue2373Save saves content as an asset and returns its id.
func issue2373Save(t *testing.T, c *client, name, contentType, content string) string {
	t.Helper()
	saved := c.call("save_asset", map[string]any{
		"name":         fmt.Sprintf("Acceptance 2373 %s %d", name, time.Now().UnixNano()),
		"content":      content,
		"content_type": contentType,
		"purpose":      issue2373Purpose,
	})
	id, _ := saved["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id, "purpose": issue2373Purpose})
	})
	return id
}

// issue2373Share makes a link share of the asset and returns its viewer path.
func issue2373Share(t *testing.T, c *client, assetID, mode string) string {
	t.Helper()
	args := map[string]any{"action": "share", "asset_id": assetID, "access_mode": mode, "purpose": issue2373Purpose}
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

// issue2373Ink is the share of a tile's pixels that differ from its corner,
// which is the document's background: a skeleton whose library did not run
// draws nothing on it.
func issue2373Ink(img image.Image) float64 {
	b := img.Bounds()
	r0, g0, b0, _ := img.At(b.Min.X+1, b.Min.Y+1).RGBA()
	ink, total := 0, 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			total++
			if issue2068Abs(int(r>>8)-int(r0>>8))+issue2068Abs(int(g>>8)-int(g0>>8))+issue2068Abs(int(bl>>8)-int(b0>>8)) > 48 {
				ink++
			}
		}
	}
	return float64(ink) / float64(total)
}

// TestIssue2373_PlatformInfoAndSaveAssetNameThePage: an agent asked for a
// chart is told the page exists, in the first response and where it saves.
func TestIssue2373_PlatformInfoAndSaveAssetNameThePage(t *testing.T) {
	c := connect(t)
	for _, form := range []struct {
		name string
		args map[string]any
	}{{"absent params", nil}, {"empty object", map[string]any{}}} {
		t.Run(form.name, func(t *testing.T) {
			if info := issue1767Instructions(t, c, form.args); !strings.Contains(info, issue2373Ref) {
				t.Fatalf("platform_info does not name %s:\n%s", issue2373Ref, info)
			}
		})
	}
	for _, tool := range c.tools() {
		if tool.Name == "save_asset" {
			if !strings.Contains(tool.Description, issue2373Ref) {
				t.Fatalf("save_asset's description does not name %s:\n%s", issue2373Ref, tool.Description)
			}
			return
		}
	}
	t.Fatal("save_asset is not listed")
}

// TestIssue2373_ThePageCarriesASkeletonPerLibraryAndEveryFileItNamesLoads:
// every library the table names has a section with a skeleton, and every CDN
// file the page names answers.
func TestIssue2373_ThePageCarriesASkeletonPerLibraryAndEveryFileItNamesLoads(t *testing.T) {
	c := connect(t)
	page := issue2373Page(t, c)
	sections := map[string]bool{}
	for _, b := range issue2373Blocks(page) {
		sections[b.section] = true
	}
	for _, lib := range []string{"Vega-Lite", "Mermaid", "Markmap", "D3 with d3-sankey", "Frappe Gantt", "Cytoscape.js", "Graphviz", "Rough.js", "dotLottie", "In a JSX asset"} {
		if !sections[lib] {
			t.Errorf("the page has no skeleton under %q", lib)
		}
	}
	urls := map[string]bool{}
	for _, u := range issue2373CDN.FindAllString(page, -1) {
		urls[u] = true
	}
	if len(urls) < 12 {
		t.Fatalf("the page names %d CDN files; expected every library's", len(urls))
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	for u := range urls {
		resp, err := hc.Get(u) //nolint:noctx // test request
		if err != nil {
			t.Errorf("GET %s: %v", u, err)
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s answered %d", u, resp.StatusCode)
		}
	}
}

// TestIssue2373_EverySkeletonIsDrawnIntoItsTileLightAndDark: the platform's
// renderer draws each skeleton, saved exactly as the page prints it, into the
// asset's tile in both schemes.
func TestIssue2373_EverySkeletonIsDrawnIntoItsTileLightAndDark(t *testing.T) {
	c := connect(t)
	for _, b := range issue2373Blocks(issue2373Page(t, c)) {
		if b.lang != "html" {
			continue
		}
		t.Run(b.section, func(t *testing.T) {
			id := issue2373Save(t, c, b.section, "text/html", b.body)
			for _, variant := range []string{"light", "dark"} {
				if ink := issue2373Ink(issue2068Tile(t, c, id, variant)); ink < 0.005 {
					t.Errorf("the %s tile is blank (%.4f of its pixels differ from the background); the library did not draw", variant, ink)
				}
			}
		})
	}
}

// TestIssue2373_EverySkeletonDrawsWhereADocumentIsRead: the skeletons and
// the JSX form draw on the asset page, an authenticated share and a public
// share signed out, light and dark, with nothing refused by the page's policy.
func TestIssue2373_EverySkeletonDrawsWhereADocumentIsRead(t *testing.T) {
	c := connect(t)
	type browserCase struct {
		Name        string `json:"name"`
		Asset       string `json:"asset"`
		AuthShare   string `json:"authShare"`
		PublicShare string `json:"publicShare"`
	}
	var cases []browserCase
	for _, b := range issue2373Blocks(issue2373Page(t, c)) {
		contentType := "text/html"
		if b.lang == "jsx" {
			contentType = "text/jsx"
		}
		id := issue2373Save(t, c, b.section, contentType, b.body)
		cases = append(cases, browserCase{
			Name: b.section, Asset: id,
			AuthShare: issue2373Share(t, c, id, "authenticated"), PublicShare: issue2373Share(t, c, id, "public"),
		})
	}
	raw, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.Abs("../..")
	cmd := exec.Command("npx", "playwright", "test", "--config", "e2e/visual-libraries/playwright.config.ts")
	cmd.Dir = filepath.Join(root, "ui")
	cmd.Env = append(os.Environ(), "VISUALS_BASE_URL="+baseURL(), "VISUALS_CASES="+string(raw),
		"VISUALS_SIGN_IN_EMAIL="+issue2373SignInEmail, "VISUALS_SIGN_IN_PASSWORD="+issue2373SignInPassword)
	out, err := cmd.CombinedOutput()
	t.Logf("playwright:\n%s", out)
	if err != nil {
		t.Fatalf("a skeleton did not draw on every surface: %v", err)
	}
}

// issue2373Deck is a deck written as the page's "On a slide" section says:
// the libraries' scripts in the head, each figure a fixed size in slide
// pixels, drawn at load. With draw false it is the same deck with its figures
// left undrawn, the control the drawn deck's PDF is measured against.
func issue2373Deck(draw bool) string {
	scripts := `<script src="https://cdn.jsdelivr.net/npm/vega@6.4.0/build/vega.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/vega-lite@6.5.0/build/vega-lite.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/vega-embed@7.3.0/build/vega-embed.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/vega-interpreter@2.3.2/build/vega-interpreter.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/mermaid@12.2.0/dist/mermaid.min.js"></script>`
	drawing := `<script>
  vegaEmbed("#chart", { width: 820, height: 380, data: { values: [{ q: "Q1", v: 38 }, { q: "Q2", v: 39 }, { q: "Q3", v: 41 }] },
    mark: "bar", encoding: { x: { field: "q", type: "ordinal" }, y: { field: "v", type: "quantitative" } } },
    { actions: false, ast: true, expr: vega.expressionInterpreter });
  mermaid.initialize({ startOnLoad: false, securityLevel: "strict" });
  mermaid.render("flow-svg", "flowchart LR\n  A[Order] --> B{Stock?}\n  B -- yes --> C[Ship]\n  B -- no --> D[Back order]")
    .then(({ svg }) => { document.getElementById("flow").innerHTML = svg; });
</script>
<script type="module">
  import { Graphviz } from "https://cdn.jsdelivr.net/npm/@hpcc-js/wasm-graphviz@1.29.2/dist/index.js";
  const graphviz = await Graphviz.load();
  document.getElementById("graph").innerHTML = graphviz.dot("digraph { rankdir=LR; a -> b -> c; a -> c; c -> d }");
</script>`
	if !draw {
		scripts, drawing = "", ""
	}
	return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Acceptance deck 2373</title>
<link rel="stylesheet" href="/portal/vendor/reveal/reset.css">
<link rel="stylesheet" href="/portal/vendor/reveal/reveal.css">
<link rel="stylesheet" href="/portal/vendor/reveal/theme/white.css">
` + scripts + `
<style>#chart, #flow, #graph { width: 860px; height: 420px; margin: 0 auto; } #graph svg, #flow svg { width: 100%; height: 100%; }</style>
</head>
<body>
<div class="reveal"><div class="slides">
<section><h1>Acceptance deck 2373</h1></section>
<section><h2>Revenue</h2><div id="chart"></div></section>
<section><h2>Flow</h2><div id="flow"></div></section>
<section><h2>Lineage</h2><div id="graph"></div></section>
</div></div>
<script src="/portal/vendor/reveal/reveal.js"></script>
<script>Reveal.initialize({ hash: false });</script>
` + drawing + `
</body>
</html>`
}

// issue2373PathOps counts the path-building operators a PDF's content
// streams carry: a figure printed as vectors is made of them, and a slide
// holding only its heading has almost none.
func issue2373PathOps(pdf []byte) int {
	return len(regexp.MustCompile(`(?m)\s(?:m|l|c|re|h)$`).FindAllString(issue1983Streams(pdf), -1))
}

// TestIssue2373_ADeckWithThreeLibrariesExportsEachFigureToPDF: Export PDF
// prints a Vega-Lite chart, a Mermaid flow and a Graphviz graph, measured
// against the same deck with its figures left undrawn.
func TestIssue2373_ADeckWithThreeLibrariesExportsEachFigureToPDF(t *testing.T) {
	c := connect(t)
	pdfOf := func(draw bool) []byte {
		id := issue2373Save(t, c, fmt.Sprintf("deck draw=%v", draw), "text/html", issue2373Deck(draw))
		status, contentType, pdf := issue1983Get(t, "/api/v1/portal/assets/"+id+"/pdf", c.apiKey)
		if status != http.StatusOK || contentType != "application/pdf" {
			t.Fatalf("GET pdf (draw=%v): HTTP %d %s: %.300s", draw, status, contentType, pdf)
		}
		if n := issue1983Pages(pdf); n != 4 {
			t.Fatalf("the PDF (draw=%v) has %d pages; the deck has 4 slides", draw, n)
		}
		return pdf
	}
	drawn, bare := issue2373PathOps(pdfOf(true)), issue2373PathOps(pdfOf(false))
	t.Logf("path operators: drawn deck %d, undrawn deck %d", drawn, bare)
	if drawn < bare+60 {
		t.Fatalf("the exported deck carries %d path operators against %d for the same deck undrawn; its figures did not print", drawn, bare)
	}
}

// TestIssue2373_TheSharePolicyCompilesWebAssemblyAndEvaluatesNoSource: the
// public share page a reader meets admits WebAssembly, which Graphviz and
// dotLottie need, and still refuses 'unsafe-eval'.
func TestIssue2373_TheSharePolicyCompilesWebAssemblyAndEvaluatesNoSource(t *testing.T) {
	c := connect(t)
	id := issue2373Save(t, c, "policy", "text/html", "<!DOCTYPE html><html><body><p>policy</p></body></html>")
	resp, _ := issue1767Get(t, c.base, issue2373Share(t, c, id, "public"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the public share answered %d", resp.StatusCode)
	}
	scriptSrc := ""
	for _, d := range strings.Split(resp.Header.Get("Content-Security-Policy"), ";") {
		if d = strings.TrimSpace(d); strings.HasPrefix(d, "script-src ") {
			scriptSrc = d
		}
	}
	if !strings.Contains(scriptSrc, "'wasm-unsafe-eval'") {
		t.Errorf("the share page's script-src %q does not compile WebAssembly", scriptSrc)
	}
	if strings.Contains(scriptSrc, "'unsafe-eval'") {
		t.Errorf("the share page's script-src %q evaluates source", scriptSrc)
	}
}
