//go:build integration

package acceptance

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #2811: a call refused because its arguments do not match the tool's
// schema names each unexpected property with the accepted name it most likely
// meant, each value of the wrong type, and every parameter the tool accepts.
// search keeps its intent parameter; there are no aliases.
//
// Wire forms: the criteria send the wrong forms on purpose, as literal
// tools/call params: search's query (undeclared) as a string, trino_query's
// query (undeclared) as a string, manage_site's dry_run (a boolean) as the
// string "true", and an undeclared property as a number on every tool.

// issue2811Refusal calls a tool and returns its refusal text, failing when it
// ran.
func issue2811Refusal(t *testing.T, c *client, tool string, args map[string]any) string {
	t.Helper()
	res, _, err := c.callRaw(tool, args)
	if err != nil {
		t.Fatalf("%s: transport error: %v", tool, err)
	}
	text := allText(res)
	if !res.IsError {
		t.Fatalf("%s ran with arguments its schema does not declare: %s", tool, text)
	}
	return text
}

// TestIssue2811_SearchNamesIntent: search called with query is refused naming
// intent as the parameter it takes.
func TestIssue2811_SearchNamesIntent(t *testing.T) {
	text := issue2811Refusal(t, connect(t), "search", map[string]any{"query": "revenue", "purpose": "Acceptance #2811: an argument the tool does not take."})
	for _, want := range []string{`search does not take "query"; did you mean "intent"?`, "intent (string)", "invalid_arguments"} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal lacks %q:\n%s", want, text)
		}
	}
}

// TestIssue2811_TrinoQueryNamesSQL: trino_query called with query names sql.
func TestIssue2811_TrinoQueryNamesSQL(t *testing.T) {
	text := issue2811Refusal(t, connect(t), "trino_query", map[string]any{"query": "SELECT 1", "purpose": "Acceptance #2811."})
	if !strings.Contains(text, `did you mean "sql"?`) {
		t.Errorf("the refusal does not name sql:\n%s", text)
	}
}

// TestIssue2811_AStringWhereABooleanIsDeclared names the property, the type it
// takes and the type sent.
func TestIssue2811_AStringWhereABooleanIsDeclared(t *testing.T) {
	text := issue2811Refusal(t, connect(t), "manage_site", map[string]any{"command": "publish", "site": "acc-2811", "dry_run": "true"})
	if !strings.Contains(text, "dry_run must be boolean; the call sent string.") {
		t.Errorf("the refusal does not name the property and both types:\n%s", text)
	}
}

// TestIssue2811_EveryToolListsItsParameters: every listed tool whose schema
// refuses an undeclared property says what it accepts.
func TestIssue2811_EveryToolListsItsParameters(t *testing.T) {
	c := connect(t)
	refusing := 0
	for _, tool := range c.tools() {
		if tool.Name == "platform_info" {
			continue
		}
		res, _, err := c.callRaw(tool.Name, map[string]any{"zz_acceptance_2811": 1})
		text := ""
		if res != nil {
			text = allText(res)
		}
		if err != nil || !res.IsError || !strings.Contains(text, "invalid_arguments") {
			continue
		}
		refusing++
		if !strings.Contains(text, tool.Name+" accepts: ") {
			t.Errorf("%s's argument refusal does not list its parameters:\n%.400s", tool.Name, text)
		}
	}
	if refusing < 10 {
		t.Fatalf("only %d tools refused an undeclared property; the check proves nothing", refusing)
	}
}

// allText is every text block of a result joined, and its structured content
// when it has no text: a refusal or a routing note can follow the first block.
func allText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
			b.WriteString("\n")
		}
	}
	if b.Len() == 0 && res.StructuredContent != nil {
		raw, _ := json.Marshal(res.StructuredContent)
		b.Write(raw)
	}
	return b.String()
}
