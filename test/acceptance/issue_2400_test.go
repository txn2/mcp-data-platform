//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2400: the PostgreSQL connection kind, which #2428 made the
// application database (kind appdb). Every criterion runs against the dev
// stack, whose second PostgreSQL (service postgres-target,
// dev/fixtures/postgres-target.sql) dev/platform.yaml names as `orders` and as
// the read_only `orders-ro`. The kind has no agent tool since #2428, so the
// statements run from a draft script through platform.query and
// platform.execute. The atomic statements list is #2402's; that a bound value
// reaches no sink is TestIssue2428_ABoundValueReachesNoSink.
//
// Wire forms: manage_script takes command, name and source as strings and
// allow_writes as a boolean; the statement's parameters are Starlark values in
// the source (an integer, a string); the admin connection route takes one JSON
// object.

const (
	pg2400Conn     = "orders"
	pg2400ReadOnly = "orders-ro"
)

// pgDraft2400 runs source as a draft and returns the answer and its JSON.
func pgDraft2400(c *client, source string, allowWrites bool) (map[string]any, string) {
	args := map[string]any{"command": "run_draft", "name": "acceptance-2400-" + unique1579(), "source": source}
	if allowWrites {
		args["allow_writes"] = true
	}
	out := c.call("manage_script", args)
	raw, _ := json.Marshal(out)
	return out, string(raw)
}

func TestIssue2400_QueryReturnsTypedRowsForEveryMappedType(t *testing.T) {
	c := connect(t)
	out, raw := pgDraft2400(c, `
def main():
    """Reads one row of every mapped type."""
    res = platform.query("SELECT * FROM type_samples WHERE id = :id", connection="`+pg2400Conn+`", params={"id": 1})
    print(json.encode({"columns": res["columns"], "rows": res["rows"]}))
`, false)
	if out["status"] != "succeeded" {
		t.Fatalf("the draft failed: %s", raw)
	}
	log, _ := out["log"].(string)
	var list map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(log)), &list); err != nil {
		t.Fatalf("the draft printed no JSON: %q", log)
	}

	want := map[string]struct{ value, typ string }{
		"id":       {`1`, "integer"},
		"small":    {`7`, "smallint"},
		"big":      {`"9007199254740993"`, "bigint"},
		"amount":   {`"12345678901234567890.0123456789"`, "numeric"},
		"ratio":    {`0.25`, "double precision"},
		"at":       {`"2026-11-01T10:30:00Z"`, "timestamp with time zone"},
		"local_at": {`"2026-11-01T08:00:00"`, "timestamp without time zone"},
		"day":      {`"2026-11-01"`, "date"},
		"doc":      {`{"a":[1,2],"b":{"c":null}}`, "jsonb"},
		"raw":      {`[true,"x"]`, "json"},
		"uid":      {`"6f1c2c1e-7d2b-4b9e-9a51-0c4f0d1e2a3b"`, "uuid"},
		"labels":   {`["x","y z"]`, "text[]"},
		"counts":   {`[1,null,3]`, "integer[]"},
		"blob":     {`"AAEC/w=="`, "bytea"},
		"flag":     {`true`, "boolean"},
		"note":     {`null`, "text"},
	}
	cols, _ := list["columns"].([]any)
	rows, _ := list["rows"].([]any)
	if len(rows) != 1 || len(cols) != len(want) {
		t.Fatalf("got %d rows and %d columns, want 1 and %d: %v", len(rows), len(cols), len(want), list)
	}
	row, _ := rows[0].(map[string]any)
	for _, raw := range cols {
		col, _ := raw.(map[string]any)
		name, _ := col["name"].(string)
		got, _ := json.Marshal(row[name])
		if string(got) != want[name].value || col["type"] != want[name].typ {
			t.Errorf("%s = %s (%v), want %s (%s)", name, got, col["type"], want[name].value, want[name].typ)
		}
	}
}

func TestIssue2400_ExecuteOnAReadOnlyConnectionIsRefused(t *testing.T) {
	c := connect(t)
	out, raw := pgDraft2400(c, `
def main():
    """Deletes an order through the read_only connection."""
    platform.execute("DELETE FROM orders WHERE id = :id", connection="`+pg2400ReadOnly+`", params={"id": 1})
`, true)
	if out["status"] == "succeeded" || !strings.Contains(raw, "read_only") {
		t.Fatalf("platform.execute on a read_only connection: %s", raw)
	}
	got, raw := pgDraft2400(c, `
def main():
    """Counts the order."""
    print(platform.query("SELECT count(*) AS n FROM orders WHERE id = 1", connection="`+pg2400ReadOnly+`")["rows"][0]["n"])
`, false)
	if log, _ := got["log"].(string); strings.TrimSpace(log) != "1" {
		t.Fatalf("the refused delete removed the row, or the read failed: %s", raw)
	}
}

func TestIssue2400_ThePlatformDatabaseIsRefusedAtSave(t *testing.T) {
	admin := connect(t)
	port := os.Getenv("DEV_PG_PORT")
	if port == "" {
		port = "5432"
	}
	name := "acc-2400-own-" + unique1579()
	path := "/api/v1/admin/connection-instances/appdb/" + name
	t.Cleanup(func() { admin.rest(http.MethodDelete, path, http.NoBody) })
	// 127.0.0.1 rather than the localhost the platform's own DSN names: a
	// comparison of host names would let this through.
	status, body := admin.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"config": map[string]any{
			"host": "127.0.0.1", "port": port, "database": "mcp_platform",
			"user": "platform", "password": "platform_secret", "sslmode": "disable",
		},
		"description": "Acceptance #2400: the platform's own database",
	}))
	text, _ := json.Marshal(body)
	if status < 400 || !strings.Contains(string(text), "platform's own database") {
		t.Fatalf("saving the platform's database answered %d: %s", status, text)
	}
	if status, _ := admin.rest(http.MethodGet, path, http.NoBody); status != http.StatusNotFound {
		t.Fatalf("the refused connection was stored (GET answered %d)", status)
	}
}

func TestIssue2400_ThePasswordIsNeverReadBack(t *testing.T) {
	admin := connectFor(t, 10*time.Minute)
	k := newCanary2363(t)
	port := os.Getenv("DEV_PG_TARGET_PORT")
	if port == "" {
		port = "5442"
	}
	password := k.plant("postgres-password")
	name := "acc-2400-pw-" + k.stamp[3:9]
	path := "/api/v1/admin/connection-instances/appdb/" + name
	status, body := k.rest(admin, http.MethodPut, path, map[string]any{
		"config": map[string]any{
			"host": "localhost", "port": port, "database": "orders", "user": "orders_app",
			"password": password, "sslmode": "disable",
		},
		"description": "Acceptance #2400 " + k.control,
	})
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the connection answered %d: %s", status, body)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, path, http.NoBody) })
	k.rest(admin, http.MethodGet, path, nil)
	k.rest(admin, http.MethodPost, path+"/test", map[string]any{})
	k.rest(admin, http.MethodGet, "/api/v1/admin/connection-instances", nil)
	k.rest(admin, http.MethodGet, "/api/v1/admin/config/export", nil)
	// The wrong password: the call fails at sign-in, the path whose error text
	// a driver most often fills with the connection's settings.
	ok, text := k.call(admin, "manage_script", map[string]any{
		"command": "run_draft", "name": "acceptance-2400-pw-" + k.stamp,
		"source": "def main():\n    \"\"\"Reads through the connection.\"\"\"\n    platform.query(\"SELECT 1\", connection=\"" + name + "\")\n",
	})
	if !ok || !strings.Contains(text, "could not reach its database") {
		t.Fatalf("a statement with the wrong password: %v %s", ok, text)
	}
	// The dev stack's own password, from the file configuration, is redacted in
	// the export as well.
	k.know("orders connection password (file config)", "orders_secret")

	k.awaitWriters(admin)
	k.searchDatabase()
	k.searchFiles()
	k.searchResponses()
	for _, sink := range []string{"table", "response"} {
		if !k.found[sink] {
			t.Errorf("the control value was not found in any %s; the search would miss a leak there", sink)
		}
	}
	fmt.Fprintf(os.Stderr, "planted %d values; searched %d responses\n", len(k.values), len(k.responses))
}
