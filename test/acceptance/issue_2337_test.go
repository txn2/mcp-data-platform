//go:build integration

package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Acceptance for #2337: a person signed in through the identity provider
// whose persona holds apply_knowledge but is not the administrator's is shown
// the portal's Changesets tab. The tab's routes now answer them, and the
// admin API refuses them 403 rather than 401, which the portal read as an
// expired session and sent the browser round the sign-in forever.
//
// Wire forms: every route is a GET with no body except the rollback, a POST
// with no body; apply_knowledge takes string action, sink and confirm as a
// boolean with page as an object, as literal tools/call params; the API key
// create body is literal JSON bytes (name and expires_in strings, roles an
// array of strings).
//
// The criteria run against the local stack: the platform and Keycloak.

const (
	issue2337Password = "Acceptance-2337-pass"
	// issue2337Role is the realm role the collaborator persona names for an
	// identity-provider sign-in.
	issue2337Role = "dp_collaborator"
)

// issue2337Collaborator is a person signed in through Keycloak whose persona
// is collaborator, which allows every tool and is not the administrator's.
func issue2337Collaborator(t *testing.T, admin *client) externalSession {
	t.Helper()
	token := kcAdminToken2061(t)
	if status, _ := kcAdmin2061(t, token, http.MethodGet, "/roles/"+issue2337Role, nil); status == http.StatusNotFound {
		if status, out := kcAdmin2061(t, token, http.MethodPost, "/roles", map[string]any{"name": issue2337Role}); status != http.StatusCreated {
			t.Fatalf("creating the Keycloak role %s: %d %s", issue2337Role, status, out)
		}
		t.Cleanup(func() { kcAdmin2061(t, kcAdminToken2061(t), http.MethodDelete, "/roles/"+issue2337Role, nil) })
	}
	email := issue2337Person(t, token)
	s := externalSession{hc: browserSignIn(t, baseURL(), email, issue2337Password), email: email}
	_, me := s.do(t, http.MethodGet, "/api/v1/portal/me", nil)
	s.csrf, _ = me["csrf_token"].(string)
	if me["persona"] != "collaborator" {
		t.Fatalf("%s signed in as %v, want the collaborator persona", email, me["persona"])
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/users/"+email, http.NoBody) })
	return s
}

// issue2337Person creates a Keycloak user holding issue2337Role.
func issue2337Person(t *testing.T, token string) string {
	t.Helper()
	email := fmt.Sprintf("acc-2337-%d@example.com", time.Now().UnixNano())
	if status, out := kcAdmin2061(t, token, http.MethodPost, "/users", map[string]any{
		"username": email, "email": email, "enabled": true, "emailVerified": true,
		"firstName": "Acceptance", "lastName": "Reviewer",
		"credentials": []any{map[string]any{"type": "password", "value": issue2337Password, "temporary": false}},
	}); status != http.StatusCreated {
		t.Fatalf("creating the Keycloak user %s: %d %s", email, status, out)
	}
	id := kcUserID2337(t, token, email)
	t.Cleanup(func() { kcAdmin2061(t, kcAdminToken2061(t), http.MethodDelete, "/users/"+id, nil) })
	_, roleRaw := kcAdmin2061(t, token, http.MethodGet, "/roles/"+issue2337Role, nil)
	var rep map[string]any
	if err := json.Unmarshal(roleRaw, &rep); err != nil {
		t.Fatalf("reading the Keycloak role: %s", roleRaw)
	}
	if status, out := kcAdmin2061(t, token, http.MethodPost, "/users/"+id+"/role-mappings/realm", []any{rep}); status != http.StatusNoContent {
		t.Fatalf("giving %s the role: %d %s", email, status, out)
	}
	return email
}

func kcUserID2337(t *testing.T, token, email string) string {
	t.Helper()
	_, out := kcAdmin2061(t, token, http.MethodGet, "/users?exact=true&username="+email, nil)
	var users []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &users); err != nil || len(users) != 1 {
		t.Fatalf("finding the Keycloak user %s: %s", email, out)
	}
	return users[0].ID
}

// issue2337Key mints an API key holding roles, deleted when the test ends.
func issue2337Key(t *testing.T, admin *client, label, rolesJSON string) string {
	t.Helper()
	name := fmt.Sprintf("acc-2337-%s-%d", label, time.Now().UnixNano())
	status, out := admin.rest(http.MethodPost, "/api/v1/admin/auth/keys",
		bytes.NewReader([]byte(`{"name":"`+name+`","roles":`+rolesJSON+`,"expires_in":"1h"}`)))
	if status != http.StatusCreated {
		t.Fatalf("creating key %s: %d %v", name, status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/auth/keys/"+name, nil) })
	key, _ := out["key"].(string)
	if key == "" {
		t.Fatalf("the key %s came back with no secret: %v", name, out)
	}
	return key
}

// TestIssue2337_ASignedInNonAdminIsRefusedNotUnauthenticated: the admin API
// answers a valid session that is not the administrator's 403, and a valid
// key the same; only a request with no credentials is 401.
func TestIssue2337_ASignedInNonAdminIsRefusedNotUnauthenticated(t *testing.T) {
	admin := connect(t)
	reviewer := issue2337Collaborator(t, admin)
	if status, out := reviewer.do(t, http.MethodGet, "/api/v1/admin/knowledge/changesets", nil); status != http.StatusForbidden {
		t.Fatalf("a signed-in collaborator on the admin changesets: %d %v, want 403", status, out)
	}
	key := issue2337Key(t, admin, "engineer", `["data_engineer"]`)
	if got := endpointRequest(t, baseURL(), http.MethodGet, "/api/v1/admin/knowledge/changesets", bearer(key), ""); got.status != http.StatusForbidden {
		t.Fatalf("a data engineer's key on the admin changesets: %d %s, want 403", got.status, got.body)
	}
	if got := endpointRequest(t, baseURL(), http.MethodGet, "/api/v1/admin/knowledge/changesets", nil, ""); got.status != http.StatusUnauthorized {
		t.Fatalf("no credentials on the admin changesets: %d %s, want 401", got.status, got.body)
	}
}

// TestIssue2337_AnApplyKnowledgeHolderReadsAndRollsBackChangesets: the routes
// the Changesets tab reads answer the person it is shown to -- the list, one
// changeset, and its rollback, recorded as theirs -- and refuse a persona
// without apply_knowledge.
func TestIssue2337_AnApplyKnowledgeHolderReadsAndRollsBackChangesets(t *testing.T) {
	admin := connect(t)
	reviewer := issue2337Collaborator(t, admin)

	slug := issue1696Slug("acc-2337")
	out := admin.call("apply_knowledge", map[string]any{
		"action": "apply", "sink": "knowledge_page", "confirm": true,
		"page": map[string]any{
			"slug": slug, "title": "Acceptance 2337 page", "summary": "Written by the #2337 acceptance run.",
			"body": "A reviewer rolls this page's promotion back from the portal.", "force_new": true,
		},
	})
	csID, _ := out["changeset_id"].(string)
	if csID == "" {
		t.Fatalf("the promotion recorded no changeset: %v", out)
	}

	status, list := reviewer.do(t, http.MethodGet, "/api/v1/portal/knowledge/changesets?per_page=100", nil)
	if status != http.StatusOK {
		t.Fatalf("the collaborator's portal changesets: %d %v", status, list)
	}
	if !issue2337Lists(list, csID) {
		t.Fatalf("the portal list does not hold changeset %s: %v", csID, list)
	}
	if status, one := reviewer.do(t, http.MethodGet, "/api/v1/portal/knowledge/changesets/"+csID, nil); status != http.StatusOK || one["id"] != csID {
		t.Fatalf("reading changeset %s: %d %v", csID, status, one)
	}
	status, rolled := reviewer.do(t, http.MethodPost, "/api/v1/portal/knowledge/changesets/"+csID+"/rollback", nil)
	if status != http.StatusOK {
		t.Fatalf("rolling back %s from the portal: %d %v", csID, status, rolled)
	}
	if status, one := reviewer.do(t, http.MethodGet, "/api/v1/portal/knowledge/changesets/"+csID, nil); status != http.StatusOK ||
		one["rolled_back"] != true || one["rolled_back_by"] == "" || one["rolled_back_by"] == nil {
		t.Fatalf("changeset %s after the portal rollback: %d %v", csID, status, one)
	}

	key := issue2337Key(t, admin, "engineer", `["data_engineer"]`)
	if got := endpointRequest(t, baseURL(), http.MethodGet, "/api/v1/portal/knowledge/changesets", bearer(key), ""); got.status != http.StatusForbidden {
		t.Fatalf("a data engineer, who holds no apply_knowledge, on the portal changesets: %d %s, want 403", got.status, got.body)
	}
}

// issue2337Lists reports whether a changeset page holds id.
func issue2337Lists(page map[string]any, id string) bool {
	data, _ := page["data"].([]any)
	for _, item := range data {
		if m, _ := item.(map[string]any); m["id"] == id {
			return true
		}
	}
	return false
}

// TestIssue2337_TheChangesetsTabOpensForItsAudience: in a browser, the
// collaborator signs in and opens /portal/knowledge#changesets; the tab shows
// the changesets and the browser stays on the page instead of being sent to
// sign in again.
func TestIssue2337_TheChangesetsTabOpensForItsAudience(t *testing.T) {
	admin := connect(t)
	_ = issue2337Collaborator(t, admin) // the realm role exists for the browser's person
	email := issue2337Person(t, kcAdminToken2061(t))
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/users/"+email, http.NoBody) })
	formsBrowser(t, "changesets", "FORMS_SIGN_IN_EMAIL="+email, "FORMS_SIGN_IN_PASSWORD="+issue2337Password)
}
