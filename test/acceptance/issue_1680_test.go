//go:build integration

package acceptance

// Issue #1680: the release gates accepted a tool inventory nobody had asserted
// on a running server. 1.131.0 shipped graphql_export named by its toolkit,
// carried by the admin listing, and unknown to the MCP server (#1675).
//
// The criteria here are the comparison that was missing, run against the
// deployment a caller talks to: what the platform says it registers and what a
// client can call are one set. It is deliberately not written against the
// graphql kind — the defect was in a kind, the gap was in the comparison — so
// it holds for whatever this deployment has configured.
//
// Wire forms: no parameter here admits more than one JSON form.
// GET /api/v1/admin/tools takes no body, and platform_info takes only the
// session handle the harness attaches.

import (
	"net/http"
	"sort"
	"testing"
)

// issue1680AdminTools returns the tool names the platform says it registers,
// which the admin listing builds from each toolkit's own Tools() list plus the
// platform's own tools.
func issue1680AdminTools(t *testing.T, c *client) []string {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/admin/tools", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/admin/tools: HTTP %d %v", status, body)
	}
	rows, _ := body["tools"].([]any)
	if len(rows) == 0 {
		t.Fatalf("the admin listing names no tools: %v", body)
	}
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		tool, _ := row.(map[string]any)
		// A hidden tool is one the operator's tools.deny removed from
		// tools/list on purpose, so it is not part of the comparison.
		if hidden, _ := tool["hidden"].(bool); hidden {
			continue
		}
		if name, _ := tool["name"].(string); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// issue1680ListedTools returns what this session's tools/list carries.
func issue1680ListedTools(t *testing.T, c *client) []string {
	t.Helper()
	tools := c.tools()
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

// TestIssue1680_EveryToolThePlatformNamesCanBeCalled is the criterion that was
// missing. A name in the admin listing with no handler on the server is a tool
// an operator sees, a persona can be granted, and no client can call.
func TestIssue1680_EveryToolThePlatformNamesCanBeCalled(t *testing.T) {
	c := connect(t)

	listed := make(map[string]bool)
	for _, name := range issue1680ListedTools(t, c) {
		listed[name] = true
	}
	var missing []string
	for _, name := range issue1680AdminTools(t, c) {
		if !listed[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("named by the platform and absent from tools/list: %v", missing)
	}
}

// TestIssue1680_EveryToolAClientCanCallIsNamedByThePlatform is the other
// direction. A handler on the server that the platform does not name is
// invisible to the admin surface, to platform_find_tools and to the persona
// gates, so nobody can grant it or take it away.
func TestIssue1680_EveryToolAClientCanCallIsNamedByThePlatform(t *testing.T) {
	c := connect(t)

	named := make(map[string]bool)
	for _, name := range issue1680AdminTools(t, c) {
		named[name] = true
	}
	var unclaimed []string
	for _, name := range issue1680ListedTools(t, c) {
		if !named[name] {
			unclaimed = append(unclaimed, name)
		}
	}
	if len(unclaimed) > 0 {
		t.Fatalf("callable through tools/list and named by no toolkit or platform tool: %v", unclaimed)
	}
}
