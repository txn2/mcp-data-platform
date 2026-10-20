//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// Acceptance for #2209: a connection's max_concurrent_calls holds its upstream
// to that many calls at once across both replicas, a call over it waits for a
// slot instead of failing, a call whose own time runs out while it waits fails
// naming the limit, list_connections reports the limit, and a script set to
// exclusive "queue" holds a second run pending until the first ends.
//
// Wire forms: the connection's max_concurrent_calls is sent to the admin API
// as a JSON number, the one form its config schema admits, and a string is
// refused rather than read as unlimited; manage_script's exclusive is sent as the
// string "queue" and as the boolean true; api_invoke_endpoint's query_params
// is a typed object and timeout_seconds a typed integer; run_script's
// wait_seconds a typed integer.

const issue2209FixtureURL = "http://localhost:9282"

// issue2209Connection registers an api connection to the fixture with the
// limit sent as given, and returns its name.
func issue2209Connection(t *testing.T, c *client, label string, limit any) string {
	t.Helper()
	name := fmt.Sprintf("acc-2209-%s-%d", label, time.Now().UnixNano())
	status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, map[string]any{
		"config": map[string]any{
			"base_url": issue2209FixtureURL, "connection_name": name, "connect_timeout": "5s",
			"call_timeout": "20s", "max_concurrent_calls": limit,
		},
		"description": "Acceptance 2209: " + label,
	})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register %s connection: HTTP %d", label, status)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody) })
	return name
}

// issue2209Slow calls the fixture's slow route through connection.
func issue2209Slow(c *client, connection string, ms, timeout int) (string, bool) {
	res, text, err := c.callRaw("api_invoke_endpoint", map[string]any{
		"connection": connection, "method": "GET", "path": "/v1/slow",
		"query_params": map[string]any{"ms": ms}, "timeout_seconds": timeout,
		"purpose": "Acceptance #2209: a call held to the connection's limit.",
	})
	if err != nil {
		return err.Error(), false
	}
	return text, !res.IsError
}

// TestIssue2209_ALimitedConnectionTakesOneCallAtATimeAcrossReplicas: four
// calls of 700ms, two through each replica at once, on a connection limited
// to one, all succeed and together take at least four times as long.
func TestIssue2209_ALimitedConnectionTakesOneCallAtATimeAcrossReplicas(t *testing.T) {
	a, b := connectReplicaPair(t)
	for _, limit := range []any{1, float64(1)} {
		name := issue2209Connection(t, a, "replicas", limit)
		const hold = 700
		start := time.Now()
		var wg sync.WaitGroup
		errs := make(chan string, 4)
		for i := range 4 {
			c := a
			if i%2 == 1 {
				c = b
			}
			wg.Go(func() {
				if text, ok := issue2209Slow(c, name, hold, 20); !ok {
					errs <- text
				}
			})
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			t.Errorf("a call over the limit failed instead of waiting (limit sent as %T): %s", limit, e)
		}
		if took := time.Since(start); took < 4*hold*time.Millisecond {
			t.Errorf("four calls on a connection limited to one took %s, less than four calls one after another (limit sent as %T)", took, limit)
		}
	}
}

// TestIssue2209_AWaitPastTheCallsTimeoutNamesTheLimit.
func TestIssue2209_AWaitPastTheCallsTimeoutNamesTheLimit(t *testing.T) {
	c := connect(t)
	name := issue2209Connection(t, c, "wait", 1)
	held := make(chan struct{})
	go func() {
		defer close(held)
		_, _ = issue2209Slow(c, name, 4000, 20)
	}()
	// A short call made before the held one took the slot is answered; the
	// first one made after it waits past its own one-second timeout.
	text, ok := "", true
	for range 3 {
		if text, ok = issue2209Slow(c, name, 10, 1); !ok {
			break
		}
	}
	<-held
	if ok || !strings.Contains(text, "max_concurrent_calls") || !strings.Contains(text, "waited") {
		t.Fatalf("a call whose time ran out waiting for a slot: ok=%v %s", ok, text)
	}
}

// TestIssue2209_ListConnectionsReportsTheLimit.
func TestIssue2209_ListConnectionsReportsTheLimit(t *testing.T) {
	c := connect(t)
	name := issue2209Connection(t, c, "listed", 2)
	out := c.call("list_connections", map[string]any{})
	limits, _ := out["call_limits"].([]any)
	for _, raw := range limits {
		l, _ := raw.(map[string]any)
		if l["connection"] == name && l["kind"] == "api" && fmt.Sprint(l["max_concurrent_calls"]) == "2" {
			return
		}
	}
	t.Fatalf("list_connections' call_limits does not name %s: %v", name, limits)
}

// TestIssue2209_AQueuedScriptRunsOneAfterAnother: with exclusive "queue",
// a second run_script run is accepted, stays pending while the first is
// running, and runs once it ends.
func TestIssue2209_AQueuedScriptRunsOneAfterAnother(t *testing.T) {
	c := connect(t)
	name, _ := issue1986Script(t, c, "queue2209")
	for _, mode := range []any{"queue", true} {
		out := c.call("manage_script", map[string]any{"command": "update", "name": name, "exclusive": mode})
		if out["status"] != "updated" {
			t.Fatalf("update exclusive=%v: %v", mode, out)
		}
	}
	if got := c.call("manage_script", map[string]any{"command": "get", "name": name}); got["exclusive"] != "refuse" {
		t.Fatalf("true is read as refuse: %v", got["exclusive"])
	}
	out := c.call("manage_script", map[string]any{"command": "update", "name": name, "exclusive": "queue"})
	if out["status"] != "updated" {
		t.Fatalf("update exclusive=queue: %v", out)
	}

	start := func() string {
		run := c.call("run_script", map[string]any{
			"name": name, "args": map[string]any{"hold_ms": 1500, "holds": 2}, "wait_seconds": -1,
		})
		id, _ := run["run_id"].(string)
		if id == "" {
			t.Fatalf("run_script returned no run_id: %v", run)
		}
		return id
	}
	first := start()
	await1986(t, c, first, "running")
	second := start()
	if got := c.call("manage_script", map[string]any{"command": "get_run", "run_id": second}); got["status"] != "pending" {
		t.Fatalf("the second run is not held pending while the first runs: %v", got["status"])
	}
	done := await1986(t, c, first, "succeeded")
	next := await1986(t, c, second, "succeeded")
	if fmt.Sprint(next["started_at"]) < fmt.Sprint(done["finished_at"]) {
		t.Fatalf("the queued run started at %v, before the first finished at %v", next["started_at"], done["finished_at"])
	}
}

// TestIssue2209_ALimitThePlatformCannotReadIsRefused: a string where the
// schema takes a number is refused at save, naming the key, rather than
// stored and read as no limit at all.
func TestIssue2209_ALimitThePlatformCannotReadIsRefused(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-2209-refused-%d", time.Now().UnixNano())
	status, out := c.rest(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, jsonBody(t, map[string]any{
		"config":      map[string]any{"base_url": issue2209FixtureURL, "connection_name": name, "max_concurrent_calls": "1"},
		"description": "Acceptance 2209: an unreadable limit.",
	}))
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody) })
	if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out), "max_concurrent_calls must be a whole number") {
		t.Fatalf("a string limit: HTTP %d %v", status, out)
	}
}
