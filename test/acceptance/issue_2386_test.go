//go:build integration

package acceptance

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// Issue #2386: a SQL statement against a table whose columns the session has
// not read is refused before it runs, and the refusal carries the table's
// columns, types and catalog descriptions. A describe, a fetch of the dataset,
// or the refusal itself opens the table; the non-SQL tools are never refused
// for this reason.
//
// Wire forms: sql, connection, catalog, schema, table, reference and
// entity_urn are strings, and each is sent as a JSON string; every schema the
// criteria touch admits that one form.

// issue2386Table creates a memory table on the scratch connection with a
// record id named p_id beside a plain id, the shape a guessed column gets
// wrong, and returns its schema and table names.
func issue2386Table(t *testing.T, c *client) (schema, table string) {
	t.Helper()
	schema = fmt.Sprintf("acc2386_%d", time.Now().UnixNano())
	table = "orders"
	exec1918(c, "CREATE SCHEMA memory."+schema)
	exec1918(c, "CREATE TABLE memory."+schema+"."+table+" (p_id bigint, id bigint, amount decimal(12,2))")
	exec1918(c, "INSERT INTO memory."+schema+"."+table+" VALUES (123, 7, 10.50), (7, 123, 99.00)")
	t.Cleanup(func() {
		admin := connect(t)
		for _, stmt := range []string{"DROP TABLE IF EXISTS memory." + schema + "." + table, "DROP SCHEMA IF EXISTS memory." + schema} {
			_, _, _ = admin.callRaw("trino_execute", map[string]any{
				"connection": conn1918, "sql": stmt, "purpose": "Acceptance: removing a criterion's table.",
			})
		}
	})
	return schema, table
}

// issue2386Raw calls a tool with the session handle and nothing else: no
// resend after a refusal, so the gate is met as it answers.
func issue2386Raw(t *testing.T, c *client, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	args["session_id"] = c.sessionID
	if _, ok := args["purpose"]; !ok {
		args["purpose"] = "Acceptance: checking how SQL against an unread table is answered."
	}
	res, err := c.session.CallTool(c.ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: transport error: %v", name, err)
	}
	var b strings.Builder
	for _, content := range res.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return res, b.String()
}

func issue2386Query(schema, table string) map[string]any {
	return map[string]any{
		"connection": conn1918,
		"sql":        "SELECT amount FROM memory." + schema + "." + table + " WHERE id = 123",
	}
}

func TestIssue2386_UnreadTableIsRefusedWithItsSchemaAndDescriptions(t *testing.T) {
	c := connect(t)
	schema, table := issue2386Table(t, c)

	// A catalog description for p_id, applied the way an agent applies one.
	store, db := issue1917Store(t)
	ref := catalog.DatasetRef("trino", conn1918, "memory", schema, table)
	if _, err := store.PutDataset(context.Background(), catalog.DatasetInput{
		Ref: ref, Type: catalog.DatasetTable,
		Columns: []catalog.ColumnInput{{Name: "p_id", DataType: "bigint"}, {Name: "id", DataType: "bigint"}, {Name: "amount", DataType: "decimal(12,2)"}},
	}); err != nil {
		t.Fatalf("PutDataset: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM catalog_entities WHERE reference = $1", ref.String())
	})
	issue1920Apply(t, c, ref.String(), issue1920Change("update_description", "column:p_id", "The order's record id; id is the customer."))

	res, text := issue2386Raw(t, c, "trino_query", issue2386Query(schema, table))
	if !res.IsError || !strings.HasPrefix(text, "SCHEMA_REQUIRED") {
		t.Fatalf("a query on a table never described ran or failed otherwise: %s", text)
	}
	for _, want := range []string{
		"Table memory." + schema + "." + table + " (connection " + conn1918 + ")",
		"- p_id bigint: The order's record id; id is the customer.",
		"- id bigint",
		"- amount decimal(12,2)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, text)
		}
	}

	// The refusal delivered the schema: the same statement resent runs.
	res, text = issue2386Raw(t, c, "trino_query", issue2386Query(schema, table))
	if res.IsError {
		t.Fatalf("the refused query resent unchanged did not run: %s", text)
	}
}

func TestIssue2386_DescribeOrFetchOpensTheTable(t *testing.T) {
	c := connect(t)
	schema, table := issue2386Table(t, c)

	issue2386Raw(t, c, "trino_describe_table", map[string]any{
		"connection": conn1918, "catalog": "memory", "schema": schema, "table": table,
	})
	if res, text := issue2386Raw(t, c, "trino_query", issue2386Query(schema, table)); res.IsError {
		t.Fatalf("after trino_describe_table the query was refused: %s", text)
	}

	// fetch of the dataset reference opens a second table.
	exec1918(c, "CREATE TABLE memory."+schema+".lines (p_id bigint, sku varchar)")
	t.Cleanup(func() {
		_, _, _ = connect(t).callRaw("trino_execute", map[string]any{
			"connection": conn1918, "sql": "DROP TABLE IF EXISTS memory." + schema + ".lines", "purpose": "Acceptance: cleanup.",
		})
	})
	store, db := issue1917Store(t)
	ref := catalog.DatasetRef("trino", conn1918, "memory", schema, "lines")
	if _, err := store.PutDataset(context.Background(), catalog.DatasetInput{
		Ref: ref, Type: catalog.DatasetTable,
		Columns: []catalog.ColumnInput{{Name: "p_id", DataType: "bigint"}, {Name: "sku", DataType: "varchar"}},
	}); err != nil {
		t.Fatalf("PutDataset: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM catalog_entities WHERE reference = $1", ref.String())
	})
	if res, text := issue2386Raw(t, c, "fetch", map[string]any{
		"reference": ref.String(), "purpose": "Acceptance: reading a table's schema before querying it.",
	}); res.IsError {
		t.Fatalf("fetch: %s", text)
	}
	if res, text := issue2386Raw(t, c, "trino_query", map[string]any{
		"connection": conn1918, "sql": "SELECT sku FROM memory." + schema + ".lines",
	}); res.IsError {
		t.Fatalf("after fetch of the dataset the query was refused: %s", text)
	}
}

func TestIssue2386_ReadHoldsOnEveryReplica(t *testing.T) {
	a, b := connectReplicaPair(t)
	schema, table := issue2386Table(t, a)
	issue2386Raw(t, a, "trino_describe_table", map[string]any{
		"connection": conn1918, "catalog": "memory", "schema": schema, "table": table,
	})
	if res, text := issue2386Raw(t, b, "trino_query", issue2386Query(schema, table)); res.IsError {
		t.Fatalf("a describe on one replica did not open the table on the other: %s", text)
	}
}

func TestIssue2386_NonSQLToolsAreNeverRefused(t *testing.T) {
	c := connect(t)
	for name, args := range map[string]map[string]any{
		"api_invoke_endpoint": {"connection": "api-test-fixture", "method": "GET", "path": "/status/200", "sql": "SELECT * FROM never_read"},
		"graphql_discover":    {"connection": "graphql-test-fixture", "sql": "SELECT * FROM never_read"},
	} {
		_, text := issue2386Raw(t, c, name, args)
		if strings.Contains(text, "SCHEMA_REQUIRED") {
			t.Errorf("%s was refused by the schema gate: %s", name, text)
		}
	}
}

// issue2386OffConfig is a platform on the dev stack's Trino with the schema
// gate's refusal turned off.
const issue2386OffConfig = `
server:
  name: acceptance-2386
  transport: http
  address: "%s"
auth:
  api_keys:
    enabled: true
    keys:
      - key: "acceptance-2386-key"
        name: "acceptance"
        roles: ["admin"]
personas:
  admin:
    display_name: "Administrator"
    roles: ["admin"]
    tools:
      allow: ["*"]
    connections:
      allow: ["*"]
workflow:
  require_search: false
  require_schema: false
toolkits:
  trino:
    enabled: true
    instances:
      dev:
        host: "localhost"
        port: 9283
        user: "dev"
        catalog: "memory"
        schema: "default"
        ssl: false
        read_only: false
`

func TestIssue2386_SettingOffRefusesNothing(t *testing.T) {
	c := connect(t)
	schema, table := issue2386Table(t, c)

	p := startPlatform(t, issue2386OffConfig)
	p.serving(t, 2*time.Minute)
	cs, info := issue2008Session(t, p.base, "acceptance-2386-key", issue2008Revisions[len(issue2008Revisions)-1])
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "trino_query", Arguments: map[string]any{
		"session_id": info["session_id"], "connection": "dev",
		"purpose": "Acceptance: checking a query runs with the schema gate's refusal off.",
		"sql":     "SELECT amount FROM memory." + schema + "." + table + " WHERE id = 123",
	}})
	if err != nil {
		t.Fatalf("trino_query: %v", err)
	}
	if res.IsError {
		t.Fatalf("with require_schema false a query on an unread table was refused: %s", firstText(res))
	}
}
