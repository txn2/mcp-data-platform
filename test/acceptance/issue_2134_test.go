//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Issue #2134: raw segments were kept a week after compaction, and nothing
// timed the metastore reads a file metastore with caching off makes slow.
// These criteria create a source naming no raw retention and read it back
// with a day, and see the save's partition read made against the real
// catalog: the dev catalogs cache their metastore (dev/trino/catalog), so the
// read is fast and the save carries no warning about it, and a failed read
// would be named in the warnings rather than lost.
//
// Wire forms: the admin API takes one JSON object per route.

func TestIssue2134_ARawSegmentIsKeptADayAndTheSaveTimesThePartitionRead(t *testing.T) {
	c := connect(t)
	name := issue1870Name("acc2134")
	status, out := c.rest(http.MethodPost, "/api/v1/admin/webhooks/sources",
		jsonBody(t, issue1870HMAC(name, issue2132Secret, map[string]any{})))
	if status != http.StatusCreated {
		t.Fatalf("creating the source: %d %v", status, out)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/webhooks/sources/"+name, http.NoBody) })
	warnings := fmt.Sprintf("%v", out["warnings"])
	if strings.Contains(warnings, "partitions") {
		t.Fatalf("the save's partition read was slow or failed on a cached catalog: %v", out["warnings"])
	}

	status, detail := c.rest(http.MethodGet, "/api/v1/admin/webhooks/sources/"+name, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the source: %d %v", status, detail)
	}
	src, _ := detail["source"].(map[string]any)
	cfg, _ := src["config"].(map[string]any)
	if cfg["raw_retention_days"] != float64(1) {
		t.Fatalf("raw_retention_days = %v, not the default of 1", cfg["raw_retention_days"])
	}
	if cfg["rollup_every_minutes"] != float64(5) {
		t.Fatalf("rollup_every_minutes = %v, not the default of 5", cfg["rollup_every_minutes"])
	}
}
