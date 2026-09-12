//go:build integration

package acceptance

// Issue #1691: trino_export was the one tool in the admin listing with no
// title. It is registered in this repository rather than inherited from
// mcp-trino, and its registration set a name and a description and no title,
// while the two sibling exports (api_export, graphql_export) set one.
//
// The criteria are read on both surfaces the title reaches: the admin listing
// an operator reads, and the tools/list a client reads. #1675 fixed the same
// gap on graphql_export by asserting the listed title, so this asserts it the
// same way.
//
// Wire forms: this ticket changes no tool parameter.
// GET /api/v1/admin/tools takes no body, and tools/list takes no parameters,
// so each surface has one wire form and each criterion sends it.

import (
	"net/http"
	"testing"
)

const issue1691Tool = "trino_export"

// issue1691AdminRow returns the admin listing's row for a tool, or nil when
// the deployment does not register it.
func issue1691AdminRow(t *testing.T, c *client, name string) map[string]any {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/admin/tools", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/admin/tools: HTTP %d %v", status, body)
	}
	rows, _ := body["tools"].([]any)
	if len(rows) == 0 {
		t.Fatalf("the admin listing names no tools: %v", body)
	}
	for _, row := range rows {
		tool, _ := row.(map[string]any)
		if got, _ := tool["name"].(string); got == name {
			return tool
		}
	}
	return nil
}

// TestIssue1691_TrinoExportIsListedWithATitle reads the surface the defect was
// seen on. A deployment with no trino connection does not register the tool,
// and a criterion cannot pass on a tool that is not there, so the absence
// fails rather than skips.
func TestIssue1691_TrinoExportIsListedWithATitle(t *testing.T) {
	c := connect(t)

	row := issue1691AdminRow(t, c, issue1691Tool)
	if row == nil {
		t.Fatalf("the admin listing does not name %s; this deployment needs a trino connection", issue1691Tool)
	}
	title, _ := row["title"].(string)
	if title == "" {
		t.Errorf("the admin listing carries %s with no title: %v", issue1691Tool, row)
	}
}

// TestIssue1691_TrinoExportCarriesTheSameTitleOnTheWire holds the two surfaces
// together. The admin listing and tools/list are built from different places,
// which is how graphql_export came to be listed in one and absent from the
// other (#1675); a title present in one and empty in the other is the same
// shape of drift.
func TestIssue1691_TrinoExportCarriesTheSameTitleOnTheWire(t *testing.T) {
	c := connect(t)

	row := issue1691AdminRow(t, c, issue1691Tool)
	if row == nil {
		t.Fatalf("the admin listing does not name %s; this deployment needs a trino connection", issue1691Tool)
	}
	adminTitle, _ := row["title"].(string)

	for _, tool := range c.tools() {
		if tool.Name != issue1691Tool {
			continue
		}
		if tool.Title == "" {
			t.Fatalf("tools/list carries %s with no title", issue1691Tool)
		}
		if tool.Title != adminTitle {
			t.Errorf("%s is titled %q on the wire and %q in the admin listing", issue1691Tool, tool.Title, adminTitle)
		}
		return
	}
	t.Fatalf("tools/list does not carry %s", issue1691Tool)
}
