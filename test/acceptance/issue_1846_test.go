//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1846: over HTTP only a script's owner or an administrator could run
// it, and no parameter could be bound to the caller, so an application could
// neither expose a catalog of report scripts nor keep one tenant from naming
// another.
//
// What these hold, against the running platform: an administrator issues a
// non-admin API key carrying a tenant attribute; the script's owner grants the
// key; the key runs the script over POST /api/v1/portal/scripts/{id}/runs and
// reads that run, cannot read the source or the versions, gets 404 on a script
// it was not granted, and finds the script in its scope=granted catalog; a
// parameter bound to caller.tenant takes the key's attribute, a body value for
// it is 400 and a key without the attribute is 403; the grant is listed on the
// script and recorded in the audit log.
//
// Wire forms: the portal run route's body is an object whose `params` is an
// object, sent with the bound parameter absent (accepted) and present
// (refused); the grant body's `principal_kind` and `principal` are strings;
// the key create body's `roles` is an array and `attributes` an object of
// strings; manage_script's `params` is an array of objects.

// source1846 prints the tenant it was bound to and hands it back.
const source1846 = `print("tenant", run.params["tenant"])
platform.result({"tenant": run.params["tenant"]})
`

// issueKey1846 creates a non-admin API key and deletes it at cleanup.
func issueKey1846(t *testing.T, admin *client, attributes map[string]any) (name, key string) {
	t.Helper()
	name = fmt.Sprintf("acc-1846-%d", time.Now().UnixNano())
	body := map[string]any{"name": name, "roles": []any{"inventory_analyst"}, "description": "Acceptance #1846"}
	if attributes != nil {
		body["attributes"] = attributes
	}
	status, out := admin.rest(http.MethodPost, "/api/v1/admin/auth/keys", jsonBody(t, body))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("creating the key answered %d %v", status, out)
	}
	key, _ = out["key"].(string)
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/auth/keys/"+name, http.NoBody) })
	return name, key
}

// script1846 saves a script whose tenant parameter is caller-bound, owned by
// the caller of c, and returns its name and id.
func script1846(t *testing.T, c *client) (name, id string) {
	t.Helper()
	name = fmt.Sprintf("acc-1846-%d", time.Now().UnixNano())
	c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": source1846,
		"description": "Acceptance #1846: a grant and a caller-bound parameter.",
		"params":      []any{map[string]any{"name": "tenant", "type": "string", "bind": "caller.tenant"}},
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	got := c.call("manage_script", map[string]any{"command": "get", "name": name})
	id, _ = got["id"].(string)
	if id == "" {
		t.Fatalf("the script has no id: %v", got)
	}
	return name, id
}

func TestIssue1846_AGrantedKeyRunsTheScriptAsItsTenant(t *testing.T) {
	admin := connect(t)
	_, id := script1846(t, admin)
	keyName, key := issueKey1846(t, admin, map[string]any{"tenant": "acme"})
	app := &client{t: t, ctx: admin.ctx, apiKey: key, base: admin.base}

	// Not granted yet: the script is not there to run.
	if status, _ := app.rest(http.MethodPost, "/api/v1/portal/scripts/"+id+"/runs", http.NoBody); status != http.StatusNotFound {
		t.Fatalf("an ungranted key running the script answered %d; want 404", status)
	}

	status, grants := admin.rest(http.MethodPost, "/api/v1/portal/scripts/"+id+"/grants",
		jsonBody(t, map[string]any{"principal_kind": "api_key", "principal": keyName}))
	if status != http.StatusCreated {
		t.Fatalf("granting answered %d %v", status, grants)
	}

	status, run := app.rest(http.MethodPost, "/api/v1/portal/scripts/"+id+"/runs?wait=60",
		jsonBody(t, map[string]any{"params": map[string]any{}}))
	if status != http.StatusOK || run["status"] != "succeeded" {
		t.Fatalf("the granted key's run answered %d %v", status, run)
	}
	if params, _ := run["params"].(map[string]any); params["tenant"] != "acme" {
		t.Errorf("the run recorded tenant=%v; want the key's attribute", params["tenant"])
	}
	if result, _ := run["result"].(map[string]any); result["tenant"] != "acme" {
		t.Errorf("the run handed back %v; want tenant acme", run["result"])
	}
	runID, _ := run["id"].(string)
	if status, again := app.rest(http.MethodGet, "/api/v1/portal/scripts/"+id+"/runs/"+runID, http.NoBody); status != http.StatusOK {
		t.Errorf("the grantee reading its own run answered %d %v", status, again)
	}

	for _, path := range []string{"/versions", "/state"} {
		if status, _ := app.rest(http.MethodGet, "/api/v1/portal/scripts/"+id+path, http.NoBody); status != http.StatusNotFound {
			t.Errorf("the grantee reading %s answered %d; want 404", path, status)
		}
	}
	if status, body := app.rest(http.MethodGet, "/api/v1/portal/scripts/"+id, http.NoBody); status == http.StatusOK {
		if src, _ := body["source"].(string); src != "" {
			t.Errorf("the grantee read the source: %q", src)
		}
	}
	if status, _ := app.rest(http.MethodPut, "/api/v1/portal/scripts/"+id+"/metadata",
		jsonBody(t, map[string]any{"display_name": "taken"})); status != http.StatusNotFound {
		t.Errorf("the grantee changing the script answered %d; want 404", status)
	}

	status, catalog := app.rest(http.MethodGet, "/api/v1/portal/scripts?scope=granted", http.NoBody)
	if status != http.StatusOK || !catalogHas(catalog, id) {
		t.Errorf("the grantee's catalog (%d) does not list the script: %v", status, catalog)
	}
}

// catalogHas reports whether a scope=granted listing carries the script,
// marked granted, with its parameter contract.
func catalogHas(listing map[string]any, id string) bool {
	rows, _ := listing["data"].([]any)
	for _, r := range rows {
		row, _ := r.(map[string]any)
		sc, _ := row["script"].(map[string]any)
		if sc["id"] == id && row["granted"] == true {
			params, _ := sc["params"].([]any)
			return len(params) == 1
		}
	}
	return false
}

func TestIssue1846_ABoundParameterIsTheKeysAndNothingElse(t *testing.T) {
	admin := connect(t)
	name, id := script1846(t, admin)
	withName, with := issueKey1846(t, admin, map[string]any{"tenant": "acme"})
	withoutName, without := issueKey1846(t, admin, nil)
	for _, principal := range []string{withName, withoutName} {
		if status, body := admin.rest(http.MethodPost, "/api/v1/portal/scripts/"+id+"/grants",
			jsonBody(t, map[string]any{"principal_kind": "api_key", "principal": principal})); status != http.StatusCreated {
			t.Fatalf("granting %s answered %d %v", principal, status, body)
		}
	}

	app := &client{t: t, ctx: admin.ctx, apiKey: with, base: admin.base}
	status, body := app.rest(http.MethodPost, "/api/v1/portal/scripts/"+id+"/runs",
		jsonBody(t, map[string]any{"params": map[string]any{"tenant": "globex"}}))
	if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(body), "caller") {
		t.Errorf("a body value for the bound parameter answered %d %v; want 400", status, body)
	}

	bare := &client{t: t, ctx: admin.ctx, apiKey: without, base: admin.base}
	if status, body := bare.rest(http.MethodPost, "/api/v1/portal/scripts/"+id+"/runs", http.NoBody); status != http.StatusForbidden {
		t.Errorf("a key without the attribute answered %d %v; want 403", status, body)
	}

	// A script with a caller-bound parameter has no caller on a schedule.
	_, text, err := admin.callRaw("manage_script", map[string]any{
		"command": "schedule_set", "name": name, "cron": "0 7 * * *", "timezone": "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "a schedule has no caller") {
		t.Errorf("scheduling a caller-bound script answered %q", text)
	}
}

func TestIssue1846_GrantsAreListedAndAudited(t *testing.T) {
	admin := connect(t)
	_, id := script1846(t, admin)
	keyName, _ := issueKey1846(t, admin, nil)
	if status, body := admin.rest(http.MethodPost, "/api/v1/portal/scripts/"+id+"/grants",
		jsonBody(t, map[string]any{"principal_kind": "api_key", "principal": keyName})); status != http.StatusCreated {
		t.Fatalf("granting answered %d %v", status, body)
	}
	status, listed := admin.rest(http.MethodGet, "/api/v1/portal/scripts/"+id+"/grants", http.NoBody)
	if status != http.StatusOK || listed["total"] != float64(1) {
		t.Fatalf("the script's grants (%d): %v", status, listed)
	}
	if status, body := admin.rest(http.MethodDelete, "/api/v1/portal/scripts/"+id+"/grants/api_key/"+keyName, http.NoBody); status != http.StatusOK {
		t.Fatalf("withdrawing answered %d %v", status, body)
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		_, events := admin.rest(http.MethodGet, "/api/v1/admin/audit/events?tool_name=script_grant&per_page=50", http.NoBody)
		if strings.Contains(fmt.Sprint(events), id) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no script_grant audit event names the script: %v", events)
		}
		time.Sleep(time.Second)
	}
}
