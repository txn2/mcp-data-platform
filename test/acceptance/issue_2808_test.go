//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// Issue #2808: trino_explain is no longer registered; a plan is read by
// sending EXPLAIN (TYPE ...) through trino_query, whose description says so.
// A persona that still names trino_explain starts with a warning
// (TestIssue2807_APersonaNamingARemovedToolStillStarts covers it with the
// other retired names).
//
// Wire forms: trino_query's connection, sql and purpose are strings, sent as
// strings; tools/list takes no arguments. No parameter touched admits a
// second form.

// TestIssue2808_NoTrinoExplain: tools/list holds no trino_explain.
func TestIssue2808_NoTrinoExplain(t *testing.T) {
	for _, tool := range connect(t).tools() {
		if tool.Name == "trino_explain" {
			t.Fatal("tools/list still holds trino_explain")
		}
		if tool.Name == "trino_query" && !strings.Contains(tool.Description, "EXPLAIN (TYPE DISTRIBUTED)") {
			t.Errorf("trino_query's description does not say how to read a plan:\n%s", tool.Description)
		}
	}
}

// TestIssue2808_TrinoQueryReturnsAPlan: EXPLAIN (TYPE DISTRIBUTED) SELECT ...
// through trino_query returns the plan.
func TestIssue2808_TrinoQueryReturnsAPlan(t *testing.T) {
	c := connect(t)
	out := c.call("trino_query", map[string]any{
		"connection": issue1870Conn,
		"sql":        "EXPLAIN (TYPE DISTRIBUTED) SELECT 1 AS one",
		"purpose":    "Acceptance #2808: reading a query plan through trino_query.",
	})
	if plan := asJSON(out); !strings.Contains(plan, "Fragment") {
		t.Errorf("no distributed plan came back: %.500s", plan)
	}
}
