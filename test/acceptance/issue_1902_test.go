//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Issue #1902: a replica's reload channel is a PostgreSQL LISTEN connection,
// and a notification sent while it is down never reaches that replica. Before
// the fix the replica went on serving the configuration it had until a restart.
//
// What this holds, with two replicas over one database: replica B's LISTEN
// connection is terminated, a connection is changed through replica A's admin
// API while B cannot have heard it, and once B is back B serves the changed
// connection without a restart.
//
// lib/pq reconnects the moment a connection is lost, so "before B reconnects"
// cannot be arranged by timing. B's process is paused first: its backend is
// terminated and the change is announced while nothing of B's is listening,
// so the announcement is certainly lost, and B is resumed afterwards. What B
// serves is read through the api-test fixture's echo of the header the
// connection pins, so it is B's live connection that is observed, not the
// stored row every replica reads.
//
// Wire forms: api_invoke_endpoint's `body` admits an object and a string of
// JSON; the echo call sends no body, and `connection`, `method`, `path` and
// `purpose` are strings only. The admin route takes the connection as a JSON
// object.

const (
	issue1902Header  = "X-Acc-1902-Config"
	issue1902Purpose = "Acceptance for #1902: a replica catches up on changes announced while its reload channel was down."
	// issue1902Resync bounds how long B may take to reconnect and re-read the
	// store once resumed. lib/pq reconnects at once; the re-read is one store
	// read and one rebuild per connection.
	issue1902Resync = 60 * time.Second
)

// TestIssue1902_AReplicaServesAChangeAnnouncedWhileItsReloadChannelWasDown is
// the ticket's acceptance sentence.
func TestIssue1902_AReplicaServesAChangeAnnouncedWhileItsReloadChannelWasDown(t *testing.T) {
	found := replicas(t)
	a, b := connectAt(t, found[0].base, devAPIKey()), connectAt(t, found[1].base, devAPIKey())
	name := fmt.Sprintf("acc-1902-%d", time.Now().UnixNano())

	issue1902Put(t, a, name, "before")
	if got := issue1902Served(t, b, name); got != "before" {
		t.Fatalf("B serves %s = %q before the change; want %q", issue1902Header, got, "before")
	}

	pid := issue1902ProcessOn(t, found[1].base)
	issue1902Signal(t, pid, syscall.SIGSTOP)
	resumed := false
	t.Cleanup(func() {
		if !resumed {
			issue1902Signal(t, pid, syscall.SIGCONT)
		}
	})

	killed := issue1902TerminateReloadListeners(t)
	if killed == 0 {
		t.Fatal("no reload LISTEN backend was found to terminate, so this criterion proves nothing")
	}
	issue1902Put(t, a, name, "after")
	if got := issue1902Served(t, a, name); got != "after" {
		t.Fatalf("A serves %q after its own change; want %q", got, "after")
	}

	issue1902Signal(t, pid, syscall.SIGCONT)
	resumed = true

	deadline := time.Now().Add(issue1902Resync)
	var got string
	for time.Now().Before(deadline) {
		if got = issue1902Served(t, b, name); got == "after" {
			return
		}
		time.Sleep(time.Second)
	}
	t.Errorf("B still serves %s = %q %v after it was resumed; want %q, the change announced while its reload channel was down",
		issue1902Header, got, issue1902Resync, "after")
}

// issue1902Put creates or updates the connection through c's admin API with
// the pinned header set to value.
func issue1902Put(t *testing.T, c *client, name, value string) {
	t.Helper()
	status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, map[string]any{
		"config": map[string]any{
			"base_url": issue1647FixtureURL, "connection_name": name, "auth_mode": "none",
			"connect_timeout": "5s", "call_timeout": "10s", "trust_level": "untrusted",
			"static_headers": map[string]any{issue1902Header: value},
		},
		"description": "Acceptance 1902",
	})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("PUT connection %s on %s: HTTP %d", name, c.base, status)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody) })
}

// issue1902Served is the pinned header's value as the fixture received it
// through c's replica.
func issue1902Served(t *testing.T, c *client, name string) string {
	t.Helper()
	res, text, err := c.callRaw(issue1647Tool, map[string]any{
		"connection": name, "method": http.MethodGet, "path": "/v1/echo", "purpose": issue1902Purpose,
	})
	if err != nil || res.IsError {
		t.Fatalf("echo through %s: %v %s", c.base, err, text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("echo through %s is not a JSON object: %v\n%s", c.base, err, text)
	}
	body, _ := out["body"].(map[string]any)
	values := issue1647Header(body, issue1902Header)
	if len(values) != 1 {
		t.Fatalf("echo through %s carried %s = %v; want one value", c.base, issue1902Header, values)
	}
	return values[0]
}

// issue1902ProcessOn is the process listening on base's port. The replicas of
// the dev stack are local processes (dev/start.sh), so it is found by port.
func issue1902ProcessOn(t *testing.T, base string) int {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parsing %s: %v", base, err)
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("%s names no port: %v", base, err)
	}
	out, err := exec.CommandContext(t.Context(), "lsof", "-nP", "-iTCP:"+port, "-sTCP:LISTEN", "-t").Output() // #nosec G204 -- a port the suite discovered
	if err != nil {
		t.Fatalf("finding the process listening on %s: %v", port, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(strings.Split(strings.TrimSpace(string(out)), "\n")[0]))
	if err != nil {
		t.Fatalf("lsof named no process on %s: %q", port, out)
	}
	return pid
}

func issue1902Signal(t *testing.T, pid int, sig syscall.Signal) {
	t.Helper()
	if err := syscall.Kill(pid, sig); err != nil {
		t.Fatalf("signal %v to %d: %v", sig, pid, err)
	}
}

// issue1902TerminateReloadListeners terminates every backend listening on a
// reload channel and reports how many. B's is among them; A's reconnects at
// once, which this criterion does not depend on.
func issue1902TerminateReloadListeners(t *testing.T) int {
	t.Helper()
	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the dev database: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var n int
	err = db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT pg_terminate_backend(pid) FROM pg_stat_activity
			 WHERE query ILIKE 'LISTEN %_reload%') t`).Scan(&n)
	if err != nil {
		t.Fatalf("terminating the reload listeners: %v", err)
	}
	return n
}
