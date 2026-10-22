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

// Issue #2089: sharing an asset with a persona. Every current member of the
// persona opens it and nobody else; a person who joins the persona afterwards
// opens it, and one who leaves no longer does; the share appears once in the
// asset's share list, naming the persona.
//
// Wire forms: the share body's shared_with_persona as a string, with
// permission as the string "viewer"; the admin persona body's roles as a JSON
// array of strings and priority as a number; the emailed link's token in the
// /portal/view path; manage_asset's action and asset_id as strings.

// issue2089Key is an API key whose one role is role.
func issue2089Key(t *testing.T, admin *client, name, role string) *client {
	t.Helper()
	status, out := admin.rest(http.MethodPost, "/api/v1/admin/auth/keys", jsonBody(t, map[string]any{
		"name": name, "roles": []any{role}, "description": "Acceptance 2089",
	}))
	key, _ := out["key"].(string)
	if (status != http.StatusCreated && status != http.StatusOK) || key == "" {
		t.Fatalf("creating key %s: %d %v", name, status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/auth/keys/"+name, http.NoBody) })
	return connectAs(t, key)
}

func TestIssue2089_APersonaShareFollowsMembership(t *testing.T) {
	admin := connect(t)
	stamp := time.Now().UnixNano()
	persona := fmt.Sprintf("acc-2089-%d", stamp)
	member, later := persona+"_member", persona+"_later"
	save := func(name string, priority int, roles ...string) {
		t.Helper()
		body := map[string]any{
			"name": name, "display_name": "Acceptance 2089", "roles": roles, "priority": priority,
			"allow_tools": []any{"*"}, "allow_connections": []any{"*"},
		}
		method, path := http.MethodPut, "/api/v1/admin/personas/"+name
		if status, _ := admin.rest(http.MethodGet, path, http.NoBody); status == http.StatusNotFound {
			method, path = http.MethodPost, "/api/v1/admin/personas"
		}
		if status, out := admin.rest(method, path, jsonBody(t, body)); status != http.StatusOK && status != http.StatusCreated {
			t.Fatalf("saving persona %s with roles %v: %d %v", name, roles, status, out)
		}
	}
	// The shared persona outranks the one the later person starts in, so
	// adding their role to it moves them over.
	putPersona := func(roles ...string) { t.Helper(); save(persona, 10, roles...) }
	elsewhere := persona + "-elsewhere"
	save(elsewhere, 0, later)
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+elsewhere, http.NoBody) })
	putPersona(member)
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+persona, http.NoBody) })

	saved := admin.call("save_asset", map[string]any{
		"name": fmt.Sprintf("acc-2089 %d", stamp), "content_type": "text/html",
		"content": "<p>acc-2089</p>", "description": "Acceptance #2089.",
	})
	assetID, _ := saved["asset_id"].(string)
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID})
	})

	status, share := admin.rest(http.MethodPost, "/api/v1/portal/assets/"+assetID+"/shares", jsonBody(t, map[string]any{
		"shared_with_persona": persona, "permission": "viewer",
	}))
	if status != http.StatusCreated {
		t.Fatalf("sharing with the persona: %d %v", status, share)
	}

	in := issue2089Key(t, admin, persona+"-in", member)
	if status, out := in.rest(http.MethodGet, "/api/v1/portal/assets/"+assetID, http.NoBody); status != http.StatusOK {
		t.Errorf("a member opening the asset: %d %v", status, out)
	}
	_, mine := in.rest(http.MethodGet, "/api/v1/portal/shared-with-me", http.NoBody)
	raw, _ := json.Marshal(mine)
	if !strings.Contains(string(raw), assetID) || !strings.Contains(string(raw), `"via_persona":"`+persona+`"`) {
		t.Errorf("a member's shared-with-me does not list the asset through the persona: %s", raw)
	}
	outsider := connectAs(t, devPeerAPIKey)
	if status, _ := outsider.rest(http.MethodGet, "/api/v1/portal/assets/"+assetID, http.NoBody); status == http.StatusOK {
		t.Error("someone outside the persona opened the asset")
	}

	// The link the members are emailed opens for a member and not for anyone
	// else signed in.
	created, _ := share["share"].(map[string]any)
	token, _ := created["token"].(string)
	if token == "" {
		t.Fatalf("the persona share carries no link token: %v", share)
	}
	if status, _, body := in.restGet("/portal/view/"+token, nil); status != http.StatusOK {
		t.Errorf("a member following the emailed link: %d %.200s", status, body)
	}
	if status, _, _ := outsider.restGet("/portal/view/"+token, nil); status == http.StatusOK {
		t.Error("someone outside the persona opened the emailed link")
	}

	// A member's agent reads it through the asset tools; another persona's
	// agent does not.
	if _, text, err := in.callRaw("manage_asset", map[string]any{"action": "get", "asset_id": assetID}); err != nil || strings.Contains(text, "not found") || strings.Contains(text, "denied") {
		t.Errorf("a member's agent reading the asset: %v %.200s", err, text)
	}
	if res, text, err := outsider.callRaw("manage_asset", map[string]any{"action": "get", "asset_id": assetID}); err == nil && (res == nil || !res.IsError) {
		t.Errorf("another persona's agent read the asset: %.300s", text)
	}

	// A person who joins the persona after the share opens it.
	joined := issue2089Key(t, admin, persona+"-later", later)
	if status, _ := joined.rest(http.MethodGet, "/api/v1/portal/assets/"+assetID, http.NoBody); status == http.StatusOK {
		t.Fatal("a person not yet in the persona opened the asset")
	}
	putPersona(member, later)
	issue2089Eventually(t, "the joined person opens the asset", func() bool {
		status, _ := joined.rest(http.MethodGet, "/api/v1/portal/assets/"+assetID, http.NoBody)
		return status == http.StatusOK
	})

	// A person who leaves no longer does.
	putPersona(later)
	issue2089Eventually(t, "the person who left is refused", func() bool {
		status, _ := in.rest(http.MethodGet, "/api/v1/portal/assets/"+assetID, http.NoBody)
		return status != http.StatusOK
	})

	listStatus, body := admin.restText("/api/v1/portal/assets/" + assetID + "/shares")
	var shares []map[string]any
	if err := json.Unmarshal([]byte(body), &shares); listStatus != http.StatusOK || err != nil {
		t.Fatalf("the asset's share list: %d %s", listStatus, body)
	}
	named := 0
	for _, s := range shares {
		if s["shared_with_persona"] == persona && s["revoked"] != true {
			named++
		}
	}
	if named != 1 {
		t.Errorf("the asset's share list names the persona %d times, want once: %v", named, shares)
	}
}

// issue2089Eventually waits for a persona save to reach the replica that
// answers next: the proxy alternates between two, and the other hears of the
// save over the reload bus.
func issue2089Eventually(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within 15s", what)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
