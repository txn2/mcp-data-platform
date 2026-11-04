//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2454: a result larger than max_rows read in pages with
// platform.query_iter.
//
// Wire forms: manage_script takes command, name and source as strings; the
// admin connection route takes one JSON object whose max_rows and
// iter_max_rows are JSON integers. query_iter's page_rows is an integer in
// the script source.

// iterConn2454 saves an appdb connection on the dev target's orders database
// with max_rows 1000 and the loop bound given, and returns its name.
func iterConn2454(t *testing.T, admin *client, iterMaxRows int) string {
	t.Helper()
	conn := "acc-2454-" + unique1579()
	path := "/api/v1/admin/connection-instances/appdb/" + conn
	cfg := map[string]any{
		"host": "localhost", "port": targetPort(), "database": "orders", "user": "orders_app", "password": "orders_secret",
		"sslmode": "disable", "max_conns": 2, "max_rows": 1000, "iter_max_rows": iterMaxRows,
	}
	if status, body := admin.rest(http.MethodPut, path, jsonBody(t, map[string]any{"config": cfg, "description": "Acceptance #2454"})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the connection answered %d: %v", status, body)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, path, http.NoBody) })
	return conn
}

// iterate2454 saves a script that loops over :rows rows of conn in pages of
// 5000 and counts them, and returns a function that runs it as a platform run
// (a draft's step limit is a tenth of a run's) and answers its get_run.
func iterate2454(t *testing.T, c *client, conn string) func(rows int) map[string]any {
	t.Helper()
	name := "acc-2454-iterate-" + unique1579()
	created := c.saveScript(map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2454: a large result read in pages.",
		"params": []any{map[string]any{"name": "rows", "type": "int", "required": true}},
		"source": fmt.Sprintf(`def main():
    """Counts a large result a page at a time."""
    n = 0
    for row in platform.query_iter("%s", "SELECT g AS n FROM generate_series(1, :rows) g", params = {"rows": run.params["rows"]}, page_rows = 5000):
        if row["n"] > 0:
            n += 1
    print(n)
`, conn),
	}, nil)
	if created["status"] != "created" {
		t.Fatalf("manage_script create: %v", created)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	return func(rows int) map[string]any {
		run := c.call("run_script", map[string]any{"name": name, "args": map[string]any{"rows": rows}, "wait_seconds": 240})
		if run["status"] != "succeeded" {
			t.Fatalf("iterating %d rows failed: %v", rows, run)
		}
		return c.call("manage_script", map[string]any{"command": "get_run", "run_id": run["run_id"]})
	}
}

func TestIssue2454_A250000RowResultIsReadInPagesHoldingOnePage(t *testing.T) {
	admin := connect(t)
	conn := iterConn2454(t, admin, 1000000)
	iterate := iterate2454(t, admin, conn)
	big := iterate(250000)
	if log, _ := big["log"].(string); strings.TrimSpace(log) != "250000" {
		t.Fatalf("counted %q rows, want 250000 on a connection whose max_rows is 1000", log)
	}
	// The baseline is the same loop over two pages: a loop that holds one
	// page at a time peaks the same however many pages it reads, where one
	// holding them all would peak fifty times higher.
	small := iterate(10000)
	peak := func(got map[string]any) float64 {
		m, _ := got["metrics"].(map[string]any)
		v, _ := m["peak_memory_bytes"].(float64)
		return v
	}
	bigPeak, smallPeak := peak(big), peak(small)
	t.Logf("peak memory: %.0f bytes over 50 pages, %.0f over 2", bigPeak, smallPeak)
	// A page of 5000 rows is well over 100 KB as the script's values: a
	// smaller peak means the page was not measured at all.
	if smallPeak < 100_000 {
		t.Fatalf("the two-page run reports a peak of %.0f bytes; the page it held is not in it: %v", smallPeak, small)
	}
	if bigPeak > 2*smallPeak+(1<<20) {
		t.Errorf("the 50-page loop peaked at %.0f bytes against %.0f for 2 pages; one page plus the baseline is the bound", bigPeak, smallPeak)
	}
}

func TestIssue2454_ALoopThatBreaksEarlyLeavesNoCursorOrTransaction(t *testing.T) {
	admin := connect(t)
	conn := iterConn2454(t, admin, 1000000)
	got := draft2428(admin, "acceptance-2454-break", `
def main():
    """Reads one page of a large result and stops."""
    for row in platform.query_iter("`+conn+`", "SELECT g FROM generate_series(1, 100000) g", page_rows = 100):
        if row["g"] == 50:
            break
    print("stopped")
`, false)
	if got["status"] != "succeeded" {
		t.Fatalf("the run failed: %v", got)
	}
	waitFor(t, "no cursor transaction left open on orders", func() (bool, string) {
		n := strings.TrimSpace(targetPSQL(t, "SELECT count(*) FROM pg_stat_activity WHERE datname = 'orders' "+
			"AND application_name = 'mcp-data-platform' AND state LIKE 'idle in transaction%'"))
		return n == "0", n + " sessions idle in transaction"
	})
	recording, _ := got["recording"].(string)
	rec := asJSON(admin.call("manage_script", map[string]any{"command": "recording", "run_id": recording}))
	if !strings.Contains(rec, "appdb.cursor_close") {
		t.Errorf("the loop that broke closed no cursor: %s", rec)
	}
}

func TestIssue2454_ALoopPastTheRowBoundFailsNamingIt(t *testing.T) {
	admin := connect(t)
	conn := iterConn2454(t, admin, 1000)
	got := draft2428(admin, "acceptance-2454-bound", `
def main():
    """Reads past the connection's loop bound."""
    n = 0
    for row in platform.query_iter("`+conn+`", "SELECT g FROM generate_series(1, 2000) g", page_rows = 300):
        n += 1
    print("finished with", n)
`, false)
	if got["status"] == "succeeded" {
		t.Fatalf("the loop past iter_max_rows succeeded: %v", got)
	}
	text := asJSON(got)
	if !strings.Contains(text, "iter_max_rows (1000)") {
		t.Errorf("the failure does not name the bound: %s", text)
	}
	if strings.Contains(text, "finished with") {
		t.Errorf("the loop ended as though the result were whole: %s", text)
	}
}
