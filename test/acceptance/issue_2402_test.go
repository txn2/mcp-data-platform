//go:build integration

package acceptance

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq" // the dev stack's platform database, read for the recording sink
)

// Acceptance for #2402: transactions, batches and the statement cache on an
// application database. Each criterion builds its tables with manage_schema
// in a schema of its own on the dev stack's `orders` (postgres-target).
//
// Wire forms: manage_script takes command, name and source as strings and
// allow_writes as a boolean; manage_schema takes command, connection, schema,
// name and up as strings and version as an integer; endpoint_set takes
// endpoint, path and auth_mode as strings, methods and callers as lists of
// strings and timeout_seconds, rate_limit and caller_rate_limit as integers;
// an endpoint request is an HTTP GET or POST with a bearer key and, for POST,
// a JSON object body. The statements' parameters are Starlark values in the
// script source: integers, strings and a dict per batch statement.

const conn2402 = "orders"

// p99Bound2402 is the bound the ticket's load criterion states: the 99th
// percentile of an endpoint answering 200 requests a second, five statements
// each, on the dev stack with the statement cache on.
const p99Bound2402 = 100 * time.Millisecond

// shop2402 builds a schema holding an order system's tables and returns it.
func shop2402(t *testing.T, admin *client, prefix string) string {
	t.Helper()
	schema := "acc2402_" + prefix + "_" + unique1579()
	for _, m := range []map[string]any{
		{"version": 1, "name": "orders", "up": "CREATE TABLE orders (id int PRIMARY KEY, total numeric(10,2) NOT NULL)"},
		{"version": 2, "name": "items", "up": "CREATE TABLE items (order_id int NOT NULL REFERENCES orders (id), sku text NOT NULL, qty int NOT NULL CONSTRAINT items_qty_positive CHECK (qty > 0))"},
		{"version": 3, "name": "counter", "up": "CREATE TABLE counter (id int PRIMARY KEY, n int NOT NULL); INSERT INTO counter VALUES (1, 0)"},
	} {
		args := map[string]any{"command": "add", "connection": conn2402, "schema": schema}
		for k, v := range m {
			args[k] = v
		}
		admin.call("manage_schema", args)
	}
	admin.call("manage_schema", map[string]any{"command": "apply", "connection": conn2402, "schema": schema})
	return schema
}

// count2402 counts a table's rows in schema with a draft's query.
func count2402(t *testing.T, c *client, schema, table string) string {
	t.Helper()
	out := draft2428(c, "acceptance-2402-count", `
def main():
    """Counts."""
    print(platform.query("SELECT count(*) AS n FROM `+schema+`.`+table+`", connection="`+conn2402+`")["rows"][0]["n"])
`, false)
	if out["status"] != "succeeded" {
		t.Fatalf("counting %s.%s failed: %v", schema, table, out)
	}
	log, _ := out["log"].(string)
	return strings.TrimSpace(log)
}

func TestIssue2402_AFailedLineItemLeavesNoRowsAndTheRunReportsTheConstraint(t *testing.T) {
	admin := connect(t)
	schema := shop2402(t, admin, "atomic")
	ran := draft2428(admin, "acceptance-2402-order", `
def main():
    """Places an order whose third line item is invalid."""
    def place(tx):
        """Inserts the order and its line items."""
        tx.execute("INSERT INTO `+schema+`.orders (id, total) VALUES (:id, :total)", params = {"id": 1, "total": "30.00"})
        for sku, qty in [("a", 1), ("b", 2), ("c", 0)]:
            tx.execute("INSERT INTO `+schema+`.items (order_id, sku, qty) VALUES (:o, :s, :q)", params = {"o": 1, "s": sku, "q": qty})
    platform.transaction("`+conn2402+`", place)
`, true)
	if ran["status"] == "succeeded" {
		t.Fatalf("the order with an invalid line item succeeded: %v", ran)
	}
	if text := asJSON(ran); !strings.Contains(text, "items_qty_positive") {
		t.Errorf("the run does not report the constraint: %s", text)
	}
	for _, table := range []string{"orders", "items"} {
		if n := count2402(t, admin, schema, table); n != "0" {
			t.Errorf("%s holds %s rows after the failed transaction, want 0", table, n)
		}
	}
}

func TestIssue2402_TwoSerializableRunsBothFinishOneAfterARetry(t *testing.T) {
	admin := connect(t)
	schema := shop2402(t, admin, "retry")
	source := `
def main():
    """Increments the counter under serializable isolation."""
    def bump(tx):
        """Reads the counter and writes it back plus one."""
        print("attempt")
        n = tx.query("SELECT n FROM ` + schema + `.counter WHERE id = 1")["rows"][0]["n"]
        platform.sleep(2)
        tx.execute("UPDATE ` + schema + `.counter SET n = :n WHERE id = 1", params = {"n": n + 1})
    platform.transaction("` + conn2402 + `", bump, isolation = "serializable", retry = True)
`
	runs := make([]map[string]any, 2)
	var wg sync.WaitGroup
	for i := range runs {
		c := connect(t)
		wg.Add(1)
		go func() {
			defer wg.Done()
			runs[i] = draft2428(c, fmt.Sprintf("acceptance-2402-bump-%d", i), source, true)
		}()
	}
	wg.Wait()
	attempts := 0
	for i, run := range runs {
		if run["status"] != "succeeded" {
			t.Fatalf("run %d did not finish: %v", i, run)
		}
		log, _ := run["log"].(string)
		attempts += strings.Count(log, "attempt")
	}
	if attempts < 3 {
		t.Errorf("the two runs made %d attempts between them; one should have been retried", attempts)
	}
	out := draft2428(admin, "acceptance-2402-counter", `
def main():
    """Reads the counter."""
    print(platform.query("SELECT n FROM `+schema+`.counter WHERE id = 1", connection="`+conn2402+`")["rows"][0]["n"])
`, false)
	if log, _ := out["log"].(string); strings.TrimSpace(log) != "2" {
		t.Errorf("the counter is %q after two increments, want 2", strings.TrimSpace(log))
	}
}

func TestIssue2402_AKilledRunLeavesNoRowAndNoOpenBackend(t *testing.T) {
	admin := connect(t)
	schema := shop2402(t, admin, "killed")
	ep := endpointName("acc-2402-kill")
	// Order 2's transaction is still running a statement when the request's
	// time limit kills the run; the sample the script is saved with places
	// order 1 and returns.
	handler := `def handle(req):
    """Inserts an order; order 2 is killed by its time limit inside the transaction."""
    oid = int(req["params"]["id"])
    def place(tx):
        """Inserts the order."""
        tx.execute("INSERT INTO ` + schema + `.orders (id, total) VALUES (:id, 1)", params = {"id": oid})
        if oid == 2:
            tx.query("SELECT pg_sleep(8) AS slept")
    platform.transaction("` + conn2402 + `", place)
    return {"json": {"placed": oid}}
`
	saveEndpointScript(t, admin, ep, handler, `{"params": {"id": "1"}, "caller": None}`)
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": ep}) })
	bindEndpoint(t, admin, ep, "", map[string]any{
		"endpoint": ep, "methods": []string{"GET"}, "path": "/{id}", "auth_mode": "authenticated", "callers": []string{"*"},
		"timeout_seconds": 2,
	})
	got := endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+ep+"/2", bearer(admin.apiKey), "")
	if got.status != http.StatusGatewayTimeout {
		t.Fatalf("the handler that outlives its limit answered %d: %s", got.status, got.body)
	}
	if n := count2402(t, admin, schema, "orders WHERE id = 2"); n != "0" {
		t.Errorf("the killed run left %s rows", n)
	}
	open := draft2428(admin, "acceptance-2402-backends", `
def main():
    """Counts the platform's sessions holding a transaction on the orders database."""
    print(platform.query("SELECT count(*) AS n FROM pg_stat_activity WHERE datname = 'orders' AND application_name = 'mcp-data-platform' AND state LIKE 'idle in transaction%'", connection="`+conn2402+`")["rows"][0]["n"])
`, false)
	if log, _ := open["log"].(string); strings.TrimSpace(log) != "0" {
		t.Errorf("pg_stat_activity shows %s sessions still holding a transaction after the run was killed", strings.TrimSpace(log))
	}
}

func TestIssue2402_AnEndpointPostInsertsInATransaction(t *testing.T) {
	admin := connect(t)
	schema := shop2402(t, admin, "post")
	ep := endpointName("acc-2402-post")
	handler := `def handle(req):
    """Places an order and its items in one transaction."""
    body = req["json"]
    def place(tx):
        """Inserts the order and its items."""
        tx.execute("INSERT INTO ` + schema + `.orders (id, total) VALUES (:id, :total)", params = {"id": body["id"], "total": body["total"]})
        tx.batch([{"sql": "INSERT INTO ` + schema + `.items (order_id, sku, qty) VALUES (:o, :s, :q)", "params": {"o": body["id"], "s": i["sku"], "q": i["qty"]}} for i in body["items"]])
        return tx.query("SELECT count(*) AS n FROM ` + schema + `.items WHERE order_id = :o", params = {"o": body["id"]})["rows"][0]["n"]
    n = platform.transaction("` + conn2402 + `", place)
    return {"status": 200, "json": {"items": n}}
`
	saveEndpointScript(t, admin, ep, handler, `{"params": {}, "json": {"id": 1, "total": "5.00", "items": [{"sku": "a", "qty": 1}]}, "caller": None}`)
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": ep}) })
	bindEndpoint(t, admin, ep, "", map[string]any{
		"endpoint": ep, "methods": []string{"POST"}, "path": "/", "auth_mode": "authenticated", "callers": []string{"*"},
	})
	header := bearer(admin.apiKey)
	header.Set("Content-Type", "application/json")
	got := endpointRequest(t, admin.base, http.MethodPost, "/endpoints/"+ep+"/", header,
		`{"id": 7, "total": "12.50", "items": [{"sku": "x", "qty": 2}, {"sku": "y", "qty": 1}]}`)
	if got.status != http.StatusOK || !strings.Contains(got.body, `"items":2`) {
		t.Fatalf("POST /endpoints/%s/ answered %d: %s", ep, got.status, got.body)
	}
	if n := count2402(t, admin, schema, "orders WHERE id = 7"); n != "1" {
		t.Errorf("order 7 is in %s rows after the 200, want 1", n)
	}
	if n := count2402(t, admin, schema, "items WHERE order_id = 7"); n != "2" {
		t.Errorf("order 7 has %s items after the 200, want 2", n)
	}
}

// indexBusy2402 counts the index jobs the stack is embedding or will embed.
const indexBusy2402 = `SELECT COUNT(*) FROM index_jobs WHERE status IN ('pending', 'running')`

// idle2402 waits up to five minutes for the index queue to hold no pending or
// running job, and logs how many were left when it began and if it gave up.
func idle2402(t *testing.T, db *sql.DB) {
	t.Helper()
	start := time.Now()
	first := issue1904Count(t, db, indexBusy2402)
	for left := first; left > 0; left = issue1904Count(t, db, indexBusy2402) {
		if time.Since(start) > 5*time.Minute {
			t.Logf("the index queue still has %d jobs after %s; measuring beside it", left, time.Since(start).Round(time.Second))
			return
		}
		time.Sleep(time.Second)
	}
	t.Logf("the index queue held %d jobs; idle after %s", first, time.Since(start).Round(time.Second))
}

func TestIssue2402_AnEndpointHolds200RequestsASecondWithTheCacheOn(t *testing.T) {
	admin := connect(t)
	schema := shop2402(t, admin, "load")
	ep := endpointName("acc-2402-load")
	handler := `def handle(req):
    """Runs five statements in one transaction."""
    def read(tx):
        """Runs the five reads."""
        tx.query("SELECT count(*) AS n FROM ` + schema + `.orders")
        tx.query("SELECT count(*) AS n FROM ` + schema + `.items")
        tx.query("SELECT n FROM ` + schema + `.counter WHERE id = :id", params = {"id": 1})
        tx.query("SELECT count(*) AS n FROM ` + schema + `.orders WHERE total > :t", params = {"t": "1.00"})
        tx.query("SELECT count(*) AS n FROM ` + schema + `.items WHERE qty > :q", params = {"q": 0})
        return 5
    return {"json": {"statements": platform.transaction("` + conn2402 + `", read, read_only = True)}}
`
	saveEndpointScript(t, admin, ep, handler, `{"params": {}, "caller": None}`)
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": ep}) })
	bindEndpoint(t, admin, ep, "", map[string]any{
		"endpoint": ep, "methods": []string{"GET"}, "path": "/", "auth_mode": "authenticated", "callers": []string{"*"},
		"rate_limit": 100000, "caller_rate_limit": 100000,
	})
	hc := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 400, MaxConnsPerHost: 400}, Timeout: 10 * time.Second}
	header := bearer(admin.apiKey)
	// Warm the cache and the connections, then measure.
	for range 20 {
		endpointRequestWith(t, hc, admin.base, http.MethodGet, "/endpoints/"+ep+"/", header, "")
	}
	// The stack embeds on the CPU it serves from, and an index queue earlier
	// criteria filled competes with every request measured (#2381). The bound
	// is the endpoint's, so it is measured once the queue is idle, and what
	// was left is logged either way.
	db := issue1904DB(t)
	idle2402(t, db)
	before := scrapeRaw(t)

	const rate, seconds = 200, 10
	latencies := make([]time.Duration, 0, rate*seconds)
	var mu sync.Mutex
	var wg sync.WaitGroup
	failures := 0
	tick := time.NewTicker(time.Second / rate)
	defer tick.Stop()
	for range rate * seconds {
		<-tick.C
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, admin.base+"/endpoints/"+ep+"/", http.NoBody) // #nosec G704 -- the platform under test
			req.Header = header.Clone()
			res, err := hc.Do(req)
			took := time.Since(start)
			mu.Lock()
			defer mu.Unlock()
			if err != nil || res.StatusCode != http.StatusOK {
				failures++
			} else {
				latencies = append(latencies, took)
			}
			if res != nil {
				_ = res.Body.Close()
			}
		}()
	}
	wg.Wait()
	if failures > 0 {
		t.Errorf("%d of %d requests failed", failures, rate*seconds)
	}
	slices.Sort(latencies)
	p99 := latencies[len(latencies)*99/100]
	p50 := latencies[len(latencies)/2]
	t.Logf("%d requests at %d/s: p50 %s, p99 %s; %d index jobs pending or running after", len(latencies), rate, p50, p99,
		issue1904Count(t, db, indexBusy2402))
	if p99 > p99Bound2402 {
		t.Errorf("p99 latency %s is over the %s bound", p99, p99Bound2402)
	}

	after := scrapeRaw(t)
	labels := func(result string) map[string]string {
		return map[string]string{"connection": conn2402, "result": result}
	}
	hits := metricSeries(after, "appdb_statement_cache_total", labels("hit")) - metricSeries(before, "appdb_statement_cache_total", labels("hit"))
	misses := metricSeries(after, "appdb_statement_cache_total", labels("miss")) - metricSeries(before, "appdb_statement_cache_total", labels("miss"))
	t.Logf("statement cache: %.0f hits, %.0f misses", hits, misses)
	if hits+misses < float64(len(latencies)*5) || hits/(hits+misses) < 0.9 {
		t.Errorf("the statement cache's hit rate is %.0f/%.0f; a warm cache answers nearly every statement", hits, hits+misses)
	}
}

// platformDB2402 opens the dev stack's platform database, which a sink
// criterion reads directly.
func platformDB2402(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the platform database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestIssue2402_ABoundValueIsSealedInItsRecording(t *testing.T) {
	admin := connect(t)
	schema := shop2402(t, admin, "sealed")
	// The value is assembled in the script from two literals, so the audited
	// source does not hold it; the recording holds it as a bound value.
	const head, tail = "seal-2402-", "-Kq8vX3mPz"
	value := head + unique1579() + tail
	ran := draft2428(admin, "acceptance-2402-sealed", `
def main():
    """Binds a value in a transaction."""
    sku = "`+value[:len(value)-len(tail)]+`" + "`+tail+`"
    def place(tx):
        """Inserts an order and a line item."""
        tx.execute("INSERT INTO `+schema+`.orders (id, total) VALUES (1, 1)")
        tx.execute("INSERT INTO `+schema+`.items (order_id, sku, qty) VALUES (1, :s, 1)", params = {"s": sku})
    platform.transaction("`+conn2402+`", place)
`, true)
	if ran["status"] != "succeeded" {
		t.Fatalf("the draft failed: %v", ran)
	}
	recording, _ := ran["recording"].(string)
	if recording == "" {
		t.Fatalf("the draft named no recording: %v", ran)
	}
	db := platformDB2402(t)
	var raw []byte
	if err := db.QueryRowContext(t.Context(), `SELECT data FROM script_recordings WHERE run_id = $1`, recording).Scan(&raw); err != nil {
		t.Fatalf("reading recording %s: %v", recording, err)
	}
	if !bytes.HasPrefix(raw, []byte("encb:")) {
		t.Fatalf("recording %s is stored unsealed (%q...)", recording, raw[:min(len(raw), 8)])
	}
	if _, err := gzip.NewReader(bytes.NewReader(raw)); err == nil {
		t.Error("the stored recording reads as a gzip stream")
	}
	for _, form := range []string{value, base64.StdEncoding.EncodeToString([]byte(value)), hex.EncodeToString([]byte(value)), url.QueryEscape(value)} {
		if bytes.Contains(raw, []byte(form)) {
			t.Errorf("the stored recording holds the bound value as %q", form)
		}
	}
}
