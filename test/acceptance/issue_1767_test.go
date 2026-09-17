//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Issue #1767: a presentation is an HTML asset on a slide runtime the platform
// serves itself, and the platform tells an agent so. What is held here, through
// the surface an agent and a reader actually meet: the first response of a
// session names the presentations page; the page carries the skeleton and
// names the served paths; every path it names answers, unauthenticated, with
// the runtime (the share viewer's frame carries no session, so an
// authenticated-only path would render a blank deck to every share); a deck
// saved on that skeleton is stored with its root-relative paths intact; and a
// public share of it serves a page whose script policy admits the page's own
// origin, which is where the runtime is.
//
// Rendering, the keyboard and the Present control are browser behavior and are
// held by ui/e2e/public-viewer against the same stack; the transcript at
// build/1767/acceptance.md records both runs.
//
// Wire forms: platform_info is registered through the typed mcp.AddTool form
// over an empty input struct, so its params admit an empty object and an absent
// object; both are sent. fetch's `reference` and `purpose`, save_asset's
// `name`, `content` and `content_type`, and manage_asset's `action`,
// `asset_id`, `access_mode` and `expires_in` are `{"type":"string"}` in their
// schemas, so each admits exactly one JSON form and is sent as a JSON string.

const issue1767Ref = "mcp:knowledge_page:platform-presentations"

const issue1767Purpose = "Acceptance for #1767: proving a presentation can be built on the slide runtime the platform serves."

// issue1767VendorPath matches a served-runtime path the page names.
var issue1767VendorPath = regexp.MustCompile(`/portal/vendor/reveal/[A-Za-z0-9_./-]+`)

// issue1767Deck is a deck on the page's skeleton, the smallest one that
// exercises every path the skeleton names.
const issue1767Deck = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Acceptance deck 1767</title>
<link rel="stylesheet" href="/portal/vendor/reveal/reset.css">
<link rel="stylesheet" href="/portal/vendor/reveal/reveal.css">
<link rel="stylesheet" href="/portal/vendor/reveal/theme/white.css">
</head>
<body>
<div class="reveal"><div class="slides">
<section><h1>Acceptance deck 1767</h1></section>
<section data-markdown><textarea data-template>
## Second slide
- written in markdown
</textarea></section>
</div></div>
<script src="/portal/vendor/reveal/reveal.js"></script>
<script src="/portal/vendor/reveal/plugin/markdown.js"></script>
<script src="/portal/vendor/reveal/plugin/zoom.js"></script>
<script>Reveal.initialize({ hash: false, plugins: [RevealMarkdown, RevealZoom] });</script>
</body>
</html>`

// issue1767Instructions calls platform_info in one of the two forms its schema
// admits and returns the instruction text a session is handed.
func issue1767Instructions(t *testing.T, c *client, args map[string]any) string {
	t.Helper()
	res, text, err := c.callRaw("platform_info", args)
	if err != nil {
		t.Fatalf("platform_info: transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("platform_info: tool error: %s", text)
	}
	return text
}

// issue1767Page fetches the presentations page and returns its body, the
// markdown an agent reads, decoded out of the JSON envelope fetch wraps it in.
func issue1767Page(t *testing.T, c *client) string {
	t.Helper()
	res, text, err := c.callRaw("fetch", map[string]any{
		"reference": issue1767Ref,
		"purpose":   issue1767Purpose,
	})
	if err != nil {
		t.Fatalf("fetch: transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("fetch %s: tool error: %s", issue1767Ref, text)
	}
	var out struct {
		Found    bool `json:"found"`
		Document struct {
			Body string `json:"body"`
		} `json:"document"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("fetch %s: result is not the JSON envelope: %v\n%s", issue1767Ref, err, text)
	}
	if !out.Found || out.Document.Body == "" {
		t.Fatalf("fetch %s did not find the page:\n%s", issue1767Ref, text)
	}
	return out.Document.Body
}

// issue1767Get is an unauthenticated GET of a path on the platform: what a
// browser holding a share link, and nothing else, sends.
func issue1767Get(t *testing.T, base, path string) (*http.Response, []byte) {
	t.Helper()
	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Get(base + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		t.Fatalf("GET %s: reading body: %v", path, err)
	}
	return resp, body
}

// TestIssue1767_TheFirstResponseOfASessionNamesThePresentationsPage: an agent
// asked for a deck is told the guidance exists before it writes anything.
func TestIssue1767_TheFirstResponseOfASessionNamesThePresentationsPage(t *testing.T) {
	c := connect(t)
	for _, form := range []struct {
		name string
		args map[string]any
	}{
		{"absent params", nil},
		{"empty object", map[string]any{}},
	} {
		t.Run(form.name, func(t *testing.T) {
			info := issue1767Instructions(t, c, form.args)
			if !strings.Contains(info, issue1767Ref) {
				t.Fatalf("platform_info does not name %s, so an agent asked for a deck is never told how one is built:\n%s", issue1767Ref, info)
			}
		})
	}
}

// TestIssue1767_ThePageCarriesTheSkeletonAndEveryPathItNamesIsServed: the
// page resolves on this deployment with the skeleton an agent copies, and each
// runtime path it names answers to an unauthenticated reader with the runtime
// itself, from this origin.
func TestIssue1767_ThePageCarriesTheSkeletonAndEveryPathItNamesIsServed(t *testing.T) {
	c := connect(t)
	page := issue1767Page(t, c)

	for _, rule := range []string{
		"/portal/vendor/reveal/reveal.js",
		"/portal/vendor/reveal/reveal.css",
		"/portal/vendor/reveal/plugin/markdown.js",
		`<div class="reveal">`,
		`<div class="slides">`,
		"Reveal.initialize",
		"hash: false",
		"Present",
		"speaker view",
	} {
		if !strings.Contains(page, rule) {
			t.Errorf("the presentations page does not carry %q", rule)
		}
	}

	paths := map[string]bool{}
	for _, p := range issue1767VendorPath.FindAllString(page, -1) {
		paths[strings.TrimRight(p, ".")] = true
	}
	if len(paths) < 5 {
		t.Fatalf("the page names %d served paths; expected the runtime, its stylesheets and the plugins", len(paths))
	}
	for p := range paths {
		t.Run(strings.TrimPrefix(p, "/portal/vendor/reveal/"), func(t *testing.T) {
			resp, body := issue1767Get(t, c.base, p)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s answered %d; a share viewer's frame would load nothing", p, resp.StatusCode)
			}
			ct := resp.Header.Get("Content-Type")
			wantCT := "javascript"
			if strings.HasSuffix(p, ".css") {
				wantCT = "text/css"
			}
			if !strings.Contains(ct, wantCT) {
				t.Fatalf("GET %s served Content-Type %q, which a browser refuses for a %s", p, ct, wantCT)
			}
			if strings.Contains(string(body[:min(len(body), 512)]), "<!doctype html") ||
				strings.Contains(string(body[:min(len(body), 512)]), "<!DOCTYPE html") {
				t.Fatalf("GET %s served the SPA shell rather than the runtime", p)
			}
			if p == "/portal/vendor/reveal/reveal.js" && !strings.Contains(string(body[:min(len(body), 512)]), "reveal.js") {
				t.Fatalf("GET %s does not open with the runtime's own header:\n%s", p, body[:min(len(body), 200)])
			}
		})
	}
}

// TestIssue1767_ADeckSavedOnTheSkeletonIsStoredIntactAndItsPublicShareAdmitsTheRuntime:
// save_asset keeps the deck's root-relative paths as written (the platform
// rewrites references, never these), and the public share page's script policy
// admits the page's own origin, which is the only origin the deck loads from.
func TestIssue1767_ADeckSavedOnTheSkeletonIsStoredIntactAndItsPublicShareAdmitsTheRuntime(t *testing.T) {
	c := connect(t)
	saved := c.call("save_asset", map[string]any{
		"name":         fmt.Sprintf("Acceptance deck 1767 %d", time.Now().UnixNano()),
		"content":      issue1767Deck,
		"content_type": "text/html",
		"purpose":      issue1767Purpose,
	})
	assetID, _ := saved["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("save_asset returned no asset_id: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID, "purpose": issue1767Purpose})
	})

	status, _ := c.rest(http.MethodGet, "/api/v1/portal/assets/"+assetID, nil)
	if status != http.StatusOK {
		t.Fatalf("the saved deck is not readable back: GET answered %d", status)
	}
	contentResp, err := http.NewRequest(http.MethodGet, c.base+"/api/v1/portal/assets/"+assetID+"/content", nil)
	if err != nil {
		t.Fatal(err)
	}
	contentResp.Header.Set("X-API-Key", c.apiKey)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(contentResp)
	if err != nil {
		t.Fatalf("GET content: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET content answered %d", resp.StatusCode)
	}
	for _, p := range []string{
		`src="/portal/vendor/reveal/reveal.js"`,
		`href="/portal/vendor/reveal/reveal.css"`,
		`src="/portal/vendor/reveal/plugin/markdown.js"`,
	} {
		if !strings.Contains(string(body), p) {
			t.Errorf("the stored deck no longer carries %s", p)
		}
	}
	if strings.Contains(string(body), "cdn.") {
		t.Errorf("the stored deck names a CDN, which the page tells an agent never to do")
	}

	share := c.call("manage_asset", map[string]any{
		"action":      "share",
		"asset_id":    assetID,
		"access_mode": "public",
		"expires_in":  "1h",
		"purpose":     issue1767Purpose,
	})
	shareURL, _ := share["share_url"].(string)
	if !strings.Contains(shareURL, "/portal/view/") {
		t.Fatalf("share returned no viewer URL: %v", share)
	}
	viewPath := shareURL[strings.Index(shareURL, "/portal/view/"):]
	pageResp, pageBody := issue1767Get(t, c.base, viewPath)
	if pageResp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s answered %d", viewPath, pageResp.StatusCode)
	}
	csp := pageResp.Header.Get("Content-Security-Policy")
	scriptSrc := ""
	for _, d := range strings.Split(csp, ";") {
		if strings.HasPrefix(strings.TrimSpace(d), "script-src ") {
			scriptSrc = strings.TrimSpace(d)
		}
	}
	if !strings.Contains(scriptSrc, "'self'") {
		t.Fatalf("the share page's script-src %q does not admit its own origin, which is where the runtime is served", scriptSrc)
	}
	if !strings.Contains(string(pageBody), "/portal/view/_assets/") {
		t.Fatalf("the share page carries no viewer bundle; the deck would render nothing:\n%s", pageBody[:min(len(pageBody), 400)])
	}
}
