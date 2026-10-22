//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2109: a persona whose people have no use for managed scripts can have
// Automations taken out of their portal. An administrator sets it per persona
// through the admin persona API the portal editor saves with; the portal reads
// it from /api/v1/portal/me and the portal's script routes refuse the
// persona's members, while an administrator keeps every Automations page.
//
// Each criterion creates its own database persona and an API key whose only
// role maps to it, the way an operator reaches a persona.
//
// #2225 replaced hide_automations with the automations switch of the
// persona's portal block, so the setting is portal.automations, true shows.
//
// Wire forms: portal.automations is a JSON boolean on the admin persona body;
// the true and false forms are sent, and the string "true" is refused. A
// persona created without the key is the third form, and reads as true.

// issue2109Member is a persona and an API key reaching it, removed at cleanup.
type issue2109Member struct {
	persona string
	key     string
}

// newIssue2109Member creates a database persona, sending portal.automations
// only when shows is non-nil, and a key whose role maps to it.
func newIssue2109Member(t *testing.T, admin *client, shows any) issue2109Member {
	t.Helper()
	suffix := time.Now().UnixNano()
	name := fmt.Sprintf("acc-2109-%d", suffix)
	role := fmt.Sprintf("acc_2109_%d", suffix)
	body := map[string]any{
		"name": name, "display_name": "Acceptance 2109 member", "roles": []any{role},
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"},
	}
	if shows != nil {
		body["portal"] = map[string]any{"automations": shows}
	}
	status, out := admin.rest(http.MethodPost, "/api/v1/admin/personas", jsonBody(t, body))
	if status != http.StatusCreated {
		t.Fatalf("creating persona %s answered %d %v", name, status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+name, http.NoBody) })

	status, out = admin.rest(http.MethodPost, "/api/v1/admin/auth/keys", jsonBody(t, map[string]any{
		"name": name, "roles": []any{role}, "description": "Acceptance #2109 member",
	}))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("creating key %s answered %d %v", name, status, out)
	}
	key, _ := out["key"].(string)
	if key == "" {
		t.Fatalf("key %s was created without a secret: %v", name, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/auth/keys/"+name, http.NoBody) })
	return issue2109Member{persona: name, key: key}
}

// issue2109Stored reads the setting back the way the editor loads it.
func issue2109Stored(t *testing.T, admin *client, persona string) bool {
	t.Helper()
	status, out := admin.rest(http.MethodGet, "/api/v1/admin/personas/"+persona, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading persona %s answered %d %v", persona, status, out)
	}
	return issue2109Automations(t, "persona "+persona, out)
}

// issue2109Automations is the automations switch of a body's portal block.
func issue2109Automations(t *testing.T, what string, out map[string]any) bool {
	t.Helper()
	portal, _ := out["portal"].(map[string]any)
	shows, ok := portal["automations"].(bool)
	if !ok {
		t.Fatalf("%s carries no portal.automations boolean: %v", what, out)
	}
	return shows
}

// issue2109Save saves the persona with the setting, as the editor's Save does.
func issue2109Save(t *testing.T, admin *client, persona string, shows any) (int, map[string]any) {
	t.Helper()
	status, out := admin.rest(http.MethodGet, "/api/v1/admin/personas/"+persona, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading persona %s answered %d %v", persona, status, out)
	}
	return admin.rest(http.MethodPut, "/api/v1/admin/personas/"+persona, jsonBody(t, map[string]any{
		"display_name": out["display_name"], "roles": out["roles"],
		"allow_tools": out["allow_tools"], "deny_tools": out["deny_tools"],
		"allow_connections": out["allow_connections"], "portal": map[string]any{"automations": shows},
	}))
}

// issue2109Shows is what the portal tells the browser about Automations.
func issue2109Shows(t *testing.T, c *client) bool {
	t.Helper()
	status, me := c.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/portal/me answered %d %v", status, me)
	}
	return issue2109Automations(t, "/api/v1/portal/me", me)
}

// TestIssue2109_AnAdministratorSetsItThroughTheAdminAPI: the setting is read
// and changed through the admin persona API, a persona that never set it
// shows Automations, and a value that is not a boolean is refused.
func TestIssue2109_AnAdministratorSetsItThroughTheAdminAPI(t *testing.T) {
	admin := connect(t)
	existing := newIssue2109Member(t, admin, nil)
	if !issue2109Stored(t, admin, existing.persona) {
		t.Error("a persona created without portal.automations hides Automations; it must keep showing them")
	}
	if !issue2109Shows(t, connectAs(t, existing.key)) {
		t.Error("/api/v1/portal/me says Automations are hidden for a persona that never set it")
	}

	if status, out := issue2109Save(t, admin, existing.persona, false); status != http.StatusOK || issue2109Automations(t, "the saved persona", out) {
		t.Fatalf("saving portal.automations=false answered %d %v", status, out)
	}
	if issue2109Stored(t, admin, existing.persona) {
		t.Error("portal.automations=false did not read back as false")
	}
	if status, out := issue2109Save(t, admin, existing.persona, true); status != http.StatusOK || !issue2109Automations(t, "the saved persona", out) {
		t.Fatalf("saving portal.automations=true answered %d %v", status, out)
	}
	if !issue2109Stored(t, admin, existing.persona) {
		t.Error("portal.automations=true did not read back as true")
	}
	if status, out := issue2109Save(t, admin, existing.persona, "true"); status != http.StatusBadRequest {
		t.Errorf("portal.automations as the string \"true\" answered %d %v; want 400", status, out)
	}
}

// TestIssue2109_AHiddenPersonaCannotReachAutomations: a member of a persona
// with the switch off is told the portal shows no Automations, and the
// portal's script routes refuse them by URL.
func TestIssue2109_AHiddenPersonaCannotReachAutomations(t *testing.T) {
	admin := connect(t)
	member := newIssue2109Member(t, admin, false)
	c := connectAs(t, member.key)
	if issue2109Shows(t, c) {
		t.Fatal("/api/v1/portal/me says Automations show for a persona that hides them")
	}
	for _, path := range []string{"/api/v1/portal/scripts", "/api/v1/portal/scripts/runs"} {
		status, out := c.rest(http.MethodGet, path, http.NoBody)
		if status != http.StatusForbidden {
			t.Errorf("GET %s answered %d %v; want 403", path, status, out)
			continue
		}
		if out["area"] != "automations" || !strings.Contains(fmt.Sprint(out["detail"]), "does not include automations") {
			t.Errorf("GET %s refused with %v; want the reason named", path, out)
		}
	}

	// Turning it on gives the routes back without a new key.
	if status, out := issue2109Save(t, admin, member.persona, true); status != http.StatusOK {
		t.Fatalf("saving portal.automations=true answered %d %v", status, out)
	}
	if !issue2109Shows(t, c) {
		t.Error("/api/v1/portal/me still hides Automations after the switch was turned on")
	}
	if status, out := c.rest(http.MethodGet, "/api/v1/portal/scripts", http.NoBody); status != http.StatusOK {
		t.Errorf("GET /api/v1/portal/scripts answered %d %v after the setting was turned off", status, out)
	}
}

// TestIssue2109_AdministratorsKeepAutomations: an administrator is shown
// Automations and reaches the portal and admin script routes whatever any
// persona says.
func TestIssue2109_AdministratorsKeepAutomations(t *testing.T) {
	admin := connect(t)
	newIssue2109Member(t, admin, false)
	if !issue2109Shows(t, admin) {
		t.Error("/api/v1/portal/me hides Automations from an administrator")
	}
	for _, path := range []string{"/api/v1/portal/scripts", "/api/v1/admin/scripts"} {
		if status, out := admin.rest(http.MethodGet, path, http.NoBody); status != http.StatusOK {
			t.Errorf("an administrator's GET %s answered %d %v", path, status, out)
		}
	}
}
