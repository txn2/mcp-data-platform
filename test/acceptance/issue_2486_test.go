//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2486 (epic #2469): what an upstream says about an
// operation in its answers. A call through api_invoke_endpoint to an upstream
// answering with a Sunset header records the warning for that operation and
// shows it on the connection, and the caller is handed the header; an
// operation that answered 2xx and then answers 410 records a removal;
// repeated answers notify once; and the concrete path a call addressed, which
// can carry a filled secret, is stored nowhere: a warning is filed under the
// operation id.
//
// The upstream is a public platform endpoint answering every request with a
// Sunset header, and 410 for the id "gone".
//
// Wire forms: api_invoke_endpoint takes operation_id and path_params (an
// object of strings) as literal tools/call params; the warnings route takes
// no body.

const issue2486Sunset = "Wed, 30 Jun 2027 00:00:00 GMT"

const issue2486Handler = `def handle(req):
    """Answers like a vendor retiring this operation."""
    status = 410 if req["params"]["id"] == "gone" else 200
    return {"status": status, "headers": {"Sunset": "` + issue2486Sunset + `"}, "json": {"id": req["params"]["id"]}}
`

// issue2486Spec is the operation the warning upstream serves.
const issue2486Spec = `{"openapi": "3.0.3", "info": {"title": "Things", "version": "1"},
 "paths": {"/{id}": {"get": {"operationId": "getThing", "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}],
 "responses": {"200": {"description": "ok"}}}}}}`

// issue2486Connection binds the warning upstream and an api connection whose
// base_url is it, returning the connection's name.
func issue2486Connection(t *testing.T, admin *client) string {
	t.Helper()
	name := endpointName("acc-2486-up")
	saveEndpointScript(t, admin, name, issue2486Handler, `{"params": {"id": "gone"}, "caller": None}`)
	bindEndpoint(t, admin, name, "", map[string]any{"endpoint": name, "path": "/{id}", "auth_mode": "public"})

	id := fmt.Sprintf("acc-2486-%d", time.Now().UnixNano())
	if code := admin.restJSON(http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{"id": id, "name": id, "display_name": "Things"}); code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("creating catalog %s: HTTP %d", id, code)
	}
	t.Cleanup(func() {
		admin.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+id, http.NoBody)
		admin.rest(http.MethodDelete, "/api/v1/admin/api-catalogs/"+id, http.NoBody)
	})
	if status, body := admin.rest(http.MethodPut, "/api/v1/admin/api-catalogs/"+id+"/specs/things",
		jsonBody(t, map[string]any{"source_kind": "inline", "content": issue2486Spec})); status != http.StatusOK {
		t.Fatalf("saving spec: HTTP %d %v", status, body)
	}
	if code := admin.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+id, map[string]any{
		"config": map[string]any{
			"base_url": baseURL() + "/endpoints/" + name, "auth_mode": "none", "connection_name": id, "catalog_id": id,
			"connect_timeout": "5s", "call_timeout": "20s", "trust_level": "untrusted",
		},
		"description": "The warning upstream for #2486 acceptance.",
	}); code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("registering connection %s: HTTP %d", id, code)
	}
	issue1736AwaitConnection(t, admin, id)
	return id
}

func issue2486Call(t *testing.T, admin *client, connection, id string) map[string]any {
	t.Helper()
	return admin.call("api_invoke_endpoint", map[string]any{
		"connection": connection, "operation_id": "getThing", "path_params": map[string]any{"id": id},
		"purpose": "Acceptance for #2486: an upstream retiring an operation.",
	})
}

// issue2486Warnings reads the connection's warnings once want kinds are in.
func issue2486Warnings(t *testing.T, admin *client, connection string, want ...string) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		status, out := admin.rest(http.MethodGet, "/api/v1/admin/connection-instances/api/"+connection+"/warnings", http.NoBody)
		if status != http.StatusOK {
			t.Fatalf("warnings: %d %v", status, out)
		}
		items, _ := out["warnings"].([]any)
		var list []map[string]any
		kinds := map[string]bool{}
		for _, item := range items {
			w, _ := item.(map[string]any)
			list = append(list, w)
			kinds[fmt.Sprint(w["kind"])] = true
		}
		all := true
		for _, k := range want {
			all = all && kinds[k]
		}
		if all {
			return list
		}
		if time.Now().After(deadline) {
			t.Fatalf("the warnings of %s never held %v: %v", connection, want, list)
		}
		time.Sleep(time.Second)
	}
}

// TestIssue2486_ASunsetAndARemovalAreRecordedAndSentOnce.
func TestIssue2486_ASunsetAndARemovalAreRecordedAndSentOnce(t *testing.T) {
	admin := connect(t)
	env := alertChannel(t, admin)
	putSettings(t, admin, "/api/v1/admin/settings/api-changes", map[string]any{"channels": []string{chanAlerts}})
	conn := issue2486Connection(t, admin)

	out := issue2486Call(t, admin, conn, "ok")
	if fmt.Sprint(out["status"]) != "200" || !strings.Contains(fmt.Sprint(out["headers"]), issue2486Sunset) {
		t.Fatalf("the call: want 200 carrying the Sunset header, got %v", out)
	}
	list := issue2486Warnings(t, admin, conn, "sunset")
	if list[0]["operation"] != "getThing" || list[0]["detail"] != issue2486Sunset {
		t.Fatalf("the sunset warning: %v", list[0])
	}

	time.Sleep(2 * time.Second) // the success is recorded before the removal is judged
	if out := issue2486Call(t, admin, conn, "gone"); fmt.Sprint(out["status"]) != "410" {
		t.Fatalf("the gone call: %v", out)
	}
	for _, w := range issue2486Warnings(t, admin, conn, "sunset", "removed") {
		if w["operation"] != "getThing" {
			t.Fatalf("a warning filed under %v, not the operation", w["operation"])
		}
	}

	title := "API upstream warning: " + conn + " Sunset"
	deadline := time.Now().Add(60 * time.Second)
	for !postsContain(t, env, title) {
		if time.Now().After(deadline) {
			t.Fatalf("the sunset warning never reached the channel (%s)", title)
		}
		time.Sleep(2 * time.Second)
	}
	for range 3 {
		issue2486Call(t, admin, conn, "ok")
	}
	time.Sleep(5 * time.Second)
	count := 0
	for _, p := range mattermostPosts(t, env) {
		if strings.Contains(p, title) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("repeated answers posted the sunset warning %d times, want once", count)
	}
}

// TestIssue2486_TheAddressedPathIsStoredNowhere: the concrete id a call
// addressed, which a filled secret could be, is in no warning or success row.
func TestIssue2486_TheAddressedPathIsStoredNowhere(t *testing.T) {
	admin := connect(t)
	conn := issue2486Connection(t, admin)
	const marker = "acc2486PATHSECRET"
	issue2486Call(t, admin, conn, marker)
	issue2486Warnings(t, admin, conn, "sunset")
	time.Sleep(2 * time.Second)

	db := devDB(t)
	for _, table := range []string{"api_operation_warnings", "api_operation_successes", "api_operation_callers"} {
		for _, form := range secretForms(marker) {
			var n int
			if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table+` WHERE row_to_json(`+table+`)::text LIKE '%' || $1 || '%'`, form).Scan(&n); err != nil {
				t.Fatalf("searching %s: %v", table, err)
			}
			if n != 0 {
				t.Fatalf("%s holds the addressed path (%q) in %d rows", table, form, n)
			}
		}
	}
	_, out := admin.rest(http.MethodGet, "/api/v1/admin/connection-instances/api/"+conn+"/warnings", http.NoBody)
	if strings.Contains(fmt.Sprint(out), marker) {
		t.Fatalf("the warnings route repeats the addressed path: %v", out)
	}
}
