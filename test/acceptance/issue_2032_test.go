//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #2032: a failed Trino call says whose problem it is. These criteria
// run against the dev stack's own Trino: the `unreachable` catalog is a
// PostgreSQL source nothing listens on (dev/trino/catalog), the failure a
// source down for maintenance produces, and a table that does not exist is a
// statement's own mistake. The scripts are the owner key's, run with
// run_script on the dev stack's run worker.
//
// Wire forms: trino_query's `connection`, `sql` and `purpose` are strings;
// manage_script's `command`, `name`, `description`, `source` and `run_id` are
// strings; run_script's `name` is a string and `wait_seconds` a number. Each
// is typed in its schema and admits that one form, sent as a literal
// tools/call param. on_error, retry and testing.answer's error= are script
// arguments, not tool parameters.

const (
	trinoConnection2032 = "acme"
	unreachableSQL2032  = "SELECT count(*) AS n FROM unreachable.public.orders"
	missingTableSQL2032 = "SELECT count(*) AS n FROM memory.default.acc_2032_no_such_table"
)

// queryError2032 calls trino_query as an agent does and returns the error
// envelope its failure carries.
func queryError2032(t *testing.T, c *client, sql string) map[string]any {
	t.Helper()
	res, text, err := c.callRaw("trino_query", map[string]any{
		"connection": trinoConnection2032, "sql": sql,
		"purpose": "Acceptance #2032: a failed query says whose problem it is.",
	})
	if err != nil {
		t.Fatalf("trino_query: %v", err)
	}
	if !res.IsError {
		t.Fatalf("trino_query did not fail: %s", text)
	}
	sc, _ := res.StructuredContent.(map[string]any)
	env, _ := sc["error"].(map[string]any)
	if env == nil {
		t.Fatalf("the failure carries no structuredContent.error: %v\n%s", res.StructuredContent, text)
	}
	return env
}

func TestIssue2032_AnAgentReadsTheClassOfATrinoFailure(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)

	env := queryError2032(t, c, unreachableSQL2032)
	if env["code"] != "trino_query_failed" || env["category"] != "upstream_unavailable" || env["retryable"] != true {
		t.Errorf("an unreachable source is not classified as temporary: %v", env)
	}
	trino, _ := env["trino"].(map[string]any)
	if trino["error_type"] != "EXTERNAL" {
		t.Errorf("the envelope does not carry Trino's error type: %v", env)
	}

	env = queryError2032(t, c, missingTableSQL2032)
	if env["category"] != "client_input" || env["retryable"] != false {
		t.Errorf("a missing table is not classified as the statement's: %v", env)
	}
	trino, _ = env["trino"].(map[string]any)
	if trino["error_type"] != "USER_ERROR" {
		t.Errorf("the envelope does not carry USER_ERROR: %v", env)
	}
}

// saved2032 saves source as the owner's script and returns its name.
func saved2032(t *testing.T, c *client, label, source string) string {
	t.Helper()
	name := fmt.Sprintf("acc-2032-%s-%d", label, time.Now().UnixNano())
	c.saveScript(map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2032: " + label + ".", "source": source,
	}, nil)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	return name
}

// run2032 runs a script to its end and returns the run as get_run reports it.
func run2032(t *testing.T, c *client, name string) map[string]any {
	t.Helper()
	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 120})
	return c.call("manage_script", map[string]any{"command": "get_run", "name": name, "run_id": run["run_id"]})
}

func TestIssue2032_ARunEndedByAnUnreachableSourceIsTheUpstreams(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := saved2032(t, owner, "unreachable", fmt.Sprintf(`def main():
    """Merges the staged orders into a source that is down."""
    platform.execute(%q, connection = %q)
`, unreachableSQL2032, trinoConnection2032))

	run := run2032(t, owner, name)
	if run["status"] != "failed" || run["cause"] != "upstream" || run["retryable"] != true {
		t.Fatalf("the run is not recorded as the upstream's and retryable: status=%v cause=%v retryable=%v error=%v",
			run["status"], run["cause"], run["retryable"], run["error"])
	}
	errText, _ := run["error"].(string)
	if !strings.Contains(errText, "expected to pass on its next run") || !strings.Contains(errText, "EXTERNAL") {
		t.Errorf("the run's error does not say which failure it was: %s", errText)
	}
	if log, _ := run["log"].(string); strings.Contains(log, "retried") {
		t.Errorf("a statement was issued again by the host: %s", log)
	}
	notice := issue1934Failing(connectAs(t, devOwnerAPIKey).info, name)
	if notice == nil || notice["retryable"] != true || !strings.Contains(fmt.Sprintf("%v", notice), "expected to pass on its next run") {
		t.Errorf("the failing-automation briefing does not show the class: %v", notice)
	}
}

func TestIssue2032_AReadThatKeepsFailingIsRetriedThenTheUpstreams(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := saved2032(t, owner, "read", fmt.Sprintf(`def main():
    """Counts the orders in a source that is down."""
    platform.query(%q, connection = %q)
`, unreachableSQL2032, trinoConnection2032))

	run := run2032(t, owner, name)
	if run["cause"] != "upstream" || run["retryable"] != true {
		t.Fatalf("the run is not recorded as the upstream's: %v / %v: %v", run["cause"], run["retryable"], run["error"])
	}
	log, _ := run["log"].(string)
	for _, line := range []string{"waited 1s and retried (1 of 3)", "waited 2s and retried (2 of 3)", "waited 4s and retried (3 of 3)", "after 3 retries"} {
		if !strings.Contains(log, line) {
			t.Errorf("the run's log does not record %q:\n%s", line, log)
		}
	}
}

func TestIssue2032_AStatementFailureStaysTheScripts(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := saved2032(t, owner, "missing", fmt.Sprintf(`def main():
    """Counts a table that does not exist."""
    platform.query(%q, connection = %q)
`, missingTableSQL2032, trinoConnection2032))

	run := run2032(t, owner, name)
	if run["status"] != "failed" || run["cause"] != "script" || run["retryable"] == true {
		t.Fatalf("a missing table is not recorded as the script's: %v / %v / %v", run["status"], run["cause"], run["retryable"])
	}
	if errText, _ := run["error"].(string); !strings.Contains(errText, "Trino refused the statement (USER_ERROR") {
		t.Errorf("the run's error does not say the statement was refused: %s", errText)
	}
}

func TestIssue2032_OnErrorReturnHandsTheScriptTheEnvelope(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := saved2032(t, owner, "returned", fmt.Sprintf(`def main():
    """Merges the staged orders, and says when the source is down."""
    res = platform.execute(%q, connection = %q, on_error = "return")
    if "error" in res:
        e = res["error"]
        print("class:", e["category"], e["retryable"], e["trino"]["error_type"])
        if e["retryable"]:
            fail("warehouse unreachable; the next run retries", retryable = True)
        fail("MERGE failed: " + e["message"])
`, unreachableSQL2032, trinoConnection2032))

	run := run2032(t, owner, name)
	if run["cause"] != "transient" || run["retryable"] != true {
		t.Fatalf("the script's own retryable fail() is not what the run records: %v / %v: %v", run["cause"], run["retryable"], run["error"])
	}
	if log, _ := run["log"].(string); !strings.Contains(log, "class: upstream_unavailable True EXTERNAL") {
		t.Errorf("the script did not read the envelope: %s", log)
	}
}

// testing.answer(error = {...}) reaches the script's branch on a temporary
// failure without a source being down, through manage_script command=test.
func TestIssue2032_ATestDeclaresAClassifiedFailure(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	source := fmt.Sprintf(`def main():
    """Merges the staged orders, and says when the source is down."""
    res = platform.execute(%q, connection = %q, on_error = "return")
    if "error" in res:
        fail("unreachable: %%s" %% res["error"]["trino"]["sql_state"], retryable = res["error"]["retryable"])

def test_a_source_down_is_temporary():
    testing.answer("trino_execute", {"connection": %q}, error = {"category": "upstream_unavailable",
        "retryable": True, "message": "EXTERNAL: The connection attempt failed.",
        "trino": {"error_type": "EXTERNAL", "error_name": "JDBC_ERROR", "sql_state": "08001"}})
    assert.contains(assert.fails(main), "unreachable: 08001")
`, unreachableSQL2032, trinoConnection2032, trinoConnection2032)
	out := owner.call("manage_script", map[string]any{
		"command": "test", "name": fmt.Sprintf("acc-2032-test-%d", time.Now().UnixNano()), "source": source,
	})
	tests, _ := out["tests"].([]any)
	if len(tests) != 1 {
		t.Fatalf("the source's one test did not run: %v", out)
	}
	for _, it := range tests {
		if tc, _ := it.(map[string]any); tc["passed"] != true {
			t.Errorf("test %v failed: %v", tc["name"], tc["failure"])
		}
	}
}
