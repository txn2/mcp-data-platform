//go:build integration

package acceptance

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2460: the browser tab's icon, read without a session.
//
// Wire forms: save_asset's name, content, content_type and purpose, and
// manage_asset's action, asset_id, access_mode, expires_in and purpose, are
// strings in their schemas and sent as JSON strings; the icon, the shell and
// the share page are plain GETs carrying no cookie and no Authorization.

const purpose2460 = "Acceptance for #2460: the tab icon a signed-out reader is served."

// anonymous2460 GETs path with no session and no redirect followed.
func anonymous2460(t *testing.T, base, path string) (*http.Response, []byte) {
	t.Helper()
	hc := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := hc.Get(base + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close after read
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp, body
}

func TestIssue2460_TheIconAnswersWithoutASession(t *testing.T) {
	c := connect(t)
	resp, body := anonymous2460(t, c.base, "/favicon.ico")
	switch resp.StatusCode {
	case http.StatusOK:
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
			t.Fatalf("/favicon.ico is served as %q, want an image", ct)
		}
		if !strings.Contains(string(body), "<svg") {
			t.Fatalf("/favicon.ico is not the bundled mark: %q", body[:min(len(body), 80)])
		}
	case http.StatusFound:
		if resp.Header.Get("Location") == "" {
			t.Fatal("/favicon.ico redirects nowhere")
		}
	default:
		t.Fatalf("/favicon.ico answered %d without a session, want the icon: %s", resp.StatusCode, body)
	}
	if resp.Header.Get("Cache-Control") == "" {
		t.Error("the icon carries no Cache-Control, so every page load asks again")
	}
}

func TestIssue2460_TheShellAndASharePageNameTheIcon(t *testing.T) {
	c := connect(t)
	resp, shell := anonymous2460(t, c.base, "/portal/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the portal shell answered %d", resp.StatusCode)
	}
	if !strings.Contains(string(shell), `<link rel="icon" href="/favicon.ico"`) {
		t.Errorf("the portal shell names no icon:\n%s", shell)
	}

	saved := c.call("save_asset", map[string]any{
		"name": "Acceptance icon 2460 " + time.Now().Format("150405.000"), "content": "# Icon\n\nA page read signed out.",
		"content_type": "text/markdown", "purpose": purpose2460,
	})
	assetID, _ := saved["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("save_asset returned no asset_id: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID, "purpose": purpose2460})
	})
	share := c.call("manage_asset", map[string]any{
		"action": "share", "asset_id": assetID, "access_mode": "public", "expires_in": "1h", "purpose": purpose2460,
	})
	shareURL, _ := share["share_url"].(string)
	i := strings.Index(shareURL, "/portal/view/")
	if i < 0 {
		t.Fatalf("share returned no viewer URL: %v", share)
	}
	resp, page := anonymous2460(t, c.base, shareURL[i:])
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the share page answered %d signed out", resp.StatusCode)
	}
	if !strings.Contains(string(page), `<link rel="icon" href="/favicon.ico">`) {
		t.Errorf("the share page names no icon")
	}
	// The icon the page names answers the same signed-out reader, once a
	// redirect to a configured logo is followed.
	hc := &http.Client{Timeout: 30 * time.Second}
	icon, err := hc.Get(c.base + "/favicon.ico")
	if err != nil {
		t.Fatalf("GET /favicon.ico: %v", err)
	}
	_ = icon.Body.Close()
	if icon.StatusCode != http.StatusOK {
		t.Errorf("the icon the share page names answered %d", icon.StatusCode)
	}
}
