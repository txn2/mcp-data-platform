//go:build integration

package acceptance

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #2812: a connection sent to a tool that does not serve its kind is
// routed by the refusal; SQL naming a connection where its catalog goes fails
// with the catalog the connection reads; a read sent through trino_execute
// runs and says trino_query is the read tool; and the baseline instructions
// map each connection kind the deployment has to its tools.
//
// Wire forms: trino_query's and trino_execute's connection, sql and purpose
// are strings, each sent as a JSON string; manage_table's action, reference
// and connection are strings; platform_info and list_connections take an
// empty object. No parameter touched admits a second form.

const issue2812Purpose = "Acceptance #2812: sending a connection to the tool that serves it."

// TestIssue2812_AnApplicationDatabaseSentToTrinoQuery is refused naming the
// connection's kind and manage_schema.
func TestIssue2812_AnApplicationDatabaseSentToTrinoQuery(t *testing.T) {
	res, _, err := connect(t).callRaw("trino_query", map[string]any{"connection": "orders", "sql": "SELECT 1", "purpose": issue2812Purpose})
	text := textOf(res)
	if err != nil || !res.IsError {
		t.Fatalf("trino_query ran on an application database: %v %s", err, text)
	}
	for _, want := range []string{`"orders" is an application database connection`, "manage_schema", "platform.query in a managed script"} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal lacks %q:\n%s", want, text)
		}
	}
}

// TestIssue2812_AConnectionNamedAsACatalog: the acme connection reads catalog
// warehouse; SQL naming acme as a catalog, with a column or with count(*)
// alone, fails with Trino's error and the catalog it reads.
func TestIssue2812_AConnectionNamedAsACatalog(t *testing.T) {
	c := connect(t)
	for _, sql := range []string{
		"SELECT store_id FROM acme.inventory.inventory_levels LIMIT 1",
		"SELECT count(*) FROM acme.inventory.inventory_levels",
	} {
		res, _, err := c.callRaw("trino_query", map[string]any{"connection": "acme", "sql": sql, "purpose": issue2812Purpose})
		text := textOf(res)
		if err != nil || !res.IsError {
			t.Fatalf("%s ran: %v %s", sql, err, text)
		}
		if !strings.Contains(strings.ToLower(text), "catalog 'acme'") || !strings.Contains(text, `the connection acme reads catalog "warehouse"`) {
			t.Errorf("%s: the error does not carry the connection's catalog:\n%s", sql, text)
		}
	}
}

// TestIssue2812_AReadThroughTrinoExecute runs and notes the read tool.
func TestIssue2812_AReadThroughTrinoExecute(t *testing.T) {
	res, _, err := connect(t).callRaw("trino_execute", map[string]any{"connection": issue1870Conn, "sql": "SELECT 1 AS one", "purpose": issue2812Purpose})
	text := textOf(res)
	if err != nil || res.IsError {
		t.Fatalf("a read through trino_execute did not run: %v %s", err, text)
	}
	if !strings.Contains(text, "trino_query is the tool for it") {
		t.Errorf("the result does not name the read tool:\n%s", text)
	}
}

// TestIssue2812_ManageTableNamesTheScratchConnection: manage_table given a
// connection with no scratch catalog still names one that has it.
func TestIssue2812_ManageTableNamesTheScratchConnection(t *testing.T) {
	res, _, err := connect(t).callRaw("manage_table", map[string]any{"action": "check", "connection": "acme-staging"})
	text := textOf(res)
	if err != nil || !res.IsError {
		t.Fatalf("a connection with no scratch catalog was accepted: %v %s", err, text)
	}
	if !strings.Contains(text, "acme-scratch") {
		t.Errorf("the refusal does not name a scratch connection:\n%s", text)
	}
}

// TestIssue2812_TheBaselineMapsKindsTheDeploymentHas: platform_info's
// connection line lists every kind list_connections names, and no other.
func TestIssue2812_TheBaselineMapsKindsTheDeploymentHas(t *testing.T) {
	c := connect(t)
	instructions, _ := c.call("platform_info", map[string]any{})["agent_instructions"].(string)
	i := strings.Index(instructions, "Each connection is served by the tools of its kind")
	if i < 0 {
		t.Fatalf("the baseline carries no connection line:\n%s", instructions)
	}
	section := instructions[i:]
	if listed := asJSON(c.call("list_connections", map[string]any{})); strings.Contains(section, "- GraphQL:") && !strings.Contains(listed, `"kind":"graphql"`) {
		t.Errorf("the line names GraphQL, which the deployment has no connection of:\n%s\nlist_connections: %s", section, listed)
	}
	for _, want := range []string{"- Trino: ", "- application database: manage_schema, or platform.query in a managed script"} {
		if !strings.Contains(section, want) {
			t.Errorf("the line lacks %q:\n%s", want, section)
		}
	}
}

// textOf is allText of a result, "" for none.
func textOf(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	return allText(res)
}
