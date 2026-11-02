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

// Acceptance for #2400: the postgres connection kind. Every criterion runs
// through the real MCP client against the dev stack, whose second PostgreSQL
// (service postgres-target, dev/fixtures/postgres-target.sql) dev/platform.yaml
// names as `orders` and as the read_only `orders-ro`.
//
// Wire forms: params is sent as a JSON array (bound to $1, $2, ...) and as a
// JSON object (bound to @name), and each gives the same rows; statements is a
// JSON array of {sql, params} objects; sql, connection and purpose are
// strings; limit and timeout_seconds are integers. The admin connection route
// takes one JSON object.

const (
	pg2400Conn     = "orders"
	pg2400ReadOnly = "orders-ro"
	pg2400Purpose  = "Acceptance for #2400: the postgres connection kind reads and writes the order system."
)

// pgCall2400 calls a postgres tool and decodes its structured result.
func pgCall2400(t *testing.T, c *client, tool string, args map[string]any) (map[string]any, string, bool) {
	t.Helper()
	args["purpose"] = pg2400Purpose
	res, text, err := c.callRaw(tool, args)
	if err != nil {
		t.Fatalf("%s: transport error: %v", tool, err)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(text), &out)
	full, _ := json.Marshal(res)
	return out, string(full), !res.IsError
}

func TestIssue2400_QueryReturnsTypedRowsForEveryMappedType(t *testing.T) {
	c := connect(t)
	list, _, ok := pgCall2400(t, c, "postgres_query", map[string]any{
		"connection": pg2400Conn, "sql": "SELECT * FROM type_samples WHERE id = $1", "params": []any{1},
	})
	if !ok {
		t.Fatalf("postgres_query with a params list failed: %v", list)
	}
	named, _, ok := pgCall2400(t, c, "postgres_query", map[string]any{
		"connection": pg2400Conn, "sql": "SELECT * FROM type_samples WHERE id = @id", "params": map[string]any{"id": 1},
	})
	if !ok {
		t.Fatalf("postgres_query with a params object failed: %v", named)
	}
	a, _ := json.Marshal(list["rows"])
	b, _ := json.Marshal(named["rows"])
	if string(a) != string(b) {
		t.Fatalf("a params list and a params object gave different rows:\n%s\n%s", a, b)
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
	row, _ := rows[0].([]any)
	for i, raw := range cols {
		col, _ := raw.(map[string]any)
		name, _ := col["name"].(string)
		got, _ := json.Marshal(row[i])
		if string(got) != want[name].value || col["type"] != want[name].typ {
			t.Errorf("%s = %s (%v), want %s (%s)", name, got, col["type"], want[name].value, want[name].typ)
		}
	}
}

func TestIssue2400_AFailedStatementsListLeavesNoRow(t *testing.T) {
	c := connect(t)
	stamp := unique1579()
	first, second := "first-"+stamp+"@acme.example", "second-"+stamp+"@acme.example"
	out, full, ok := pgCall2400(t, c, "postgres_execute", map[string]any{
		"connection": pg2400Conn,
		"statements": []any{
			map[string]any{"sql": "INSERT INTO customers (email, name) VALUES ($1, $2)", "params": []any{first, "First"}},
			map[string]any{"sql": "INSERT INTO customers (email, name) VALUES (@email, @name)", "params": map[string]any{"email": second, "name": "Second"}},
			map[string]any{"sql": "INSERT INTO customers (email, name) VALUES ($1, $2)", "params": []any{"ada@acme.example", "Duplicate"}},
		},
	})
	if ok {
		t.Fatalf("a statements list whose third statement fails succeeded: %v", out)
	}
	if !strings.Contains(full, "in statement 2") || !strings.Contains(full, "23505") {
		t.Errorf("the failure does not name the third statement and its SQLSTATE: %s", full)
	}
	got, _, ok := pgCall2400(t, c, "postgres_query", map[string]any{
		"connection": pg2400Conn, "sql": "SELECT count(*) AS n FROM customers WHERE email = ANY($1)",
		"params": []any{[]any{first, second}},
	})
	if !ok {
		t.Fatalf("counting the rows failed: %v", got)
	}
	if n, _ := json.Marshal(got["rows"]); string(n) != "[[0]]" {
		t.Fatalf("the failed list left rows behind: %s", n)
	}

	// The same list without the failing statement commits both rows.
	out, _, ok = pgCall2400(t, c, "postgres_execute", map[string]any{
		"connection": pg2400Conn,
		"statements": []any{
			map[string]any{"sql": "INSERT INTO customers (email, name) VALUES ($1, $2) RETURNING id", "params": []any{first, "First"}},
			map[string]any{"sql": "DELETE FROM customers WHERE email = @email", "params": map[string]any{"email": first}},
		},
	})
	if !ok || out["committed"] != true {
		t.Fatalf("a statements list that succeeds did not commit: %v", out)
	}
}

func TestIssue2400_ExecuteOnAReadOnlyConnectionIsRefused(t *testing.T) {
	c := connect(t)
	out, full, ok := pgCall2400(t, c, "postgres_execute", map[string]any{
		"connection": pg2400ReadOnly, "sql": "DELETE FROM orders WHERE id = $1", "params": []any{1},
	})
	if ok || !strings.Contains(full, "read_only_connection") {
		t.Fatalf("postgres_execute on a read_only connection: %v %s", out, full)
	}
	got, _, ok := pgCall2400(t, c, "postgres_query", map[string]any{
		"connection": pg2400ReadOnly, "sql": "SELECT count(*) FROM orders WHERE id = 1",
	})
	if !ok {
		t.Fatalf("postgres_query on the read_only connection failed: %v", got)
	}
	if n, _ := json.Marshal(got["rows"]); string(n) != "[[1]]" {
		t.Fatalf("the refused delete removed the row: %s", n)
	}
}

func TestIssue2400_ThePlatformDatabaseIsRefusedAtSave(t *testing.T) {
	admin := connect(t)
	port := os.Getenv("DEV_PG_PORT")
	if port == "" {
		port = "5432"
	}
	name := "acc-2400-own-" + unique1579()
	path := "/api/v1/admin/connection-instances/postgres/" + name
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

func TestIssue2400_AParameterAndADetailValueReachNoSink(t *testing.T) {
	admin := connectFor(t, 10*time.Minute)
	k := newCanary2363(t)
	listValue := k.stamp + "-pg-param-list-Zq7@acme.example"
	objectValue := k.stamp + "-pg-param-object-Zq7@acme.example"
	// The caller is handed the DETAIL of its own failure, so the values are
	// searched everywhere but the responses.
	k.returned[listValue] = "postgres param (list)"
	k.returned[objectValue] = "postgres param (object)"

	for _, args := range []map[string]any{
		{"connection": pg2400Conn, "sql": "INSERT INTO customers (email, name) VALUES ($1, $2)", "params": []any{listValue, "Canary"}},
		{"connection": pg2400Conn, "sql": "INSERT INTO customers (email, name) VALUES (@email, @name)", "params": map[string]any{"email": objectValue, "name": "Canary"}},
	} {
		if out, full, ok := pgCall2400(t, admin, "postgres_execute", args); !ok {
			t.Fatalf("the insert failed: %v %s", out, full)
		}
		// The same insert again: a unique violation whose DETAIL repeats the
		// value, "Key (email)=(...) already exists".
		_, full, ok := pgCall2400(t, admin, "postgres_execute", args)
		if ok {
			t.Fatal("a duplicate insert succeeded")
		}
		if !strings.Contains(full, "already exists") {
			t.Fatalf("the caller was not handed the DETAIL: %s", full)
		}
	}
	t.Cleanup(func() {
		pgCall2400(t, admin, "postgres_execute", map[string]any{
			"connection": pg2400Conn, "sql": "DELETE FROM customers WHERE email = ANY($1)",
			"params": []any{[]any{listValue, objectValue}},
		})
	})
	// The values in the statement text of a query reading them back would be
	// audited as written; read with params, so the statement holds none.
	if out, _, ok := pgCall2400(t, admin, "postgres_query", map[string]any{
		"connection": pg2400Conn, "sql": "SELECT name FROM customers WHERE email = $1", "params": []any{listValue},
	}); !ok {
		t.Fatalf("reading the row back failed: %v", out)
	}

	k.awaitWriters(admin)

	rows := admin.list("/api/v1/admin/audit/events?tool_name=postgres_execute&per_page=50&session_id=" + admin.sessionID)
	raw, _ := json.Marshal(rows)
	if !strings.Contains(string(raw), `"$1":"string"`) || !strings.Contains(string(raw), `"email":"string"`) {
		t.Errorf("the audit rows do not keep each parameter's name and type: %s", raw)
	}
	if !strings.Contains(string(raw), "INSERT INTO customers") {
		t.Errorf("the audit rows do not keep the statement: %s", raw)
	}
	calls := admin.list("/api/v1/admin/calls?tool_name=postgres_execute&per_page=50")
	callsRaw, _ := json.Marshal(calls)
	if !strings.Contains(string(callsRaw), "INSERT INTO customers") {
		t.Errorf("the call catalog holds no postgres_execute record: %s", callsRaw)
	}
	k.report("audit route", raw, k.patterns(true))
	k.report("call catalog route", callsRaw, k.patterns(true))
	k.searchDatabase()
	k.searchFiles()
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
	path := "/api/v1/admin/connection-instances/postgres/" + name
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
	ok, text := k.call(admin, "postgres_query", map[string]any{
		"connection": name, "sql": "SELECT 1", "purpose": pg2400Purpose,
	})
	if ok || !strings.Contains(text, "could not reach its database") {
		t.Fatalf("a call with the wrong password: %v %s", ok, text)
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
