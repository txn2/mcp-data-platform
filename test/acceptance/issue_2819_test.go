//go:build integration

package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Issue #2819: the audit reports agent traffic and success as they are. A
// table registered from a managed script's platform.call is audited with
// source script, and the same registration from an agent session with source
// mcp; a tool whose only non-successes are approval holds reads 100% with a
// hold count; and a tool's breakdown counts its tool calls, with other event
// kinds reported apart.
//
// Wire forms: manage_resource's action, filename, path, content and
// content_type are strings; manage_table's action, reference, connection and
// table_name are strings; manage_script's command, name, description and
// source are strings and allow_writes a boolean; the admin metrics routes are
// GETs with query parameters. No parameter touched admits a second form.

// issue2819Source waits for the table_register audit row of a table and
// returns its source.
func issue2819Source(t *testing.T, table string) string {
	t.Helper()
	db := devDB(t)
	var source string
	waitFor(t, "the table_register row for "+table, func() (bool, string) {
		err := db.QueryRowContext(context.Background(), `SELECT source FROM audit_logs
			WHERE tool_name = 'table_register' AND parameters ->> 'table' LIKE '%' || $1
			ORDER BY timestamp DESC LIMIT 1`, table).Scan(&source)
		if err != nil {
			return false, err.Error()
		}
		return true, ""
	})
	return source
}

// TestIssue2819_ARegistrationIsAuditedWithItsCallersSource: the same file
// registered by an agent reads source mcp, and by a script's platform.call
// source script.
func TestIssue2819_ARegistrationIsAuditedWithItsCallersSource(t *testing.T) {
	c := connect(t)
	stamp := time.Now().UnixNano()
	created := c.call("manage_resource", map[string]any{
		"action": "create", "display_name": fmt.Sprintf("Acceptance 2819 %d", stamp), "filename": fmt.Sprintf("acc2819-%d.csv", stamp), "path": "acceptance",
		"content": "store_id,units\n101,10\n102,20\n", "content_type": "text/csv",
		"description": "Acceptance #2819: a file registered by an agent and by a script.",
	})
	ref, _ := created["reference"].(string)
	if ref == "" {
		t.Fatalf("no resource: %v", created)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_resource", map[string]any{"action": "delete", "reference": ref})
	})

	agentTable := fmt.Sprintf("acc2819_agent_%d", stamp)
	register1583(t, c, ref, agentTable, nil)
	if got := issue2819Source(t, agentTable); got != "mcp" {
		t.Errorf("an agent's registration is audited with source %q, want mcp", got)
	}

	scriptTable := fmt.Sprintf("acc2819_script_%d", stamp)
	name := fmt.Sprintf("acceptance-2819-%d", stamp)
	source := fmt.Sprintf(`
def main():
    """Registers a table over the acceptance file."""
    got = platform.call("manage_table", {"action": "register", "reference": %q, "connection": %q, "table_name": %q})
    print("registered=" + str(got.get("registration_id", "")))
`, ref, scratchResourceConnection, scriptTable)
	c.saveScript(map[string]any{"command": "create", "name": name, "description": "Acceptance #2819: a registration a script makes.", "source": source}, nil)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	c.call("manage_script", map[string]any{"command": "run_draft", "name": name, "allow_writes": true})
	t.Cleanup(func() {
		listed := c.call("manage_table", map[string]any{"action": "list", "reference": ref})
		regs, _ := listed["table_registrations"].([]any)
		for _, r := range regs {
			reg, _ := r.(map[string]any)
			if id, _ := reg["registration_id"].(string); id != "" {
				_, _, _ = c.callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": id})
			}
		}
	})
	if got := issue2819Source(t, scriptTable); got != "script" {
		t.Errorf("a script's registration is audited with source %q, want script", got)
	}
}

// issue2819Seed writes audit rows for a tool name no one calls: two
// successes, two approval holds, and a resource read recorded under its name.
func issue2819Seed(t *testing.T, tool string) {
	t.Helper()
	db := devDB(t)
	rows := []struct {
		success  bool
		category string
		kind     string
	}{{true, "", "mcp_tool_call"}, {true, "", "mcp_tool_call"}, {false, "input_required", "mcp_tool_call"}, {false, "input_required", "mcp_tool_call"}, {true, "", "resource_read"}}
	for i, r := range rows {
		if _, err := db.ExecContext(context.Background(), `INSERT INTO audit_logs (id, tool_name, success, error_category, event_kind, source, user_id)
			VALUES ($1, $2, $3, NULLIF($4, ''), $5, 'mcp', 'acceptance-2819')`,
			fmt.Sprintf("a2819%d%d", time.Now().UnixNano()%1e12, i), tool, r.success, r.category, r.kind); err != nil {
			t.Fatalf("seeding the audit: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM audit_logs WHERE tool_name = $1`, tool)
	})
}

// TestIssue2819_HoldsAreNotFailures: the tool's breakdown reads a 100%
// success rate with a hold count of two, and counts its four tool calls, not
// the resource read recorded under its name, which the event-kind breakdown
// reports apart.
func TestIssue2819_HoldsAreNotFailures(t *testing.T) {
	c := connect(t)
	tool := fmt.Sprintf("acc2819_tool_%d", time.Now().UnixNano())
	issue2819Seed(t, tool)
	entries := issue2819Breakdown(t, c, "group_by=tool_name&event_kind=mcp_tool_call&limit=100")
	found := false
	for _, e := range entries {
		if e["dimension"] != tool {
			continue
		}
		found = true
		if e["count"] != float64(4) || e["success_rate"] != float64(1) || e["hold_count"] != float64(2) {
			t.Errorf("the tool's row = %v; want 4 calls, 100%% success and 2 holds", e)
		}
	}
	if !found {
		t.Fatalf("the breakdown does not hold the seeded tool: %v", entries)
	}
	kinds := false
	for _, e := range issue2819Breakdown(t, c, "group_by=event_kind&limit=100") {
		kinds = kinds || e["dimension"] == "resource_read"
	}
	if !kinds {
		t.Error("the event-kind breakdown does not report resource reads apart")
	}
}

// issue2819Breakdown reads the admin breakdown route, whose body is a JSON
// array.
func issue2819Breakdown(t *testing.T, c *client, query string) []map[string]any {
	t.Helper()
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, c.base+"/api/v1/admin/audit/metrics/breakdown?"+query, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	var entries []map[string]any
	if err := json.NewDecoder(res.Body).Decode(&entries); err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("breakdown %s = %d: %v", query, res.StatusCode, err)
	}
	return entries
}
