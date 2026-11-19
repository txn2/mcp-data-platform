//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
)

// Issue #2724: an external person's Home lists what they opened and starred.
// The recents and favorites routes answer them, list only what is shared with
// them, and the clean-up lists an owner reads stay refused.
//
// Wire forms: POST /api/v1/portal/recents takes a JSON object {kind, id},
// both strings, the one form it admits; PUT /api/v1/portal/favorites/{kind}/{id}
// takes its values in the path and no body; the GETs take none.

// ids2724 is the item ids a library list answered with.
func ids2724(t *testing.T, body map[string]any) string {
	t.Helper()
	items, _ := body["items"].([]any)
	var out []string
	for _, it := range items {
		m, _ := it.(map[string]any)
		if id, _ := m["id"].(string); id != "" {
			out = append(out, id)
		}
	}
	return strings.Join(out, ",")
}

// TestIssue2724_AnExternalPersonsHomeListsWhatTheyOpenedAndStarred: an asset
// shared with an external person's persona is recorded as opened and starred
// by them and listed back; an asset not shared with them is refused and never
// listed; trash, unused and duplicates stay refused.
func TestIssue2724_AnExternalPersonsHomeListsWhatTheyOpenedAndStarred(t *testing.T) {
	admin := connect(t)
	p := newPersona2290(t, admin, "home-2724", map[string]any{"external": true})
	person := newPerson2290(t, admin, p, "home")
	shared := issue2290Asset(t, admin, "shared-2724")
	hidden := issue2290Asset(t, admin, "hidden-2724")
	issue2290Share(t, admin, shared, map[string]any{"shared_with_persona": p.name, "permission": "viewer"})

	if status, body := person.do(t, http.MethodPost, "/api/v1/portal/recents", map[string]any{"kind": "asset", "id": shared}); status != http.StatusNoContent {
		t.Fatalf("an external person's open of a shared asset answered %d %v; want 204", status, body)
	}
	if status, body := person.do(t, http.MethodPut, "/api/v1/portal/favorites/asset/"+shared, nil); status != http.StatusNoContent {
		t.Fatalf("an external person's star of a shared asset answered %d %v; want 204", status, body)
	}
	if status, _ := person.do(t, http.MethodPost, "/api/v1/portal/recents", map[string]any{"kind": "asset", "id": hidden}); status != http.StatusNotFound {
		t.Errorf("recording an open of an asset not shared with them answered %d; want 404", status)
	}
	if status, _ := person.do(t, http.MethodPut, "/api/v1/portal/favorites/asset/"+hidden, nil); status != http.StatusNotFound {
		t.Errorf("starring an asset not shared with them answered %d; want 404", status)
	}

	for _, path := range []string{"/api/v1/portal/recents?limit=20", "/api/v1/portal/favorites?limit=20"} {
		status, body := person.do(t, http.MethodGet, path, nil)
		if status != http.StatusOK {
			t.Fatalf("GET %s as an external person answered %d %v; want 200", path, status, body)
		}
		got := ids2724(t, body)
		if !strings.Contains(got, shared) {
			t.Errorf("GET %s lists %q; want the shared asset %s", path, got, shared)
		}
		if strings.Contains(got, hidden) {
			t.Errorf("GET %s lists %s, an asset not shared with them", path, hidden)
		}
	}

	for _, path := range []string{"/api/v1/portal/trash", "/api/v1/portal/unused", "/api/v1/portal/duplicates"} {
		if status, _ := person.do(t, http.MethodGet, path, nil); status != http.StatusForbidden {
			t.Errorf("GET %s as an external person answered %d; want 403", path, status)
		}
	}
}
