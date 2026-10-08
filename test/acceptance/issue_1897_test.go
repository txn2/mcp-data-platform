//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Issue #1897: every background loop reports that it is alive, the queues it
// drains, and what failed, and every unit of background work has a root span.
//
// Wire forms: manage_script's command, name, cron, timezone and source are
// strings; notify's action, channel, title and body are strings. Each schema admits one form for each of these
// and each is sent as that literal form. The SMTP settings and the channel
// are written through the admin REST routes as JSON objects.
//
// Stack: TestIssue1897_AStoppedSchedulerShowsItsDueSchedulesAging needs
// scripts.worker.enabled: false on every replica of the dev stack
// (DEV_SCRIPT_WORKER_ENABLED=false make dev), and every other criterion here
// needs the worker on, so the file is run twice, once against each.

const (
	// issue1897Sample is how long the worker-off criterion waits between the
	// two scrapes it compares: longer than the scrape cache, so the second
	// read is fresh.
	issue1897Sample = 15 * time.Second
	// issue1897Wait bounds every wait for a background effect.
	issue1897Wait = 90 * time.Second
)

// issue1897Await polls the replicas' scrape until check reports true.
func issue1897Await(t *testing.T, what string, check func(body string) bool) {
	t.Helper()
	deadline := time.Now().Add(issue1897Wait)
	for {
		body := scrapeRaw(t)
		if check(body) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s within %s", what, issue1897Wait)
		}
		time.Sleep(time.Second)
	}
}

// issue1897Script saves a script owned by the caller and removes it after.
func issue1897Script(t *testing.T, c *client, name, source string) {
	t.Helper()
	c.saveScriptCovering(map[string]any{
		"command": "create", "name": name, "source": source,
		"description": "Acceptance #1897: a script whose run is traced and whose schedule is measured.",
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})
}

// TestIssue1897_AStoppedSchedulerShowsItsDueSchedulesAging: with the script
// worker disabled on the only replica and a schedule due, the oldest-due
// gauge grows and the scheduler's last-success time does not advance.
func TestIssue1897_AStoppedSchedulerShowsItsDueSchedulesAging(t *testing.T) {
	c := connect(t)
	if metricSeries(scrapeRaw(t), "background_loop_iterations_total", map[string]string{"loop": "script_worker"}) > 0 {
		t.Fatal("a script worker is running on this stack; this criterion needs scripts.worker.enabled: false on every replica")
	}
	name := "acceptance-1897-sched-" + unique1551()
	issue1897Script(t, c, name, "def main():\n    \"\"\"Does nothing; the schedule is what is measured.\"\"\"\n    print(\"tick\")\n")
	c.call("manage_script", map[string]any{
		"command": "schedule_set", "name": name, "cron": "* * * * *", "timezone": "UTC",
	})
	backdateSchedule1897(t, name)

	age := map[string]string{"loop": "script_scheduler"}
	issue1897Await(t, "background_queue_oldest_age_seconds{loop=script_scheduler} never reported a due schedule", func(body string) bool {
		return metricSeries(body, "background_queue_oldest_age_seconds", age) > 0
	})
	before := scrapeRaw(t)
	time.Sleep(issue1897Sample)
	after := scrapeRaw(t)

	if a, b := metricSeries(before, "background_queue_oldest_age_seconds", age), metricSeries(after, "background_queue_oldest_age_seconds", age); b <= a {
		t.Errorf("the oldest due schedule did not age: %v then %v", a, b)
	}
	if n := metricSeries(after, "background_queue_items", map[string]string{"loop": "script_scheduler", "state": "pending"}); n < 1 {
		t.Errorf("no due schedule counted: %v", n)
	}
	last := "background_loop_last_success_timestamp_seconds"
	if a, b := metricSeries(before, last, age), metricSeries(after, last, age); a != b {
		t.Errorf("the scheduler's last success advanced with no worker: %v then %v", a, b)
	}
}

// backdateSchedule1897 moves the schedule's next fire into the past, so it is
// due now rather than at the next minute boundary.
func backdateSchedule1897(t *testing.T, name string) {
	t.Helper()
	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the dev database: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := db.ExecContext(ctx, `UPDATE script_schedules SET next_run_at = NOW() - INTERVAL '5 minutes'
		WHERE script_id = (SELECT id FROM scripts WHERE name = $1)`, name)
	if err != nil {
		t.Fatalf("backdating the schedule: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("backdated %d schedules, want 1", n)
	}
}

// TestIssue1897_ANotificationToAnUnreachableMailServerIsCounted: a
// notification sent while the mail server does not answer increments the
// delivery failure counter.
func TestIssue1897_ANotificationToAnUnreachableMailServerIsCounted(t *testing.T) {
	c := connect(t)
	status, current := c.rest(http.MethodGet, "/api/v1/admin/settings/smtp", nil)
	if status != http.StatusOK {
		t.Fatalf("GET SMTP settings: %d %v", status, current)
	}
	t.Cleanup(func() {
		restore := map[string]any{}
		for _, k := range []string{"enabled", "host", "port", "username", "from", "from_name", "tls_mode"} {
			if v, ok := current[k]; ok {
				restore[k] = v
			}
		}
		_, _ = c.rest(http.MethodPut, "/api/v1/admin/settings/smtp", jsonBody(t, restore))
	})
	unreachable := map[string]any{
		"enabled": true, "host": "127.0.0.1", "port": 1, "from": "platform@example.com", "tls_mode": "none",
	}
	if status, out := c.rest(http.MethodPut, "/api/v1/admin/settings/smtp", jsonBody(t, unreachable)); status != http.StatusOK {
		t.Fatalf("PUT SMTP settings: %d %v", status, out)
	}

	failures := func(body string) float64 {
		return metricSeries(body, "notification_delivery_attempts_total", map[string]string{"kind": "email", "result": "retry"}) +
			metricSeries(body, "notification_delivery_attempts_total", map[string]string{"kind": "email", "result": "failed"})
	}
	before := failures(scrapeRaw(t))

	channel := "acc-1897-email-" + unique1551()
	putChannel(t, c, channel, map[string]any{"kind": "email", "recipients": []string{"acc-1897@example.com"}})
	t.Cleanup(func() { deleteChannel(t, c, channel) })
	c.call("notify", map[string]any{
		"action": "send", "channel": channel,
		"title": "acc-1897 unreachable mail server", "body": "This cannot be delivered.",
	})

	issue1897Await(t, "notification_delivery_attempts_total{kind=email,result=retry|failed} did not increase", func(body string) bool {
		return failures(body) > before
	})
}

// issue1897TwoToolScript calls two tools, so its run's trace holds the run's
// span and both calls' spans.
const issue1897TwoToolScript = `
def main():
    """Calls two tools, so one run's trace holds both calls."""
    platform.call("list_connections", {})
    platform.call("platform_info", {})
`

// TestIssue1897_AScriptRunIsOneTrace: a scheduled script run that calls two
// tools exports one trace holding the run's span and both tools/call spans,
// each a child of the run's span.
func TestIssue1897_AScriptRunIsOneTrace(t *testing.T) {
	c := connect(t)
	name := "acceptance-1897-trace-" + unique1551()
	issue1897Script(t, c, name, issue1897TwoToolScript)
	c.call("manage_script", map[string]any{
		"command": "schedule_set", "name": name, "cron": "0 0 1 1 *", "timezone": "UTC",
	})
	backdateSchedule1897(t, name)
	runID := scheduledRun1897(t, name)

	deadline := time.Now().Add(spanWait)
	for {
		var run *exportedSpan
		spans := readSpans(t)
		for i := range spans {
			if spans[i].Name == "loop script_run" && spans[i].Attrs["mcp_platform.script.run_id"] == runID {
				run = &spans[i]
			}
		}
		calls := map[string]exportedSpan{}
		if run != nil {
			for _, sp := range spans {
				if sp.TraceID == run.TraceID && isToolCallSpan(sp) {
					calls[sp.Name] = sp
				}
			}
		}
		if run != nil && len(calls) >= 2 {
			if run.ParentSpanID != "" {
				t.Errorf("the run's span has a parent %s; a run is a root", run.ParentSpanID)
			}
			for _, tool := range []string{"list_connections", "platform_info"} {
				sp, ok := calls["tools/call "+tool]
				if !ok {
					t.Errorf("no tools/call %s span in the run's trace %s", tool, run.TraceID)
					continue
				}
				if sp.ParentSpanID != run.SpanID {
					t.Errorf("tools/call %s's parent is %s, want the run's span %s", tool, sp.ParentSpanID, run.SpanID)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the run %s's trace did not hold the run span and both tool calls within %s", runID, spanWait)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// scheduledRun1897 waits for the scheduler to fire the script's backdated
// schedule and for that run to succeed, and returns the run's id.
func scheduledRun1897(t *testing.T, name string) string {
	t.Helper()
	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the dev database: %v", err)
	}
	defer func() { _ = db.Close() }()
	deadline := time.Now().Add(issue1897Wait)
	for {
		var id, status string
		err := db.QueryRowContext(context.Background(), `SELECT r.id, r.status FROM script_runs r
			JOIN scripts s ON s.id = r.script_id
			WHERE s.name = $1 AND r.trigger_kind = 'schedule'
			ORDER BY r.created_at DESC LIMIT 1`, name).Scan(&id, &status)
		switch {
		case err == nil && status == "succeeded":
			return id
		case err == nil && status == "failed":
			t.Fatalf("the scheduled run %s failed", id)
		case err != nil && err != sql.ErrNoRows:
			t.Fatalf("reading the scheduled run: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("no scheduled run of %s succeeded within %s", name, issue1897Wait)
		}
		time.Sleep(time.Second)
	}
}

// TestIssue1897_ALostListenConnectionIsCountedAsAReconnect: terminating the
// PostgreSQL backends under the LISTEN adapters increments their reconnect
// counter and puts them back up.
func TestIssue1897_ALostListenConnectionIsCountedAsAReconnect(t *testing.T) {
	reconnects := func(body string) float64 {
		var total float64
		for _, loop := range []string{"listen_index_jobs", "listen_session_broadcast"} {
			total += metricSeries(body, "pg_listen_reconnects_total", map[string]string{"loop": loop})
		}
		return total
	}
	before := reconnects(scrapeRaw(t))

	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the dev database: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (
		SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE query ILIKE 'LISTEN %') t`).Scan(&n); err != nil {
		t.Fatalf("terminating the LISTEN backends: %v", err)
	}
	if n == 0 {
		t.Fatal("no backend was LISTENing; the platform's LISTEN adapters are not connected")
	}

	issue1897Await(t, "pg_listen_reconnects_total did not increase after the LISTEN backends were terminated", func(body string) bool {
		return reconnects(body) > before
	})
	issue1897Await(t, "a LISTEN connection did not come back up", func(body string) bool {
		return metricSeries(body, "pg_listen_connected", map[string]string{"loop": "listen_index_jobs"}) >= 1
	})
}

// TestIssue1897_TheSemgrepRuleRefusesALoopOffTheHelper:
// .semgrep/go-background-loop.yml fails on a ticker and on an infinite loop
// around a select in non-test Go outside internal/bgloop, and passes the
// helper itself and a test file.
func TestIssue1897_TheSemgrepRuleRefusesALoopOffTheHelper(t *testing.T) {
	semgrep, err := exec.LookPath("semgrep")
	if err != nil {
		t.Fatalf("semgrep is not installed; make verify's semgrep gate needs it and so does this criterion")
	}
	rule, err := filepath.Abs(filepath.Join("..", "..", ".semgrep", "go-background-loop.yml"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	src := "package x\n\nimport \"time\"\n\nfunc a(c chan int) {\n\tt := time.NewTicker(time.Second)\n\tdefer t.Stop()\n\tfor {\n\t\tselect {\n\t\tcase <-c:\n\t\t\treturn\n\t\tcase <-t.C:\n\t\t}\n\t}\n}\n"
	for _, rel := range []string{"pkg/x/x.go", "internal/bgloop/x.go", "pkg/x/x_test.go"} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(semgrep, "scan", "--config", rule, "--quiet", "--json", ".") //nolint:gosec // test runs the gate's tool on a fixture
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("semgrep: %v", err)
	}
	var report struct {
		Results []struct {
			Path  string `json:"path"`
			Start struct {
				Line int `json:"line"`
			} `json:"start"`
		} `json:"results"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("semgrep output: %v: %s", err, out)
	}
	found := []string{}
	for _, r := range report.Results {
		found = append(found, fmt.Sprintf("%s:%d", r.Path, r.Start.Line))
	}
	if got := strings.Join(found, ","); got != "pkg/x/x.go:6,pkg/x/x.go:8" {
		t.Errorf("findings %q, want the ticker and the loop in pkg/x/x.go only", got)
	}
}
