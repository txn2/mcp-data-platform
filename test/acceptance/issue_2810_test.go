//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// Issue #2810: the schema gate refuses only what can be guessed. A statement
// whose only reference to an unread table's columns is * or count(*) runs; a
// successful SELECT * is the read; count(*) is not; a statement naming any
// column of an unread table is refused with its columns, as before. The
// baseline instructions name both tools that count as the read.
//
// Wire forms: trino_query's connection, sql and purpose are strings, each sent
// as a JSON string; platform_info takes an empty object. No parameter touched
// admits a second form.

func issue2810Run(t *testing.T, c *client, sql string) (bool, string) {
	t.Helper()
	res, text := issue2386Raw(t, c, "trino_query", map[string]any{"connection": conn1918, "sql": sql})
	return res.IsError, text
}

// TestIssue2810_StarAndCountRunUnread: SELECT * ... LIMIT 5 and SELECT
// count(*) of a table no session read run with no refusal.
func TestIssue2810_StarAndCountRunUnread(t *testing.T) {
	for _, stmt := range []string{"SELECT * FROM memory.%s.%s LIMIT 5", "SELECT count(*) FROM memory.%s.%s"} {
		c := connect(t)
		schema, table := issue2386Table(t, c)
		sql := strings.Replace(strings.Replace(stmt, "%s", schema, 1), "%s", table, 1)
		if refused, text := issue2810Run(t, c, sql); refused {
			t.Errorf("%s was refused: %s", sql, text)
		}
	}
}

// TestIssue2810_AStarWithAColumnIsRefused: SELECT * with a WHERE naming a
// column is refused with the table's columns.
func TestIssue2810_AStarWithAColumnIsRefused(t *testing.T) {
	c := connect(t)
	schema, table := issue2386Table(t, c)
	refused, text := issue2810Run(t, c, "SELECT * FROM memory."+schema+"."+table+" WHERE id = 1")
	if !refused || !strings.HasPrefix(text, "SCHEMA_REQUIRED") || !strings.Contains(text, "- p_id bigint") {
		t.Fatalf("a statement naming a column ran or was refused without the schema: %s", text)
	}
	if !strings.Contains(text, "trino_describe_table") || !strings.Contains(text, "mcp:dataset") {
		t.Errorf("the refusal does not teach the read first: %s", text)
	}
}

// TestIssue2810_ASuccessfulStarIsTheRead: after SELECT * ... LIMIT 5 a
// statement naming a column of the table runs.
func TestIssue2810_ASuccessfulStarIsTheRead(t *testing.T) {
	c := connect(t)
	schema, table := issue2386Table(t, c)
	if refused, text := issue2810Run(t, c, "SELECT * FROM memory."+schema+"."+table+" LIMIT 5"); refused {
		t.Fatalf("SELECT * was refused: %s", text)
	}
	if refused, text := issue2810Run(t, c, "SELECT amount FROM memory."+schema+"."+table+" WHERE id = 123"); refused {
		t.Errorf("a column statement after SELECT * was refused: %s", text)
	}
}

// TestIssue2810_CountAloneIsNotTheRead: after count(*) alone a statement
// naming a column is refused.
func TestIssue2810_CountAloneIsNotTheRead(t *testing.T) {
	c := connect(t)
	schema, table := issue2386Table(t, c)
	if refused, text := issue2810Run(t, c, "SELECT count(*) FROM memory."+schema+"."+table); refused {
		t.Fatalf("count(*) was refused: %s", text)
	}
	if refused, text := issue2810Run(t, c, "SELECT amount FROM memory."+schema+"."+table+" WHERE id = 123"); !refused || !strings.HasPrefix(text, "SCHEMA_REQUIRED") {
		t.Errorf("a column statement after count(*) alone ran: %s", text)
	}
}

// TestIssue2810_TheBaselineNamesBothReads: platform_info's agent instructions
// name trino_describe_table and fetch as the read.
func TestIssue2810_TheBaselineNamesBothReads(t *testing.T) {
	info := connect(t).call("platform_info", map[string]any{})
	instructions, _ := info["agent_instructions"].(string)
	for _, want := range []string{"`trino_describe_table`", "`fetch` of its `mcp:dataset` reference"} {
		if !strings.Contains(instructions, want) {
			t.Errorf("the baseline does not name %s", want)
		}
	}
}
