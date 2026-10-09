//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
)

// Issue #2037: the E2E suite's admin server is now the one the platform
// mounts, and the first thing it found is that the mounted server answered 500
// for deleting a persona no row holds, where the suite's hand-built copy (no
// persona store) answered 404. These criteria are that route on the running
// platform.
//
// Wire forms: DELETE /api/v1/admin/personas/{name} takes no body; the name is
// a path segment. POST /api/v1/admin/personas takes one JSON object, sent with
// roles and allow_tools as lists.

func TestIssue2037_DeletingAPersonaNoRowHoldsIs404(t *testing.T) {
	admin := connect(t)
	name := "acc2037-absent-" + strings.ToLower(unique1579())
	status, body := admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+name, http.NoBody)
	if status != http.StatusNotFound {
		t.Fatalf("DELETE of a persona that does not exist answered %d %v; want 404", status, body)
	}
	if detail, _ := body["detail"].(string); detail != "persona not found" {
		t.Errorf("detail = %q; want %q", detail, "persona not found")
	}
}

func TestIssue2037_APersonaIsDeletedOnceThenNotFound(t *testing.T) {
	admin := connect(t)
	name := "acc2037-" + strings.ToLower(unique1579())
	created := admin.restJSON(http.MethodPost, "/api/v1/admin/personas", map[string]any{
		"name": name, "display_name": "Acceptance 2037", "roles": []string{"acc2037_role"},
		"allow_tools": []string{"platform_info"},
	})
	if created != http.StatusCreated && created != http.StatusOK {
		t.Fatalf("creating the persona answered %d", created)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+name, http.NoBody) })

	if status, body := admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+name, http.NoBody); status != http.StatusOK {
		t.Fatalf("the first DELETE answered %d %v; want 200", status, body)
	}
	if status, body := admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+name, http.NoBody); status != http.StatusNotFound {
		t.Fatalf("the second DELETE answered %d %v; want 404", status, body)
	}
}
