//go:build integration

package acceptance

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Acceptance for #1983: exporting a deck to PDF gives one page per slide at
// its final state, in the deck's own colors, printed by the platform's
// renderer.
//
// The deck is saved through save_asset as an agent saves one, on the served
// runtime, dark, with one slide that builds in three steps. The PDF is read
// from the route the viewer's Export PDF button calls, with the caller's
// credential, and from the share page's route with none.
//
// Wire forms: save_asset's content is a string, its one form; the PDF routes
// are GETs with no body.

// issue1983Deck has three slides; the second builds in three fragments, which
// a print that gave every build step a page would print as five pages.
const issue1983Deck = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><title>Q3 review</title>
<link rel="stylesheet" href="/portal/vendor/reveal/reveal.css">
<link rel="stylesheet" href="/portal/vendor/reveal/theme/black.css">
<style>:root{--r-background-color:#0f172a;--r-main-color:#e2e8f0;}</style>
</head><body>
<div class="reveal"><div class="slides">
<section><h1>Q3 review</h1></section>
<section><h2>Three steps</h2><p class="fragment">One</p><p class="fragment">Two</p><p class="fragment">Three</p></section>
<section><h2>Close</h2></section>
</div></div>
<script src="/portal/vendor/reveal/reveal.js"></script>
<script>Reveal.initialize({hash:false});</script>
</body></html>`

// issue1983Background is the deck's background, #0f172a, as the fill color
// a PDF content stream sets it with: each channel over 255, rounded, with or
// without the leading zero (Chrome writes ".0588 .0902 .1647 rg").
var issue1983Background = regexp.MustCompile(`0?\.0588\d* 0?\.090[12]\d* 0?\.1647\d* (rg|sc|scn)`)

func issue1983Save(t *testing.T, c *client, contentType, body string) string {
	t.Helper()
	out := c.call("save_asset", map[string]any{
		"name":         fmt.Sprintf("acceptance-1983-%d", time.Now().UnixNano()),
		"content":      body,
		"content_type": contentType,
		"description":  "Acceptance #1983: a deck exported to PDF.",
	})
	id, _ := out["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", out)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })
	return id
}

// issue1983Get fetches path with the caller's credential, or none when key is
// empty, and returns the status, content type and body.
func issue1983Get(t *testing.T, path, key string) (int, string, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL()+path, http.NoBody) //nolint:noctx // test request
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("GET %s: reading: %v", path, err)
	}
	return res.StatusCode, res.Header.Get("Content-Type"), body
}

// issue1983Pages counts the page objects in a PDF.
func issue1983Pages(pdf []byte) int {
	return len(regexp.MustCompile(`/Type\s*/Page[^s]`).FindAll(pdf, -1))
}

// issue1983Streams is every Flate-compressed content stream in a PDF,
// inflated: what each page draws, including the fills it paints.
func issue1983Streams(pdf []byte) string {
	var out strings.Builder
	rest := pdf
	for {
		i := bytes.Index(rest, []byte("stream"))
		if i < 0 {
			break
		}
		rest = rest[i+len("stream"):]
		rest = bytes.TrimLeft(rest, "\r\n")
		end := bytes.Index(rest, []byte("endstream"))
		if end < 0 {
			break
		}
		if r, err := zlib.NewReader(bytes.NewReader(rest[:end])); err == nil {
			raw, _ := io.ReadAll(r)
			out.Write(raw)
			out.WriteByte('\n')
		}
		rest = rest[end:]
	}
	return out.String()
}

// TestIssue1983_ADeckExportsOnePagePerSlideInItsOwnColors is the ticket's
// expectation: one page per slide at its final state, color-matched.
func TestIssue1983_ADeckExportsOnePagePerSlideInItsOwnColors(t *testing.T) {
	c := connect(t)
	id := issue1983Save(t, c, "text/html", issue1983Deck)

	status, contentType, pdf := issue1983Get(t, "/api/v1/portal/assets/"+id+"/pdf", c.apiKey)
	if status != http.StatusOK {
		t.Fatalf("GET pdf: HTTP %d: %s", status, pdf)
	}
	if contentType != "application/pdf" || !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("the answer is not a PDF: %s, %q", contentType, pdf[:min(len(pdf), 16)])
	}
	if pages := issue1983Pages(pdf); pages != 3 {
		t.Errorf("the deck exported as %d pages, want 3: one per slide, the three build steps on one", pages)
	}
	if !issue1983Background.MatchString(issue1983Streams(pdf)) {
		t.Error("no page is filled with the deck's own background, #0f172a: the colors were remapped or the background dropped")
	}
}

// TestIssue1983_TheSharePageExportsTheSameDeck reads the PDF from the public
// share page's route, which is what its Export PDF button calls.
func TestIssue1983_TheSharePageExportsTheSameDeck(t *testing.T) {
	c := connect(t)
	id := issue1983Save(t, c, "text/html", issue1983Deck)
	status, share := c.rest(http.MethodPost, "/api/v1/portal/assets/"+id+"/shares",
		jsonBody(t, map[string]any{"access_mode": "public", "expires_in": "1h"}))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("create share: HTTP %d: %v", status, share)
	}
	created, _ := share["share"].(map[string]any)
	token, _ := created["token"].(string)
	if token == "" {
		t.Fatalf("the share carries no token: %v", share)
	}

	status, _, pdf := issue1983Get(t, "/portal/view/"+token+"/pdf", "")
	if status != http.StatusOK || !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("GET the share page's pdf: HTTP %d: %q", status, pdf[:min(len(pdf), 64)])
	}
	if pages := issue1983Pages(pdf); pages != 3 {
		t.Errorf("the shared deck exported as %d pages, want 3", pages)
	}
}

// TestIssue1983_ThePDFRouteKeepsTheContentRoutesRules holds the route to
// exactly the access its content route grants, and to HTML.
func TestIssue1983_ThePDFRouteKeepsTheContentRoutesRules(t *testing.T) {
	c := connect(t)
	deck := issue1983Save(t, c, "text/html", issue1983Deck)
	if status, _, _ := issue1983Get(t, "/api/v1/portal/assets/"+deck+"/pdf", ""); status != http.StatusUnauthorized {
		t.Errorf("an anonymous caller was answered %d, want the content route's 401", status)
	}
	notes := issue1983Save(t, c, "text/markdown", "# Notes\n\nNot a deck.")
	if status, _, body := issue1983Get(t, "/api/v1/portal/assets/"+notes+"/pdf", c.apiKey); status != http.StatusUnsupportedMediaType {
		t.Errorf("a Markdown asset was answered %d (%s), want 415", status, body)
	}
}
