//go:build integration

package acceptance

import (
	"database/sql"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

// Acceptance for #2480: the webhook receiver no longer asks the database
// which automations name a source on every delivery. A burst of deliveries to
// a source no automation names reads the scripts table a handful of times, not
// once per delivery; an automation bound to the source afterwards gets a run
// for the next event without waiting out the re-read interval, and one
// unbound gets none.
//
// Wire forms: each delivery is a JSON object body signed with HMAC (the
// receiver's posted form); manage_script update takes event_source as a
// string, "" to clear, as a literal tools/call param.

const issue2480Secret = "acc-2480-shared-secret"

// issue2480ScriptReads is how many times the scripts table has been scanned
// other than by primary key: the receiver's old per-delivery lookup was one
// such scan per delivery, and the binding map's re-read is one per interval.
func issue2480ScriptReads(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	// Counters reach pg_stat only when a backend flushes them, at most a
	// second after it goes idle; the pause makes the read include them.
	time.Sleep(2 * time.Second)
	var seq, idx int64
	err := db.QueryRowContext(t.Context(), `
		SELECT COALESCE(t.seq_scan, 0),
		       COALESCE((SELECT SUM(i.idx_scan) FROM pg_stat_user_indexes i
		                  WHERE i.relname = 'scripts' AND i.indexrelname <> 'scripts_pkey'), 0)
		  FROM pg_stat_user_tables t WHERE t.relname = 'scripts'`).Scan(&seq, &idx)
	if err != nil {
		t.Fatalf("reading the scripts table's scans: %v", err)
	}
	return seq + idx
}

// issue2480Deliver posts n signed events to source, each with its own id,
// twenty at a time, as a bursting sender does.
func issue2480Deliver(t *testing.T, source string, n int, prefix string) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan string, n)
	sem := make(chan struct{}, 20)
	for i := range n {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			body := []byte(fmt.Sprintf(`{"id": "%s-%d", "type": "sale", "amount": %d}`, prefix, i, i))
			res, text := issue1870Signed(t, source, issue2480Secret, "application/json", body)
			if res.StatusCode != http.StatusAccepted {
				errs <- fmt.Sprintf("delivery %d to %s: %d %s", i, source, res.StatusCode, text)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

func issue2480Source(t *testing.T, admin *client) string {
	t.Helper()
	source := issue1870Name("acc-2480")
	issue1870Create(t, admin, issue1870HMAC(source, issue2480Secret, map[string]any{"event_id_path": "$.id", "rate_limit_per_minute": 100000, "rate_limit_burst": 1000, "flush_max_interval_ms": 50}))
	// A new source is served from the receiver's next re-read of the sources.
	deadline := time.Now().Add(time.Minute)
	for {
		res, _ := issue1870Signed(t, source, issue2480Secret, "application/json", []byte(`{"id": "ready"}`))
		if res.StatusCode == http.StatusAccepted {
			return source
		}
		if time.Now().After(deadline) {
			t.Fatalf("source %s never accepted a delivery: %d", source, res.StatusCode)
		}
		time.Sleep(time.Second)
	}
}

// TestIssue2480_ABurstToAnUnboundSourceDoesNotReadScriptsPerDelivery: 200
// deliveries to a source no automation names, made within a few seconds, scan
// the scripts table far fewer than 200 times.
func TestIssue2480_ABurstToAnUnboundSourceDoesNotReadScriptsPerDelivery(t *testing.T) {
	admin := connect(t)
	db := devDB(t)
	source := issue2480Source(t, admin)
	issue2480Deliver(t, source, 5, "warm") // the map is read once, whatever came before

	before := issue2480ScriptReads(t, db)
	const deliveries = 200
	start := time.Now()
	issue2480Deliver(t, source, deliveries, "burst")
	elapsed := time.Since(start)
	burst := issue2480ScriptReads(t, db) - before

	// The same length of time with no delivery: what the stack's own work and
	// the binding map's re-reads (every 5 seconds on each replica) cost.
	before = issue2480ScriptReads(t, db)
	time.Sleep(elapsed)
	idle := issue2480ScriptReads(t, db) - before

	t.Logf("%d deliveries in %s read scripts %d times; the same time idle, %d", deliveries, elapsed.Round(time.Millisecond), burst, idle)
	if extra := burst - idle; extra >= 30 {
		t.Fatalf("%d deliveries to an unbound source scanned scripts %d times more than idle; the per-delivery lookup is back", deliveries, extra)
	}
}

// TestIssue2480_ABindingReachesTheNextDeliveryAtOnce: an automation bound to
// the source after deliveries began gets a run for the next event, with no
// wait on the re-read interval; after it is unbound, the next event makes no
// run.
func TestIssue2480_ABindingReachesTheNextDeliveryAtOnce(t *testing.T) {
	admin := connect(t)
	source := issue2480Source(t, admin)
	issue2480Deliver(t, source, 3, "before") // the map now holds the source as unbound

	name := issue2332Automation(t, admin, "acc-2480-run", source, nil)
	issue2480Deliver(t, source, 1, "after")
	runs := issue2332Runs(t, admin, name, 1)
	if got := len(runs); got != 1 {
		t.Fatalf("the automation bound after the burst made %d event runs, want 1: %v", got, runs)
	}

	admin.webhookOff(name, source)
	if got := admin.webhookSources(name); len(got) != 0 {
		t.Fatalf("unbinding %s left it running for %v", name, got)
	}
	issue2480Deliver(t, source, 1, "unbound")
	time.Sleep(3 * time.Second) // a run, if one were made, would be enqueued with the delivery
	out := admin.call("manage_script", map[string]any{"command": "runs", "name": name, "limit": 50})
	list, _ := out["runs"].([]any)
	if len(list) != 1 {
		t.Fatalf("after unbinding, the next event made a run: %d runs %v", len(list), list)
	}
}
