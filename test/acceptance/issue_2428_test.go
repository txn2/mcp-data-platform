//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2428: the application database. The dev stack's second
// PostgreSQL (service postgres-target) is named `orders` and the read_only
// `orders-ro`, kind appdb. No agent tool reaches it; scripts and endpoints do,
// through platform.query and platform.execute.
//
// Wire forms: manage_script takes command, name and source as strings,
// allow_writes as a boolean and args as an object; list_connections,
// platform_find_tools and search take their documented string arguments; the
// admin connection route takes one JSON object; an endpoint request is an
// HTTP GET with a bearer key; endpoint_set takes endpoint, path and auth_mode
// as strings and methods and callers as lists of strings. The statement parameters are Starlark values in
// the script source: an integer, a string and a list.

const (
	appdb2428       = "orders"
	appdb2428RO     = "orders-ro"
	ingestKey2428   = "acme-ingest-key"
	readTypes2428   = "SELECT id, big, amount, at, doc, labels, blob FROM type_samples WHERE id = :id"
	expected2428    = `[{"amount":"12345678901234567890.0123456789","at":"2026-11-01T10:30:00Z","big":"9007199254740993","blob":"AAEC/w==","doc":{"a":[1,2],"b":{"c":null}},"id":1,"labels":["x","y z"]}]`
	purposeSrch2428 = "Acceptance #2428: an application database is not on the agent's data surface."
)

// readTypesSource prints the rows of one type_samples row as JSON.
func readTypesSource(conn string) string {
	return `
def main():
    """Reads one row of every mapped type."""
    res = platform.query("` + readTypes2428 + `", connection="` + conn + `", params={"id": 1})
    print(json.encode(res["rows"]))
`
}

// draft2428 runs a draft and returns the answer.
func draft2428(c *client, name, source string, allowWrites bool) map[string]any {
	args := map[string]any{"command": "run_draft", "name": name, "source": source}
	if allowWrites {
		args["allow_writes"] = true
	}
	return c.call("manage_script", args)
}

// canonical2428 re-encodes JSON with sorted keys.
func canonical2428(t *testing.T, raw string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &v); err != nil {
		t.Fatalf("not JSON: %q: %v", raw, err)
	}
	out, _ := json.Marshal(v)
	return string(out)
}

func TestIssue2428_NoAgentToolReachesAnApplicationDatabase(t *testing.T) {
	c := connect(t)
	tools := c.tools()
	for _, tool := range tools {
		if strings.HasPrefix(tool.Name, "postgres_") || strings.HasPrefix(tool.Name, "appdb") {
			t.Errorf("tools/list holds %s", tool.Name)
		}
	}
	if len(tools) < 10 {
		t.Fatalf("tools/list holds %d tools; the check would prove nothing", len(tools))
	}
	found := c.call("platform_find_tools", map[string]any{"query": "postgres query"})
	raw, _ := json.Marshal(found)
	if strings.Contains(string(raw), "postgres_query") || strings.Contains(string(raw), "postgres_execute") {
		t.Errorf("platform_find_tools returns a postgres tool: %s", raw)
	}
	res, text, err := c.callRaw("search", map[string]any{"intent": "a tool that runs a SQL query on a postgres database", "purpose": purposeSrch2428})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if res.IsError {
		t.Fatalf("search failed: %s", text)
	}
	if strings.Contains(text, "postgres_query") || strings.Contains(text, "postgres_execute") {
		t.Errorf("search returns a postgres tool: %s", text)
	}
}

func TestIssue2428_ConnectionsAreApplicationDatabases(t *testing.T) {
	c := connect(t)
	out := c.call("list_connections", map[string]any{})
	conns, _ := out["connections"].([]any)
	kinds := map[string]string{}
	for _, raw := range conns {
		e, _ := raw.(map[string]any)
		name, _ := e["name"].(string)
		kind, _ := e["kind"].(string)
		kinds[name] = kind
	}
	for _, name := range []string{appdb2428, appdb2428RO} {
		if kinds[name] != "appdb" {
			t.Errorf("list_connections reports %s as kind %q, want appdb (all: %v)", name, kinds[name], kinds)
		}
	}
	status, body := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/appdb/"+appdb2428, http.NoBody)
	if status != http.StatusOK || body["kind"] != "appdb" {
		t.Errorf("the admin connection route (%d) does not read %s as kind appdb: %v", status, appdb2428, body)
	}
}

func TestIssue2428_ASavedRunADraftAndAnEndpointReadTheSameRows(t *testing.T) {
	admin := connect(t)
	want := canonical2428(t, expected2428)

	draft := draft2428(admin, "acceptance-2428-draft-"+unique1579(), readTypesSource(appdb2428), false)
	if draft["status"] != "succeeded" {
		t.Fatalf("the draft failed: %v", draft)
	}
	if got := canonical2428(t, draft["log"].(string)); got != want {
		t.Errorf("draft rows:\n got %s\nwant %s", got, want)
	}

	name := "acceptance-2428-saved-" + unique1579()
	_, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	admin.saveScript(map[string]any{"command": "create", "name": name, "source": readTypesSource(appdb2428)}, nil)
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	run := admin.call("run_script", map[string]any{"name": name, "wait_seconds": 60})
	if run["status"] != "succeeded" {
		t.Fatalf("the saved run failed: %v", run)
	}
	if got := canonical2428(t, run["log"].(string)); got != want {
		t.Errorf("saved run rows:\n got %s\nwant %s", got, want)
	}

	ep := endpointName("acc-2428")
	handler := `def handle(req):
    """Answers with one row of every mapped type."""
    res = platform.query("` + readTypes2428 + `", connection="` + appdb2428 + `", params={"id": int(req["params"]["id"])})
    return {"json": res["rows"]}
`
	saveEndpointScript(t, admin, ep, handler, `{"params": {"id": "1"}, "caller": None}`)
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": ep}) })
	bindEndpoint(t, admin, ep, "", map[string]any{
		"endpoint": ep, "methods": []string{"GET"}, "path": "/{id}", "auth_mode": "authenticated", "callers": []string{"*"},
	})
	got := endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+ep+"/1", bearer(admin.apiKey), "")
	if got.status != http.StatusOK {
		t.Fatalf("GET /endpoints/%s/1: %d %s", ep, got.status, got.body)
	}
	if rows := canonical2428(t, got.body); rows != want {
		t.Errorf("endpoint rows:\n got %s\nwant %s", rows, want)
	}
}

func TestIssue2428_ExecuteWritesAndADraftReportsItAsAWrite(t *testing.T) {
	admin := connect(t)
	email := "acc-2428-" + unique1579() + "@acme.example"
	insert := `
def main():
    """Adds one customer."""
    platform.execute("INSERT INTO customers (email, name) VALUES (:email, :name)", connection="` + appdb2428 + `", params={"email": "` + email + `", "name": "Acceptance"})
`
	stopped := draft2428(admin, "acceptance-2428-barred", insert, false)
	if stopped["status"] == "succeeded" || !strings.Contains(asJSON(stopped), "appdb.execute") {
		t.Fatalf("a draft that writes to an application database was not stopped at the write: %v", stopped)
	}
	count := func() string {
		out := draft2428(admin, "acceptance-2428-count", `
def main():
    """Counts the customer."""
    print(platform.query("SELECT count(*) AS n FROM customers WHERE email = :e", connection="`+appdb2428+`", params={"e": "`+email+`"})["rows"][0]["n"])
`, false)
		log, _ := out["log"].(string)
		return strings.TrimSpace(log)
	}
	if n := count(); n != "0" {
		t.Fatalf("the refused draft wrote: %s rows", n)
	}
	ran := draft2428(admin, "acceptance-2428-allowed", insert, true)
	if ran["status"] != "succeeded" {
		t.Fatalf("the allowed draft failed: %v", ran)
	}
	t.Cleanup(func() {
		draft2428(admin, "acceptance-2428-cleanup", `
def main():
    """Removes the customer."""
    platform.execute("DELETE FROM customers WHERE email = :e", connection="`+appdb2428+`", params={"e": "`+email+`"})
`, true)
	})
	if n := count(); n != "1" {
		t.Fatalf("the allowed draft left %s rows, want 1", n)
	}

	ro := draft2428(admin, "acceptance-2428-ro", strings.Replace(insert, `connection="`+appdb2428+`"`, `connection="`+appdb2428RO+`"`, 1), true)
	if ro["status"] == "succeeded" || !strings.Contains(asJSON(ro), "read_only") {
		t.Fatalf("platform.execute on the read_only connection: %v", ro)
	}
}

func TestIssue2428_ARunWhosePersonaDeniesTheConnectionIsRefused(t *testing.T) {
	ingest := connectAs(t, ingestKey2428)
	out := draft2428(ingest, "acceptance-2428-denied-"+unique1579(), readTypesSource(appdb2428), false)
	text := asJSON(out)
	if out["status"] == "succeeded" || !strings.Contains(text, `application database \"orders\" is not allowed`) {
		t.Fatalf("a run whose persona denies the connection: %s", text)
	}
}

func TestIssue2428_ABoundValueReachesNoSink(t *testing.T) {
	admin := connectFor(t, 10*time.Minute)
	k := newCanary2363(t)
	const suffix = "-appdb-bound-Zq7@acme.example"
	value := k.stamp + suffix
	// The script is handed the DETAIL of its own failure. The script source is
	// audited with the save, so the value is assembled in the script from two
	// literals neither of which is the value; a recording keeps the statement
	// with its values, gzipped, for replay (docs/server/appdb.md).
	k.returned[value] = "application database bound value"

	insert := `
def main():
    """Inserts the canary twice; the second is a unique violation."""
    email = "` + k.stamp + `" + "` + suffix + `"
    sql = "INSERT INTO customers (email, name) VALUES (:email, :name)"
    platform.execute(sql, connection="` + appdb2428 + `", params={"email": email, "name": "Canary"})
    res = platform.execute(sql, connection="` + appdb2428 + `", params={"email": email, "name": "Canary"}, on_error="return")
    print(json.encode(res))
`
	ran := draft2428(admin, "acceptance-2428-canary", insert, true)
	if ran["status"] != "succeeded" {
		t.Fatalf("the canary draft failed: %v", ran)
	}
	if log, _ := ran["log"].(string); !strings.Contains(log, "already exists") {
		t.Errorf("the script was not handed the DETAIL of its failure: %s", log)
	}
	t.Cleanup(func() {
		draft2428(admin, "acceptance-2428-canary-cleanup", `
def main():
    """Removes the canary."""
    platform.execute("DELETE FROM customers WHERE email = :e", connection="`+appdb2428+`", params={"e": "`+k.stamp+`" + "`+suffix+`"})
`, true)
	})
	k.awaitWriters(admin)
	k.searchDatabase()
	k.searchFiles()
}

func TestIssue2428_AScriptTestAnswersFromTheRecordingWithTheDatabaseGone(t *testing.T) {
	admin := connect(t)
	port := os.Getenv("DEV_PG_TARGET_PORT")
	if port == "" {
		port = "5442"
	}
	conn := "acc-2428-rec-" + unique1579()
	path := "/api/v1/admin/connection-instances/appdb/" + conn
	cfg := map[string]any{"host": "localhost", "port": port, "database": "orders", "user": "orders_app", "password": "orders_secret", "sslmode": "disable"}
	if status, body := admin.rest(http.MethodPut, path, jsonBody(t, map[string]any{"config": cfg, "description": "Acceptance #2428"})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the connection answered %d: %v", status, body)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, path, http.NoBody) })

	name := "acceptance-2428-replay-" + unique1579()
	withTest := admin.tested(map[string]any{"command": "create", "name": name, "source": readTypesSource(conn)}, nil)

	// The connection now points at a port nothing listens on.
	cfg["port"] = 1
	if status, body := admin.rest(http.MethodPut, path, jsonBody(t, map[string]any{"config": cfg, "description": "Acceptance #2428, unreachable"})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("repointing the connection answered %d: %v", status, body)
	}
	live := draft2428(admin, name+"-live", readTypesSource(conn), false)
	if live["status"] == "succeeded" {
		t.Fatalf("a live run reached the database the connection no longer points at: %v", live)
	}
	out := admin.call("manage_script", map[string]any{"command": "test", "name": name, "source": withTest["source"]})
	if out["failed"] != float64(0) || out["passed"] == float64(0) {
		t.Fatalf("the script's test did not pass from its recording: %v", out)
	}
}

func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestIssue2428_ANameATrinoConnectionHoldsIsRefused(t *testing.T) {
	admin := connect(t)
	// acme is the dev stack's Trino connection.
	path := "/api/v1/admin/connection-instances/appdb/acme"
	status, body := admin.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"config":      map[string]any{"host": "localhost", "database": "orders", "user": "orders_app", "password": "orders_secret", "sslmode": "disable"},
		"description": "Acceptance #2428: a name a Trino connection holds",
	}))
	text, _ := json.Marshal(body)
	if status != http.StatusConflict || !strings.Contains(string(text), "already a Trino connection") {
		t.Fatalf("saving an application database named like a Trino connection answered %d: %s", status, text)
	}
	if status, _ := admin.rest(http.MethodGet, path, http.NoBody); status != http.StatusNotFound {
		t.Fatalf("the refused connection was stored (GET answered %d)", status)
	}
}
