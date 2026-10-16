//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Issue #2140: v1.146.1 merged a webhook source's open window into one object
// and deleted the merged segments two minutes later. A query that listed the
// window before the delete and read after it failed (HIVE_CANNOT_OPEN_SPLIT),
// and until the delete every merged event was returned twice. These criteria
// read a source's open window through the real receiver, compactor and query
// engine: an event a sender delivered twice is returned once by the view while
// the raw table holds both copies, the segments of the open window are all
// still there to read, the view is the one that deduplicates, and the source's
// status reports the window's segments.
//
// Wire forms: each event is posted as raw JSON bytes (application/json), one
// request per segment; the admin API takes one JSON object per route, with
// config.flush_max_events as an integer and config.event_id_path as a string;
// trino_query's connection, sql and purpose are strings.

const issue2140Secret = "whsec_2140"

// issue2140Post posts one event as its own segment.
func issue2140Post(t *testing.T, source, body string) {
	t.Helper()
	if res, text := issue1870Signed(t, source, issue2140Secret, "application/json", []byte(body)); res.StatusCode != http.StatusAccepted {
		t.Fatalf("posting %s: status %d: %s", body, res.StatusCode, text)
	}
}

// TestIssue2140_ARetriedEventIsReadOnce: an event delivered twice is in the
// raw table twice and returned by the view once, beside an event delivered
// once, so the open window answers what its compaction will.
func TestIssue2140_ARetriedEventIsReadOnce(t *testing.T) {
	issue2132AwayFromTheHour()
	c := connect(t)
	name := issue1870Name("acc2140d")
	issue1870Create(t, c, issue1870HMAC(name, issue2140Secret, map[string]any{
		"flush_max_events": 1, "event_id_path": "$.id",
	}))
	issue2140Post(t, name, `{"id":"retried","n":1}`)
	issue2140Post(t, name, `{"id":"retried","n":1}`)
	issue2140Post(t, name, `{"id":"once","n":2}`)

	issue1870AwaitCount(t, c, "SELECT count(*) FROM "+issue1870Table(name)+"_raw", 3)
	if n := issue1870Rows(t, c, name, ""); n != 2 {
		t.Fatalf("the view returns %d events for two distinct ids delivered three times, not 2", n)
	}
	if n := issue1870Rows(t, c, name, "event_id = 'retried'"); n != 1 {
		t.Fatalf("the view returns the retried event %d times, not once", n)
	}
}

// TestIssue2140_TheOpenWindowKeepsItsSegments: six events posted as six
// segments are read from six segment objects, none merged into another, and
// the source's status reports the window's segments.
func TestIssue2140_TheOpenWindowKeepsItsSegments(t *testing.T) {
	issue2132AwayFromTheHour()
	c := connect(t)
	name := issue1870Name("acc2140s")
	issue1870Create(t, c, issue1870HMAC(name, issue2140Secret, map[string]any{"flush_max_events": 1}))
	for i := range 6 {
		issue2140Post(t, name, fmt.Sprintf(`{"id":"e%d-%s","n":%d}`, i, name, i))
	}
	issue1870AwaitRows(t, c, name, "", 6)

	raw := issue1870Table(name) + "_raw"
	if n := issue1870Count(t, c, `SELECT count(DISTINCT "$path") FROM `+raw); n != 6 {
		t.Fatalf("the open window is read from %d objects, not its 6 segments", n)
	}
	if n := issue1870Count(t, c, `SELECT count(*) FROM `+raw+` WHERE "$path" LIKE '%/rollup-%'`); n != 0 {
		t.Fatalf("%d events were read from a merged object", n)
	}

	got := c.call("trino_query", map[string]any{
		"connection": issue1870Conn, "purpose": issue1870Purpose,
		"sql": "SHOW CREATE VIEW " + issue1870Table(name),
	})
	if view := fmt.Sprintf("%v", got["rows"]); !strings.Contains(view, "row_number()") || !strings.Contains(view, `"event_id"`) {
		t.Fatalf("the view does not keep one event per event id: %s", view)
	}

	status, detail := c.rest(http.MethodGet, "/api/v1/admin/webhooks/sources/"+name, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the source: %d %v", status, detail)
	}
	st, _ := detail["status"].(map[string]any)
	if n, _ := st["segments_open_window"].(float64); n < 6 {
		t.Errorf("the source reports %v segments in its open window, not the 6 written: %v", st["segments_open_window"], st)
	}
	src, _ := detail["source"].(map[string]any)
	if cfg, _ := src["config"].(map[string]any); cfg["rollup_every_minutes"] != nil {
		t.Errorf("the source still carries rollup_every_minutes: %v", cfg)
	}
}
