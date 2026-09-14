//go:build integration

package acceptance

import (
	"net/http"
	"testing"
	"time"
)

// Issue #1738: every request a criterion makes runs on the context connect
// builds, which was capped at sessionTimeout for every session the suite
// opened. A criterion that waits on something the platform runs on its own
// schedule can outlast that cap, and its last assertions then fail with
// "context deadline exceeded" rather than on the platform's behavior:
// TestIssue1694_ARevocationNobodyActedOnIsEscalated waits 30s for the first
// alert, up to 150s for the escalation sweep and 65s for one sweep after it,
// and failed that way at 122.71s on 2026-09-13 while the next run of the same
// code passed at 84.46s. A criterion now says how long it waits for.
//
// Wire forms: this ticket touches no tool parameter. The criteria call
// `platform_info` (no arguments) and the admin notifications route (a GET with
// no body), which is what the #1694 criterion's final assertions were made of.

// issue1738PastTheDefault is how long these criteria idle before their last
// call: longer than the deadline a session used to be given, so a session that
// still carried it would fail rather than answer.
const issue1738PastTheDefault = sessionTimeout + 15*time.Second

// TestIssue1738_ASessionSizedToItsWaitsOutlivesTheDefault is the ticket's
// criterion: a session opened for longer than sessionTimeout still serves both
// a tool call and a REST read after the default deadline would have passed.
// Both surfaces are asserted because the #1694 failure took the REST reads and
// the cleanup writes down with the tool calls -- they share the one context.
func TestIssue1738_ASessionSizedToItsWaitsOutlivesTheDefault(t *testing.T) {
	c := connectFor(t, issue1738PastTheDefault+2*time.Minute)

	started := time.Now()
	if status, _ := c.rest(http.MethodGet, "/api/v1/admin/notifications?limit=1", http.NoBody); status != http.StatusOK {
		t.Fatalf("the REST read at the start of the session answered HTTP %d", status)
	}

	time.Sleep(issue1738PastTheDefault)

	info := c.call("platform_info", nil)
	if id, _ := info["session_id"].(string); id == "" {
		t.Errorf("platform_info after %s returned no session_id: %v", time.Since(started).Round(time.Second), info)
	}
	status, body := c.rest(http.MethodGet, "/api/v1/admin/notifications?limit=1", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("the REST read %s into the session answered HTTP %d (%v)", time.Since(started).Round(time.Second), status, body)
	}
	if elapsed := time.Since(started); elapsed <= sessionTimeout {
		t.Fatalf("the criterion finished in %s, inside the deadline a session used to carry: it proves nothing", elapsed)
	}
}

// TestIssue1738_TheDefaultStillBoundsAnOrdinarySession keeps the fix off the
// rest of the suite: connect is unchanged, so a criterion that does not ask
// for a longer deadline gets the one every criterion has always had.
func TestIssue1738_TheDefaultStillBoundsAnOrdinarySession(t *testing.T) {
	c := connect(t)
	deadline, ok := c.ctx.Deadline()
	if !ok {
		t.Fatal("an ordinary session carries no deadline; the suite bounds every session")
	}
	if remaining := time.Until(deadline); remaining > sessionTimeout {
		t.Errorf("an ordinary session has %s left, more than the %s default", remaining, sessionTimeout)
	}
}
