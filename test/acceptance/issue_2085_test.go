//go:build integration

package acceptance

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #2085: v1.143.0 refused a script's timeout_seconds above
// scripts.sql.max_timeout_seconds, so a scheduled script saved earlier, when
// mcp-trino cut the request to 300 seconds, failed every run after the
// upgrade. A version saved before the ceiling was enforced now runs the
// request at the ceiling and says so; a version saved since is refused as
// before. These criteria save a script through manage_script, then rewrite
// its stored source to the one a pre-1.143.0 save could have stored (a
// current save refuses it, which is the point) and mark the version as
// migration 000188 marks every version that existed when it ran, and run it
// through run_script against the dev stack's Trino.
//
// Wire forms: manage_script's `command`, `name`, `description`, `source` and
// `run_id` are strings; run_script's `name` is a string and `wait_seconds` a
// number. Each is typed in its schema and admits that one form, sent as
// literal params. timeout_seconds is a script argument the host passes to
// trino_query as an integer, through platform.query and through a
// platform.call args dict.

// saved2085 saves a script whose two statements ask for 300 seconds, then
// stores the same source asking for 600 with the version marked as saved
// before the ceiling or not, and returns its name.
func saved2085(t *testing.T, c *client, label string, predates bool) string {
	t.Helper()
	name := fmt.Sprintf("acc-2085-%s-%d", label, time.Now().UnixNano())
	c.saveScript(map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2085: " + label + ".",
		"source": fmt.Sprintf(`def main():
    """Counts one row under a long statement limit."""
    rows = platform.query("SELECT 1 AS n", connection = %q, timeout_seconds = 300)["rows"]
    called = platform.call("trino_query", {"connection": %q, "sql": "SELECT 2 AS n", "timeout_seconds": 300})
    platform.result({"n": rows[0]["n"], "called": called["rows"][0]["n"]})
`, trinoConnection2073, trinoConnection2073),
	}, nil)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the dev database: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx := t.Context()
	for _, stmt := range []string{
		`UPDATE scripts SET source_code = replace(replace(source_code,
		    'timeout_seconds = 300', 'timeout_seconds = 600'), '"timeout_seconds": 300', '"timeout_seconds": 600')
		  WHERE name = $1`,
		`UPDATE script_versions SET source_code = replace(replace(source_code,
		    'timeout_seconds = 300', 'timeout_seconds = 600'), '"timeout_seconds": 300', '"timeout_seconds": 600'),
		    predates_statement_cap = $2
		  WHERE script_id = (SELECT id FROM scripts WHERE name = $1)`,
	} {
		args := []any{name}
		if strings.Contains(stmt, "$2") {
			args = append(args, predates)
		}
		if _, err := db.ExecContext(ctx, stmt, args...); err != nil {
			t.Fatalf("storing the pre-1.143.0 source: %v", err)
		}
	}
	return name
}

// TestIssue2085_AVersionSavedBeforeTheCeilingRunsAtIt: the run succeeds, both
// statements ran at the 300-second ceiling, get_run reports the limit as
// clamped, and the run's log says each request was lowered and why.
func TestIssue2085_AVersionSavedBeforeTheCeilingRunsAtIt(t *testing.T) {
	admin := connect(t)
	name := saved2085(t, admin, "predates", true)

	run := run2073(t, admin, name)
	if run["status"] != "succeeded" {
		t.Fatalf("a version saved before the ceiling did not run: %v: %v", run["status"], run["error"])
	}
	result, _ := run["result"].(map[string]any)
	if result["n"] != float64(1) || result["called"] != float64(2) {
		t.Errorf("the two statements did not both return their row: %v", result)
	}
	slowest, _ := run["slowest_statement"].(map[string]any)
	t.Logf("slowest_statement: %v", slowest)
	if slowest["limit_ms"] != float64(300000) || slowest["limit_source"] != "clamped" {
		t.Errorf("get_run does not report the statement at the clamped 300-second limit: %v", slowest)
	}
	log, _ := run["log"].(string)
	t.Logf("the run's log:\n%s", log)
	if strings.Count(log, "WARN statement timeout: trino_query asked for timeout_seconds=600; it ran at 300") != 2 ||
		!strings.Contains(log, "saved before that limit was enforced") {
		t.Errorf("the run's log does not say each request was lowered to the ceiling:\n%s", log)
	}
}

// TestIssue2085_AVersionSavedSinceIsStillRefused: the same source in a
// version saved under the ceiling fails the run naming the ceiling and its
// key, as #2073 intends.
func TestIssue2085_AVersionSavedSinceIsStillRefused(t *testing.T) {
	admin := connect(t)
	name := saved2085(t, admin, "since", false)

	run := run2073(t, admin, name)
	errText, _ := run["error"].(string)
	if run["status"] != "failed" ||
		!strings.Contains(errText, "timeout_seconds is 600; the most a statement may run on this deployment is 300 (scripts.sql.max_timeout_seconds)") {
		t.Errorf("a version saved under the ceiling is not refused by name: %v: %s", run["status"], errText)
	}
}
