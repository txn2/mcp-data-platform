//go:build integration

package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2795: the audit breakdown counts the failed calls of any
// window by the category each was refused with, by where it came from and by
// persona, exactly, past the event list's 10,000-row count cap.
//
// Wire forms: GET /api/v1/admin/audit/metrics/breakdown takes group_by,
// success (true or false), start_time and end_time (RFC 3339) as query
// strings; GET /api/v1/admin/audit/events the same filters; trino_query and
// trino_execute take string connection, sql and purpose through the MCP
// client. No parameter admits a second form.
//
// Other criteria run against the same stack at the same time, so a count is
// held to the audit rows of its window as the database holds them, not to the
// number of calls this criterion made.

// breakdown2795 is the breakdown's counts by dimension for a window.
func breakdown2795(t *testing.T, c *client, groupBy, success string, from, to time.Time) map[string]int {
	t.Helper()
	q := url.Values{
		"group_by": {groupBy}, "limit": {"100"},
		"start_time": {from.UTC().Format(time.RFC3339Nano)}, "end_time": {to.UTC().Format(time.RFC3339Nano)},
	}
	if success != "" {
		q.Set("success", success)
	}
	status, body := c.restText("/api/v1/admin/audit/metrics/breakdown?" + q.Encode())
	if status != http.StatusOK {
		t.Fatalf("the breakdown by %s: %d %s", groupBy, status, body)
	}
	var rows []struct {
		Dimension string `json:"dimension"`
		Count     int    `json:"count"`
	}
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("decoding the breakdown: %v: %s", err, body)
	}
	out := map[string]int{}
	for _, r := range rows {
		out[r.Dimension] = r.Count
	}
	return out
}

// dbCounts2795 is the failed audit rows of a window grouped by column, as the
// database holds them.
func dbCounts2795(t *testing.T, column string, from, to time.Time) map[string]int {
	t.Helper()
	db := platformDB2539(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT COALESCE(`+column+`, ''), count(*) FROM audit_logs
		WHERE timestamp >= $1 AND timestamp <= $2 AND success = false GROUP BY 1`, from, to) // #nosec G202 -- a column the criterion names
	if err != nil {
		t.Fatalf("counting audit_logs by %s: %v", column, err)
	}
	defer rows.Close() //nolint:errcheck // read to the end
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			t.Fatal(err)
		}
		out[k] = n
	}
	return out
}

// TestIssue2795_RefusalsAndScriptFailuresAreCountedByCategoryAndSource: calls
// the real client makes against a table it never read are refused with
// SCHEMA_REQUIRED and counted under schema_required; a script's failing call
// is counted under source script; every bucket of the window equals what
// audit_logs holds for it.
func TestIssue2795_RefusalsAndScriptFailuresAreCountedByCategoryAndSource(t *testing.T) {
	c := connect(t)
	name := "acc2795-" + unique1579()
	src := `def main():
    """Reads a table that does not exist, and reports what it was answered."""
    out = platform.call("trino_query", {"connection": "` + conn1918 + `", "sql": "SELECT 1 FROM memory.acc2795_missing.nothing", "purpose": "Acceptance for #2795: a failing call from a script."}, on_error = "return")
    platform.result(out)
`
	c.saveScript(map[string]any{"command": "create", "name": name, "description": "Acceptance #2795: a failing call.", "source": src}, nil)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	from := time.Now().Add(-time.Second)
	// The gate opens a table once it has refused it (#2386), so each refusal
	// is of a table of its own.
	const refusals = 3
	for range refusals {
		schema, table := issue2386Table(t, c)
		res, text := issue2386Raw(t, c, "trino_query", issue2386Query(schema, table))
		if !res.IsError || !strings.HasPrefix(text, "SCHEMA_REQUIRED") {
			t.Fatalf("a query on an unread table was not refused with SCHEMA_REQUIRED: %s", text)
		}
	}
	c.call("run_script", map[string]any{"name": name, "args": map[string]any{}, "wait_seconds": 120})

	// The window closes now; the audit rows of calls inside it are written
	// asynchronously, so the breakdown is read until it holds this criterion's
	// own and agrees with the database on every bucket.
	to := time.Now()
	for _, column := range []string{"error_category", "source", "persona"} {
		waitFor(t, "the failures by "+column, func() (bool, string) {
			got := breakdown2795(t, c, column, "false", from, to)
			want := dbCounts2795(t, column, from, to)
			mine := true
			switch column {
			case "error_category":
				mine = got["schema_required"] >= refusals
			case "source":
				mine = got["script"] >= 1 && got["mcp"] >= refusals
			}
			return mine && fmt.Sprint(got) == fmt.Sprint(want),
				fmt.Sprintf("the breakdown counts %v, audit_logs holds %v", got, want)
		})
	}
}

// TestIssue2795_AWindowPastTheEventListsCapIsCountedExactly: a window holding
// 10,050 calls is counted as 10,050 by the breakdown, where the event list
// stops at 10,000 and says so.
func TestIssue2795_AWindowPastTheEventListsCapIsCountedExactly(t *testing.T) {
	c := connect(t)
	db := platformDB2539(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	// A minute ten days back that no other criterion writes into.
	at := time.Now().Add(-10 * 24 * time.Hour).Truncate(time.Minute)
	tag := "acc2795-" + unique1579()
	// Each row as the platform's own writer fills one, so the event list can
	// read it back as it reads any other.
	if _, err := db.ExecContext(ctx, `INSERT INTO audit_logs (id, timestamp, duration_ms, request_id, session_id, user_id, user_email,
			persona, tool_name, toolkit_kind, toolkit_name, connection, purpose, parameters, success, error_message, error_category,
			transport, source, event_kind, created_date)
		SELECT $1 || '-' || g, $2::timestamptz + make_interval(secs => g / 1000.0), 1, '', $1, $1, '',
			'analyst', 'search', '', '', '', '', '{}'::jsonb, g % 10 <> 0, '', CASE WHEN g % 10 = 0 THEN 'tool_error' ELSE '' END,
			'http', 'mcp', 'mcp_tool_call', ($2::timestamptz)::date
		  FROM generate_series(1, 10050) AS g`, tag, at); err != nil {
		t.Fatalf("writing the window's calls: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), `DELETE FROM audit_logs WHERE user_id = $1`, tag) })

	from, to := at, at.Add(time.Minute)
	total := 0
	for _, n := range breakdown2795(t, c, "source", "", from, to) {
		total += n
	}
	if total != 10050 {
		t.Fatalf("the breakdown counts %d calls in the window, want 10,050", total)
	}
	if failed := breakdown2795(t, c, "source", "false", from, to)["mcp"]; failed != 1005 {
		t.Fatalf("the breakdown counts %d failed calls, want 1,005", failed)
	}
	q := url.Values{"start_time": {from.UTC().Format(time.RFC3339Nano)}, "end_time": {to.UTC().Format(time.RFC3339Nano)}, "per_page": {"1"}}
	status, out := c.rest(http.MethodGet, "/api/v1/admin/audit/events?"+q.Encode(), http.NoBody)
	if status != http.StatusOK || out["total"] != float64(10000) || out["total_capped"] != true {
		t.Fatalf("the event list answered %d total=%v capped=%v, want 10,000 and capped", status, out["total"], out["total_capped"])
	}
}
