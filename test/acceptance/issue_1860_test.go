//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1860: a run whose worker died (OOM kill, node loss) was reclaimed on
// lease expiry with no bound, re-executed from the top, killed the next
// replica, and read as plain `running` the whole time. cancel_run needed a
// living worker to act.
//
// A worker that dies leaves exactly one trace: a `running` row whose holder
// no longer reports. These criteria write that trace into the dev database —
// the one part of the scenario no surface can drive — and read everything
// else through the MCP surface: the claim fails a run whose reclaims are
// spent, get_run and manage_script get report an unresponsive holder and the
// attempt history, and cancel_run ends an orphaned run directly.
//
// Wire forms: manage_script's `command`, `name`, `description`, `source` and
// `run_id` are typed string. Each is sent in that one form.

// issue1860Orphan is a running row whose holder is gone, as a killed worker
// leaves it.
type issue1860Orphan struct {
	attempt, reclaims int
	// leaseLeft is how long the dead holder's lease still runs; negative is
	// already expired.
	leaseLeft time.Duration
	// silentFor is how long ago the holder last reported.
	silentFor time.Duration
}

// issue1860Script creates a script whose version the orphan row runs, and
// returns its name.
func issue1860Script(t *testing.T, c *client, label string) string {
	t.Helper()
	name := fmt.Sprintf("acc-1860-%s-%d", label, time.Now().UnixNano())
	c.call("manage_script", map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #1860: a run whose worker died.",
		"source":      `print("the re-execution of an orphaned run")`,
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	return name
}

// issue1860Plant writes the orphan row for the named script's latest version
// and returns its id.
func issue1860Plant(t *testing.T, script string, o issue1860Orphan) string {
	t.Helper()
	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the dev database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id := fmt.Sprintf("run_acc1860_%d", time.Now().UnixNano())
	_, err = db.ExecContext(ctx, `
		INSERT INTO script_runs (id, script_id, script_version_id, version, status, attempt, reclaims,
		                         locked_by, locked_until, claimed_at, heartbeat_at, started_at, requested_by)
		SELECT $1, s.id, v.id, v.version, 'running', $3, $4, 'worker-acc1860-gone',
		       NOW() + ($5 || ' seconds')::INTERVAL, NOW() - ($6 || ' seconds')::INTERVAL,
		       NOW() - ($6 || ' seconds')::INTERVAL, NOW() - INTERVAL '1 hour', 'acceptance@example.com'
		  FROM scripts s JOIN script_versions v ON v.script_id = s.id
		 WHERE s.name = $2
		 ORDER BY v.version DESC LIMIT 1`,
		id, script, o.attempt, o.reclaims, int(o.leaseLeft.Seconds()), int(o.silentFor.Seconds()))
	if err != nil {
		t.Fatalf("planting the orphaned run: %v", err)
	}
	return id
}

// TestIssue1860_AReclaimedRunIsFailedAtTheCap plants a run that has already
// been reclaimed the dev stack's maximum number of times and whose lease has
// expired again. The next claim must fail it, naming the dead holder, rather
// than execute it a fourth time.
func TestIssue1860_AReclaimedRunIsFailedAtTheCap(t *testing.T) {
	c := connect(t)
	name := issue1860Script(t, c, "cap")
	id := issue1860Plant(t, name, issue1860Orphan{attempt: 3, reclaims: 2, leaseLeft: -time.Minute, silentFor: 25 * time.Minute})

	run := issue1860Await(t, c, id, func(run map[string]any) bool { return run["status"] == "failed" })
	errText, _ := run["error"].(string)
	for _, want := range []string{"stopped without reporting a result 3 times", "worker-acc1860-gone"} {
		if !strings.Contains(errText, want) {
			t.Errorf("error %q does not say %q", errText, want)
		}
	}
	if run["cause"] != "worker_lost" || run["retryable"] != false {
		t.Errorf("cause = %v, retryable = %v; want worker_lost and false", run["cause"], run["retryable"])
	}
	if log, _ := run["log"].(string); strings.Contains(log, "re-execution") {
		t.Errorf("the run was executed again after its reclaims were spent:\n%s", log)
	}
	attempts, _ := run["attempts"].([]any)
	if len(attempts) == 0 {
		t.Fatalf("the run reports no attempt history: %v", run)
	}
	last, _ := attempts[len(attempts)-1].(map[string]any)
	if last["outcome"] != "lease_expired" || last["worker"] != "worker-acc1860-gone" {
		t.Errorf("the last attempt reads %v; want the dead holder's lease expiry", last)
	}
	if !issue1860Counted(t) {
		t.Errorf("no replica's /metrics counts a reclaim that failed the run (script_run_reclaims_total{outcome=\"failed\"})")
	}
}

// issue1860Counted scrapes each dev replica's metrics endpoint (dev/start.sh
// serves them on :9464 and :9465) for a failed reclaim.
func issue1860Counted(t *testing.T) bool {
	t.Helper()
	for _, port := range []string{"9464", "9465"} {
		resp, err := http.Get("http://localhost:" + port + "/metrics") //nolint:noctx // a test scrape
		if err != nil {
			t.Logf("scraping :%s: %v", port, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		for _, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(line, "script_run_reclaims_total{") && strings.Contains(line, `outcome="failed"`) &&
				!strings.HasSuffix(line, " 0") {
				return true
			}
		}
	}
	return false
}

// TestIssue1860_AnUnresponsiveHolderIsReportedAndListedWithTheScript plants a
// run whose holder stopped reporting while its lease still runs, the state a
// killed replica leaves for up to a lease. It must not read as a plain running
// run, and the script must list it.
func TestIssue1860_AnUnresponsiveHolderIsReportedAndListedWithTheScript(t *testing.T) {
	c := connect(t)
	name := issue1860Script(t, c, "silent")
	id := issue1860Plant(t, name, issue1860Orphan{attempt: 1, leaseLeft: 15 * time.Minute, silentFor: 5 * time.Minute})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "cancel_run", "run_id": id}) })

	run := c.call("manage_script", map[string]any{"command": "get_run", "run_id": id})
	if run["liveness"] != "unresponsive" {
		t.Errorf("liveness = %v; want unresponsive for a holder silent for five minutes: %v", run["liveness"], run)
	}
	for _, key := range []string{"locked_by", "locked_until", "heartbeat_at", "attempt", "reclaims"} {
		if _, ok := run[key]; !ok {
			t.Errorf("get_run does not report %s: %v", key, run)
		}
	}
	if msg, _ := run["message"].(string); !strings.Contains(msg, "stopped reporting") {
		t.Errorf("the message for an unresponsive run reads %q", msg)
	}

	got := c.call("manage_script", map[string]any{"command": "get", "name": name})
	live, _ := got["live_runs"].([]any)
	found := false
	for _, raw := range live {
		if r, _ := raw.(map[string]any); r["run_id"] == id {
			found = true
			if r["liveness"] != "unresponsive" {
				t.Errorf("the script lists the run with liveness %v; want unresponsive", r["liveness"])
			}
		}
	}
	if !found {
		t.Errorf("manage_script get does not list the orphaned run among live_runs: %v", live)
	}
}

// TestIssue1860_CancelEndsAnOrphanedRunDirectly cancels a run nobody is
// executing. No worker will observe the request, so the store ends it.
func TestIssue1860_CancelEndsAnOrphanedRunDirectly(t *testing.T) {
	c := connect(t)
	name := issue1860Script(t, c, "cancel")
	id := issue1860Plant(t, name, issue1860Orphan{attempt: 1, leaseLeft: 15 * time.Minute, silentFor: 5 * time.Minute})

	canceled := c.call("manage_script", map[string]any{"command": "cancel_run", "run_id": id})
	if canceled["status"] != "canceled" {
		t.Fatalf("cancel_run on an orphaned run answered %v; want it canceled at once", canceled)
	}
	run := c.call("manage_script", map[string]any{"command": "get_run", "run_id": id})
	if run["status"] != "canceled" {
		t.Errorf("the run reads %v after the cancel; want canceled", run["status"])
	}
	if errText, _ := run["error"].(string); !strings.Contains(errText, "stopped reporting") {
		t.Errorf("the canceled run's error %q does not say its worker was gone", errText)
	}
}

// issue1860Await polls get_run until done holds.
func issue1860Await(t *testing.T, c *client, id string, done func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		run := c.call("manage_script", map[string]any{"command": "get_run", "run_id": id})
		if done(run) {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s never reached the expected state: %v", id, run)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
