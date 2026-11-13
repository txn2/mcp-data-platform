//go:build integration

package acceptance

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2483 (epic #2469): a URL-sourced spec checked against its
// upstream. A changed upstream is recorded as a report naming each operation
// added, removed, changed and newly deprecated and the info.version change,
// and applied, so api_discover shows the new operations; an unchanged
// upstream answers the conditional fetch with 304 and records nothing; with
// hold_changes the changed spec waits, api_discover unchanged, until Apply,
// and Discard leaves it as it was; a spec made due is checked by the schedule
// on its own; and the report reaches the change notification channel.
//
// The upstream is a public platform endpoint serving the spec, with an ETag
// and a 304 for If-None-Match; re-saving its script is the vendor publishing
// a new version. The dev stack lists 127.0.0.0/8 in
// apigateway.catalog_fetch.allow_private_cidrs.
//
// Wire forms: PUT .../watch takes {"refresh_interval": "<duration>" or "",
// "hold_changes": bool, "upstream_url": "<url>" or ""}, each sent both set
// and empty; the refresh, apply and discard routes take no body; PUT
// /api/v1/admin/settings/api-changes takes {"channels": ["<name>"]}.
// api_discover takes literal tools/call params.

const issue2483Purpose = "Acceptance for #2483: a catalog spec kept current."

// issue2483Op is one operation of a fixture spec.
type issue2483Op struct {
	id, method, path string
	deprecated       bool
	required         string // a required query parameter, "" for none
}

// issue2483Spec is an OpenAPI document of ops at version.
func issue2483Spec(version string, ops ...issue2483Op) string {
	paths := map[string]map[string]any{}
	for _, op := range ops {
		o := map[string]any{
			"operationId": op.id, "summary": op.id,
			"responses": map[string]any{"200": map[string]any{"description": "ok"}},
		}
		var params []any
		if strings.Contains(op.path, "{id}") {
			params = append(params, map[string]any{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}})
		}
		if op.required != "" {
			params = append(params, map[string]any{"name": op.required, "in": "query", "required": true, "schema": map[string]any{"type": "string"}})
		}
		if params != nil {
			o["parameters"] = params
		}
		if op.deprecated {
			o["deprecated"] = true
		}
		if paths[op.path] == nil {
			paths[op.path] = map[string]any{}
		}
		paths[op.path][strings.ToLower(op.method)] = o
	}
	raw, _ := json.Marshal(map[string]any{
		"openapi": "3.0.3", "info": map[string]any{"title": "Pets", "version": version}, "paths": paths,
	})
	return string(raw)
}

// issue2483V1 and issue2483V2 are one API before and after a release:
// createPet added, deletePet removed, listPets gains a required parameter,
// getPet deprecated, version 1.0 to 2.0.
var (
	issue2483V1 = issue2483Spec("1.0",
		issue2483Op{id: "listPets", method: "GET", path: "/pets"},
		issue2483Op{id: "getPet", method: "GET", path: "/pets/{id}"},
		issue2483Op{id: "deletePet", method: "DELETE", path: "/pets/{id}"})
	issue2483V2 = issue2483Spec("2.0",
		issue2483Op{id: "listPets", method: "GET", path: "/pets", required: "species"},
		issue2483Op{id: "getPet", method: "GET", path: "/pets/{id}", deprecated: true},
		issue2483Op{id: "createPet", method: "POST", path: "/pets"})
	issue2483V3 = issue2483Spec("3.0",
		issue2483Op{id: "listPets", method: "GET", path: "/pets", required: "species"},
		issue2483Op{id: "getPet", method: "GET", path: "/pets/{id}", deprecated: true},
		issue2483Op{id: "createPet", method: "POST", path: "/pets"},
		issue2483Op{id: "adoptPet", method: "POST", path: "/pets/{id}"})
)

// issue2483Handler is the endpoint script serving spec under etag: a 304 for
// a request naming that etag, the document otherwise. Its main drafts both.
func issue2483Handler(spec, etag string) string {
	return `SPEC = ` + strconv.Quote(spec) + `
ETAG = ` + strconv.Quote(etag) + `

def handle(req):
    """Serves the spec, answering 304 to a reader holding this version."""
    if req["headers"].get("if-none-match", "") == ETAG:
        return {"status": 304, "body": ""}
    return {"headers": {"ETag": ETAG, "Content-Type": "application/json"}, "body": SPEC}

def main():
    """Drafts a first read and a conditional one."""
    first = handle({"headers": {}, "params": {}, "caller": None})
    again = handle({"headers": {"if-none-match": ETAG}, "params": {}, "caller": None})
    platform.result({"first": first["headers"]["ETag"], "again": again["status"]})
`
}

// issue2483Upstream is a public endpoint serving a spec: its URL, and a
// function that publishes the next version there.
type issue2483Upstream struct {
	name, url string
	publish   func(spec, etag string)
}

func newIssue2483Upstream(t *testing.T, admin *client, prefix, spec, etag string) issue2483Upstream {
	t.Helper()
	name := endpointName(prefix)
	admin.saveScript(map[string]any{"command": "create", "name": name, "source": issue2483Handler(spec, etag)}, nil)
	bindEndpoint(t, admin, name, "", map[string]any{"endpoint": name, "auth_mode": "public"})
	up := issue2483Upstream{name: name, url: baseURL() + "/endpoints/" + name}
	up.publish = func(spec, etag string) {
		admin.saveEdit(map[string]any{"command": "update", "name": name, "source": issue2483Handler(spec, etag)}, nil)
	}
	if got := endpointRequest(t, baseURL(), http.MethodGet, "/endpoints/"+name, nil, ""); got.status != http.StatusOK {
		t.Fatalf("the upstream endpoint answers %d %s", got.status, got.body)
	}
	return up
}

// issue2483Catalog creates a catalog, a URL spec read from up, and an api
// connection reading the catalog (base_url baseURL, which nothing here calls),
// returning the catalog id, which is also the connection's name.
func issue2483Catalog(t *testing.T, admin *client, prefix string, spec map[string]any) string {
	t.Helper()
	id := fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	if code := admin.restJSON(http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": id, "name": id, "display_name": "Pets (" + prefix + ")",
	}); code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("creating catalog %s: HTTP %d", id, code)
	}
	t.Cleanup(func() {
		admin.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+id, http.NoBody)
		admin.rest(http.MethodDelete, "/api/v1/admin/api-catalogs/"+id, http.NoBody)
	})
	if status, body := admin.rest(http.MethodPut, "/api/v1/admin/api-catalogs/"+id+"/specs/pets", jsonBody(t, spec)); status != http.StatusOK {
		t.Fatalf("saving spec pets: HTTP %d %v", status, body)
	}
	if code := admin.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+id, map[string]any{
		"config": map[string]any{
			"base_url": baseURL(), "auth_mode": "none", "connection_name": id, "catalog_id": id,
			"connect_timeout": "5s", "call_timeout": "20s", "trust_level": "untrusted",
		},
		"description": "Reads the catalog " + id + " for #2469 acceptance.",
	}); code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("registering connection %s: HTTP %d", id, code)
	}
	issue1736AwaitConnection(t, admin, id)
	return id
}

func issue2483SpecPath(catalog string) string {
	return "/api/v1/admin/api-catalogs/" + catalog + "/specs/pets"
}

// issue2483Refresh refreshes the spec and returns the response.
func issue2483Refresh(t *testing.T, admin *client, catalog string) map[string]any {
	t.Helper()
	status, out := admin.rest(http.MethodPost, issue2483SpecPath(catalog)+"/refresh", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("refreshing %s: HTTP %d %v", catalog, status, out)
	}
	return out
}

// issue2483Watch sets the spec's watch.
func issue2483Watch(t *testing.T, admin *client, catalog string, body map[string]any) map[string]any {
	t.Helper()
	status, out := admin.rest(http.MethodPut, issue2483SpecPath(catalog)+"/watch", jsonBody(t, body))
	if status != http.StatusOK {
		t.Fatalf("setting the watch of %s: HTTP %d %v", catalog, status, out)
	}
	return out
}

// issue2483Ops is the operation ids api_discover lists on connection.
func issue2483Ops(t *testing.T, admin *client, connection string) map[string]bool {
	t.Helper()
	found := map[string]map[string]any{}
	collectOperations(admin.call("api_discover", map[string]any{"connection": connection, "purpose": issue2483Purpose}), found)
	out := map[string]bool{}
	for id := range found {
		out[id] = true
	}
	return out
}

// issue2483AwaitOps waits until api_discover on connection lists want and
// not absent: an applied spec reaches the other replica over the reload bus.
func issue2483AwaitOps(t *testing.T, admin *client, connection string, want, absent []string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		ops := issue2483Ops(t, admin, connection)
		ok := true
		for _, id := range want {
			ok = ok && ops[id]
		}
		for _, id := range absent {
			ok = ok && !ops[id]
		}
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("api_discover on %s lists %v, want %v and not %v", connection, ops, want, absent)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// issue2483Names is the ids of a report list.
func issue2483Names(list any) []string {
	items, _ := list.([]any)
	out := []string{}
	for _, item := range items {
		m, _ := item.(map[string]any)
		out = append(out, fmt.Sprint(m["id"]))
	}
	return out
}

// TestIssue2483_AChangedSpecIsReportedAndApplied.
func TestIssue2483_AChangedSpecIsReportedAndApplied(t *testing.T) {
	admin := connect(t)
	up := newIssue2483Upstream(t, admin, "acc-2483-up", issue2483V1, `"v1"`)
	catalog := issue2483Catalog(t, admin, "acc-2483", map[string]any{"source_kind": "url", "source_url": up.url})
	issue2483AwaitOps(t, admin, catalog, []string{"deletePet"}, nil)

	unchanged := issue2483Refresh(t, admin, catalog)
	if unchanged["not_modified"] != true || unchanged["change"] != nil {
		t.Fatalf("an unchanged upstream: want not_modified and no change, got %v", unchanged)
	}

	up.publish(issue2483V2, `"v2"`)
	out := issue2483Refresh(t, admin, catalog)
	change, _ := out["change"].(map[string]any)
	if change == nil || change["status"] != "applied" || change["trigger"] != "manual" {
		t.Fatalf("a changed upstream: want an applied change, got %v", out)
	}
	report, _ := change["report"].(map[string]any)
	for key, want := range map[string]string{"added": "[createPet]", "removed": "[deletePet]", "deprecated": "[getPet]"} {
		if got := fmt.Sprint(issue2483Names(report[key])); got != want {
			t.Errorf("report %s = %s, want %s", key, got, want)
		}
	}
	changed, _ := report["changed"].([]any)
	if len(changed) != 1 || !strings.Contains(fmt.Sprint(changed[0]), "listPets") || !strings.Contains(fmt.Sprint(changed[0]), "parameters") {
		t.Errorf("report changed = %v, want listPets with parameters", changed)
	}
	if report["from_version"] != "1.0" || report["to_version"] != "2.0" {
		t.Errorf("report versions %v to %v, want 1.0 to 2.0", report["from_version"], report["to_version"])
	}
	if change["summary"] != "1 added, 1 removed, 1 changed, 1 deprecated; version 1.0 to 2.0" {
		t.Errorf("summary %v", change["summary"])
	}
	issue2483AwaitOps(t, admin, catalog, []string{"createPet"}, []string{"deletePet"})

	status, list := admin.rest(http.MethodGet, issue2483SpecPath(catalog)+"/changes", http.NoBody)
	if status != http.StatusOK || !strings.Contains(fmt.Sprint(list), "createPet") {
		t.Fatalf("the spec's changes: %d %v", status, list)
	}
	if again := issue2483Refresh(t, admin, catalog); again["not_modified"] != true {
		t.Fatalf("the same upstream again answers 304: %v", again)
	}
}

// TestIssue2483_AHeldChangeWaitsUntilApplied.
func TestIssue2483_AHeldChangeWaitsUntilApplied(t *testing.T) {
	admin := connect(t)
	up := newIssue2483Upstream(t, admin, "acc-2483-hold", issue2483V1, `"v1"`)
	catalog := issue2483Catalog(t, admin, "acc-2483-hold", map[string]any{"source_kind": "url", "source_url": up.url})
	watch := issue2483Watch(t, admin, catalog, map[string]any{"refresh_interval": "24h", "hold_changes": true, "upstream_url": ""})
	if watch["refresh_interval"] != "24h0m0s" || watch["hold_changes"] != true || watch["next_check_at"] == nil {
		t.Fatalf("the watch: %v", watch)
	}

	up.publish(issue2483V2, `"v2"`)
	out := issue2483Refresh(t, admin, catalog)
	change, _ := out["change"].(map[string]any)
	if change == nil || change["status"] != "pending" {
		t.Fatalf("a held change: want pending, got %v", out)
	}
	time.Sleep(2 * time.Second) // a reload, if one were made, would have reached both replicas
	issue2483AwaitOps(t, admin, catalog, []string{"deletePet"}, []string{"createPet"})
	if got := admin.call("api_discover", map[string]any{"connection": catalog, "purpose": issue2483Purpose}); strings.Contains(fmt.Sprint(got), "createPet") {
		t.Fatalf("api_discover lists the held spec's operation before Apply: %v", got)
	}
	if status, w := admin.rest(http.MethodGet, issue2483SpecPath(catalog)+"/watch", http.NoBody); status != http.StatusOK || w["pending_change"] != change["id"] {
		t.Fatalf("the watch names the waiting change: %d %v", status, w)
	}

	status, applied := admin.rest(http.MethodPost, issue2483SpecPath(catalog)+"/changes/"+fmt.Sprint(change["id"])+"/apply", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("apply: %d %v", status, applied)
	}
	issue2483AwaitOps(t, admin, catalog, []string{"createPet"}, []string{"deletePet"})
	if status, _ := admin.rest(http.MethodPost, issue2483SpecPath(catalog)+"/changes/"+fmt.Sprint(change["id"])+"/apply", http.NoBody); status != http.StatusConflict {
		t.Fatalf("a settled change applied again: %d, want 409", status)
	}

	up.publish(issue2483V3, `"v3"`)
	held, _ := issue2483Refresh(t, admin, catalog)["change"].(map[string]any)
	if held == nil || held["status"] != "pending" {
		t.Fatalf("the next change: %v", held)
	}
	status, discarded := admin.rest(http.MethodPost, issue2483SpecPath(catalog)+"/changes/"+fmt.Sprint(held["id"])+"/discard", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("discard: %d %v", status, discarded)
	}
	time.Sleep(2 * time.Second)
	issue2483AwaitOps(t, admin, catalog, []string{"createPet"}, []string{"adoptPet"})

	cleared := issue2483Watch(t, admin, catalog, map[string]any{"refresh_interval": "", "hold_changes": false, "upstream_url": ""})
	if cleared["refresh_interval"] != "" || cleared["next_check_at"] != nil {
		t.Fatalf("an empty interval checks on request only: %v", cleared)
	}
}

// TestIssue2483_AScheduledCheckRunsWhenDueAndReachesTheChannel: a spec made
// due is checked by the loop on its own, and its report is posted to the
// change notification channel.
func TestIssue2483_AScheduledCheckRunsWhenDueAndReachesTheChannel(t *testing.T) {
	admin := connect(t)
	env := alertChannel(t, admin)
	putSettings(t, admin, "/api/v1/admin/settings/api-changes", map[string]any{"channels": []string{chanAlerts}})
	up := newIssue2483Upstream(t, admin, "acc-2483-sched", issue2483V1, `"v1"`)
	catalog := issue2483Catalog(t, admin, "acc-2483-sched", map[string]any{"source_kind": "url", "source_url": up.url})
	issue2483Watch(t, admin, catalog, map[string]any{"refresh_interval": "168h", "hold_changes": false, "upstream_url": ""})
	up.publish(issue2483V2, `"v2"`)

	// Due now: the loop polls every minute.
	if _, err := devDB(t).ExecContext(t.Context(),
		`UPDATE api_catalog_spec_watch SET next_check_at = NOW() - interval '1 second' WHERE catalog_id = $1`, catalog); err != nil {
		t.Fatalf("making the spec due: %v", err)
	}
	deadline := time.Now().Add(150 * time.Second)
	var change map[string]any
	for change == nil {
		if time.Now().After(deadline) {
			t.Fatal("the scheduled check never recorded a change")
		}
		time.Sleep(3 * time.Second)
		_, list := admin.rest(http.MethodGet, issue2483SpecPath(catalog)+"/changes", http.NoBody)
		if items, _ := list["changes"].([]any); len(items) > 0 {
			change, _ = items[0].(map[string]any)
		}
	}
	if change["trigger"] != "schedule" || change["status"] != "applied" {
		t.Fatalf("the scheduled change: %v", change)
	}
	_, watch := admin.rest(http.MethodGet, issue2483SpecPath(catalog)+"/watch", http.NoBody)
	if watch["last_checked_at"] == nil || watch["last_error"] != nil {
		t.Fatalf("the watch after the scheduled check: %v", watch)
	}

	title := "API spec changed: " + catalog + "/pets"
	deadline = time.Now().Add(60 * time.Second)
	for !postsContain(t, env, title) {
		if time.Now().After(deadline) {
			t.Fatalf("the change report never reached the channel (%s)", title)
		}
		time.Sleep(2 * time.Second)
	}
	if !postsContain(t, env, "deletePet") {
		t.Fatal("the posted report does not name the removed operation")
	}
}

// TestIssue2483_AFailedCheckNeverRepeatsTheURL: a check whose upstream fails
// stamps the failure, and neither the response nor the stored last_error nor
// the posted report carries the spec URL's query, where a vendor key goes.
func TestIssue2483_AFailedCheckNeverRepeatsTheURL(t *testing.T) {
	admin := connect(t)
	up := newIssue2483Upstream(t, admin, "acc-2483-key", issue2483V1, `"v1"`)
	const key = "acc2483sekretKEY"
	catalog := issue2483Catalog(t, admin, "acc-2483-key", map[string]any{"source_kind": "url", "source_url": up.url + "?key=" + key})
	// The endpoint is unbound, so the upstream answers 404.
	if out := admin.endpointEnable(up.name, false); out["enabled"] != false {
		t.Logf("handler_disable: %v", out)
	}
	status, out := admin.rest(http.MethodPost, issue2483SpecPath(catalog)+"/refresh", http.NoBody)
	if status == http.StatusOK {
		t.Fatalf("a refresh from a disabled endpoint succeeded: %v", out)
	}
	_, watch := admin.rest(http.MethodGet, issue2483SpecPath(catalog)+"/watch", http.NoBody)
	for _, sink := range []string{fmt.Sprint(out), fmt.Sprint(watch)} {
		for _, form := range secretForms(key) {
			if strings.Contains(sink, form) {
				t.Fatalf("the vendor key reached a sink: %q in %s", form, sink)
			}
		}
	}
	if watch["last_error"] == nil {
		t.Fatalf("the failure was not stamped: %v", watch)
	}
}

// secretForms is s as written, base64, hex, URL-escaped and JSON-escaped: the
// spellings a sink search looks for.
func secretForms(s string) []string {
	quoted, _ := json.Marshal(s)
	return []string{
		s, base64.StdEncoding.EncodeToString([]byte(s)), hex.EncodeToString([]byte(s)),
		url.QueryEscape(s), strings.Trim(string(quoted), `"`),
	}
}
