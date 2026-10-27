//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"testing"
	"time"
)

// Issue #2228: one persona resolution and one administrator rule on every
// surface. Each criterion creates its personas and a key through the admin
// API, then asks the admin API, the portal's /me, managed resources and the
// MCP tools who the key is.
//
// Wire forms: the persona body's priority is a JSON number and roles a list
// of strings; the key's roles a list of strings.

// issue2228Key creates a key holding roles, removed after.
func issue2228Key(t *testing.T, admin *client, roles ...string) *client {
	t.Helper()
	name := fmt.Sprintf("acc-2228-%d", time.Now().UnixNano())
	list := make([]any, 0, len(roles))
	for _, r := range roles {
		list = append(list, r)
	}
	status, out := admin.rest(http.MethodPost, "/api/v1/admin/auth/keys", jsonBody(t, map[string]any{
		"name": name, "roles": list, "description": "Acceptance #2228",
	}))
	key, _ := out["key"].(string)
	if (status != http.StatusCreated && status != http.StatusOK) || key == "" {
		t.Fatalf("creating key: %d %v", status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/auth/keys/"+name, http.NoBody) })
	return connectAs(t, key)
}

// issue2228Persona creates a database persona at a priority.
func issue2228Persona(t *testing.T, admin *client, name, role string, priority int) {
	t.Helper()
	status, out := admin.rest(http.MethodPost, "/api/v1/admin/personas", jsonBody(t, map[string]any{
		"name": name, "display_name": name, "roles": []any{role}, "priority": priority,
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"},
	}))
	if status != http.StatusCreated {
		t.Fatalf("creating persona %s: %d %v", name, status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+name, http.NoBody) })
}

// surfaces is what each surface says about one key. The admin API refuses a
// non-administrator with 403 (#2337).
type surfaces struct {
	persona      string
	portalAdmin  bool
	adminAPI     int
	globalUpload int
	mcpPersona   string
}

// askEverySurface asks the admin API, /me, resources and the MCP tools.
func askEverySurface(t *testing.T, c *client) surfaces {
	t.Helper()
	var s surfaces
	_, me := c.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)
	s.persona, _ = me["persona"].(string)
	s.portalAdmin, _ = me["is_admin"].(bool)
	s.adminAPI, _ = c.rest(http.MethodGet, "/api/v1/admin/personas", http.NoBody)
	s.globalUpload, _ = c.rest(http.MethodPost, "/api/v1/resources/folders", jsonBody(t, map[string]any{
		"scope": "global", "path": fmt.Sprintf("acc-2228-%d", time.Now().UnixNano()),
	}))
	s.mcpPersona = mcpPersona(t, c)
	return s
}

// mcpPersona is the persona the MCP path resolved: platform_info's persona, or
// the one its refusal names when the persona may not call it.
func mcpPersona(t *testing.T, c *client) string {
	t.Helper()
	res, text, err := c.callRaw("platform_info", map[string]any{})
	if err == nil && res != nil && !res.IsError {
		var info struct {
			Persona struct {
				Name string `json:"name"`
			} `json:"persona"`
		}
		if json.Unmarshal([]byte(text), &info) == nil {
			return info.Persona.Name
		}
	}
	if m := personaInRefusal.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	t.Fatalf("platform_info named no persona: %v %.300s", err, text)
	return ""
}

// personaInRefusal reads the persona a tool refusal names.
var personaInRefusal = regexp.MustCompile(`Persona "([^"]+)" is not permitted`)

// TestIssue2228_AHigherPriorityPersonaIsNotAdministration: a user whose roles
// match the admin persona and a higher-priority persona resolves to the
// higher one on every surface and is an administrator on none.
func TestIssue2228_AHigherPriorityPersonaIsNotAdministration(t *testing.T) {
	admin := connect(t)
	suffix := time.Now().UnixNano()
	high := fmt.Sprintf("acc-2228-high-%d", suffix)
	role := fmt.Sprintf("acc_2228_high_%d", suffix)
	issue2228Persona(t, admin, high, role, 1000)
	c := issue2228Key(t, admin, "admin", role)

	got := askEverySurface(t, c)
	if got.persona != high || got.mcpPersona != high {
		t.Errorf("resolved %q in the portal and %q over MCP, want %q on both", got.persona, got.mcpPersona, high)
	}
	if got.portalAdmin {
		t.Error("/me reports an administrator")
	}
	if got.adminAPI == http.StatusOK {
		t.Error("the admin API served a non-administrator")
	}
	if got.globalUpload == http.StatusCreated || got.globalUpload == http.StatusOK {
		t.Errorf("resources let a non-administrator create a global folder (%d)", got.globalUpload)
	}

	control := askEverySurface(t, issue2228Key(t, admin, "admin"))
	if !control.portalAdmin || control.adminAPI != http.StatusOK {
		t.Errorf("the admin role alone: /me admin %v, admin API %d", control.portalAdmin, control.adminAPI)
	}
}

// TestIssue2228_AMappedRoleResolvesTheSameEverywhere: a role named in
// oidc_to_persona (dev/platform.yaml maps dp_inventory_analyst to
// inventory-analyst, a persona that may call platform_info) selects its persona
// in the portal and the admin API as it does over MCP, ahead of the admin role
// the key also holds.
func TestIssue2228_AMappedRoleResolvesTheSameEverywhere(t *testing.T) {
	admin := connect(t)
	c := issue2228Key(t, admin, "dp_inventory_analyst", "admin")
	got := askEverySurface(t, c)
	if got.persona != "inventory-analyst" || got.mcpPersona != "inventory-analyst" {
		t.Errorf("resolved %q in the portal and %q over MCP, want inventory-analyst on both", got.persona, got.mcpPersona)
	}
	if got.portalAdmin || got.adminAPI == http.StatusOK {
		t.Errorf("a key mapped to inventory-analyst is an administrator: /me %v, admin API %d", got.portalAdmin, got.adminAPI)
	}
}

// TestIssue2228_EqualPrioritiesResolveTheSameOnEveryRequest: two personas of
// equal priority matching one user resolve to the same one every time.
func TestIssue2228_EqualPrioritiesResolveTheSameOnEveryRequest(t *testing.T) {
	admin := connect(t)
	suffix := time.Now().UnixNano()
	first := fmt.Sprintf("acc-2228-a-%d", suffix)
	second := fmt.Sprintf("acc-2228-b-%d", suffix)
	roleA, roleB := fmt.Sprintf("acc_2228_a_%d", suffix), fmt.Sprintf("acc_2228_b_%d", suffix)
	issue2228Persona(t, admin, second, roleB, 7)
	issue2228Persona(t, admin, first, roleA, 7)
	c := issue2228Key(t, admin, roleB, roleA)
	for i := range 15 {
		_, me := c.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)
		if me["persona"] != first {
			t.Fatalf("request %d resolved %v, want %s", i, me["persona"], first)
		}
	}
}
