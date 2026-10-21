//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
)

// Issue #2226: a reader whose access to an asset is viewer sees only its
// current version. An asset with three versions, shared as viewer: version
// 3's content, no history, and 403 from the version routes, the signed
// content URL naming an earlier version, and manage_asset list_versions and
// diff. Shared as editor: all three.
//
// Wire forms: the share body's permission is sent as both of its strings,
// "viewer" and "editor"; the content URL's version is a query string number.

// threeVersions saves an asset and writes it twice more.
func threeVersions(t *testing.T, c *client) string {
	t.Helper()
	assetID, _ := saveReport(t, c, "2226 history")
	for _, content := range []string{"region,total\nwest,900\n", "region,total\nwest,999\n"} {
		res := c.call("manage_asset", map[string]any{"action": "update", "asset_id": assetID, "content": content})
		if _, isErr := res["error"]; isErr {
			t.Fatalf("update: %v", res)
		}
	}
	_, a := c.rest(http.MethodGet, "/api/v1/portal/assets/"+assetID, http.NoBody)
	if number(t, a, "current_version") != 3 {
		t.Fatalf("the asset is at version %v, want 3", a["current_version"])
	}
	return assetID
}

// shareAs shares the asset with a member at a permission.
func shareAs(t *testing.T, owner *client, assetID string, m issue2241Member, permission string) {
	t.Helper()
	if status, out := owner.rest(http.MethodPost, "/api/v1/portal/assets/"+assetID+"/shares", jsonBody(t, map[string]any{
		"shared_with_user_id": m.userID, "permission": permission, "notify": false,
	})); status != http.StatusCreated {
		t.Fatalf("sharing as %s: %d %v", permission, status, out)
	}
}

// TestIssue2226_AViewerReadsTheCurrentVersionOnly is the ticket's first
// acceptance sentence.
func TestIssue2226_AViewerReadsTheCurrentVersionOnly(t *testing.T) {
	owner := connect(t)
	assetID := threeVersions(t, owner)
	viewer := newIssue2241Member(t, owner, "2226-viewer", nil)
	shareAs(t, owner, assetID, viewer, "viewer")

	base := "/api/v1/portal/assets/" + assetID
	if status, body := rawGet(t, owner.base, base+"/content", viewer.key); status != http.StatusOK || !strings.Contains(string(body), "west,999") {
		t.Errorf("the current content: %d %.100s", status, body)
	}
	for path, want := range map[string]int{
		base + "/versions":                     http.StatusForbidden,
		base + "/versions/1/content":           http.StatusForbidden,
		base + "/versions/3/content":           http.StatusOK,
		base + "/content-url?version=1&ttl=60": http.StatusForbidden,
	} {
		if status, body := rawGet(t, owner.base, path, viewer.key); status != want {
			t.Errorf("%s: %d, want %d (%.200s)", path, status, want, body)
		}
	}
	for _, action := range []string{"list_versions", "diff"} {
		_, text, _ := viewer.client.callRaw("manage_asset", map[string]any{"action": action, "asset_id": assetID})
		if !strings.Contains(text, "earlier versions") {
			t.Errorf("manage_asset %s as a viewer: %.200s", action, text)
		}
	}
}

// TestIssue2226_AnEditorReadsEveryVersion is the second.
func TestIssue2226_AnEditorReadsEveryVersion(t *testing.T) {
	owner := connect(t)
	assetID := threeVersions(t, owner)
	editor := newIssue2241Member(t, owner, "2226-editor", nil)
	shareAs(t, owner, assetID, editor, "editor")

	status, raw := rawGet(t, owner.base, "/api/v1/portal/assets/"+assetID+"/versions", editor.key)
	if status != http.StatusOK || !strings.Contains(string(raw), `"total":3`) {
		t.Errorf("versions as an editor: %d %.200s", status, raw)
	}
	if status, body := rawGet(t, owner.base, "/api/v1/portal/assets/"+assetID+"/versions/1/content", editor.key); status != http.StatusOK || !strings.Contains(string(body), "west,812") {
		t.Errorf("version 1 as an editor: %d %.100s", status, body)
	}
}
