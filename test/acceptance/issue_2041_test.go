//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// #2041: a failing scheduled automation is one incident. Its first failed run
// emails the owner and posts to the run-failure channels; the failed runs
// while it keeps failing send nothing; the run that succeeds sends one
// recovered email and post, naming the run that began it, the count and the
// duration. The incident is a database row, so the replicas behind the dev
// stack's proxy, whichever claims each run, pair the failure with the
// recovery.
//
// Wire forms: manage_script's command, name, description, source, cron and
// timezone are strings with one form each; the script-run-alert route's
// channels is a list of strings (#1724 covers its other forms).

const (
	failing2041 = `def main():
    """Loads yesterday's orders; the warehouse is short today."""
    fail("the warehouse returned 0 rows for yesterday", retryable = False)
`
	fixed2041 = `def main():
    """Loads yesterday's orders."""
    platform.result({"loaded": 1})
`
)

// row2041 is one notification queued for the script's owner.
type row2041 struct {
	ItemID   string
	Recovery map[string]any
}

// rows2041 is what the owner was queued about the script named title, oldest
// first.
func rows2041(t *testing.T, title string) []row2041 {
	t.Helper()
	rows, err := devDB(t).QueryContext(t.Context(), `
		SELECT payload->>'item_id', COALESCE(payload->'recovery', 'null'::jsonb)
		  FROM notifications
		 WHERE payload->>'kind' = 'script_run' AND payload->>'item_title' = $1
		 ORDER BY id`, title)
	if err != nil {
		t.Fatalf("reading notifications: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []row2041
	for rows.Next() {
		var r row2041
		var raw []byte
		if err := rows.Scan(&r.ItemID, &raw); err != nil {
			t.Fatalf("scanning a notification: %v", err)
		}
		_ = json.Unmarshal(raw, &r.Recovery)
		out = append(out, r)
	}
	return out
}

// fire2041 makes the script's schedule due and waits for the scheduled run
// that follows the ones already seen to finish, returning it.
func fire2041(t *testing.T, c *client, name, scriptID string, seen map[string]bool) map[string]any {
	t.Helper()
	fireNow1986(t, scriptID)
	run := awaitRun1986(t, c, name, "a new finished scheduled run", func(r map[string]any) bool {
		id, _ := r["run_id"].(string)
		return r["trigger"] == "schedule" && !seen[id] && (r["status"] == "succeeded" || r["status"] == "failed")
	})
	seen[run["run_id"].(string)] = true
	return run
}

// TestIssue2041_OneFailureEmailThenOneRecoveredEmail is the whole incident: two
// failed scheduled runs queue one email, the fix and the next scheduled run
// queue one recovered email naming the first failed run and the count, and the
// channel hears both.
func TestIssue2041_OneFailureEmailThenOneRecoveredEmail(t *testing.T) {
	c := connect(t)
	env := alertChannel(t, c)
	putSettings(t, c, "/api/v1/admin/settings/script-run-alert", map[string]any{"channels": []string{chanAlerts}})

	name := fmt.Sprintf("acc-2041-%d", time.Now().UnixNano())
	c.saveScript(map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2041: fails, then is fixed.", "source": failing2041,
	}, nil)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	got := c.call("manage_script", map[string]any{"command": "get", "name": name})
	scriptID, _ := got["id"].(string)
	c.call("manage_script", map[string]any{"command": "schedule_set", "name": name, "cron": "0 3 * * *", "timezone": "UTC"})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "schedule_disable", "name": name})
	})

	seen := map[string]bool{}
	first := fire2041(t, c, name, scriptID, seen)
	second := fire2041(t, c, name, scriptID, seen)
	if first["status"] != "failed" || second["status"] != "failed" {
		t.Fatalf("want two failed runs: %v, %v", first, second)
	}
	waitFor(t, "the failure email", func() (bool, string) {
		rows := rows2041(t, name)
		return len(rows) >= 1, fmt.Sprint(rows)
	})
	if rows := rows2041(t, name); len(rows) != 1 || rows[0].ItemID != first["run_id"] || rows[0].Recovery != nil {
		t.Fatalf("two failed runs queued %v; want one email, about the first run %v", rows, first["run_id"])
	}
	var open int
	if err := devDB(t).QueryRowContext(t.Context(),
		`SELECT failed_runs FROM script_failure_incidents WHERE script_id = $1`, scriptID).Scan(&open); err != nil || open != 2 {
		t.Fatalf("the open incident counts %d failed runs (%v); want 2", open, err)
	}

	c.saveEdit(map[string]any{"command": "update", "name": name, "source": fixed2041}, nil)
	recovered := fire2041(t, c, name, scriptID, seen)
	if recovered["status"] != "succeeded" {
		t.Fatalf("the fixed run: %v", recovered)
	}
	waitFor(t, "the recovered email", func() (bool, string) {
		rows := rows2041(t, name)
		return len(rows) == 2, fmt.Sprint(rows)
	})
	rec := rows2041(t, name)[1]
	if rec.ItemID != recovered["run_id"] || rec.Recovery == nil {
		t.Fatalf("the second email %+v is not the recovery of run %v", rec, recovered["run_id"])
	}
	if fmt.Sprint(rec.Recovery["failed_runs"]) != "2" || rec.Recovery["opening_run_id"] != first["run_id"] {
		t.Errorf("the recovery says %v; want 2 failed runs opened by %v", rec.Recovery, first["run_id"])
	}
	if !strings.Contains(fmt.Sprint(rec.Recovery["opening_error"]), "0 rows for yesterday") {
		t.Errorf("the recovery does not carry the line naming the opening run's failure: %v", rec.Recovery)
	}
	if err := devDB(t).QueryRowContext(t.Context(),
		`SELECT failed_runs FROM script_failure_incidents WHERE script_id = $1`, scriptID).Scan(&open); err == nil {
		t.Errorf("the incident is still open after the recovery")
	}
	waitFor(t, "the recovered post", func() (bool, string) {
		return postsContain(t, env, fmt.Sprintf("%q recovered", name)), "waiting for a post saying " + name + " recovered"
	})
}

// TestIssue2041_OneFailureThenSuccessIsExactlyTwoEmails: an isolated failure
// followed by a success is the failure email and the recovered email, nothing
// else.
func TestIssue2041_OneFailureThenSuccessIsExactlyTwoEmails(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-2041-once-%d", time.Now().UnixNano())
	c.saveScript(map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2041: fails once.", "source": failing2041,
	}, nil)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	got := c.call("manage_script", map[string]any{"command": "get", "name": name})
	scriptID, _ := got["id"].(string)
	c.call("manage_script", map[string]any{"command": "schedule_set", "name": name, "cron": "0 3 * * *", "timezone": "UTC"})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "schedule_disable", "name": name})
	})

	seen := map[string]bool{}
	if run := fire2041(t, c, name, scriptID, seen); run["status"] != "failed" {
		t.Fatalf("want a failed run: %v", run)
	}
	c.saveEdit(map[string]any{"command": "update", "name": name, "source": fixed2041}, nil)
	if run := fire2041(t, c, name, scriptID, seen); run["status"] != "succeeded" {
		t.Fatalf("want a succeeded run: %v", run)
	}
	if run := fire2041(t, c, name, scriptID, seen); run["status"] != "succeeded" {
		t.Fatalf("want a second succeeded run: %v", run)
	}
	waitFor(t, "two emails", func() (bool, string) {
		rows := rows2041(t, name)
		return len(rows) == 2, fmt.Sprint(rows)
	})
	rows := rows2041(t, name)
	if rows[0].Recovery != nil || rows[1].Recovery == nil || fmt.Sprint(rows[1].Recovery["failed_runs"]) != "1" {
		t.Errorf("want a failure email then a recovery of one failed run: %+v", rows)
	}
}
