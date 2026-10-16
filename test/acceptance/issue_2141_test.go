//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2141: the scratch-schema reconcile listed a managed script's working
// tables (managed tables trino_execute created, with no external location) as
// orphans, "not the platform's". A registration is always an external table
// over a stored file, so a managed table is never one. These criteria create a
// managed table and an external table over somewhere else in the scratch
// schema, read the reconcile, and ask it to drop both.
//
// Wire forms: the admin routes take query parameters as strings and one JSON
// object per POST; trino_execute takes connection, sql and purpose as strings.

func TestIssue2141_AManagedTableIsNotAnOrphan(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())

	status, rec := c.rest(http.MethodGet, "/api/v1/admin/tables/reconcile?connection="+scratchResourceConnection, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("the reconcile: %d %v", status, rec)
	}
	schema := fmt.Sprintf("%v.%v", rec["catalog"], rec["schema"])

	// A managed table, as a script's working table is: no external_location.
	working := "acc2141_work_" + stamp
	c.call("trino_execute", map[string]any{
		"connection": scratchResourceConnection,
		"sql":        "CREATE TABLE " + schema + "." + working + " AS SELECT 1 AS batch",
		"purpose":    "Acceptance #2141: a managed table, as a script's working table is.",
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("trino_execute", map[string]any{
			"connection": scratchResourceConnection,
			"sql":        "DROP TABLE IF EXISTS " + schema + "." + working,
			"purpose":    "Acceptance #2141: remove the table the test left.",
		})
	})
	// An external table over somewhere the platform does not store: still an
	// orphan, and still left alone.
	byHand := "acc2141_hand_" + stamp
	orphan2131(t, c, schema+"."+byHand, "s3://dev-scratch-resources/elsewhere-"+stamp+"/")

	status, rec = c.rest(http.MethodGet, "/api/v1/admin/tables/reconcile?connection="+scratchResourceConnection, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("the reconcile: %d %v", status, rec)
	}
	listed := map[string]map[string]any{}
	for _, o := range rec["orphans"].([]any) {
		m, _ := o.(map[string]any)
		listed[m["table"].(string)] = m
	}
	if o, ok := listed[working]; ok {
		t.Fatalf("the managed table is listed as an orphan: %v", o)
	}
	if o := listed[byHand]; o == nil || o["provable"] != false {
		t.Fatalf("the external table over somewhere else is not an unprovable orphan: %v", rec["orphans"])
	}

	body := map[string]any{"connection": scratchResourceConnection, "repair": []any{
		map[string]any{"action": "drop", "table": working},
	}}
	status, out := c.rest(http.MethodPost, "/api/v1/admin/tables/reconcile", jsonBody(t, body))
	if status != http.StatusOK {
		t.Fatalf("the repair: %d %v", status, out)
	}
	outcomes, _ := out["outcomes"].([]any)
	if len(outcomes) != 1 {
		t.Fatalf("the repair reported %v", out)
	}
	r, _ := outcomes[0].(map[string]any)
	if r["done"] != false || !strings.Contains(fmt.Sprint(r["reason"]), "managed table") {
		t.Fatalf("the repair of a managed table: %v", r)
	}
	remaining := c.call("trino_query", map[string]any{
		"connection": scratchResourceConnection, "purpose": "Acceptance #2141: the managed table is still there.",
		"sql": "SELECT DISTINCT table_name FROM " + fmt.Sprint(rec["catalog"]) +
			".information_schema.tables WHERE table_name = '" + working + "'",
	})
	if rows, _ := remaining["rows"].([]any); len(rows) != 1 {
		t.Fatalf("the managed table is gone after the refused drop: %v", remaining["rows"])
	}
}
