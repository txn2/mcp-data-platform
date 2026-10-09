//go:build integration

package acceptance

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Issue #2058: a managed-script run in flight on a replica that is told to
// stop finishes instead of failing with `calling "tools/call": EOF`.
//
// The criterion is a rolling update's step on the dev stack's two replicas: a
// run is made to execute on replica B (A is paused with SIGSTOP while B claims
// it, then resumed), B is sent SIGTERM while the run is mid-way through its
// slow calls, and the run is read back through A. Before the fix B's drain
// closed every MCP session three seconds after the pre-shutdown delay, the
// run's in-process session among them, and the run's next tools/call read EOF
// and was recorded as failed. B is brought back afterwards by rewriting a Go
// source file with its own bytes, which both replicas' air processes rebuild
// on (dev/ is outside what air watches).
//
// Wire forms: run_script's name is a typed string, args a typed object (two
// integers) and wait_seconds a typed integer (negative: queue and return at
// once); manage_script's command and run_id are typed strings. Each admits one
// JSON form and is sent as a literal tools/call parameter of that form.

// The run makes holds2058 slow calls of holdMS2058 each: long enough to be
// mid-way when SIGTERM lands, and well inside the drain's default budget (a
// 2s pre-shutdown delay and a 25s grace period).
const (
	holdMS2058 = 4000
	holds2058  = 4
)

func TestIssue2058_ARunInFlightAtSIGTERMFinishes(t *testing.T) {
	reps := replicas(t)
	if len(reps) < 2 {
		t.Fatalf("the dev stack serves %d replica(s); this criterion needs two (DEV_REPLICAS=2)", len(reps))
	}
	a, b := reps[0], reps[1]
	pidA, pidB := issue1902ProcessOn(t, a.base), issue1902ProcessOn(t, b.base)
	t.Cleanup(func() { restartReplicas2058(t, b.base, a.base) })

	ca := connectAt(t, a.base, devAPIKey())
	name, _ := issue1986Script(t, ca, "2058")

	// Only B may claim: A is paused until B is executing the run.
	issue1902Signal(t, pidA, syscall.SIGSTOP)
	resumed := false
	resume := func() {
		if !resumed {
			issue1902Signal(t, pidA, syscall.SIGCONT)
			resumed = true
		}
	}
	t.Cleanup(resume)
	cb := connectAt(t, b.base, devAPIKey())
	out := cb.call("run_script", map[string]any{
		"name": name, "args": map[string]any{"hold_ms": holdMS2058, "holds": holds2058}, "wait_seconds": -1,
	})
	runID, _ := out["run_id"].(string)
	if runID == "" {
		t.Fatalf("run_script returned no run_id: %v", out)
	}
	await1986(t, cb, runID, "running")
	resume()

	// One slow call in, B is told to stop.
	time.Sleep(holdMS2058 * time.Millisecond / 2)
	signalled := time.Now()
	issue1902Signal(t, pidB, syscall.SIGTERM)

	run := terminalRun2058(t, ca, runID)
	if run["status"] != "succeeded" {
		t.Fatalf("the run in flight at SIGTERM ended %v: %v", run["status"], run)
	}
	if attempt, _ := run["attempt"].(float64); attempt != 1 {
		t.Errorf("attempt = %v; the run finished on the worker that claimed it, not a retry", run["attempt"])
	}
	if reclaims, _ := run["reclaims"].(float64); reclaims != 0 {
		t.Errorf("reclaims = %v; no other worker took the run over", run["reclaims"])
	}
	finished, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(stringOf(run["finished_at"])))
	if err != nil {
		t.Fatalf("finished_at %v: %v", run["finished_at"], err)
	}
	// The session close that used to end the run comes at the pre-shutdown
	// delay plus the three-second settle; the run outlived it on B.
	if after := finished.Sub(signalled); after < 5*time.Second {
		t.Errorf("the run finished %s after SIGTERM; it should have outlived the session close", after)
	}

	// B drained and exited on its own once the run was done.
	deadline := time.Now().Add(30 * time.Second)
	for !errors.Is(syscall.Kill(pidB, 0), syscall.ESRCH) {
		if time.Now().After(deadline) {
			t.Fatalf("replica B (pid %d) was still running 30s after the run finished", pidB)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// terminalRun2058 polls get_run until the run is no longer pending or running.
func terminalRun2058(t *testing.T, c *client, runID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		run := c.call("manage_script", map[string]any{"command": "get_run", "run_id": runID})
		if s := run["status"]; s != "pending" && s != "running" {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not finish: %v", runID, run)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// restartReplicas2058 brings the stopped replica back: both replicas' air
// processes rebuild and restart on a write to a Go source file, and the file
// is written with the bytes it already holds. It waits until each answers
// /readyz, the stopped one first, so a later criterion in the same run is not
// served by a replica still starting.
func restartReplicas2058(t *testing.T, bases ...string) {
	t.Helper()
	src := filepath.Join("..", "..", "cmd", "mcp-data-platform", "main.go")
	body, err := os.ReadFile(src) // #nosec G304 -- a fixed path in this repository
	if err == nil {
		err = os.WriteFile(src, body, 0o644) // #nosec G306 -- the file's own mode in the checkout
	}
	if err != nil {
		t.Errorf("rewriting %s to restart the replicas: %v", src, err)
		return
	}
	deadline := time.Now().Add(3 * time.Minute)
	for _, base := range bases {
		for {
			// A cleanup runs after t.Context() is canceled.
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/readyz", http.NoBody) // #nosec G704 -- the platform under test
			if res, err := http.DefaultClient.Do(req); err == nil {
				_ = res.Body.Close()
				if res.StatusCode == http.StatusOK {
					break
				}
			}
			if time.Now().After(deadline) {
				t.Errorf("replica %s did not come back after the restart", base)
				return
			}
			time.Sleep(time.Second)
		}
	}
}
