//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2408: an application database as the input of a webhook
// source. An outbox source files each row its trigger writes, at once on the
// notification and on a poll for the rows no one heard; a notify source files
// each pg_notify payload and refuses one past its bound. Each criterion keeps
// its tables in a schema of its own on the dev stack's `orders` application
// database, built with manage_schema's outbox template, and names a source of
// its own on a script that hands back the event it ran for.
//
// Wire forms: the admin source route takes one JSON object whose
// config.postgres is a JSON object (connection, mode and table strings,
// channels a JSON array of strings, poll_seconds and max_payload_bytes JSON
// numbers); manage_schema add takes template "outbox" and capture as a JSON
// array of strings; manage_script update takes event_source as a string. No
// parameter admits a second form.

// echo2408 hands back the event its run is for.
const echo2408 = `def main():
    """Hands back the event this run is for."""
    ev = run.event or {"id": "", "payload": {}}
    platform.result({"event_id": ev["id"], "payload": ev["payload"]})
`

// outbox2408 builds a schema holding an orders table whose rows are written
// to an outbox, and returns the schema.
func outbox2408(t *testing.T, admin *client) string {
	t.Helper()
	schema := "acc2408_" + unique1579()
	holdSchema(t, admin, conn2404, schema)
	schemaCall(admin, schema, map[string]any{
		"command": "add", "version": 1, "name": "orders",
		"up": "CREATE TABLE orders (id int PRIMARY KEY, total numeric(10,2) NOT NULL)",
	})
	schemaCall(admin, schema, map[string]any{"command": "add", "version": 2, "template": "outbox", "capture": []string{"orders"}})
	schemaCall(admin, schema, map[string]any{"command": "apply"})
	t.Cleanup(func() { targetPSQL(t, "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })
	return schema
}

// source2408 creates a postgres source reading pg, and an automation naming
// it, and returns the source's and the script's names.
func source2408(t *testing.T, admin *client, prefix string, pg map[string]any) (source, script string) {
	t.Helper()
	source = endpointName(prefix)
	issue1870Create(t, admin, map[string]any{
		"name": source, "connection": issue1870Conn,
		"config": map[string]any{"input": "postgres", "postgres": pg},
	})
	script = endpointName(prefix + "-run")
	admin.saveScript(map[string]any{"command": "create", "name": script, "source": echo2408}, nil)
	admin.webhookSet(script, source)
	t.Cleanup(func() {
		admin.webhookOff(script, source)
		admin.callRaw("manage_script", map[string]any{"command": "delete", "name": script})
	})
	return source, script
}

// payloads2408 are the payloads of a script's first want ended event runs.
func payloads2408(t *testing.T, admin *client, script string, want int) []map[string]any {
	t.Helper()
	runs := issue2332Runs(t, admin, script, want)
	out := make([]map[string]any, 0, len(runs))
	for _, r := range runs {
		if r["status"] != "succeeded" {
			t.Fatalf("an event run of %s failed: %v", script, r)
		}
		res := issue2332Result(t, admin, fmt.Sprint(r["run_id"]))
		p, _ := res["payload"].(map[string]any)
		out = append(out, p)
	}
	return out
}

// listening2408 waits for a replica to listen for source.
func listening2408(t *testing.T, admin *client, source string) map[string]any {
	t.Helper()
	var state map[string]any
	waitFor(t, "a replica listening for "+source, func() (bool, string) {
		_, detail := admin.rest(http.MethodGet, "/api/v1/admin/webhooks/sources/"+source, http.NoBody)
		state, _ = detail["postgres"].(map[string]any)
		return state["replica"] != nil && state["replica"] != "", fmt.Sprint(state)
	})
	return state
}

func TestIssue2408_AnInsertIntoAnOutboxedTableRunsTheScriptWithTheRowWithinSeconds(t *testing.T) {
	admin := connect(t)
	schema := outbox2408(t, admin)
	source, script := source2408(t, admin, "acc-2408-outbox", map[string]any{
		"connection": conn2404, "mode": "outbox", "table": schema + ".outbox", "poll_seconds": 300,
	})
	listening2408(t, admin, source)

	started := time.Now()
	targetPSQL(t, "INSERT INTO "+schema+".orders VALUES (7, 12.50)")
	got := payloads2408(t, admin, script, 1)
	took := time.Since(started)
	if took > 30*time.Second {
		t.Errorf("the run started %s after the insert; with a 300 s poll it must come from the notification", took)
	}
	p := got[0]
	row, _ := p["row"].(map[string]any)
	if p["table"] != schema+".orders" || p["op"] != "INSERT" || fmt.Sprint(row["id"]) != "7" || row["total"] != 12.5 {
		t.Errorf("the event must carry the inserted row: %v", p)
	}
	if n := strings.TrimSpace(targetPSQL(t, "SELECT count(*) FROM "+schema+".outbox")); n != "0" {
		t.Errorf("the outbox holds %s rows after they were filed; want none", n)
	}
	t.Logf("insert to finished run: %s", took)
}

func TestIssue2408_RowsWrittenWhileNoReplicaListensAreDeliveredOnceEachWhenOneListensAgain(t *testing.T) {
	admin := connect(t)
	schema := outbox2408(t, admin)
	pg := map[string]any{"connection": conn2404, "mode": "outbox", "table": schema + ".outbox", "poll_seconds": 300}
	source, script := source2408(t, admin, "acc-2408-restart", pg)
	listening2408(t, admin, source)

	// Disabling the source stops its listener on every replica: no session
	// listens, so the notifications these inserts send reach no one.
	off := false
	if status, out := admin.rest(http.MethodPut, "/api/v1/admin/webhooks/sources/"+source, jsonBody(t, map[string]any{
		"enabled": off, "config": map[string]any{"input": "postgres", "postgres": pg},
	})); status != http.StatusOK {
		t.Fatalf("disabling %s: %d %v", source, status, out)
	}
	waitFor(t, "no replica listening for "+source, func() (bool, string) {
		_, detail := admin.rest(http.MethodGet, "/api/v1/admin/webhooks/sources/"+source, http.NoBody)
		state, _ := detail["postgres"].(map[string]any)
		return state["replica"] == "", fmt.Sprint(state)
	})
	targetPSQL(t, "INSERT INTO "+schema+".orders VALUES (1, 1.00), (2, 2.00), (3, 3.00)")
	if n := strings.TrimSpace(targetPSQL(t, "SELECT count(*) FROM "+schema+".outbox")); n != "3" {
		t.Fatalf("the outbox holds %s rows while no one listens; want 3", n)
	}

	on := true
	if status, out := admin.rest(http.MethodPut, "/api/v1/admin/webhooks/sources/"+source, jsonBody(t, map[string]any{
		"enabled": on, "config": map[string]any{"input": "postgres", "postgres": pg},
	})); status != http.StatusOK {
		t.Fatalf("enabling %s: %d %v", source, status, out)
	}
	got := payloads2408(t, admin, script, 3)
	ids := []string{}
	for _, p := range got {
		row, _ := p["row"].(map[string]any)
		ids = append(ids, fmt.Sprint(row["id"]))
	}
	sort.Strings(ids)
	if strings.Join(ids, ",") != "1,2,3" {
		t.Fatalf("the three rows must each be delivered once: %v", ids)
	}
	// Nothing more arrives: no row is delivered twice.
	time.Sleep(10 * time.Second)
	out := admin.call("manage_script", map[string]any{"command": "runs", "name": script, "limit": 50})
	if runs, _ := out["runs"].([]any); len(runs) != 3 {
		t.Errorf("%s has %d runs, want exactly 3: %v", script, len(runs), runs)
	}
	if n := strings.TrimSpace(targetPSQL(t, "SELECT count(*) FROM "+schema+".outbox")); n != "0" {
		t.Errorf("the outbox holds %s rows after delivery; want none", n)
	}
}

func TestIssue2408_ANotifySourceDeliversPayloadsAndRefusesAnOversizedOne(t *testing.T) {
	admin := connect(t)
	channel := "acc2408_" + unique1579()
	source, script := source2408(t, admin, "acc-2408-notify", map[string]any{
		"connection": conn2404, "mode": "notify", "channels": []string{channel}, "max_payload_bytes": 64,
	})
	listening2408(t, admin, source)

	targetPSQL(t, `SELECT pg_notify('`+channel+`', '{"order": 7, "status": "paid"}')`)
	got := payloads2408(t, admin, script, 1)
	inner, _ := got[0]["payload"].(map[string]any)
	if got[0]["channel"] != channel || fmt.Sprint(inner["order"]) != "7" || inner["status"] != "paid" {
		t.Errorf("the event must carry the notification's payload: %v", got[0])
	}

	targetPSQL(t, `SELECT pg_notify('`+channel+`', '`+strings.Repeat("x", 100)+`')`)
	var state map[string]any
	waitFor(t, "the oversized payload counted as refused", func() (bool, string) {
		_, detail := admin.rest(http.MethodGet, "/api/v1/admin/webhooks/sources/"+source, http.NoBody)
		state, _ = detail["postgres"].(map[string]any)
		rejected, _ := state["rejected"].(float64)
		return rejected >= 1, fmt.Sprint(state)
	})
	_, detail := admin.rest(http.MethodGet, "/api/v1/admin/webhooks/sources/"+source, http.NoBody)
	if !strings.Contains(fmt.Sprint(detail["status"]), "too_large") {
		t.Errorf("the source's rejections must list the oversized payload as too_large: %v", detail["status"])
	}
	if strings.Contains(fmt.Sprint(detail), strings.Repeat("x", 100)) {
		t.Error("the refusal repeats the payload")
	}
	out := admin.call("manage_script", map[string]any{"command": "runs", "name": script, "limit": 50})
	if runs, _ := out["runs"].([]any); len(runs) != 1 {
		t.Errorf("an oversized payload must run nothing: %d runs", len(runs))
	}
}
