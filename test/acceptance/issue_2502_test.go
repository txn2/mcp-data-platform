//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2502: an asset carries a status, Draft, Published or Deprecated with
// an optional note, set by its owner and editors from the portal and from
// save_asset and manage_asset. A status is informational: it changes no
// access, a new asset has none until someone sets one, and every change is
// recorded in the asset's history.
//
// Wire forms: save_asset's `status` and `status_note` and manage_asset's
// `action`, `asset_id`, `status` and `status_note` are typed string, sent as
// strings (the one form each admits); the portal routes take a JSON object
// body {status, note}.

// analystKey and engineerKey are the dev stack's two collaborators, people
// with every tool and no admin role who own their own work (dev/platform.yaml):
// the pair a reader-and-owner criterion is exercised with.
const (
	analystKey    = devPeerAPIKey
	analystEmail  = devPeerEmailAddr
	engineerKey   = devOwnerAPIKey
	engineerEmail = devOwnerEmail
)

// stamp2502 makes a name unique to this run.
func stamp2502() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }

// saveAsset2502 saves an asset as c with extra save_asset arguments, deletes
// it when the test ends, and returns its id.
func saveAsset2502(t *testing.T, c *client, name, contentType, content string, extra map[string]any) string {
	t.Helper()
	args := map[string]any{"name": name, "content_type": contentType, "content": content}
	for k, v := range extra {
		args[k] = v
	}
	out := c.call("save_asset", args)
	id, _ := out["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", out)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/portal/assets/"+id, nil) })
	return id
}

// share2502 shares an asset the owner holds with one person.
func share2502(t *testing.T, owner *client, assetID, email, permission string) {
	t.Helper()
	owner.call("manage_asset", map[string]any{
		"action": "share", "asset_id": assetID, "recipient": email, "permission": permission,
	})
}

// portalAsset2502 reads an asset the way its portal page does.
func portalAsset2502(t *testing.T, c *client, id string) (int, map[string]any) {
	t.Helper()
	return c.rest(http.MethodGet, "/api/v1/portal/assets/"+id, nil)
}

// statusOf is an asset's status value and note, empty when it has none.
func statusOf(asset map[string]any) (value, note string) {
	st, _ := asset["status"].(map[string]any)
	if st == nil {
		return "", ""
	}
	value, _ = st["value"].(string)
	note, _ = st["note"].(string)
	return value, note
}

// TestIssue2502_ASaveSetsAStatusAndWithoutOneTheAssetHasNone: save_asset with
// status draft saves a Draft asset; without status the asset has none.
func TestIssue2502_ASaveSetsAStatusAndWithoutOneTheAssetHasNone(t *testing.T) {
	c := connect(t)
	s := stamp2502()
	draft := saveAsset2502(t, c, "acc-2502-draft-"+s, "text/markdown", "# Draft\n", map[string]any{"status": "draft"})
	plain := saveAsset2502(t, c, "acc-2502-plain-"+s, "text/markdown", "# Plain\n", nil)

	got := c.call("manage_asset", map[string]any{"action": "get", "asset_id": draft})
	if v, _ := statusOf(got); v != "draft" {
		t.Fatalf("the asset saved with status draft reads status %q: %v", v, got["status"])
	}
	got = c.call("manage_asset", map[string]any{"action": "get", "asset_id": plain})
	if v, _ := statusOf(got); v != "" {
		t.Fatalf("an asset saved without status reads status %q; a new asset has none", v)
	}
	res, text, err := c.callRaw("save_asset", map[string]any{
		"name": "acc-2502-bad-" + s, "content_type": "text/markdown", "content": "# x\n", "status": "final",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("a save with an unknown status was accepted: %s", text)
	}
	if n := assetsNamed1875(t, c, "acc-2502-bad-"+s); n != 0 {
		t.Fatalf("the refused save left %d asset(s) behind", n)
	}
}

// TestIssue2502_UpdateSetsDeprecatedWithANoteTheReaderSees: manage_asset
// update sets Deprecated with a note; the portal page reads the status and
// the note its banner shows, and the history records who changed it from what
// to what.
func TestIssue2502_UpdateSetsDeprecatedWithANoteTheReaderSees(t *testing.T) {
	c := connect(t)
	id := saveAsset2502(t, c, "acc-2502-dep-"+stamp2502(), "text/markdown", "# Q3\n", map[string]any{"status": "published"})
	c.call("manage_asset", map[string]any{
		"action": "update", "asset_id": id, "status": "deprecated", "status_note": "Replaced by the Q4 report",
	})
	status, asset := portalAsset2502(t, c, id)
	if status != http.StatusOK {
		t.Fatalf("GET asset: HTTP %d", status)
	}
	if v, note := statusOf(asset); v != "deprecated" || note != "Replaced by the Q4 report" {
		t.Fatalf("the portal reads status %q note %q; want deprecated with its note", v, note)
	}
	status, hist := c.rest(http.MethodGet, "/api/v1/portal/assets/"+id+"/status/history", nil)
	if status != http.StatusOK {
		t.Fatalf("GET history: HTTP %d", status)
	}
	events, _ := hist["events"].([]any)
	if len(events) < 2 {
		t.Fatalf("the history holds %d events; want the published and deprecated changes: %v", len(events), hist)
	}
	newest, _ := events[0].(map[string]any)
	if newest["from"] != "published" || newest["to"] != "deprecated" || newest["actor"] != "admin@example.com" {
		t.Fatalf("the newest change reads %v; want published -> deprecated by admin@example.com", newest)
	}

	// The portal's own routes set and clear it the same way.
	if status, body := c.rest(http.MethodPut, "/api/v1/portal/assets/"+id+"/status",
		jsonBody(t, map[string]any{"status": "draft"})); status != http.StatusOK {
		t.Fatalf("PUT status: HTTP %d %v", status, body)
	}
	if status, body := c.rest(http.MethodDelete, "/api/v1/portal/assets/"+id+"/status", nil); status != http.StatusOK {
		t.Fatalf("DELETE status: HTTP %d %v", status, body)
	} else if v, _ := statusOf(body); v != "" {
		t.Fatalf("a cleared status reads %q", v)
	}
}

// TestIssue2502_AViewerSeesTheStatusAndCannotChangeIt: a viewer-share
// recipient sees the badge and is refused through the API; Draft on a shared
// asset leaves the recipient able to open it.
func TestIssue2502_AViewerSeesTheStatusAndCannotChangeIt(t *testing.T) {
	owner := connect(t)
	viewer := connectAs(t, analystKey)
	id := saveAsset2502(t, owner, "acc-2502-shared-"+stamp2502(), "text/markdown", "# Shared\n", nil)
	share2502(t, owner, id, analystEmail, "viewer")
	owner.call("manage_asset", map[string]any{"action": "update", "asset_id": id, "status": "draft"})

	status, asset := portalAsset2502(t, viewer, id)
	if status != http.StatusOK {
		t.Fatalf("Draft took the share recipient's access away: HTTP %d", status)
	}
	if v, _ := statusOf(asset); v != "draft" {
		t.Fatalf("the recipient reads status %q; want the badge draft", v)
	}
	if status, _ := viewer.rest(http.MethodPut, "/api/v1/portal/assets/"+id+"/status",
		jsonBody(t, map[string]any{"status": "published"})); status != http.StatusForbidden {
		t.Fatalf("a viewer's status change answered HTTP %d, want 403", status)
	}
	if status, _ := viewer.rest(http.MethodDelete, "/api/v1/portal/assets/"+id+"/status", nil); status != http.StatusForbidden {
		t.Fatalf("a viewer's status clear answered HTTP %d, want 403", status)
	}
	res, text, err := viewer.callRaw("manage_asset", map[string]any{"action": "update", "asset_id": id, "status": "published"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("a viewer's manage_asset status change was accepted: %s", text)
	}
	if _, asset = portalAsset2502(t, owner, id); func() string { v, _ := statusOf(asset); return v }() != "draft" {
		t.Fatalf("a refused change moved the status: %v", asset["status"])
	}
}

// TestIssue2502_TheAllScopeFiltersByStatus: filtering by Draft in the All
// scope returns a Draft asset shared with the caller, and "none" returns an
// asset with no status and not the Draft one, in the listing and in search.
func TestIssue2502_TheAllScopeFiltersByStatus(t *testing.T) {
	owner := connect(t)
	viewer := connectAs(t, analystKey)
	s := stamp2502()
	word := "zq2502" + s[len(s)-6:]
	draft := saveAsset2502(t, owner, "acc-2502-filter-draft-"+word, "text/markdown", "# "+word+"\n", map[string]any{"status": "draft"})
	plain := saveAsset2502(t, owner, "acc-2502-filter-plain-"+word, "text/markdown", "# "+word+"\n", nil)
	share2502(t, owner, draft, analystEmail, "viewer")
	share2502(t, owner, plain, analystEmail, "viewer")

	ids := func(path string) []string {
		status, page := viewer.rest(http.MethodGet, path, nil)
		if status != http.StatusOK {
			t.Fatalf("GET %s: HTTP %d %v", path, status, page)
		}
		var out []string
		rows, _ := page["data"].([]any)
		for _, r := range rows {
			row, _ := r.(map[string]any)
			if a, ok := row["asset"].(map[string]any); ok {
				row = a
			}
			if id, _ := row["id"].(string); id != "" {
				out = append(out, id)
			}
		}
		return out
	}
	for _, path := range []string{
		"/api/v1/portal/assets?scope=all&status=draft&limit=200",
		"/api/v1/portal/assets/search?scope=all&status=draft&q=" + word,
	} {
		got := strings.Join(ids(path), ",")
		if !strings.Contains(got, draft) || strings.Contains(got, plain) {
			t.Errorf("%s returned %s; want the shared Draft asset %s and not %s", path, got, draft, plain)
		}
	}
	got := strings.Join(ids("/api/v1/portal/assets/search?scope=all&status=none&q="+word), ",")
	if !strings.Contains(got, plain) || strings.Contains(got, draft) {
		t.Errorf("status=none returned %s; want %s and not the Draft %s", got, plain, draft)
	}
}
