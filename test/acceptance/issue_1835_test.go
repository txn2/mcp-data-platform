//go:build integration

package acceptance

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1835: asset content was served `Cache-Control: private` with a
// Last-Modified and no validator of its own, so a browser reused its copy
// under heuristic freshness. The serving rewrite that turns a declared
// reference into a working URL depends on the asset's references, which change
// without the content or its version changing, so after a manage_asset update
// references=[...] the viewer kept rendering raw mcp:// URIs and broken images
// until a hard refresh.
//
// What these hold, against the running platform: the content route answers
// `private, no-cache` with an ETag and no Last-Modified; the held copy
// revalidates to a 304 while nothing changed; declaring a reference, and
// removing it again, each answer the held ETag with the new body in full; and
// the version and public share content routes, which the same rewrite serves,
// answer the same way.
//
// Wire forms: manage_asset's `references` is generated from a []string field
// and admits one form, a JSON array of strings, sent as a one-element array and
// as an empty one. `action`, `asset_id`, `name`, `content_type` and `content`
// are typed string and sent in that one form. The content routes take no body.

// restGet issues an authenticated GET carrying extra request headers and
// returns the status, the response headers and the body.
func (c *client) restGet(path string, headers map[string]string) (int, http.Header, string) {
	c.t.Helper()
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, c.base+path, http.NoBody)
	if err != nil {
		c.t.Fatalf("GET %s: %v", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		c.t.Fatalf("GET %s: reading the body: %v", path, err)
	}
	return res.StatusCode, res.Header, string(raw)
}

// restText is restGet with no extra headers, for a criterion that reads a body.
func (c *client) restText(path string) (int, string) {
	c.t.Helper()
	status, _, body := c.restGet(path, nil)
	return status, body
}

// issue1835Asset saves an HTML asset naming the file without declaring it.
func issue1835Asset(t *testing.T, c *client, uri string) string {
	t.Helper()
	out := c.call("save_asset", map[string]any{
		"name": "Acceptance 1835 report", "content_type": "text/html",
		"content": `<html><body><img src="` + uri + `" alt="logo"></body></html>`,
	})
	id, _ := out["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset: %v", out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id})
	})
	return id
}

// requireRevalidated checks a content response carries the revalidation
// contract and returns its ETag.
func requireRevalidated(t *testing.T, path string, status int, h http.Header) string {
	t.Helper()
	if status != http.StatusOK {
		t.Fatalf("%s: status %d", path, status)
	}
	if got := h.Get("Cache-Control"); got != "private, no-cache" {
		t.Errorf("%s: Cache-Control = %q; want %q", path, got, "private, no-cache")
	}
	if got := h.Get("Last-Modified"); got != "" {
		t.Errorf("%s: Last-Modified = %q; want none", path, got)
	}
	tag := h.Get("ETag")
	if tag == "" {
		t.Fatalf("%s: no ETag", path)
	}
	return tag
}

// TestIssue1835_AReferencesChangeReachesAHeldCopy is the ticket's
// reproduction, as the browser's conditional request makes it.
func TestIssue1835_AReferencesChangeReachesAHeldCopy(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	file := createResource1584(t, c, "persona", personaLibrary1584, "logo-1835-"+stamp+".csv")
	assetID := issue1835Asset(t, c, file.uri)
	path := "/api/v1/portal/assets/" + assetID + "/content"

	status, h, body := c.restGet(path, nil)
	held := requireRevalidated(t, path, status, h)
	if !strings.Contains(body, file.uri) {
		t.Fatalf("the undeclared reference was rewritten: %s", body)
	}

	status, _, _ = c.restGet(path, map[string]string{"If-None-Match": held})
	if status != http.StatusNotModified {
		t.Errorf("an unchanged body answered %d; want 304", status)
	}

	c.call("manage_asset", map[string]any{"action": "update", "asset_id": assetID, "references": []string{file.uri}})
	status, h, body = c.restGet(path, map[string]string{"If-None-Match": held})
	if status != http.StatusOK {
		t.Fatalf("after declaring the reference the held copy answered %d; want 200 with the new body", status)
	}
	declared := requireRevalidated(t, path, status, h)
	if declared == held || strings.Contains(body, file.uri) || !strings.Contains(body, "/portal/refs/"+assetID+"/") {
		t.Errorf("the declaration did not reach the held copy: tag %s -> %s, body %s", held, declared, body)
	}

	c.call("manage_asset", map[string]any{"action": "update", "asset_id": assetID, "references": []string{}})
	status, h, body = c.restGet(path, map[string]string{"If-None-Match": declared})
	if status != http.StatusOK || h.Get("ETag") == declared || !strings.Contains(body, file.uri) {
		t.Errorf("removing the reference did not reach the held copy: status %d, tag %s, body %s", status, h.Get("ETag"), body)
	}
}

// TestIssue1835_EveryRouteTheRewriteServesRevalidates covers the version and
// public share content routes, which serve the same rewritten body.
func TestIssue1835_EveryRouteTheRewriteServesRevalidates(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	file := createResource1584(t, c, "persona", personaLibrary1584, "logo-1835-routes-"+stamp+".csv")
	assetID := issue1835Asset(t, c, file.uri)

	path := "/api/v1/portal/assets/" + assetID + "/versions/1/content"
	status, h, _ := c.restGet(path, nil)
	requireRevalidated(t, path, status, h)

	shared := c.call("manage_asset", map[string]any{
		"action": "share", "asset_id": assetID, "access_mode": "public",
		"permission": "viewer", "expires_in": "1h",
	})
	shareURL, _ := shared["share_url"].(string)
	if shareURL == "" {
		t.Fatalf("the share carries no link: %v", shared)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, shareURL+"/content", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	requireRevalidated(t, shareURL+"/content", res.StatusCode, res.Header)
}
