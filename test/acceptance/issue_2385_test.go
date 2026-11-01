//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// Issue #2385: the SQL tools' descriptions say to know a table's columns and
// types before writing SQL, and that a guessed column can return rows that
// look right and are wrong. No other tool carries it.
//
// Wire forms: tools/list takes no arguments.

func TestIssue2385_SQLToolDescriptionsRequireTheSchema(t *testing.T) {
	c := connect(t)
	sqlTools := map[string]bool{"trino_query": true, "trino_execute": true, "trino_export": true}
	seen := map[string]bool{}
	for _, tool := range c.tools() {
		carries := strings.Contains(tool.Description, "Never guess a column name") &&
			strings.Contains(tool.Description, "trino_describe_table") &&
			strings.Contains(tool.Description, "look right and are wrong")
		switch {
		case sqlTools[tool.Name]:
			seen[tool.Name] = true
			if !carries {
				t.Errorf("%s does not require the schema:\n%s", tool.Name, tool.Description)
			}
		case strings.Contains(tool.Description, "Never guess a column name"):
			t.Errorf("%s, not a SQL tool, carries the schema requirement", tool.Name)
		}
	}
	for name := range sqlTools {
		if !seen[name] {
			t.Errorf("%s is not listed", name)
		}
	}
}
