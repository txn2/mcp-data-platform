//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Issue #2133: every retention pass ran sync_partition_metadata FULL on each
// source's raw table, listing every raw segment the source keeps. These
// criteria read the coordinator's own record of the statements it ran
// (system.runtime.queries): a retention pass registers the window a source
// wrote into by name and sends no FULL sync, the raw table lists the window,
// and the administrator's resync sends the FULL sync once.
//
// Wire forms: events are posted as raw JSON bytes (application/json); the
// resync is a POST with no body; trino_query's connection, sql and purpose are
// strings.

// issue2133FullSyncs counts the FULL syncs of a raw table the coordinator ran.
func issue2133FullSyncs(t *testing.T, c *client, raw string) int {
	t.Helper()
	return issue1870Count(t, c, "SELECT count(*) FROM system.runtime.queries "+
		"WHERE query LIKE '%sync_partition_metadata%' AND query LIKE '%"+raw+"%' AND query LIKE '%FULL%' "+
		"AND query NOT LIKE '%system.runtime.queries%'")
}

func TestIssue2133_RetentionKeepsRawPartitionsByName(t *testing.T) {
	issue2132AwayFromTheHour()
	c := connect(t)
	name := issue1870Name("acc2133")
	issue1870Create(t, c, issue1870HMAC(name, issue2132Secret, map[string]any{"flush_max_events": 1}))
	raw := "webhook_" + replaceDash(name) + "_raw"
	body := []byte(fmt.Sprintf(`{"id":"e-%s"}`, name))
	if res, text := issue1870Signed(t, name, issue2132Secret, "application/json", body); res.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d: %s", res.StatusCode, text)
	}
	issue1870AwaitRows(t, c, name, "", 1)
	start := time.Now().UTC().Truncate(time.Hour)
	values := fmt.Sprintf("ARRAY[''%s'', ''%s'', ''00'']", start.Format("2006-01-02"), start.Format("15"))

	// A retention pass registers the raw partition of the window written into,
	// by name; the receiver registered it once already. Two such calls mean a
	// pass has run.
	waitFor(t, "a retention pass", func() (bool, string) {
		n := issue1870Count(t, c, "SELECT count(*) FROM system.runtime.queries "+
			"WHERE query LIKE '%register_partition%' AND query LIKE '%"+raw+"%' AND query LIKE '%"+values+"%' "+
			"AND query NOT LIKE '%unregister_partition%' AND query NOT LIKE '%system.runtime.queries%'")
		return n >= 2, fmt.Sprintf("%d registrations of the raw window", n)
	})
	if n := issue2133FullSyncs(t, c, raw); n != 0 {
		t.Fatalf("retention listed the raw table's location %d times", n)
	}
	if n := issue1870Count(t, c, `SELECT count(*) FROM `+issue1870Schema+`."`+raw+`$partitions"`); n < 1 {
		t.Fatalf("the raw table lists no partition for the window written into")
	}

	status, out := c.rest(http.MethodPost, "/api/v1/admin/webhooks/sources/"+name+"/sync-partitions", http.NoBody)
	if status != http.StatusNoContent {
		t.Fatalf("the resync answered %d: %v", status, out)
	}
	if n := issue2133FullSyncs(t, c, raw); n != 1 {
		t.Fatalf("the resync ran the full listing %d times, not once", n)
	}
	if n := issue1870Rows(t, c, name, ""); n != 1 {
		t.Fatalf("the view returns %d events after the resync, not 1", n)
	}
	if status, _ := c.rest(http.MethodPost, "/api/v1/admin/webhooks/sources/nope-2133/sync-partitions", http.NoBody); status != http.StatusNotFound {
		t.Fatalf("a resync of no source answered %d", status)
	}
}

// replaceDash is a source name as its tables name it.
func replaceDash(s string) string {
	out := []byte(s)
	for i, b := range out {
		if b == '-' {
			out[i] = '_'
		}
	}
	return string(out)
}
