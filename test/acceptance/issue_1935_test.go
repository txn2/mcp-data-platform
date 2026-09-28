//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1935: a run that failed because an upstream answered with an error
// was recorded as the script's own failure, told the owner the same inputs
// fail the same way, and was not retryable. These criteria run scripts
// against the api-test fixture, whose /v1/status/{code} answers with the
// status it is asked for, through run_script and manage_script as an agent
// does, and read the verdict back with get_run.
//
// Wire forms: manage_script's command, name, description, source, cron and
// timezone, run_script's name, and get_run's run_id are typed strings, and
// run_script's wait_seconds an integer, so each admits one JSON form and is
// sent as a literal tools/call parameter of it.

// issue1935Run creates a script from source, runs it to the end and returns
// its get_run record.
func issue1935Run(t *testing.T, c *client, label, source string) map[string]any {
	t.Helper()
	name := fmt.Sprintf("acc-1935-%s-%d", label, time.Now().UnixNano())
	created := c.saveScript(map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #1935: " + label,
		"source":      source,
	}, nil)
	if created["status"] == "invalid" {
		t.Fatalf("the script was refused on save: %v", created["findings"])
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 60})
	return c.call("manage_script", map[string]any{"command": "get_run", "run_id": run["run_id"]})
}

// issue1935Status is a script that calls the fixture for each code in turn and
// fails when the last answer is not a 200, as a script watching an upstream
// does. The calls before the last are statements, so no answer is bound and
// never read.
func issue1935Status(codes ...int) string {
	var b strings.Builder
	b.WriteString(`def main():
    """Calls the upstream and fails on anything but a 200."""
`)
	for i, code := range codes {
		bind := ""
		if i == len(codes)-1 {
			bind = "res = "
		}
		fmt.Fprintf(&b, `    %splatform.call("api_invoke_endpoint", {"connection": %q, "method": "GET", "path": "/v1/status/%d"})
`, bind, apiTestConnection, code)
	}
	b.WriteString(`    if res["status"] != 200:
        fail("the upstream returned %d" % res["status"])
    fail("the script's own mistake")
`)
	return b.String()
}

// TestIssue1935_AFailureAfterAnUpstream500IsTheUpstreams is the ticket's
// case: the upstream answered 500 and the script called fail(). The run is
// recorded as upstream and retryable, its log says why, and its message does
// not say the same inputs fail the same way.
func TestIssue1935_AFailureAfterAnUpstream500IsTheUpstreams(t *testing.T) {
	got := issue1935Run(t, connect(t), "after-500", issue1935Status(500))
	if got["status"] != "failed" || got["cause"] != "upstream" || got["retryable"] != true {
		t.Fatalf("status = %v, cause = %v, retryable = %v; want failed, upstream, true: %v",
			got["status"], got["cause"], got["retryable"], got)
	}
	if log, _ := got["log"].(string); !strings.Contains(log, "answered 500 Internal Server Error; recorded as an upstream failure") {
		t.Errorf("the run's log does not say why it was recorded as upstream: %q", log)
	}
	if msg, _ := got["message"].(string); strings.Contains(msg, "deterministic") || strings.Contains(msg, "fails the same way") {
		t.Errorf("the message still promises determinism: %q", msg)
	}
}

// TestIssue1935_AFailureAfterAGoodCallIsTheScripts holds the rule to the call
// made last: a 500 the script got past, followed by a 200, does not excuse a
// failure after it.
func TestIssue1935_AFailureAfterAGoodCallIsTheScripts(t *testing.T) {
	got := issue1935Run(t, connect(t), "after-200", issue1935Status(500, 200))
	if got["cause"] != "script" || got["retryable"] != false {
		t.Fatalf("cause = %v, retryable = %v; want script, false: %v", got["cause"], got["retryable"], got)
	}
	if msg, _ := got["message"].(string); !strings.Contains(msg, "if the same failure repeats") {
		t.Errorf("a script failure's message reads %q", msg)
	}
}

// TestIssue1935_FailRetryableIsTransient is the author's own signal.
func TestIssue1935_FailRetryableIsTransient(t *testing.T) {
	got := issue1935Run(t, connect(t), "retryable", `def main():
    """Reports a failure the author knows is temporary."""
    fail("the feed has not published today", retryable = True)
`)
	if got["cause"] != "transient" || got["retryable"] != true {
		t.Fatalf("cause = %v, retryable = %v; want transient, true: %v", got["cause"], got["retryable"], got)
	}
	if e, _ := got["error"].(string); !strings.Contains(e, "fail: the feed has not published today") {
		t.Errorf("the failure text is not fail()'s own: %q", e)
	}
}

// TestIssue1935_ARepeatedFailureEmailSaysToCorrectTheScript is the email: the
// third scheduled run in a row to fail the same way tells the owner the script
// needs correcting. The two earlier failures are written into the dev database
// and the schedule's next fire is moved into the past, as #1904's criterion
// backdates its deletion; firing, running, classifying and mailing are the
// running platform's work.
func TestIssue1935_ARepeatedFailureEmailSaysToCorrectTheScript(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	db := issue1904DB(t)
	name := fmt.Sprintf("acc-1935-mail-%d", time.Now().UnixNano())
	created := owner.saveScript(map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #1935: a failure that repeats",
		"source": `def main():
    """Fails the way a script handed input it does not expect does."""
    fail("the input was not what this script expects")
`,
	}, nil)
	if created["status"] == "invalid" {
		t.Fatalf("the script was refused on save: %v", created["findings"])
	}
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	owner.call("manage_script", map[string]any{
		"command": "schedule_set", "name": name, "cron": "0 3 * * *", "timezone": "UTC",
	})

	const lastLine = "Error in fail: fail: the input was not what this script expects"
	for i := 1; i <= 2; i++ {
		issue1904Exec(t, db, `
			INSERT INTO script_runs (id, script_id, script_version_id, version, trigger_kind, status, error, created_at, finished_at)
			SELECT $2, s.id, v.id, v.version, 'schedule', 'failed', $3,
			       NOW() - make_interval(hours => $4), NOW() - make_interval(hours => $4)
			  FROM scripts s JOIN script_versions v ON v.script_id = s.id AND v.version = s.version
			 WHERE s.name = $1`,
			name, fmt.Sprintf("dpx_acc1935_%d_%d", time.Now().UnixNano(), i),
			"Traceback (most recent call last):\n  "+name+":3:5: in main\n"+lastLine, 3-i)
	}
	issue1904Exec(t, db, `UPDATE script_schedules SET next_run_at = NOW() - interval '1 second'
		WHERE script_id = (SELECT id FROM scripts WHERE name = $1)`, name)

	var body string
	issue1904Await(t, "the failure email for the third run in a row", func() bool {
		body = issue1935MailBody(t, name)
		return body != ""
	})
	if !strings.Contains(body, "failed 3 runs in a row the same way, so the script needs correcting") {
		t.Errorf("the email for a third failure in a row reads:\n%s", body)
	}
	if strings.Contains(body, "fails the same way") {
		t.Errorf("the email still promises determinism:\n%s", body)
	}
}

// issue1935MailBody returns the text of the failure email naming script, or
// "" while there is none.
func issue1935MailBody(t *testing.T, script string) string {
	t.Helper()
	out := getJSON(t, mailpitBase()+"/api/v1/messages?limit=100", "")
	items, _ := out["messages"].([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		if subject, _ := m["Subject"].(string); !strings.Contains(subject, script) {
			continue
		}
		id, _ := m["ID"].(string)
		msg := getJSON(t, mailpitBase()+"/api/v1/message/"+id, "")
		text, _ := msg["Text"].(string)
		return text
	}
	return ""
}
