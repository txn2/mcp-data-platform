//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #1998: a Trino connection's catalog and schema are the
// session's for every query on it (mcp-trino v1.6.0), so SQL that names a table
// without them resolves, and a connection that sets neither takes the default
// connection's, as it takes every other key it leaves unset.
//
// trino_query is called through the MCP client with string `connection`, `sql`
// and `purpose`; the admin API takes one JSON object per route.

const issue1998Purpose = "Acceptance for #1998: a connection's catalog and schema are the session's."

// issue1998Session reads the session catalog and schema a connection's
// queries run with.
func issue1998Session(t *testing.T, c *client, conn string) (catalog, schema any) {
	t.Helper()
	out := c.call("trino_query", map[string]any{
		"connection": conn, "purpose": issue1998Purpose,
		"sql": "SELECT current_catalog AS cat, current_schema AS sch",
	})
	rows, _ := out["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("%s: the query returned %v", conn, out)
	}
	row, _ := rows[0].(map[string]any)
	return row["cat"], row["sch"]
}

// TestIssue1998_TheConnectionsCatalogAndSchemaAreTheSessions runs
// SELECT current_catalog, current_schema on a connection configured with both.
func TestIssue1998_TheConnectionsCatalogAndSchemaAreTheSessions(t *testing.T) {
	c := connect(t)
	cat, sch := issue1998Session(t, c, issue1870Conn)
	if want := strings.Split(issue1870Schema, "."); cat != want[0] || sch != want[1] {
		t.Fatalf("%s runs with session %v.%v, want %s", issue1870Conn, cat, sch, issue1870Schema)
	}
}

// TestIssue1998_AnUnqualifiedViewNameResolves lands an event in a webhook
// source and reads it as `SELECT * FROM webhook_<source>` on the source's
// scratch connection, as the webhook documentation writes it.
func TestIssue1998_AnUnqualifiedViewNameResolves(t *testing.T) {
	c := connect(t)
	source := issue1870Name("unq1998")
	const secret = "acceptance-1998"
	issue1870Create(t, c, issue1870HMAC(source, secret, map[string]any{"event_id_path": "$.id"}))
	res, body := issue1870Signed(t, source, secret, "application/json", []byte(`{"id":"e1"}`))
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("posting the event: %d %s", res.StatusCode, body)
	}
	view := "webhook_" + strings.ReplaceAll(source, "-", "_")
	if n := issue1870Count(t, c, "SELECT count(*) FROM "+view); n != 1 {
		t.Fatalf("SELECT count(*) FROM %s on %s counted %d, want 1", view, issue1870Conn, n)
	}
}

// TestIssue1998_AConnectionWithoutCatalogOrSchemaInheritsTheDefaults
// registers a connection to the same Trino naming neither. Like every key it
// leaves unset (host, user, password), it takes the default connection's, as
// mcp-trino's multiserver configuration defines, so its session is the
// default connection's; a fully qualified name resolves as it always did.
func TestIssue1998_AConnectionWithoutCatalogOrSchemaInheritsTheDefaults(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-1998-bare-%d", time.Now().UnixNano())
	path := "/api/v1/admin/connection-instances/trino/" + name
	if status, out := c.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"description": "Acceptance #1998: a connection with no catalog or schema.",
		"config":      map[string]any{"host": "localhost", "port": 9283, "user": "dev", "ssl": false},
	})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("registering the connection: %d %v", status, out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, path, http.NoBody) })

	defCat, defSch := issue1998Session(t, c, "acme")
	if cat, sch := issue1998Session(t, c, name); cat != defCat || sch != defSch {
		t.Errorf("a connection naming no catalog runs with session %v.%v, want the default connection's %v.%v",
			cat, sch, defCat, defSch)
	}
	out := c.call("trino_query", map[string]any{
		"connection": name, "purpose": issue1998Purpose, "sql": "SELECT count(*) AS n FROM system.runtime.nodes",
	})
	if rows, _ := out["rows"].([]any); len(rows) != 1 {
		t.Errorf("a fully qualified name did not resolve: %v", out)
	}
}
