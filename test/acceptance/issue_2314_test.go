//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2314: the Automations list says which automations are
// endpoints. Each listed script bound to an endpoint carries its name,
// address, access mode and whether it is on; one bound to none carries none;
// endpoint=bound and endpoint=unbound narrow the listing on the server, and
// any other value is refused.
//
// Wire forms: GET /api/v1/portal/scripts takes endpoint and search as query
// strings, each sent once in that form.

// issue2314Rows reads the listing for query and returns its rows by script
// name.
func issue2314Rows(t *testing.T, c *client, query string) map[string]map[string]any {
	t.Helper()
	status, out := c.rest(http.MethodGet, "/api/v1/portal/scripts?"+query, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/portal/scripts?%s: %d %v", query, status, out)
	}
	rows := map[string]map[string]any{}
	data, _ := out["data"].([]any)
	for _, r := range data {
		row, _ := r.(map[string]any)
		script, _ := row["script"].(map[string]any)
		rows[fmt.Sprint(script["name"])] = row
	}
	return rows
}

// TestIssue2314_TheListingCarriesEachScriptsEndpointAndNarrowsByIt.
func TestIssue2314_TheListingCarriesEachScriptsEndpointAndNarrowsByIt(t *testing.T) {
	admin := connect(t)
	tag := fmt.Sprintf("acc-2314-%d", time.Now().UnixNano()%1e9)
	on, off, unbound := tag+"-on", tag+"-off", tag+"-unbound"
	for _, name := range []string{on, off, unbound} {
		saveEndpointScript(t, admin, name, issue2171EchoHandler, `{"params": {}, "caller": None}`)
	}
	bindEndpoint(t, admin, on, "", map[string]any{"endpoint": on, "path": "/{id}", "auth_mode": "public"})
	bindEndpoint(t, admin, off, "", map[string]any{"endpoint": off, "auth_mode": "authenticated", "callers": []string{"*"}})
	admin.call("manage_script", map[string]any{"command": "endpoint_disable", "name": off})

	all := issue2314Rows(t, admin, "search="+tag)
	if len(all) != 3 {
		t.Fatalf("the listing of %s*: %d rows, want 3", tag, len(all))
	}
	onEndpoint, _ := all[on]["endpoint"].(map[string]any)
	if onEndpoint["name"] != on || onEndpoint["path"] != "/endpoints/"+on+"/{id}" || onEndpoint["auth_mode"] != "public" || onEndpoint["enabled"] != true {
		t.Fatalf("the bound script's endpoint: %v", all[on]["endpoint"])
	}
	offEndpoint, _ := all[off]["endpoint"].(map[string]any)
	if offEndpoint["enabled"] != false || offEndpoint["auth_mode"] != "authenticated" {
		t.Fatalf("the turned-off binding: %v", all[off]["endpoint"])
	}
	if _, present := all[unbound]["endpoint"]; present {
		t.Fatalf("a script bound to no endpoint carries one: %v", all[unbound]["endpoint"])
	}

	bound := issue2314Rows(t, admin, "search="+tag+"&endpoint=bound")
	if len(bound) != 2 || bound[on] == nil || bound[off] == nil {
		t.Fatalf("endpoint=bound: %v", keys2314(bound))
	}
	notBound := issue2314Rows(t, admin, "search="+tag+"&endpoint=unbound")
	if len(notBound) != 1 || notBound[unbound] == nil {
		t.Fatalf("endpoint=unbound: %v", keys2314(notBound))
	}
	status, out := admin.rest(http.MethodGet, "/api/v1/portal/scripts?endpoint=yes", nil)
	if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out["detail"]), `unknown endpoint "yes"`) {
		t.Fatalf("endpoint=yes: want 400, got %d %v", status, out)
	}
}

func keys2314(rows map[string]map[string]any) []string {
	out := make([]string, 0, len(rows))
	for k := range rows {
		out = append(out, k)
	}
	return out
}
