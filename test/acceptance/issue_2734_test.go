//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2734: Home's Recent and Favorites name whose an item is and
// when it last changed. The recents and favorites reads return each item's
// owner and last change time; a change to the item moves its time; and an
// item shared with the reader names its owner, not the reader.
//
// Wire forms: POST /api/v1/portal/recents takes a JSON object {kind, id}, both
// strings; PUT /api/v1/portal/favorites/{kind}/{id} takes both in the path;
// GET /api/v1/portal/recents and /favorites take no body; manage_asset's
// `action`, `asset_id` and `content` are strings.

// item2734 is the library entry for id in c's list which, or nil.
func item2734(t *testing.T, c *client, which, id string) map[string]any {
	t.Helper()
	status, out := c.rest(http.MethodGet, "/api/v1/portal/"+which+"?limit=200", nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: %d %v", which, status, out)
	}
	items, _ := out["items"].([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m["id"] == id {
			return m
		}
	}
	return nil
}

func changed2734(t *testing.T, item map[string]any) time.Time {
	t.Helper()
	s, _ := item["updated_at"].(string)
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("updated_at %q is not a time: %v (item %v)", s, err, item)
	}
	return at
}

// TestIssue2734_RecentsAndFavoritesNameTheOwnerAndTheLastChange: a shared
// asset the reader opened and starred names its owner and when it changed, in
// both lists, and changing it moves that time on the next read.
func TestIssue2734_RecentsAndFavoritesNameTheOwnerAndTheLastChange(t *testing.T) {
	analyst, engineer := connectAs(t, analystKey), connectAs(t, engineerKey)
	name := "acc2734 shared " + stamp2502()
	id := saveAsset2502(t, analyst, name, "text/markdown", "# v1\n", nil)
	share2502(t, analyst, id, engineerEmail, "viewer")
	if status := open2504(t, engineer, id); status != http.StatusNoContent {
		t.Fatalf("recording an open: %d", status)
	}
	if status := star2504(t, engineer, id); status != http.StatusNoContent {
		t.Fatalf("starring: %d", status)
	}
	t.Cleanup(func() { engineer.rest(http.MethodDelete, "/api/v1/portal/favorites/asset/"+id, nil) })

	var before time.Time
	for _, which := range []string{"recents", "favorites"} {
		it := item2734(t, engineer, which, id)
		if it == nil {
			t.Fatalf("%s does not list the shared asset", which)
		}
		if owner, _ := it["owner_email"].(string); !strings.EqualFold(owner, analystEmail) {
			t.Errorf("%s names the owner %q; want %s, who shared it", which, owner, analystEmail)
		}
		before = changed2734(t, it)
	}

	analyst.call("manage_asset", map[string]any{"action": "update", "asset_id": id, "content": "# v2\n"})
	it := item2734(t, engineer, "recents", id)
	if it == nil {
		t.Fatal("recents lost the asset after it changed")
	}
	if after := changed2734(t, it); !after.After(before) {
		t.Errorf("updated_at stayed %s after the asset changed (was %s)", after, before)
	}
}
