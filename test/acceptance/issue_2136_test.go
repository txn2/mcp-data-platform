//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Issue #2136: nothing listed the scratch schema's tables against the
// registrations, and a table with no registration could not be removed
// through the platform. These criteria list registrations by persona and by
// source, leave a table over a stored file's directory with no registration
// and another over somewhere else, read the reconcile, repair the provable
// one, and see the other refused and left in place.
//
// Wire forms: the admin routes take query parameters as strings and one JSON
// object per POST; manage_resource, manage_table and trino_execute take
// string parameters.

func TestIssue2136_TheReconcileFindsAndRepairsWhatIsProvable(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	reference := file2131(t, c, stamp, "2136")
	reg := register2131(t, c, reference, "acc2136_"+stamp)
	table := queryTable2131(t, reg)
	location := location2131(t, c, table)
	resourceID := strings.TrimPrefix(reference, "mcp:resource:")

	// The registrations listing, by persona and by source.
	status, list := c.rest(http.MethodGet, "/api/v1/admin/tables/registrations?source_kind=resource&source_id="+
		url.QueryEscape(resourceID), http.NoBody)
	if status != http.StatusOK || list["total"] != float64(1) {
		t.Fatalf("the listing of one source: %d %v", status, list)
	}
	persona := strings.SplitN(table[strings.LastIndex(table, ".")+1:], "_", 2)[0]
	status, list = c.rest(http.MethodGet, "/api/v1/admin/tables/registrations?persona="+persona+"&q="+stamp, http.NoBody)
	if status != http.StatusOK || list["total"] != float64(1) {
		t.Fatalf("the listing by persona %s: %d %v", persona, status, list)
	}

	// A table over this file's directory and one over somewhere else, both
	// with no registration.
	schema := table[:strings.LastIndex(table, ".")]
	left := schema + ".acc2136_left_" + stamp
	byHand := schema + ".acc2136_hand_" + stamp
	orphan2131(t, c, left, location)
	orphan2131(t, c, byHand, "s3://dev-scratch-resources/elsewhere-"+stamp+"/")

	status, rec := c.rest(http.MethodGet, "/api/v1/admin/tables/reconcile?connection="+scratchResourceConnection, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("the reconcile: %d %v", status, rec)
	}
	orphans := map[string]map[string]any{}
	for _, o := range rec["orphans"].([]any) {
		m, _ := o.(map[string]any)
		orphans[m["table"].(string)] = m
	}
	short := func(q string) string { return q[strings.LastIndex(q, ".")+1:] }
	if o := orphans[short(left)]; o == nil || o["provable"] != true {
		t.Fatalf("the table over the file's directory is not a provable orphan: %v", rec["orphans"])
	} else if src, _ := o["source"].(map[string]any); src["id"] != resourceID {
		t.Fatalf("the orphan does not name the file it reads: %v", o)
	}
	if o := orphans[short(byHand)]; o == nil || o["provable"] != false {
		t.Fatalf("the table over somewhere else is not reported as unprovable: %v", rec["orphans"])
	}
	if _, listed := orphans[short(table)]; listed {
		t.Fatalf("the registered table is listed as an orphan")
	}

	body := map[string]any{"connection": scratchResourceConnection, "repair": []any{
		map[string]any{"action": "drop", "table": short(left)},
		map[string]any{"action": "drop", "table": short(byHand)},
	}}
	status, out := c.rest(http.MethodPost, "/api/v1/admin/tables/reconcile", jsonBody(t, body))
	if status != http.StatusOK {
		t.Fatalf("the repair: %d %v", status, out)
	}
	outcomes, _ := out["outcomes"].([]any)
	first, _ := outcomes[0].(map[string]any)
	second, _ := outcomes[1].(map[string]any)
	if first["done"] != true {
		t.Fatalf("the provable orphan was not dropped: %v", first)
	}
	if second["done"] != false || !strings.Contains(fmt.Sprintf("%v", second["reason"]), "not the platform's") {
		t.Fatalf("the unprovable table was not refused: %v", second)
	}
	remaining := c.call("trino_query", map[string]any{
		"connection": scratchResourceConnection, "purpose": "Acceptance #2136: which of the two tables remain.",
		// DISTINCT: with the catalog's metastore cache on, Trino lists a table
		// twice when the IN list also names one dropped a moment before.
		"sql": "SELECT DISTINCT table_name FROM " + schema[:strings.Index(schema, ".")] +
			".information_schema.tables WHERE table_name IN ('" + short(left) + "', '" + short(byHand) + "')",
	})
	if rows, _ := remaining["rows"].([]any); len(rows) != 1 || !strings.Contains(fmt.Sprintf("%v", rows), short(byHand)) {
		t.Fatalf("after the repair %v remain, not only the table left alone; outcomes %v", remaining["rows"], outcomes)
	}
}
