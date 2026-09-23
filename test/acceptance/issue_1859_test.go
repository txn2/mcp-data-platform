//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1859: a scheduled script failed because an upstream API answered one
// request with 429. api_export into a managed resource raised on the non-2xx
// answer, so a script (which has no try/except) could not handle it; the run
// was recorded as a deterministic script error; and the help promised a wait
// the host only made for the platform's own rate limiter.
//
// What these hold, against the running platform and the api-test fixture's
// GET /v1/status/{code} and GET /v1/slow: api_export answers a non-2xx upstream
// with the status as data and leaves the resource unchanged; inside a script
// the host waits and retries an upstream 429 before handing the script the
// answer; a run that fails on an upstream that timed out is marked retryable
// with an upstream cause, and one that fails in the script is not.
//
// Wire forms: api_export's `resource` is typed object, `query_params` object,
// `method`, `path`, `name` and `connection` string, and `timeout_seconds`
// integer; manage_script's `command`, `name`, `description`, `source` and
// `run_id` string; run_script's `wait_seconds` integer. Each is sent in that
// one form.

// TestIssue1859_ApiExportToAResourceReturnsANon2xxAsData calls api_export
// directly: a 503 upstream answer comes back as a result, not an error, naming
// the status, marking the resource unchanged, and saying a retry may succeed.
func TestIssue1859_ApiExportToAResourceReturnsANon2xxAsData(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	// call fails the test on a tool error, which is the first half of the
	// criterion: a 503 answer is not one.
	out := c.call("api_export", map[string]any{
		"connection": apiTestConnection,
		"method":     "GET",
		"path":       "/v1/status/503",
		"name":       "Acceptance 1859 " + stamp,
		"resource": map[string]any{
			"path": "acceptance/issue-1859", "filename": "status-" + stamp + ".json",
			"change_summary": "acceptance #1859",
		},
		"purpose": "Acceptance #1859: a non-2xx upstream answer is data.",
	})
	if got := number(t, out, "upstream_status"); got != 503 {
		t.Errorf("upstream_status = %v; want 503", got)
	}
	if out["resource_unchanged"] != true {
		t.Errorf("resource_unchanged = %v; want true: %v", out["resource_unchanged"], out)
	}
	if out["upstream_retryable"] != true {
		t.Errorf("upstream_retryable = %v; want true for a GET answered 503: %v", out["upstream_retryable"], out)
	}
	if _, landed := out["resource"]; landed {
		t.Errorf("a 503 answer landed in the resource: %v", out)
	}
	if msg, _ := out["message"].(string); !strings.Contains(msg, "unchanged") {
		t.Errorf("message %q does not say the resource is unchanged", msg)
	}
}

// issue1859ExportSource exports the fixture's 429 into a resource and records
// what the script was handed. The first verb is the connection, the second the
// file name.
const issue1859ExportSource = `
exp = platform.call("api_export", {
    "connection": %q,
    "method": "GET",
    "path": "/v1/status/429",
    "name": "Acceptance 1859 throttled",
    "resource": {"path": "acceptance/issue-1859", "filename": %q, "change_summary": "acceptance #1859"},
    "purpose": "Acceptance #1859: a throttled download is data.",
})
if exp.get("upstream_status") != 200:
    print("download returned upstream HTTP {}".format(exp.get("upstream_status")))
platform.save_state({"status": exp.get("upstream_status"), "unchanged": exp.get("resource_unchanged")})
`

// TestIssue1859_AScriptSeesAThrottledExportAndCarriesOn is the ticket's
// script: its status branch now runs. The host waited and retried the 429
// before handing it over, and the run log says so.
func TestIssue1859_AScriptSeesAThrottledExportAndCarriesOn(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	name := "acc-1859-throttled-" + stamp
	c.call("manage_script", map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #1859: a throttled export is handled by the script.",
		"source":      fmt.Sprintf(issue1859ExportSource, apiTestConnection, "throttled-"+stamp+".json"),
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 90})
	if run["status"] != "succeeded" {
		t.Fatalf("the run did not succeed on a throttled export: %v", run)
	}
	got := c.call("manage_script", map[string]any{"command": "get_run", "run_id": run["run_id"]})
	log, _ := got["log"].(string)
	if n := strings.Count(log, "upstream answered 429"); n != 3 {
		t.Errorf("the log records %d waits on the upstream 429; want 3:\n%s", n, log)
	}
	if !strings.Contains(log, "download returned upstream HTTP 429") {
		t.Errorf("the script's own status branch did not run:\n%s", log)
	}
	state := c.call("manage_script", map[string]any{"command": "state", "name": name, "state_action": "get"})
	saved, _ := state["state"].(map[string]any)
	if number(t, saved, "status") != 429 || saved["unchanged"] != true {
		t.Errorf("the script was handed %v; want status 429 and the resource unchanged", saved)
	}
}

// TestIssue1859_AnUpstreamTimeoutFailsTheRunAsRetryable holds the
// classification: a run ended by an upstream that did not answer in time is
// retryable, with an upstream cause and a message that does not send the owner
// looking for a bug.
func TestIssue1859_AnUpstreamTimeoutFailsTheRunAsRetryable(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-1859-timeout-%d", time.Now().UnixNano())
	c.call("manage_script", map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #1859: an upstream timeout is transient.",
		"source": fmt.Sprintf(`platform.call("api_invoke_endpoint", {
    "connection": %q, "method": "GET", "path": "/v1/slow",
    "query_params": {"ms": 5000}, "timeout_seconds": 1,
})
`, apiTestConnection),
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 60})
	if run["status"] != "failed" {
		t.Fatalf("a run whose upstream timed out did not fail: %v", run)
	}
	got := c.call("manage_script", map[string]any{"command": "get_run", "run_id": run["run_id"]})
	if got["retryable"] != true || got["cause"] != "upstream" {
		t.Errorf("retryable = %v, cause = %v; want true and upstream: %v", got["retryable"], got["cause"], got)
	}
	msg, _ := got["message"].(string)
	if strings.Contains(msg, "Fix the script") || !strings.Contains(msg, "temporar") {
		t.Errorf("the message for an upstream failure reads %q", msg)
	}
}

// TestIssue1859_AScriptErrorStaysDeterministic is the other half: fail() in
// the script is the script's, and the run says to fix it.
func TestIssue1859_AScriptErrorStaysDeterministic(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-1859-fail-%d", time.Now().UnixNano())
	c.call("manage_script", map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #1859: a script error is deterministic.",
		"source":      `fail("the input was not what this script expects")`,
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 60})
	got := c.call("manage_script", map[string]any{"command": "get_run", "run_id": run["run_id"]})
	if got["status"] != "failed" || got["retryable"] != false || got["cause"] != "script" {
		t.Errorf("status = %v, retryable = %v, cause = %v; want failed, false, script",
			got["status"], got["retryable"], got["cause"])
	}
	if msg, _ := got["message"].(string); !strings.Contains(msg, "Fix the script") {
		t.Errorf("the message for a script error reads %q", msg)
	}
}

// TestIssue1859_HelpDescribesTheUpstreamWait holds the help to what the host
// now does.
func TestIssue1859_HelpDescribesTheUpstreamWait(t *testing.T) {
	c := connect(t)
	_, text, err := c.callRaw("manage_script", map[string]any{"command": "help"})
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	for _, want := range []string{"upstream_retryable", "429"} {
		if !strings.Contains(text, want) {
			t.Errorf("help does not mention %q", want)
		}
	}
}
