//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Issue #2073: a managed script's statement that runs past its timeout is
// reported as the statement's, not as Trino being unreachable. These criteria
// run scripts against the dev stack's own Trino through run_script and read
// the run back with get_run, as the owner of a script would. The statements
// are pure computation over generated rows, so how long one runs depends on
// nothing but the engine: the endless one is a cross join of three 10,000-row
// sequences, which no limit here outlasts.
//
// Wire forms: manage_script's `command`, `name`, `description`, `source` and
// `run_id` are strings and `allow_writes` a boolean; run_script's `name` is a string and `wait_seconds` a
// number; the admin connection route takes a JSON object. Each is typed in its
// schema and admits that one form, sent as literal params. timeout_seconds is
// a script argument the host passes to trino_query and trino_execute as an
// integer, and a platform.call passes it in its args dict the same way.

const (
	trinoConnection2073 = "acme"
	endlessSQL2073      = "SELECT count(*) AS n FROM UNNEST(sequence(1, 10000)) AS a(x) " +
		"CROSS JOIN UNNEST(sequence(1, 10000)) AS b(y) CROSS JOIN UNNEST(sequence(1, 10000)) AS c(z)"
	// mediumSQL2073 runs for a few seconds on the dev stack's Trino: past a
	// one-second limit, well inside a sixty-second one.
	mediumSQL2073 = "SELECT sum((x * y) % 7) AS n FROM UNNEST(sequence(1, 10000)) AS a(x) " +
		"CROSS JOIN UNNEST(sequence(1, 10000)) AS b(y)"
)

// saved2073 saves source as the administrator's script and returns its name.
func saved2073(t *testing.T, c *client, label, source string) string {
	t.Helper()
	name := fmt.Sprintf("acc-2073-%s-%d", label, time.Now().UnixNano())
	c.saveScript(map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2073: " + label + ".", "source": source,
	}, nil)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	return name
}

// run2073 runs a script to its end and returns the run as get_run reports it.
func run2073(t *testing.T, c *client, name string) map[string]any {
	t.Helper()
	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 120})
	return c.call("manage_script", map[string]any{"command": "get_run", "name": name, "run_id": run["run_id"]})
}

// queryIDPattern2073 reads the query id the run's error names.
var queryIDPattern2073 = regexp.MustCompile(`query (\d{8}_\d{6}_\d{5}_\w+)\)`)

// trinoQueryState2073 is the state and error Trino's own record of a query
// holds, read inside the dev stack's Trino container.
func trinoQueryState2073(t *testing.T, queryID string) string {
	t.Helper()
	sql := fmt.Sprintf("SELECT state || ' ' || coalesce(error_code, '') FROM system.runtime.queries WHERE query_id = '%s'", queryID)
	// The CLI's JVM writes its start-up notices to stderr; the row is stdout.
	out, err := exec.CommandContext(t.Context(), "docker", "exec", "acme-dev-trino", //nolint:gosec // a test querying its own stack
		"trino", "--output-format", "TSV", "--execute", sql).Output()
	if err != nil {
		t.Fatalf("reading query %s from Trino: %v\n%s", queryID, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestIssue2073_AStatementStoppedAtItsTimeoutIsTheScripts: a statement the
// coordinator accepted and stopped at its limit is recorded as the script's
// and not retryable; the error names the limit, the elapsed time and the
// query id and says the query was canceled; Trino's own record shows the
// query canceled; and get_run reports it as the run's slowest statement.
func TestIssue2073_AStatementStoppedAtItsTimeoutIsTheScripts(t *testing.T) {
	admin := connect(t)
	name := saved2073(t, admin, "stopped", fmt.Sprintf(`def main():
    """Counts a cross join no limit outlasts."""
    platform.query(%q, connection = %q, timeout_seconds = 2)
`, endlessSQL2073, trinoConnection2073))

	run := run2073(t, admin, name)
	if run["status"] != "failed" || run["cause"] != "script" || run["retryable"] == true {
		t.Fatalf("a statement stopped at its timeout is not recorded as the script's: status=%v cause=%v retryable=%v error=%v",
			run["status"], run["cause"], run["retryable"], run["error"])
	}
	errText, _ := run["error"].(string)
	t.Logf("the run's error:\n%s", errText)
	for _, want := range []string{"Trino stopped the statement at its 2s limit after ", "(statement timeout, query ", "the query was canceled."} {
		if !strings.Contains(errText, want) {
			t.Errorf("the run's error does not say %q:\n%s", want, errText)
		}
	}
	if strings.Contains(errText, "could not be reached") {
		t.Errorf("the run's error reads as Trino unreachable:\n%s", errText)
	}
	m := queryIDPattern2073.FindStringSubmatch(errText)
	if m == nil {
		t.Fatalf("the run's error names no query id:\n%s", errText)
	}
	if state := trinoQueryState2073(t, m[1]); !strings.HasPrefix(state, "FAILED USER_CANCELED") {
		t.Errorf("Trino's record of query %s is %q, want it canceled", m[1], state)
	}

	slowest, _ := run["slowest_statement"].(map[string]any)
	t.Logf("slowest_statement: %v", slowest)
	if slowest["timed_out"] != true || slowest["limit_ms"] != float64(2000) || slowest["query_id"] != m[1] {
		t.Errorf("get_run does not report the stopped statement as the slowest: %v", slowest)
	}
	if share, _ := slowest["share_of_limit"].(float64); share < 1 {
		t.Errorf("a stopped statement's share of its limit is %v, want at least 1", share)
	}
}

// TestIssue2073_AStatementThatNeverReachedTrinoStaysTheUpstreams: a statement
// sent to a coordinator nothing answers for, stopped by the same timeout,
// never reached Trino and is still the upstream's, retryable.
func TestIssue2073_AStatementThatNeverReachedTrinoStaysTheUpstreams(t *testing.T) {
	admin := connect(t)
	conn := fmt.Sprintf("acc-2073-dead-%d", time.Now().UnixNano())
	path := "/api/v1/admin/connection-instances/trino/" + conn
	// Port 9 is discard: nothing listens, so the client retries the dial
	// until the statement's limit stops it.
	status, body := admin.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"description": "Acceptance #2073: a coordinator nothing answers for.",
		"config":      map[string]any{"host": "127.0.0.1", "port": 9, "user": "acceptance"},
	}))
	t.Cleanup(func() { _, _ = admin.rest(http.MethodDelete, path, http.NoBody) })
	if status != http.StatusOK {
		t.Fatalf("creating the connection answered %d: %v", status, body)
	}
	name := saved2073(t, admin, "unreached", fmt.Sprintf(`def main():
    """Counts rows on a coordinator nothing answers for."""
    platform.query("SELECT 1", connection = %q, timeout_seconds = 3)
`, conn))

	run := run2073(t, admin, name)
	if run["cause"] != "upstream" || run["retryable"] != true {
		t.Fatalf("a statement that never reached Trino is not the upstream's: cause=%v retryable=%v error=%v",
			run["cause"], run["retryable"], run["error"])
	}
	if errText, _ := run["error"].(string); !strings.Contains(errText, "Trino could not be reached") {
		t.Errorf("the run's error does not say Trino could not be reached:\n%s", errText)
	}
}

// TestIssue2073_ALongerTimeoutRunsTheStatementToTheEnd: the same statement
// stopped at a one-second limit runs to the end given timeout_seconds within
// the ceiling, through platform.query and through a platform.call to
// trino_query.
func TestIssue2073_ALongerTimeoutRunsTheStatementToTheEnd(t *testing.T) {
	admin := connect(t)
	short := saved2073(t, admin, "short", fmt.Sprintf(`def main():
    """Sums a cross join under a one-second limit."""
    platform.query(%q, connection = %q, timeout_seconds = 1)
`, mediumSQL2073, trinoConnection2073))
	if run := run2073(t, admin, short); run["status"] != "failed" || run["cause"] != "script" {
		t.Fatalf("the statement was not stopped at one second, so it does not show the limit applies: %v / %v: %v",
			run["status"], run["cause"], run["error"])
	}

	long := saved2073(t, admin, "long", fmt.Sprintf(`def main():
    """Sums a cross join with time to finish."""
    rows = platform.query(%q, connection = %q, timeout_seconds = 60)["rows"]
    called = platform.call("trino_query", {"connection": %q, "sql": %q, "timeout_seconds": 60})
    platform.result({"n": rows[0]["n"], "called": called["rows"][0]["n"]})
`, mediumSQL2073, trinoConnection2073, trinoConnection2073, mediumSQL2073))
	run := run2073(t, admin, long)
	if run["status"] != "succeeded" {
		t.Fatalf("the statement given sixty seconds did not finish: %v: %v", run["status"], run["error"])
	}
	result, _ := run["result"].(map[string]any)
	if result["n"] == nil || result["n"] != result["called"] {
		t.Errorf("the two statements did not both return the sum: %v", result)
	}
	slowest, _ := run["slowest_statement"].(map[string]any)
	if slowest["limit_ms"] != float64(60000) || slowest["limit_source"] != "requested" || slowest["timed_out"] == true {
		t.Errorf("get_run does not report the slowest statement against its sixty-second limit: %v", slowest)
	}
}

// TestIssue2073_ATimeoutAboveTheCeilingIsRefusedByName: a timeout_seconds
// above the deployment's ceiling (300 on the dev stack) fails the run naming
// the ceiling and its key, from platform.execute and from a platform.call.
func TestIssue2073_ATimeoutAboveTheCeilingIsRefusedByName(t *testing.T) {
	admin := connect(t)
	for label, call := range map[string]string{
		"execute": fmt.Sprintf(`platform.execute("SELECT 1", connection = %q, timeout_seconds = 301)`, trinoConnection2073),
		"call":    fmt.Sprintf(`platform.call("trino_query", {"connection": %q, "sql": "SELECT 1", "timeout_seconds": 301})`, trinoConnection2073),
	} {
		out := admin.call("manage_script", map[string]any{
			// A draft refuses a write unless asked to make it, before any
			// argument of the call is read.
			"command": "run_draft", "allow_writes": true, "name": fmt.Sprintf("acc-2073-ceiling-%s-%d", label, time.Now().UnixNano()),
			"source": "def main():\n    \"\"\"Asks for more time than the deployment allows.\"\"\"\n    " + call + "\n",
		})
		errText, _ := out["error"].(string)
		if out["status"] != "failed" ||
			!strings.Contains(errText, "timeout_seconds is 301; the most a statement may run on this deployment is 300 (scripts.sql.max_timeout_seconds)") {
			t.Errorf("%s: a timeout above the ceiling is not refused by name: %v: %s", label, out["status"], errText)
		}
	}
	info := admin.call("platform_info", map[string]any{})
	limits, _ := info["limits"].(map[string]any)
	query, _ := limits["query"].(map[string]any)
	if query["script_max_timeout_seconds"] != float64(300) || query["script_max_timeout_key"] != "scripts.sql.max_timeout_seconds" {
		t.Errorf("platform_info does not report the ceiling: %v", query)
	}
}
