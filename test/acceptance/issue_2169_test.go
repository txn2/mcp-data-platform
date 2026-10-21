//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Acceptance for #2169: a replica held its background locks on a pooled
// connection each, and once the locks outnumbered the free connections it
// served nothing that read the database while /readyz said 200. Every
// try-lock the platform holds across work now shares one session per process.
// A replica holding more locks than its pool has connections keeps one of
// them for all of the locks and goes on answering search, admin reads and
// /readyz; and a pool smaller than the platform's minimum is refused at
// startup, naming the minimum.
//
// The locks are connlimit slots: they are the locks a caller can hold for as
// long as it likes, a slot per call in flight on a connection with
// max_concurrent_calls. The background loops' locks are the same lock on the
// same session, held for as long as a pass runs, and
// internal/advisorylock's RealDB tests hold the loops' own together.
//
// Wire forms: the connection's max_concurrent_calls is sent to the admin API
// as a JSON number; api_invoke_endpoint's query_params is a typed object and
// timeout_seconds a typed integer; search's intent and purpose are strings. The startup
// refusal is the configuration file's database.max_open_conns, a YAML
// integer.

const (
	// issue2169Slots is more slots than a dev replica's pool (10) has
	// connections, so a session per slot would hold every one of them.
	issue2169Slots = 15
	// issue2169Hold is how long each call holds its slot, in milliseconds.
	issue2169Hold = 6000
	// issue2169Answer bounds a request the replica serves while the slots are
	// held.
	issue2169Answer = 3 * time.Second
	// issue2169Purpose is the sentence each search states.
	issue2169Purpose = "Acceptance #2169: a replica holding many locks keeps serving."
)

// issue2169SlotHolders counts the granted two-integer advisory locks (the
// form a slot takes) on the dev database and the sessions holding them.
func issue2169SlotHolders(t *testing.T, db *sql.DB) (locks, sessions int) {
	t.Helper()
	err := db.QueryRowContext(t.Context(), `
		SELECT count(*), count(DISTINCT pid) FROM pg_locks
		 WHERE locktype = 'advisory' AND objsubid = 2 AND granted
		   AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`).Scan(&locks, &sessions)
	if err != nil {
		t.Fatalf("reading the held advisory locks: %v", err)
	}
	return locks, sessions
}

// issue2169Timed runs fn and fails when it takes longer than the bound.
func issue2169Timed(t *testing.T, what string, fn func()) {
	t.Helper()
	start := time.Now()
	fn()
	if took := time.Since(start); took > issue2169Answer {
		t.Errorf("%s took %s while the replica held %d slots; the bound is %s", what, took, issue2169Slots, issue2169Answer)
	}
}

// TestIssue2169_AReplicaHoldingMoreLocksThanConnectionsKeepsServing: fifteen
// calls in flight through one replica on a connection limited to fifteen hold
// fifteen slots on one database session, and the replica answers search, an
// admin read and /readyz within the bound while they do. With a session per
// slot the fifteen needed fifteen of the replica's ten connections, and
// nothing else on it was served until they finished.
func TestIssue2169_AReplicaHoldingMoreLocksThanConnectionsKeepsServing(t *testing.T) {
	target := replicas(t)[0]
	c := connectAtFor(t, target.base, devAPIKey(), 3*time.Minute)
	db := devDB(t)
	name := issue2209Connection(t, c, "2169-slots", issue2169Slots)
	c.call("search", map[string]any{"intent": "acceptance 2169: a replica holding many locks", "purpose": issue2169Purpose})

	before, _ := issue2169SlotHolders(t, db)
	var wg sync.WaitGroup
	failures := make(chan string, issue2169Slots)
	for range issue2169Slots {
		wg.Go(func() {
			if text, ok := issue2209Slow(c, name, issue2169Hold, 60); !ok {
				failures <- text
			}
		})
	}

	deadline := time.Now().Add(issue2169Hold * time.Millisecond / 2)
	var locks, sessions int
	for time.Now().Before(deadline) {
		if locks, sessions = issue2169SlotHolders(t, db); locks-before >= issue2169Slots {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if locks-before < issue2169Slots {
		t.Fatalf("%d slots held after %s; the criterion needs all %d in flight at once", locks-before, issue2169Hold*time.Millisecond/2, issue2169Slots)
	}
	if sessions > 1 {
		t.Errorf("%d slots are held by %d database sessions; a replica holds its locks on one", locks, sessions)
	}

	issue2169Timed(t, "search", func() {
		c.call("search", map[string]any{"intent": "acceptance 2169: served while the slots are held", "purpose": issue2169Purpose})
	})
	issue2169Timed(t, "an admin read", func() {
		if status, out := c.rest(http.MethodGet, "/api/v1/admin/connection-instances", nil); status != http.StatusOK {
			t.Errorf("GET /api/v1/admin/connection-instances: HTTP %d: %v", status, out)
		}
	})
	issue2169Timed(t, "/readyz", func() {
		ctx, cancel := context.WithTimeout(t.Context(), issue2169Answer)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.base+"/readyz", http.NoBody) // #nosec G704 -- the platform under test, named by the suite's own environment
		if err != nil {
			t.Fatal(err)
		}
		res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test, named by the suite's own environment
		if err != nil {
			t.Errorf("/readyz: %v", err)
			return
		}
		_ = res.Body.Close() //nolint:errcheck // only the status is read
		if res.StatusCode != http.StatusOK {
			t.Errorf("/readyz answered %d", res.StatusCode)
		}
	})

	wg.Wait()
	close(failures)
	for f := range failures {
		t.Errorf("a call holding a slot failed: %s", f)
	}
}

// TestIssue2169_APoolBelowTheMinimumIsRefusedAtStartup: the platform started
// with database.max_open_conns 3 exits before it opens anything, naming the
// value and the minimum of 4.
func TestIssue2169_APoolBelowTheMinimumIsRefusedAtStartup(t *testing.T) {
	bin := platformBinary(t)
	cfg := filepath.Join(t.TempDir(), "platform.yaml")
	body := fmt.Sprintf("server:\n  name: acc-2169\n  transport: stdio\ndatabase:\n  dsn: %q\n  max_open_conns: 3\n", issue1694DevDSN())
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--config", cfg).CombinedOutput() // #nosec G204 -- the binary this tree built, with a config this test wrote
	if err == nil {
		t.Fatalf("the platform started with max_open_conns 3:\n%s", out)
	}
	if ctx.Err() != nil {
		t.Fatalf("the platform did not exit within a minute of starting with max_open_conns 3:\n%s", out)
	}
	if want := "database.max_open_conns is 3; the platform needs at least 4"; !strings.Contains(string(out), want) {
		t.Errorf("the refusal does not say %q:\n%s", want, out)
	}
}
