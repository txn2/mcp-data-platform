//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2509: an owner tidies their library. Trash lists what they deleted
// with the day each is purged, restores an item with its shares and collection
// memberships, or deletes it for good. Unused lists their assets nobody has
// opened in a period, counts only. Possible duplicates groups their
// near-identical assets; a dismissed group stays dismissed until one of its
// assets gets a new version. Each list is the caller's own (an administrator
// may ask for everyone's), and manage_asset lists the trash and restores.
//
// Wire forms: the portal routes take everyone=true and days, limit and offset
// as query strings; POST /api/v1/portal/duplicates/dismissals takes a JSON
// object {digest} with a string digest (the one form it admits); restore and
// purge take kind and id in the path; manage_asset's `action`, `asset_id` and
// `collection_id` are strings.

// dupWait2509 bounds the wait for two just-saved assets to be embedded and
// grouped; a nightly runner embeds on CPU behind whatever the queue holds.
const dupWait2509 = 3 * time.Minute

// trashItems2509 reads the caller's trash: each item's id mapped to its row,
// and the raw body.
func trashItems2509(t *testing.T, c *client, query string) (map[string]map[string]any, string) {
	t.Helper()
	status, raw := c.restText("/api/v1/portal/trash?limit=200" + query)
	if status != http.StatusOK {
		t.Fatalf("GET trash: %d %.300s", status, raw)
	}
	_, out := c.rest(http.MethodGet, "/api/v1/portal/trash?limit=200"+query, nil)
	items, _ := out["items"].([]any)
	byID := map[string]map[string]any{}
	for _, it := range items {
		m, _ := it.(map[string]any)
		id, _ := m["id"].(string)
		byID[id] = m
	}
	byID["_retention_days"] = map[string]any{"days": out["retention_days"]}
	return byID, raw
}

func deleteAsset2509(t *testing.T, c *client, id string) {
	t.Helper()
	c.call("manage_asset", map[string]any{"action": "delete", "asset_id": id})
}

func TestIssue2509_DeletingShowsTheAssetInTrashAndRestoringBringsItBack(t *testing.T) {
	analyst, engineer := connectAs(t, analystKey), connectAs(t, engineerKey)
	stamp := stamp2502()
	name := "acc2509 trash " + stamp
	id := saveAsset2502(t, analyst, name, "text/html", "<p>trash</p>", nil)
	share2502(t, analyst, id, engineerEmail, "viewer")
	coll := analyst.call("manage_asset", map[string]any{
		"action": "create_collection", "name": "acc2509 collection " + stamp,
		"sections": []any{map[string]any{"title": "Only", "items": []any{map[string]any{"asset_id": id}}}},
	})
	collID, _ := coll["collection_id"].(string)
	if collID == "" {
		t.Fatalf("create_collection returned no id: %v", coll)
	}
	t.Cleanup(func() {
		_, _, _ = analyst.callRaw("manage_asset", map[string]any{"action": "delete_collection", "collection_id": collID})
	})

	deleteAsset2509(t, analyst, id)
	trash, _ := trashItems2509(t, analyst, "")
	row := trash[id]
	if row == nil {
		t.Fatalf("the deleted asset is not in the trash")
	}
	days, _ := trash["_retention_days"]["days"].(float64)
	deleted, err1 := time.Parse(time.RFC3339Nano, fmt.Sprint(row["deleted_at"]))
	purge, err2 := time.Parse(time.RFC3339Nano, fmt.Sprint(row["purge_at"]))
	if err1 != nil || err2 != nil || days <= 0 {
		t.Fatalf("the trash row carries no deletion and purge date: %v (retention %v)", row, days)
	}
	if got := purge.Sub(deleted); got != time.Duration(days)*24*time.Hour {
		t.Errorf("the purge date is %v after deletion, want the %v-day retention", got, days)
	}

	// The recipient lost it with the delete, and does not see the owner's trash.
	if status, _ := portalAsset2502(t, engineer, id); status == http.StatusOK {
		t.Errorf("the recipient still opens the deleted asset")
	}
	if _, body := trashItems2509(t, engineer, ""); strings.Contains(body, id) || strings.Contains(body, name) {
		t.Errorf("another person's trash names the owner's deleted asset")
	}

	// manage_asset lists the trash and restores from it.
	listed := analyst.call("manage_asset", map[string]any{"action": "list_trash"})
	if !strings.Contains(fmt.Sprint(listed["items"]), id) {
		t.Errorf("list_trash does not list the deleted asset: %v", listed)
	}
	if res, text, _ := engineer.callRaw("manage_asset", map[string]any{"action": "restore", "asset_id": id}); res == nil || !res.IsError {
		t.Errorf("another person restored the owner's asset: %.200s", text)
	}
	analyst.call("manage_asset", map[string]any{"action": "restore", "asset_id": id})

	if status, _ := portalAsset2502(t, analyst, id); status != http.StatusOK {
		t.Fatalf("the restored asset does not open for its owner: %d", status)
	}
	if status, _ := portalAsset2502(t, engineer, id); status != http.StatusOK {
		t.Errorf("the restored asset's share did not come back: the recipient gets %d", status)
	}
	shares := analyst.call("manage_asset", map[string]any{"action": "list_shares", "asset_id": id})
	if !strings.Contains(fmt.Sprint(shares["shares"]), engineerEmail) {
		t.Errorf("the restored asset lists no share with the recipient: %v", shares)
	}
	got := analyst.call("manage_asset", map[string]any{"action": "get_collection", "collection_id": collID})
	if !strings.Contains(fmt.Sprint(got), id) {
		t.Errorf("the restored asset is not back in its collection: %v", got)
	}
	if trash, _ := trashItems2509(t, analyst, ""); trash[id] != nil {
		t.Errorf("the restored asset is still in the trash")
	}
}

func TestIssue2509_ACollectionRestoresAndAnAssetDeletesForGood(t *testing.T) {
	analyst, engineer := connectAs(t, analystKey), connectAs(t, engineerKey)
	stamp := stamp2502()

	coll := analyst.call("manage_asset", map[string]any{"action": "create_collection", "name": "acc2509 restore me " + stamp})
	collID, _ := coll["collection_id"].(string)
	analyst.call("manage_asset", map[string]any{"action": "delete_collection", "collection_id": collID})
	t.Cleanup(func() {
		_, _, _ = analyst.callRaw("manage_asset", map[string]any{"action": "delete_collection", "collection_id": collID})
	})
	if status, out := engineer.rest(http.MethodPost, "/api/v1/portal/trash/collection/"+collID+"/restore", nil); status != http.StatusNotFound {
		t.Errorf("another person restoring the owner's collection answered %d %v, want 404", status, out)
	}
	if status, out := analyst.rest(http.MethodPost, "/api/v1/portal/trash/collection/"+collID+"/restore", nil); status != http.StatusOK {
		t.Fatalf("restoring the collection: %d %v", status, out)
	}
	if res, text, _ := analyst.callRaw("manage_asset", map[string]any{"action": "get_collection", "collection_id": collID}); res == nil || res.IsError {
		t.Errorf("the restored collection does not open: %.200s", text)
	}

	name := "acc2509 purge " + stamp
	id := saveAsset2502(t, analyst, name, "text/html", "<p>purge</p>", nil)
	deleteAsset2509(t, analyst, id)
	if status, _ := engineer.rest(http.MethodDelete, "/api/v1/portal/trash/asset/"+id, nil); status != http.StatusNotFound {
		t.Errorf("another person deleting the owner's asset for good answered %d, want 404", status)
	}
	if status, out := analyst.rest(http.MethodDelete, "/api/v1/portal/trash/asset/"+id, nil); status != http.StatusOK {
		t.Fatalf("deleting for good: %d %v", status, out)
	}
	if trash, _ := trashItems2509(t, analyst, ""); trash[id] != nil {
		t.Errorf("an asset deleted for good is still in the trash")
	}
	if status, out := analyst.rest(http.MethodPost, "/api/v1/portal/trash/asset/"+id+"/restore", nil); status != http.StatusNotFound {
		t.Errorf("an asset deleted for good can still be restored: %d %v", status, out)
	}
}

// Everyone's trash, unused and duplicates are an administrator's alone.
func TestIssue2509_EveryonesListsAreAnAdministratorsAlone(t *testing.T) {
	admin, analyst := connect(t), connectAs(t, analystKey)
	for _, path := range []string{"/api/v1/portal/trash", "/api/v1/portal/unused", "/api/v1/portal/duplicates"} {
		if status, _ := analyst.rest(http.MethodGet, path+"?everyone=true", nil); status != http.StatusForbidden {
			t.Errorf("a non-administrator asking for everyone's %s got %d, want 403", path, status)
		}
	}
	name := "acc2509 everyone " + stamp2502()
	id := saveAsset2502(t, analyst, name, "text/html", "<p>everyone</p>", nil)
	deleteAsset2509(t, analyst, id)
	if trash, _ := trashItems2509(t, admin, "&everyone=true"); trash[id] == nil {
		t.Errorf("an administrator's everyone's trash leaves out the analyst's deleted asset")
	}
}

// unusedHas2509 reports whether the caller's Unused list for days holds id,
// paging through it.
func unusedHas2509(t *testing.T, c *client, days int, id string) (found bool, raw string) {
	t.Helper()
	for offset := 0; ; offset += 200 {
		path := fmt.Sprintf("/api/v1/portal/unused?days=%d&limit=200&offset=%d", days, offset)
		status, body := c.restText(path)
		if status != http.StatusOK {
			t.Fatalf("GET %s: %d %.300s", path, status, body)
		}
		_, out := c.rest(http.MethodGet, path, nil)
		items, _ := out["items"].([]any)
		for _, it := range items {
			m, _ := it.(map[string]any)
			if m["id"] == id {
				return true, body
			}
		}
		total, _ := out["total"].(float64)
		if len(items) == 0 || float64(offset+len(items)) >= total {
			return false, body
		}
	}
}

func TestIssue2509_AnUnopenedAssetIsUnusedUntilItIsOpened(t *testing.T) {
	analyst, engineer := connectAs(t, analystKey), connectAs(t, engineerKey)
	name := "acc2509 unused " + stamp2502()
	id := saveAsset2502(t, analyst, name, "text/html", "<p>unused</p>", nil)

	found, body := unusedHas2509(t, analyst, 30, id)
	if !found {
		t.Fatalf("an asset nobody opened is not in Unused")
	}
	if strings.Contains(body, engineerEmail) {
		t.Errorf("Unused names who opened an asset: %.300s", body)
	}
	if found, _ := unusedHas2509(t, engineer, 30, id); found {
		t.Errorf("another person's Unused lists the analyst's asset")
	}
	if status := open2504(t, analyst, id); status != http.StatusNoContent {
		t.Fatalf("recording the open: %d", status)
	}
	if found, _ := unusedHas2509(t, analyst, 30, id); found {
		t.Errorf("an asset opened just now is still in Unused")
	}
}

// group2509 finds the possible-duplicates group holding both ids in the
// caller's list, returning its digest.
func group2509(t *testing.T, c *client, a, b string) string {
	t.Helper()
	_, out := c.rest(http.MethodGet, "/api/v1/portal/duplicates", nil)
	groups, _ := out["groups"].([]any)
	for _, g := range groups {
		m, _ := g.(map[string]any)
		members := fmt.Sprint(m["members"])
		if strings.Contains(members, a) && strings.Contains(members, b) {
			digest, _ := m["digest"].(string)
			return digest
		}
	}
	return ""
}

func TestIssue2509_NearIdenticalAssetsGroupUntilDismissedAndComeBackWhenOneChanges(t *testing.T) {
	analyst, engineer := connectAs(t, analystKey), connectAs(t, engineerKey)
	stamp := stamp2502()
	extra := map[string]any{
		"description": "Quarterly revenue by region with the regional breakdown table, " + stamp,
		"tags":        []any{"acc2509", "revenue"},
	}
	first := saveAsset2502(t, analyst, "acc2509 revenue by region "+stamp, "text/html", "<p>one</p>", extra)
	second := saveAsset2502(t, analyst, "acc2509 revenue by region "+stamp+" copy", "text/html", "<p>two</p>", extra)

	var digest string
	for deadline := time.Now().Add(dupWait2509); time.Now().Before(deadline); time.Sleep(3 * time.Second) {
		if digest = group2509(t, analyst, first, second); digest != "" {
			break
		}
	}
	if digest == "" {
		t.Fatalf("two near-identical assets were not grouped within %v", dupWait2509)
	}
	if status, body := engineer.restText("/api/v1/portal/duplicates"); status != http.StatusOK || strings.Contains(body, first) {
		t.Errorf("another person's Possible duplicates names the analyst's assets: %d", status)
	}

	if status, out := analyst.rest(http.MethodPost, "/api/v1/portal/duplicates/dismissals",
		jsonBody(t, map[string]any{"digest": digest})); status != http.StatusNoContent {
		t.Fatalf("dismissing the group: %d %v", status, out)
	}
	if group2509(t, analyst, first, second) != "" {
		t.Fatalf("a dismissed group is still listed")
	}

	analyst.call("manage_asset", map[string]any{"action": "update", "asset_id": second, "content": "<p>two, revised</p>"})
	var back string
	for deadline := time.Now().Add(dupWait2509); time.Now().Before(deadline); time.Sleep(3 * time.Second) {
		if back = group2509(t, analyst, first, second); back != "" {
			break
		}
	}
	if back == "" {
		t.Fatalf("the dismissed group did not come back when one of its assets got a new version")
	}
	if back == digest {
		t.Errorf("the group came back under the digest it was dismissed with")
	}
}
