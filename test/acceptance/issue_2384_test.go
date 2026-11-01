//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"
)

// Issue #2384: the call catalog's records are how a later session finds a past
// call for a dataset, and a statement naming schema.table (or a bare table)
// recorded no target at all, while an api call's target was a form no catalog
// reference finds.
//
// Wire forms: sql, connection, operation_id, method and path are strings, and
// each is sent as a JSON string, the one form their schemas admit.

// issue2384Calls waits for the call records an admin lookup by target returns
// for one session, and returns them once there are want of them.
func issue2384Calls(t *testing.T, admin *client, target, sessionID string, want int) []map[string]any {
	t.Helper()
	path := "/api/v1/admin/calls?target=" + url.QueryEscape(target) + "&session_id=" + url.QueryEscape(sessionID)
	var rows []any
	for attempt := range recordAttempts {
		if attempt > 0 {
			time.Sleep(recordRetryPause)
		}
		if rows = admin.list(path); len(rows) >= want {
			break
		}
	}
	if len(rows) < want {
		t.Fatalf("a lookup by target %s found %d call records for the session, want %d", target, len(rows), want)
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		rec, _ := r.(map[string]any)
		out = append(out, rec)
	}
	return out
}

// TestIssue2384_APartlyQualifiedTableIsATarget is the first criterion: a
// trino_query naming schema.table, and one naming a bare table, on a
// connection with a configured catalog and schema, each record the
// mcp:dataset:(trino,...) reference of that table.
func TestIssue2384_APartlyQualifiedTableIsATarget(t *testing.T) {
	c := connect(t)
	schema := schema1918(t, c, "2384", "orders")
	exec1918(c, "CREATE TABLE memory."+schema+".orders (id bigint, amount decimal(12,2))")

	name := fmt.Sprintf("acc-2384-%d", time.Now().UnixNano())
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/trino/"+name, http.NoBody) })
	status, out := c.rest(http.MethodPut, "/api/v1/admin/connection-instances/trino/"+name, jsonBody(t, map[string]any{
		"config": map[string]any{
			"host": "localhost", "port": 9283, "user": "dev", "ssl": false,
			"catalog": "memory", "schema": schema,
		},
		"description": "Acceptance #2384: a connection whose catalog and schema complete a partly qualified name.",
	}))
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("creating the connection answered %d %v", status, out)
	}

	q := connect(t)
	q.call("search", map[string]any{"intent": "orders in schema " + schema, "purpose": "Acceptance #2384: discovery before a query."})
	for _, sql := range []string{"SELECT amount FROM " + schema + ".orders", "SELECT amount FROM orders"} {
		q.call("trino_query", map[string]any{
			"connection": name, "sql": sql,
			"purpose": "Acceptance #2384: a query naming the table without its catalog.",
		})
	}

	target := "mcp:dataset:(trino," + name + ",memory." + schema + ".orders)"
	recs := issue2384Calls(t, c, target, q.sessionID, 2)
	for _, rec := range recs {
		targets, _ := rec["targets"].([]any)
		if !slices.Contains(targets, any(target)) {
			t.Errorf("record %v carries targets %v, want %s", rec["id"], targets, target)
		}
	}
}

// TestIssue2384_AnAPICallIsFoundByItsOperationDataset is the second criterion:
// an api_invoke_endpoint call, by operation id and by method and path, records
// the operation's catalog dataset, so a lookup by that reference finds both.
func TestIssue2384_AnAPICallIsFoundByItsOperationDataset(t *testing.T) {
	c := connect(t)
	ops := operations1626(t, c.call("api_discover", map[string]any{
		"connection": apiTestConnection, "query": "whoami", "ranking": "lexical",
	}))
	var opID, method, path string
	for _, op := range ops {
		id, _ := op["operation_id"].(string)
		p, _ := op["path"].(string)
		m, _ := op["method"].(string)
		if id != "" && p != "" && m == http.MethodGet {
			opID, method, path = id, m, p
			break
		}
	}
	if opID == "" {
		t.Fatalf("api_discover named no GET operation with an id on %s: %v", apiTestConnection, ops)
	}

	q := connect(t)
	q.call("api_invoke_endpoint", map[string]any{
		"connection": apiTestConnection, "operation_id": opID,
		"purpose": "Acceptance #2384: an api call addressed by operation id.",
	})
	q.call("api_invoke_endpoint", map[string]any{
		"connection": apiTestConnection, "method": method, "path": path,
		"purpose": "Acceptance #2384: an api call addressed by method and path.",
	})

	target := "mcp:dataset:(api," + apiTestConnection + "," + opID + ")"
	issue2384Calls(t, c, target, q.sessionID, 2)
}
