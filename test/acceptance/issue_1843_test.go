//go:build integration

package acceptance

import (
	"fmt"
	"sort"
	"testing"
	"time"
)

// Issue #1843: a replica executed one run at a time, and a run's timeout, step
// cap and query row cap were fixed. The worker now admits runs while the
// replica has headroom (adaptive by default), and the ceilings are
// configurable and reported.
//
// What these hold, against the running platform: runs queued together execute
// at the same time rather than one after another, and manage_script help
// reports the ceilings a platform run meets on this deployment.
//
// Wire forms: manage_script's `command`, `name`, `description`, `source` and
// `run_id` are typed string and `params` an array of objects; run_script's
// `args` is an object and `wait_seconds` an integer. Each is sent in that one
// form.

func TestIssue1843_HelpReportsThePlatformRunCeilings(t *testing.T) {
	c := connect(t)
	help := c.call("manage_script", map[string]any{"command": "help"})
	limits, _ := help["limits"].(map[string]any)
	for key, want := range map[string]any{
		"run_timeout":      "15m0s",
		"run_max_steps":    float64(20_000_000),
		"run_max_rows":     float64(20_000),
		"run_result_bytes": float64(1 << 20),
	} {
		if limits[key] != want {
			t.Errorf("limits.%s = %v; want %v (the default on the dev stack)", key, limits[key], want)
		}
	}
}

// TestIssue1843_RunsQueuedTogetherExecuteAtTheSameTime queues more runs than
// the dev stack has replicas. Each run queries the warehouse several times, so
// it spends its life waiting on Trino; the one-at-a-time worker executed them
// in sequence, and adaptive admission overlaps them. With three runs over two
// replicas, all three overlapping means some replica executed two at once.
func TestIssue1843_RunsQueuedTogetherExecuteAtTheSameTime(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-1843-%d", time.Now().UnixNano())
	c.call("manage_script", map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #1843: runs overlap.",
		"source": fmt.Sprintf(`for i in range(6):
    platform.query(connection=%q, sql="SELECT count(*) AS n FROM UNNEST(sequence(1, 10000)) AS a(x) CROSS JOIN UNNEST(sequence(1, 100)) AS b(y)")
`, scratchResourceConnection),
		"params": []any{map[string]any{"name": "slot", "type": "int"}},
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	const runs = 3
	ids := make([]string, 0, runs)
	for i := range runs {
		out := c.call("run_script", map[string]any{"name": name, "args": map[string]any{"slot": i}, "wait_seconds": -1})
		id, _ := out["run_id"].(string)
		ids = append(ids, id)
	}
	var starts, ends []time.Time
	for _, id := range ids {
		run := awaitRun1843(t, c, id)
		if run["status"] != "succeeded" {
			t.Fatalf("run %s did not succeed: %v", id, run)
		}
		starts = append(starts, when1843(t, run, "started_at"))
		ends = append(ends, when1843(t, run, "finished_at"))
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })
	sort.Slice(ends, func(i, j int) bool { return ends[i].Before(ends[j]) })
	latestStart, earliestEnd := starts[len(starts)-1], ends[0]
	t.Logf("latest start %s, earliest finish %s", latestStart.Format(time.RFC3339Nano), earliestEnd.Format(time.RFC3339Nano))
	if !latestStart.Before(earliestEnd) {
		t.Errorf("the last run started at %s, after the first finished at %s: the runs did not execute at the same time",
			latestStart.Format(time.RFC3339Nano), earliestEnd.Format(time.RFC3339Nano))
	}
}

func awaitRun1843(t *testing.T, c *client, runID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for {
		run := c.call("manage_script", map[string]any{"command": "get_run", "run_id": runID})
		switch run["status"] {
		case "succeeded", "failed", "canceled":
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s never finished: %v", runID, run)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func when1843(t *testing.T, run map[string]any, key string) time.Time {
	t.Helper()
	raw, _ := run[key].(string)
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatalf("%s = %q: %v", key, raw, err)
	}
	return at
}
