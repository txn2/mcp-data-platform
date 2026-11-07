//go:build integration

package acceptance

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

// Acceptance for #2527: an application's page names each part instead of
// its id, with its newest version; the Add parts search lists a part that
// belongs to another application with that application's name; and two parts
// added in one go are both added.
//
// Wire forms: the portal's parts route takes one JSON object of two strings
// (kind, id); the candidates route takes q as a query string; manage_script
// and manage_application take their arguments as strings. No parameter admits
// a second form.

const apps2527 = "/api/v1/portal/applications/"

// script2527 saves a script and returns its id.
func script2527(t *testing.T, c *client, name string) string {
	t.Helper()
	c.saveScript(map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2527",
		"source": "def main():\n    \"\"\"Answers that it ran.\"\"\"\n    platform.result({\"ok\": True})\n",
	}, nil)
	got := c.call("manage_script", map[string]any{"command": "get", "name": name})
	id, _ := got["id"].(string)
	if id == "" {
		t.Fatalf("get %s carried no id: %v", name, got)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	return id
}

// app2527 creates an application and removes it when the test ends.
func app2527(t *testing.T, c *client, name string) {
	t.Helper()
	c.application(map[string]any{"command": "create", "name": name, "description": "Acceptance #2527"})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_application", map[string]any{"command": "delete", "application": name})
	})
}

func TestIssue2527_PartsAreNamedAndTwoAddedInOneGo(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	stamp := unique1579()
	first, second := "acc2527-orders-sync-"+stamp, "acc2527-revenue-digest-"+stamp
	firstID, secondID := script2527(t, admin, first), script2527(t, admin, second)
	app := "acc2527-shop-" + stamp
	app2527(t, admin, app)

	// The dialog adds the parts it was given one request each.
	for _, id := range []string{firstID, secondID} {
		if code, body := admin.rest(http.MethodPost, apps2527+app+"/parts", jsonBody(t, map[string]any{"kind": "script", "id": id})); code != http.StatusOK {
			t.Fatalf("adding script %s answered %d: %v", id, code, body)
		}
	}
	code, detail := admin.rest(http.MethodGet, apps2527+app, http.NoBody)
	if code != http.StatusOK {
		t.Fatalf("the application's page answered %d: %v", code, detail)
	}
	parts, _ := detail["parts"].([]any)
	if len(parts) != 2 {
		t.Fatalf("both parts were not added: %v", detail["parts"])
	}
	names := map[string]map[string]any{}
	for _, p := range parts {
		m, _ := p.(map[string]any)
		names[m["id"].(string)] = m
	}
	for id, want := range map[string]string{firstID: first, secondID: second} {
		got := names[id]
		if got["name"] != want || got["state"] != "ok" {
			t.Errorf("part %s reads %v, not the name %q", id, got, want)
		}
		if v, ok := got["latest_version"].(float64); !ok || v < 1 {
			t.Errorf("part %s carries no latest version: %v", id, got["latest_version"])
		}
	}
}

func TestIssue2527_TheSearchNamesTheApplicationThatHoldsAPart(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	stamp := unique1579()
	held := "acc2527-held-" + stamp
	heldID := script2527(t, admin, held)
	free := "acc2527-free-" + stamp
	freeID := script2527(t, admin, free)
	billing, shop := "acc2527-billing-"+stamp, "acc2527-store-"+stamp
	app2527(t, admin, billing)
	app2527(t, admin, shop)
	admin.application(map[string]any{"command": "add_part", "application": billing, "kind": "script", "part_id": heldID})

	code, out := admin.rest(http.MethodGet, apps2527+shop+"/candidates?q="+url.QueryEscape("acc2527-"), http.NoBody)
	if code != http.StatusOK {
		t.Fatalf("the candidates route answered %d: %v", code, out)
	}
	found := map[string]map[string]any{}
	list, _ := out["candidates"].([]any)
	for _, c := range list {
		m, _ := c.(map[string]any)
		found[m["id"].(string)] = m
	}
	heldRow, ok := found[heldID]
	if !ok {
		t.Fatalf("the part another application holds is not listed: %v", out)
	}
	if appRef, _ := heldRow["application"].(map[string]any); appRef["name"] != billing {
		t.Errorf("the held part names %v, not %q", heldRow["application"], billing)
	}
	if freeRow, ok := found[freeID]; !ok || freeRow["application"] != nil || freeRow["name"] != free {
		t.Errorf("the free part reads %v", freeRow)
	}
	if counts, _ := out["counts"].(map[string]any); counts["script"] == nil {
		t.Errorf("the counts carry no scripts: %v", out["counts"])
	}

	// A person who neither owns the application nor administers is refused.
	peer := connectAs(t, "acme-peer-key")
	if code, _ := peer.rest(http.MethodGet, apps2527+shop+"/candidates", http.NoBody); code != http.StatusForbidden {
		t.Errorf("a collaborator searching another person's application answered %d", code)
	}
}
