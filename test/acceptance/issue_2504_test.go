//go:build integration

package acceptance

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// Issue #2504: a person gets back to what they were working on. Opening an
// asset or collection in the portal records it in their Recent list, newest
// first; starring one keeps it in their Favorites, in the sidebar and on Home,
// which is now the portal's landing page at /. Both are private to the person
// and grant nothing: an item they can no longer open drops out of both on the
// next read, and an item they cannot open cannot be starred or recorded. An
// agent lists the caller's recents and favorites with manage_asset.
//
// Wire forms: POST /api/v1/portal/recents takes a JSON object {kind, id}, both
// strings (the one form each admits); PUT and DELETE
// /api/v1/portal/favorites/{kind}/{id} take both in the path; manage_asset's
// `action` is a string and `limit` a number.

// open2504 records that c opened an asset, as the asset viewer reports it.
func open2504(t *testing.T, c *client, id string) int {
	t.Helper()
	status, _ := c.rest(http.MethodPost, "/api/v1/portal/recents", jsonBody(t, map[string]any{"kind": "asset", "id": id}))
	return status
}

// star2504 stars an asset as c.
func star2504(t *testing.T, c *client, id string) int {
	t.Helper()
	status, _ := c.rest(http.MethodPut, "/api/v1/portal/favorites/asset/"+id, nil)
	return status
}

// list2504 reads a library list (recents or favorites) as c: its raw body,
// to search for what must not be there, and the ids in order.
func list2504(t *testing.T, c *client, which string) (body string, ids []string) {
	t.Helper()
	status, raw := c.restText("/api/v1/portal/" + which + "?limit=200")
	if status != http.StatusOK {
		t.Fatalf("GET %s: %d %.300s", which, status, raw)
	}
	_, out := c.rest(http.MethodGet, "/api/v1/portal/"+which+"?limit=200", nil)
	items, _ := out["items"].([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		id, _ := m["id"].(string)
		ids = append(ids, id)
	}
	return raw, ids
}

func indexOf2504(ids []string, id string) int {
	for i, v := range ids {
		if v == id {
			return i
		}
	}
	return -1
}

func TestIssue2504_OpeningAssetsListsThemOnHomeNewestFirst(t *testing.T) {
	analyst, engineer := connectAs(t, analystKey), connectAs(t, engineerKey)
	stamp := stamp2502()
	names := []string{"acc2504 first " + stamp, "acc2504 second " + stamp, "acc2504 third " + stamp}
	ids := make([]string, len(names))
	for i, n := range names {
		ids[i] = saveAsset2502(t, analyst, n, "text/html", "<p>"+n+"</p>", nil)
	}
	for _, id := range ids {
		if status := open2504(t, analyst, id); status != http.StatusNoContent {
			t.Fatalf("recording an open of %s: %d", id, status)
		}
	}

	_, recents := list2504(t, analyst, "recents")
	third, second, first := indexOf2504(recents, ids[2]), indexOf2504(recents, ids[1]), indexOf2504(recents, ids[0])
	if third < 0 || second < 0 || first < 0 {
		t.Fatalf("Recent does not list the three assets just opened: %v", recents)
	}
	if third >= second || second >= first {
		t.Errorf("Recent is not newest first: positions third=%d second=%d first=%d", third, second, first)
	}

	if status := star2504(t, analyst, ids[1]); status != http.StatusNoContent {
		t.Fatalf("starring: %d", status)
	}
	t.Cleanup(func() { analyst.rest(http.MethodDelete, "/api/v1/portal/favorites/asset/"+ids[1], nil) })
	if _, favs := list2504(t, analyst, "favorites"); indexOf2504(favs, ids[1]) < 0 {
		t.Errorf("the starred asset is not under Favorites: %v", favs)
	}

	// Another person sees neither list: no id and no name of the analyst's.
	for _, which := range []string{"recents", "favorites"} {
		body, _ := list2504(t, engineer, which)
		for i := range ids {
			if strings.Contains(body, ids[i]) || strings.Contains(body, names[i]) {
				t.Errorf("another person's %s names the analyst's asset %s", which, names[i])
			}
		}
	}
}

func TestIssue2504_ARevokedShareLeavesFavoritesAndRecents(t *testing.T) {
	analyst, engineer := connectAs(t, analystKey), connectAs(t, engineerKey)
	name := "acc2504 shared " + stamp2502()
	id := saveAsset2502(t, analyst, name, "text/html", "<p>shared</p>", nil)
	share2502(t, analyst, id, engineerEmail, "viewer")

	if status := open2504(t, engineer, id); status != http.StatusNoContent {
		t.Fatalf("the recipient recording an open of the shared asset: %d", status)
	}
	if status := star2504(t, engineer, id); status != http.StatusNoContent {
		t.Fatalf("the recipient starring the shared asset: %d", status)
	}
	t.Cleanup(func() { engineer.rest(http.MethodDelete, "/api/v1/portal/favorites/asset/"+id, nil) })
	for _, which := range []string{"recents", "favorites"} {
		if _, got := list2504(t, engineer, which); indexOf2504(got, id) < 0 {
			t.Fatalf("the recipient's %s does not list the shared asset: %v", which, got)
		}
	}

	shares := analyst.call("manage_asset", map[string]any{"action": "list_shares", "asset_id": id})
	list, _ := shares["shares"].([]any)
	for _, s := range list {
		m, _ := s.(map[string]any)
		if sid, _ := m["share_id"].(string); sid != "" {
			analyst.call("manage_asset", map[string]any{"action": "revoke_share", "share_id": sid})
		}
	}

	for _, which := range []string{"recents", "favorites"} {
		body, _ := list2504(t, engineer, which)
		if strings.Contains(body, id) || strings.Contains(body, name) {
			t.Errorf("after the share was revoked the recipient's %s still names the asset: %.300s", which, body)
		}
	}
}

func TestIssue2504_AnItemTheCallerCannotOpenIsNeitherStarredNorRecorded(t *testing.T) {
	analyst, engineer := connectAs(t, analystKey), connectAs(t, engineerKey)
	name := "acc2504 private " + stamp2502()
	id := saveAsset2502(t, analyst, name, "text/html", "<p>private</p>", nil)

	if status := star2504(t, engineer, id); status != http.StatusNotFound {
		t.Errorf("starring an asset the caller cannot open answered %d, want 404", status)
	}
	if status := open2504(t, engineer, id); status != http.StatusNotFound {
		t.Errorf("recording an open of an asset the caller cannot open answered %d, want 404", status)
	}
	for _, which := range []string{"recents", "favorites"} {
		body, _ := list2504(t, engineer, which)
		if strings.Contains(body, id) || strings.Contains(body, name) {
			t.Errorf("the caller's %s names an asset they cannot open", which)
		}
	}
}

func TestIssue2504_TheAgentListsTheCallersRecentsAndFavorites(t *testing.T) {
	analyst, engineer := connectAs(t, analystKey), connectAs(t, engineerKey)
	stamp := stamp2502()
	older := saveAsset2502(t, analyst, "acc2504 agent older "+stamp, "text/html", "<p>older</p>", nil)
	newer := saveAsset2502(t, analyst, "acc2504 agent newer "+stamp, "text/html", "<p>newer</p>", nil)
	open2504(t, analyst, older)
	open2504(t, analyst, newer)
	star2504(t, analyst, older)
	t.Cleanup(func() { analyst.rest(http.MethodDelete, "/api/v1/portal/favorites/asset/"+older, nil) })

	ids := func(out map[string]any) []string {
		items, _ := out["items"].([]any)
		var got []string
		for _, it := range items {
			m, _ := it.(map[string]any)
			id, _ := m["id"].(string)
			got = append(got, id)
		}
		return got
	}
	recents := ids(analyst.call("manage_asset", map[string]any{"action": "list_recents", "limit": 50}))
	if len(recents) == 0 || recents[0] != newer {
		t.Errorf("list_recents does not lead with the asset opened last: %v", recents)
	}
	if indexOf2504(recents, older) < 0 {
		t.Errorf("list_recents leaves out an asset the caller opened: %v", recents)
	}
	if favs := ids(analyst.call("manage_asset", map[string]any{"action": "list_favorites"})); indexOf2504(favs, older) < 0 {
		t.Errorf("list_favorites leaves out the starred asset: %v", favs)
	}
	other := ids(engineer.call("manage_asset", map[string]any{"action": "list_recents", "limit": 200}))
	if indexOf2504(other, newer) >= 0 || indexOf2504(other, older) >= 0 {
		t.Errorf("another person's list_recents names the analyst's assets: %v", other)
	}
}

// Home is the portal root, and the asset and collection addresses that worked
// before still open their pages.
func TestIssue2504_HomeIsThePortalRootAndOldAddressesStillOpen(t *testing.T) {
	analyst := connectAs(t, analystKey)
	id := saveAsset2502(t, analyst, "acc2504 address "+stamp2502(), "text/html", "<p>address</p>", nil)
	for _, path := range []string{"/portal/", "/portal/assets", "/portal/assets/" + id, "/portal/trash"} {
		res, err := http.Get(baseURL() + path) //nolint:noctx,gosec // the dev stack's own address
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK || !strings.Contains(strings.ToLower(string(body)), "<!doctype html") {
			t.Errorf("GET %s did not serve the portal: %d %.120s", path, res.StatusCode, body)
		}
	}
}
