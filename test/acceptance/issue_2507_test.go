//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2507: bulk actions on assets. One request applies one action to many
// assets; each asset is authorized with its own single-asset rule, nothing is
// applied to an asset the caller may not act on, and a skipped asset says
// why. An asset the caller cannot open is skipped as "not found or no
// access", the same as one that does not exist, and nothing else about it is
// returned. manage_asset takes several ids for the same actions.
//
// Wire forms: POST /api/v1/portal/assets/bulk's action, collection_id,
// status and note as strings, asset_ids and tags as JSON arrays of strings,
// share as an object of strings; asset_ids as a JSON string (refused 400).
// manage_asset's action and status as strings, asset_ids, add_tags and
// remove_tags as JSON arrays of strings; asset_ids as a string (refused by the
// schema). save_asset's name, content, content_type as strings and tags as an
// array. The share body's shared_with_persona and permission as strings.

const reasonHidden2507 = "not found or no access"

// bulkResult2507 is the bulk route's answer.
type bulkResult2507 struct {
	Action    string   `json:"action"`
	Requested int      `json:"requested"`
	Applied   []string `json:"applied"`
	Skipped   []struct {
		AssetID string `json:"asset_id"`
		Reason  string `json:"reason"`
	} `json:"skipped"`
}

func save2507(t *testing.T, c *client, name string, tags ...string) string {
	t.Helper()
	args := map[string]any{"name": name, "content_type": "text/markdown", "content": "# " + name}
	if len(tags) > 0 {
		args["tags"] = tags
	}
	out := c.call("save_asset", args)
	id, _ := out["asset_id"].(string)
	if id == "" {
		t.Fatalf("saving %s: %v", name, out)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })
	return id
}

// bulk2507 posts one bulk action and decodes the answer, returning the raw
// body too so a criterion can search it.
func bulk2507(t *testing.T, c *client, body map[string]any) (int, bulkResult2507, string) {
	t.Helper()
	status, out := c.rest(http.MethodPost, "/api/v1/portal/assets/bulk", jsonBody(t, body))
	raw, _ := json.Marshal(out)
	var res bulkResult2507
	_ = json.Unmarshal(raw, &res)
	return status, res, string(raw)
}

func assetTags2507(t *testing.T, c *client, id string) []string {
	t.Helper()
	status, out := c.rest(http.MethodGet, "/api/v1/portal/assets/"+id, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading asset %s: %d %v", id, status, out)
	}
	raw, _ := json.Marshal(out["tags"])
	var tags []string
	_ = json.Unmarshal(raw, &tags)
	return tags
}

func TestIssue2507_TaggingSixtySelectedAssetsIsOneRequest(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	stamp := time.Now().UnixNano()
	tag := fmt.Sprintf("acc2507bulk%d", stamp)
	ids := make([]any, 0, 60)
	for i := range 60 {
		ids = append(ids, save2507(t, owner, fmt.Sprintf("acc2507 tag %d %d", stamp, i)))
	}
	status, res, raw := bulk2507(t, owner, map[string]any{"action": "add_tags", "asset_ids": ids, "tags": []any{tag}})
	if status != http.StatusOK {
		t.Fatalf("bulk add_tags: %d %s", status, raw)
	}
	if res.Requested != 60 || len(res.Applied) != 60 || len(res.Skipped) != 0 {
		t.Fatalf("tagging 60 owned assets applied %d of %d, skipped %d: %s", len(res.Applied), res.Requested, len(res.Skipped), raw)
	}
	status, out := owner.rest(http.MethodGet, "/api/v1/portal/assets?scope=mine&tag="+tag+"&limit=100", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("listing by the tag: %d %v", status, out)
	}
	if total, _ := out["total"].(float64); int(total) != 60 {
		t.Errorf("all 60 assets carry the tag after one request: %v carry it", out["total"])
	}
}

func TestIssue2507_AMixedSelectionSharesOnlyWhatTheCallerOwns(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	stamp := time.Now().UnixNano()
	persona := fmt.Sprintf("acc-2507-%d", stamp)
	if status, out := admin.rest(http.MethodPost, "/api/v1/admin/personas", jsonBody(t, map[string]any{
		"name": persona, "display_name": "Acceptance 2507", "roles": []any{persona + "_member"}, "priority": 10,
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"},
	})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving persona %s: %d %v", persona, status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+persona, http.NoBody) })

	mine1 := save2507(t, owner, fmt.Sprintf("acc2507 mine one %d", stamp))
	mine2 := save2507(t, owner, fmt.Sprintf("acc2507 mine two %d", stamp))
	// Shared with the owner to view: they can open it, not share it.
	viewable := save2507(t, peer, fmt.Sprintf("acc2507 peer viewable %d", stamp))
	if status, out := peer.rest(http.MethodPost, "/api/v1/portal/assets/"+viewable+"/shares", jsonBody(t, map[string]any{
		"shared_with_email": devOwnerEmail, "permission": "viewer", "notify": false,
	})); status != http.StatusCreated {
		t.Fatalf("sharing the peer's asset with the owner: %d %v", status, out)
	}
	// Never shared: the owner cannot open it.
	hiddenName := fmt.Sprintf("acc2507 peer hidden %d", stamp)
	hidden := save2507(t, peer, hiddenName)

	status, res, raw := bulk2507(t, owner, map[string]any{
		"action":    "share",
		"asset_ids": []any{mine1, mine2, viewable, hidden},
		"share":     map[string]any{"shared_with_persona": persona, "permission": "viewer"},
	})
	if status != http.StatusOK {
		t.Fatalf("bulk share: %d %s", status, raw)
	}
	if len(res.Applied) != 2 || !contains2507(res.Applied, mine1) || !contains2507(res.Applied, mine2) {
		t.Errorf("the share applies to the two owned assets: applied %v", res.Applied)
	}
	reasons := map[string]string{}
	for _, s := range res.Skipped {
		reasons[s.AssetID] = s.Reason
	}
	if r := reasons[viewable]; r == "" || r == reasonHidden2507 {
		t.Errorf("the viewer-shared asset is skipped with the share route's own reason, got %q", r)
	}
	if r := reasons[hidden]; r != reasonHidden2507 {
		t.Errorf("the asset the owner cannot open is skipped as %q, got %q", reasonHidden2507, r)
	}
	if strings.Contains(raw, hiddenName) || strings.Contains(raw, devPeerEmailAddr) {
		t.Errorf("the answer names nothing about the asset the owner cannot open: %s", raw)
	}
	for _, id := range []string{viewable, hidden} {
		status, out := peer.rest(http.MethodGet, "/api/v1/portal/assets/"+id+"/shares", http.NoBody)
		if status != http.StatusOK {
			t.Fatalf("listing shares of %s: %d %v", id, status, out)
		}
		if body, _ := json.Marshal(out); strings.Contains(string(body), persona) {
			t.Errorf("nothing changed on the skipped asset %s: it now carries a share to %s", id, persona)
		}
	}
}

func TestIssue2507_BulkDeleteSendsAssetsToTrashAndTheyRestore(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	stamp := time.Now().UnixNano()
	a := save2507(t, owner, fmt.Sprintf("acc2507 trash a %d", stamp))
	b := save2507(t, owner, fmt.Sprintf("acc2507 trash b %d", stamp))
	status, res, raw := bulk2507(t, owner, map[string]any{"action": "delete", "asset_ids": []any{a, b}})
	if status != http.StatusOK || len(res.Applied) != 2 {
		t.Fatalf("bulk delete of two owned assets: %d %s", status, raw)
	}
	status, out := owner.rest(http.MethodGet, "/api/v1/portal/trash", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the trash: %d %v", status, out)
	}
	body, _ := json.Marshal(out)
	if !strings.Contains(string(body), a) || !strings.Contains(string(body), b) {
		t.Fatalf("both deleted assets are in the trash: %s", body)
	}
	if status, out := owner.rest(http.MethodPost, "/api/v1/portal/trash/asset/"+a+"/restore", http.NoBody); status != http.StatusOK {
		t.Fatalf("restoring %s: %d %v", a, status, out)
	}
	if status, out := owner.rest(http.MethodGet, "/api/v1/portal/assets/"+a, http.NoBody); status != http.StatusOK {
		t.Errorf("the restored asset opens again: %d %v", status, out)
	}
}

func TestIssue2507_ManageAssetActsOnSeveralIds(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	stamp := time.Now().UnixNano()
	a := save2507(t, owner, fmt.Sprintf("acc2507 mcp a %d", stamp), "Finance")
	b := save2507(t, owner, fmt.Sprintf("acc2507 mcp b %d", stamp))
	hiddenName := fmt.Sprintf("acc2507 mcp hidden %d", stamp)
	hidden := save2507(t, peer, hiddenName)

	out := owner.call("manage_asset", map[string]any{
		"action": "update", "asset_ids": []any{a, b, hidden}, "status": "draft",
	})
	applied, _ := out["applied"].([]any)
	if len(applied) != 2 {
		t.Fatalf("manage_asset update with asset_ids sets a status on each owned asset: %v", out)
	}
	for _, id := range []string{a, b} {
		got := owner.call("manage_asset", map[string]any{"action": "get", "asset_id": id})
		if v, _ := statusOf(got); v != "draft" {
			t.Errorf("asset %s carries status draft: %v", id, got["status"])
		}
	}
	text, _ := json.Marshal(out)
	if strings.Contains(string(text), hiddenName) || !strings.Contains(string(text), reasonHidden2507) {
		t.Errorf("the asset the owner cannot open is skipped as %q and not named: %s", reasonHidden2507, text)
	}

	owner.call("manage_asset", map[string]any{
		"action": "update", "asset_ids": []any{a, b}, "add_tags": []any{"finance", "board"}, "remove_tags": []any{"draftish"},
	})
	if tags := assetTags2507(t, owner, a); strings.Join(tags, ",") != "Finance,board" {
		t.Errorf("add_tags keeps the asset's own spelling of a tag it carries: %v", tags)
	}
	if tags := assetTags2507(t, owner, b); strings.Join(tags, ",") != "finance,board" {
		t.Errorf("add_tags adds to an asset with none: %v", tags)
	}
}

func TestIssue2507_SelectionsPastTheLimitAndStringIdsAreRefused(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	ids := make([]any, 501)
	for i := range ids {
		ids[i] = fmt.Sprintf("acc2507-missing-%d", i)
	}
	if status, _, raw := bulk2507(t, owner, map[string]any{"action": "delete", "asset_ids": ids}); status != http.StatusBadRequest ||
		!strings.Contains(raw, "at most 500") {
		t.Errorf("501 ids are refused naming the limit: %d %s", status, raw)
	}
	if status, _, raw := bulk2507(t, owner, map[string]any{"action": "delete", "asset_ids": "one-id"}); status != http.StatusBadRequest {
		t.Errorf("asset_ids as a string is refused: %d %s", status, raw)
	}
	res, text, err := owner.callRaw("manage_asset", map[string]any{"action": "delete", "asset_ids": "one-id"})
	if err == nil && (res == nil || !res.IsError) {
		t.Errorf("manage_asset refuses asset_ids as a string: %s", text)
	}
	res, text, err = owner.callRaw("manage_asset", map[string]any{"action": "delete", "asset_ids": ids})
	if err == nil && (res == nil || !res.IsError) {
		t.Errorf("manage_asset refuses 501 ids: %s", text)
	}
}

func contains2507(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
