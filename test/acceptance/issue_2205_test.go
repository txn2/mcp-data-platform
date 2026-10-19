//go:build integration

package acceptance

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Issue #2205: in v1.147.0 every portal URL of an asset answered Go's plain
// "404 page not found". The asset viewer route /portal/assets/{id} shares the
// prefix of Vite's build files, and the rule that a missing build file is a
// 404 (#2139) took the route for one. A share link redirects a signed-in
// reader there, and notifications and save_asset's portal_url point there.
//
// Wire forms: save_asset with its content as a string, its one form; the share
// create as one JSON object of strings; plain HTTP GETs of the share link
// (with the dev API key as Bearer) and of the asset page (no credential: the
// portal shell is public, its data is not).

func issue2205Save(t *testing.T, c *client) (id, portalURL string) {
	t.Helper()
	out := c.call("save_asset", map[string]any{
		"name":         fmt.Sprintf("acceptance-2205-%d", time.Now().UnixNano()),
		"content":      "# Shared\n\nAcceptance #2205.",
		"content_type": "text/markdown",
		"description":  "Acceptance #2205: an asset opened by its portal URL.",
	})
	id, _ = out["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", out)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })
	portalURL, _ = out["portal_url"].(string)
	return id, portalURL
}

// issue2205Get fetches path without following a redirect.
func issue2205Get(t *testing.T, path, key string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL()+path, http.NoBody) //nolint:noctx // test request
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("GET %s: reading: %v", path, err)
	}
	return res, string(raw)
}

// issue2205AssertPortalPage fails unless path answers the portal's index.html.
func issue2205AssertPortalPage(t *testing.T, path string) {
	t.Helper()
	res, body := issue2205Get(t, path, "")
	if res.StatusCode != http.StatusOK || !strings.Contains(res.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("GET %s answered %d %s: %.200s", path, res.StatusCode, res.Header.Get("Content-Type"), body)
	}
	if !strings.Contains(body, "/portal/assets/") || !strings.Contains(strings.ToLower(body), "<!doctype html>") {
		t.Fatalf("GET %s is not the portal's index.html: %.200s", path, body)
	}
}

func TestIssue2205_AShareLinkOpensTheAssetInThePortal(t *testing.T) {
	c := connect(t)
	id, _ := issue2205Save(t, c)
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

	// A signed-in reader of the link is sent to the asset in the portal.
	res, body := issue2205Get(t, "/portal/view/"+token, devAPIKey())
	if res.StatusCode != http.StatusFound {
		t.Fatalf("GET /portal/view/%s signed in answered %d, want 302: %.200s", token, res.StatusCode, body)
	}
	loc, err := url.Parse(res.Header.Get("Location"))
	if err != nil || loc.Path != "/portal/assets/"+id || loc.Query().Get("share") != token {
		t.Fatalf("the share link redirects to %q, want /portal/assets/%s?share=%s", res.Header.Get("Location"), id, token)
	}
	issue2205AssertPortalPage(t, loc.RequestURI())
}

func TestIssue2205_AnAssetsPortalURLOpensThePortal(t *testing.T) {
	c := connect(t)
	id, portalURL := issue2205Save(t, c)
	u, err := url.Parse(portalURL)
	if err != nil || u.Path != "/portal/assets/"+id {
		t.Fatalf("save_asset portal_url = %q, want a URL whose path is /portal/assets/%s", portalURL, id)
	}
	issue2205AssertPortalPage(t, u.Path)
	issue2205AssertPortalPage(t, u.Path+"/provenance")
}
