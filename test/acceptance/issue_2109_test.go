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
// Wire forms: hide_automations is a JSON boolean on the admin persona body; the
// true and false forms are sent, and the string "true" is refused. A persona
// created without the key is the third form, and reads as false.

// issue2109Member is a persona and an API key reaching it, removed at cleanup.
type issue2109Member struct {
	persona string
	key     string
}

// newIssue2109Member creates a database persona, sending hide_automations only
// when hide is non-nil, and a key whose role maps to it.
func newIssue2109Member(t *testing.T, admin *client, hide any) issue2109Member {
	t.Helper()
	suffix := time.Now().UnixNano()
	name := fmt.Sprintf("acc-2109-%d", suffix)
	role := fmt.Sprintf("acc_2109_%d", suffix)
	body := map[string]any{
		"name": name, "display_name": "Acceptance 2109 member", "roles": []any{role},
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"},
	}
	if hide != nil {
		body["hide_automations"] = hide
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

// issue2109Hidden reads the setting back the way the editor loads it.
func issue2109Hidden(t *testing.T, admin *client, persona string) bool {
	t.Helper()
	status, out := admin.rest(http.MethodGet, "/api/v1/admin/personas/"+persona, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading persona %s answered %d %v", persona, status, out)
	}
	hidden, ok := out["hide_automations"].(bool)
	if !ok {
		t.Fatalf("persona %s carries no hide_automations boolean: %v", persona, out)
	}
	return hidden
}

// issue2109Save saves the persona with the setting, as the editor's Save does.
func issue2109Save(t *testing.T, admin *client, persona string, hide any) (int, map[string]any) {
	t.Helper()
	status, out := admin.rest(http.MethodGet, "/api/v1/admin/personas/"+persona, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading persona %s answered %d %v", persona, status, out)
	}
	return admin.rest(http.MethodPut, "/api/v1/admin/personas/"+persona, jsonBody(t, map[string]any{
		"display_name": out["display_name"], "roles": out["roles"],
		"allow_tools": out["allow_tools"], "deny_tools": out["deny_tools"],
		"allow_connections": out["allow_connections"], "hide_automations": hide,
	}))
}

// issue2109Shows is what the portal tells the browser about Automations.
func issue2109Shows(t *testing.T, c *client) bool {
	t.Helper()
	status, me := c.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/portal/me answered %d %v", status, me)
	}
	shows, ok := me["automations"].(bool)
	if !ok {
		t.Fatalf("/api/v1/portal/me carries no automations boolean: %v", me)
	}
	return shows
}

// TestIssue2109_AnAdministratorSetsItThroughTheAdminAPI: the setting is read
// and changed through the admin persona API, a persona that never set it
// shows Automations, and a value that is not a boolean is refused.
func TestIssue2109_AnAdministratorSetsItThroughTheAdminAPI(t *testing.T) {
	admin := connect(t)
	existing := newIssue2109Member(t, admin, nil)
	if issue2109Hidden(t, admin, existing.persona) {
		t.Error("a persona created without hide_automations hides Automations; it must keep showing them")
	}
	if !issue2109Shows(t, connectAs(t, existing.key)) {
		t.Error("/api/v1/portal/me says Automations are hidden for a persona that never set it")
	}

	if status, out := issue2109Save(t, admin, existing.persona, true); status != http.StatusOK || out["hide_automations"] != true {
		t.Fatalf("saving hide_automations=true answered %d %v", status, out)
	}
	if !issue2109Hidden(t, admin, existing.persona) {
		t.Error("hide_automations=true did not read back as true")
	}
	if status, out := issue2109Save(t, admin, existing.persona, false); status != http.StatusOK || out["hide_automations"] != false {
		t.Fatalf("saving hide_automations=false answered %d %v", status, out)
	}
	if issue2109Hidden(t, admin, existing.persona) {
		t.Error("hide_automations=false did not read back as false")
	}
	if status, out := issue2109Save(t, admin, existing.persona, "true"); status != http.StatusBadRequest {
		t.Errorf("hide_automations as the string \"true\" answered %d %v; want 400", status, out)
	}
}

// TestIssue2109_AHiddenPersonaCannotReachAutomations: a member of a persona
// with the setting on is told the portal shows no Automations, and the
// portal's script routes refuse them by URL.
func TestIssue2109_AHiddenPersonaCannotReachAutomations(t *testing.T) {
	admin := connect(t)
	member := newIssue2109Member(t, admin, true)
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
		if !strings.Contains(fmt.Sprint(out["detail"]), "automations are not available to your persona") {
			t.Errorf("GET %s refused with %v; want the reason named", path, out)
		}
	}

	// Turning it off gives the routes back without a new key.
	if status, out := issue2109Save(t, admin, member.persona, false); status != http.StatusOK {
		t.Fatalf("saving hide_automations=false answered %d %v", status, out)
	}
	if !issue2109Shows(t, c) {
		t.Error("/api/v1/portal/me still hides Automations after the setting was turned off")
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
	newIssue2109Member(t, admin, true)
	if !issue2109Shows(t, admin) {
		t.Error("/api/v1/portal/me hides Automations from an administrator")
	}
	for _, path := range []string{"/api/v1/portal/scripts", "/api/v1/admin/scripts"} {
		if status, out := admin.rest(http.MethodGet, path, http.NoBody); status != http.StatusOK {
			t.Errorf("an administrator's GET %s answered %d %v", path, status, out)
		}
	}
}
