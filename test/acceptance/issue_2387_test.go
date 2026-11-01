//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// Issue #2387: a SQL statement that fails on an unknown column or a type
// mismatch comes back with the columns and types of each table it names. A
// failure unrelated to the schema comes back as it is.
//
// Wire forms: sql, connection, catalog, schema and table are strings, sent as
// JSON strings; each schema touched admits that one form.

func TestIssue2387_SchemaFailureCarriesTheColumns(t *testing.T) {
	c := connect(t)
	schema, table := issue2386Table(t, c)
	// Read the table first, so the statement reaches the engine and fails
	// there rather than at the schema gate.
	issue2386Raw(t, c, "trino_describe_table", map[string]any{
		"connection": conn1918, "catalog": "memory", "schema": schema, "table": table,
	})
	for name, sql := range map[string]string{
		"unknown column": "SELECT customer_id FROM memory." + schema + "." + table,
		"type mismatch":  "SELECT amount FROM memory." + schema + "." + table + " WHERE p_id = 'abc'",
	} {
		t.Run(name, func(t *testing.T) {
			res, text := issue2386Raw(t, c, "trino_query", map[string]any{"connection": conn1918, "sql": sql})
			if !res.IsError {
				t.Fatalf("the statement succeeded: %s", text)
			}
			for _, want := range []string{"These are the real columns", "- p_id bigint", "- id bigint", "- amount decimal(12,2)"} {
				if !strings.Contains(text, want) {
					t.Errorf("the failure does not carry %q:\n%s", want, text)
				}
			}
		})
	}
}

func TestIssue2387_OtherFailuresAreReturnedAsTheyAre(t *testing.T) {
	c := connect(t)
	// A catalog the engine does not have is not a column or type failure.
	res, text := issue2386Raw(t, c, "trino_query", map[string]any{
		"connection": conn1918, "sql": "SELECT 1 FROM no_such_catalog.s.t",
	})
	if !res.IsError {
		t.Fatalf("a query on a missing catalog succeeded: %s", text)
	}
	if strings.Contains(text, "These are the real columns") || strings.Contains(text, "SCHEMA_REQUIRED") {
		t.Errorf("a failure unrelated to columns was enriched or refused:\n%s", text)
	}
}
