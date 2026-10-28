//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// Acceptance for #2272: the audit events view reads a bounded amount of
// audit_logs however large it grows. 30,000 rows are written under one tool
// name straight into the dev database (no surface writes them faster), and
// the admin API is read as the portal reads it: the total stops at 10,000 and
// says so, a deep page answers as fast as the first, and the filter values
// list the tool.
//
// The routes take query parameters only; there is no JSON body to send in
// another form.

// issue2272Rows is how many events the criterion writes: three times the cap.
const issue2272Rows = 30_000

// issue2272Target is the time each read of the view is held to.
const issue2272Target = 2 * time.Second

// issue2272Seed writes the rows and removes them when the test ends.
func issue2272Seed(t *testing.T, tool string) {
	t.Helper()
	db := devDB(t)
	if _, err := db.ExecContext(t.Context(), `
INSERT INTO audit_logs (id, timestamp, duration_ms, request_id, user_id, user_email, persona, tool_name, toolkit_kind,
  toolkit_name, connection, success, error_message, created_date, session_id, source, event_kind)
SELECT left(md5($1 || g::text), 32), now() - g * interval '1 second', 1, '', 'acc-2272', 'acc-2272@example.com', 'admin',
       $1, 'acceptance', 'acceptance', '', true, '', (now() - g * interval '1 second')::date, '', 'mcp', 'mcp_tool_call'
FROM generate_series(1, $2) g`, tool, issue2272Rows); err != nil {
		t.Fatalf("writing %d audit rows: %v", issue2272Rows, err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM audit_logs WHERE tool_name = $1`, tool); err != nil {
			t.Logf("removing the rows: %v", err)
		}
	})
}

// issue2272Get reads an admin route and how long it took.
func issue2272Get(t *testing.T, c *client, path string) (map[string]any, time.Duration) {
	t.Helper()
	start := time.Now()
	status, out := c.rest(http.MethodGet, path, http.NoBody)
	took := time.Since(start)
	if status != http.StatusOK {
		t.Fatalf("GET %s: HTTP %d %v", path, status, out)
	}
	return out, took
}

// TestIssue2272_TheTotalStopsAtTheCap is the bounded count: past 10,000
// matching events the total is 10,000 and total_capped says there are more.
func TestIssue2272_TheTotalStopsAtTheCap(t *testing.T) {
	c := connect(t)
	tool := fmt.Sprintf("acc_2272_%d", time.Now().UnixNano())
	issue2272Seed(t, tool)
	out, took := issue2272Get(t, c, "/api/v1/admin/audit/events?per_page=20&tool_name="+url.QueryEscape(tool))
	if fmt.Sprint(out["total"]) != "10000" || out["total_capped"] != true {
		t.Fatalf("the total of %d matching events was %v (total_capped %v), want 10000 and true",
			issue2272Rows, out["total"], out["total_capped"])
	}
	if took > issue2272Target {
		t.Fatalf("the view answered in %s, want under %s", took, issue2272Target)
	}
	few, _ := issue2272Get(t, c, "/api/v1/admin/audit/events?per_page=20&tool_name=acc_2272_none")
	if fmt.Sprint(few["total"]) != "0" || few["total_capped"] != nil {
		t.Fatalf("a filter matching nothing answered total %v, total_capped %v", few["total"], few["total_capped"])
	}
}

// TestIssue2272_ADeepPageIsAsFastAsTheFirst is the paging criterion, read as
// the portal reads it (20 a page): the last page the cap reaches answers
// within the target, as the first does, and holds that page's events.
func TestIssue2272_ADeepPageIsAsFastAsTheFirst(t *testing.T) {
	c := connect(t)
	tool := fmt.Sprintf("acc_2272_%d", time.Now().UnixNano())
	issue2272Seed(t, tool)
	base := "/api/v1/admin/audit/events?per_page=20&tool_name=" + url.QueryEscape(tool)
	first, firstTook := issue2272Get(t, c, base+"&page=1")
	deep, deepTook := issue2272Get(t, c, base+"&page=500")
	for name, page := range map[string]map[string]any{"page 1": first, "page 500": deep} {
		if rows, _ := page["data"].([]any); len(rows) != 20 {
			t.Fatalf("%s held %d events, want 20", name, len(rows))
		}
	}
	if firstTook > issue2272Target || deepTook > issue2272Target {
		t.Fatalf("page 1 took %s and page 500 %s, want both under %s", firstTook, deepTook, issue2272Target)
	}
	t.Logf("page 1: %s, page 500: %s", firstTook, deepTook)
}

// TestIssue2272_TheFilterValuesAreListed is the dropdown criterion: the
// filter values answer within the target and list the tool, the user and its
// label, with no null among them.
func TestIssue2272_TheFilterValuesAreListed(t *testing.T) {
	c := connect(t)
	tool := fmt.Sprintf("acc_2272_%d", time.Now().UnixNano())
	issue2272Seed(t, tool)
	out, took := issue2272Get(t, c, "/api/v1/admin/audit/events/filters")
	if took > issue2272Target {
		t.Fatalf("the filter values answered in %s, want under %s", took, issue2272Target)
	}
	has := func(key, want string) bool {
		vals, _ := out[key].([]any)
		for _, v := range vals {
			if v == nil {
				t.Fatalf("%s lists a null", key)
			}
			if v == want {
				return true
			}
		}
		return false
	}
	if !has("tools", tool) || !has("users", "acc-2272") || !has("event_kinds", "mcp_tool_call") || !has("sources", "mcp") {
		t.Fatalf("the filter values do not list what the rows hold: %v", out)
	}
	labels, _ := out["user_labels"].(map[string]any)
	if labels["acc-2272"] != "acc-2272@example.com" {
		t.Fatalf("the user labels do not name acc-2272's address: %v", labels)
	}
}
