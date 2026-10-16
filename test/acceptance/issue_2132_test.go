//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2132: a webhook source's open window was read from one object per
// segment, so every reader of recent events read thousands of objects. These
// criteria post events one segment at a time, read the open window through
// the raw table as a reader does, counting the objects it reads by the
// engine's "$path" column, and see the segments merged into one rollup that
// holds every event; the source's status reports the window's segments; and a
// save whose settings imply too many segments between rollups is answered
// with a warning.
//
// The dev stack checks for a rollup every 5 seconds and deletes what one
// merged 10 seconds later (dev/platform.yaml), in place of the 1 and 2 minutes
// a deployment gets.
//
// Wire forms: each event is posted as raw JSON bytes (application/json), one
// request per segment; the admin API takes one JSON object per route, with
// config.rollup_every_minutes and config.flush_max_interval_ms as integers;
// trino_query's connection, sql and purpose are strings.

const issue2132Secret = "whsec_2132"

// issue2132AwayFromTheHour waits, when the current hour ends within three
// minutes, for the next one, so the events of a criterion land in one window.
func issue2132AwayFromTheHour() {
	now := time.Now().UTC()
	if left := now.Truncate(time.Hour).Add(time.Hour).Sub(now); left < 3*time.Minute {
		time.Sleep(left + 5*time.Second)
	}
}

// issue2132Paths counts the objects a read of the source's raw table reads,
// and the distinct events it returns.
func issue2132Paths(t *testing.T, c *client, source string) (paths, events int, names string) {
	t.Helper()
	raw := issue1870Table(source) + "_raw"
	paths = issue1870Count(t, c, `SELECT count(DISTINCT "$path") FROM `+raw)
	events = issue1870Count(t, c, "SELECT count(DISTINCT event_id) FROM "+raw)
	got := c.call("trino_query", map[string]any{
		"connection": issue1870Conn, "purpose": issue1870Purpose,
		"sql": `SELECT DISTINCT "$path" AS p FROM ` + raw,
	})
	names = fmt.Sprintf("%v", got["rows"])
	return paths, events, names
}

// TestIssue2132_TheOpenWindowIsReadFromOneRollup: six events posted as six
// segments are read, once the rollup has run and its delete delay passed,
// from one object named as a rollup, and every event is still there once.
func TestIssue2132_TheOpenWindowIsReadFromOneRollup(t *testing.T) {
	issue2132AwayFromTheHour()
	c := connect(t)
	name := issue1870Name("acc2132")
	issue1870Create(t, c, issue1870HMAC(name, issue2132Secret, map[string]any{
		"flush_max_events": 1, "rollup_every_minutes": 1,
	}))
	for i := range 6 {
		body := []byte(fmt.Sprintf(`{"id":"e%d-%s","n":%d}`, i, name, i))
		if res, text := issue1870Signed(t, name, issue2132Secret, "application/json", body); res.StatusCode != http.StatusAccepted {
			t.Fatalf("event %d: status %d: %s", i, res.StatusCode, text)
		}
	}
	issue1870AwaitRows(t, c, name, "", 6)

	waitFor(t, "the open window read from one rollup", func() (bool, string) {
		paths, events, names := issue2132Paths(t, c, name)
		return paths == 1 && events == 6 && strings.Contains(names, "/rollup-"),
			fmt.Sprintf("%d objects, %d events: %s", paths, events, names)
	})
	if n := issue1870Rows(t, c, name, ""); n != 6 {
		t.Fatalf("the view returns %d events after the rollup, not 6", n)
	}

	status, detail := c.rest(http.MethodGet, "/api/v1/admin/webhooks/sources/"+name, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the source: %d %v", status, detail)
	}
	st, _ := detail["status"].(map[string]any)
	if n, _ := st["segments_open_window"].(float64); n < 6 {
		t.Errorf("the source reports %v segments in its open window, not the 6 written: %v", st["segments_open_window"], st)
	}
	overview, out := c.rest(http.MethodGet, "/api/v1/admin/webhooks/status?range=hour&source="+name, http.NoBody)
	sources, _ := out["sources"].([]any)
	if overview != http.StatusOK || len(sources) != 1 {
		t.Fatalf("the status overview: %d %v", overview, out)
	}
	if line, _ := sources[0].(map[string]any); line["segments_per_window_p95"] == nil {
		t.Errorf("the overview carries no segments_per_window_p95: %v", line)
	}
}

// TestIssue2132_ASaveImplyingTooManySegmentsIsWarned: settings under which a
// busy source writes more than a reader should read between rollups are
// saved, with a warning naming the count; settings that do not are saved
// with none.
func TestIssue2132_ASaveImplyingTooManySegmentsIsWarned(t *testing.T) {
	c := connect(t)
	name := issue1870Name("acc2132w")
	body := issue1870HMAC(name, issue2132Secret, map[string]any{"flush_max_interval_ms": 100, "rollup_every_minutes": 5})
	status, out := c.rest(http.MethodPost, "/api/v1/admin/webhooks/sources", jsonBody(t, body))
	if status != http.StatusCreated {
		t.Fatalf("creating the source: %d %v", status, out)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/webhooks/sources/"+name, http.NoBody) })
	warnings := fmt.Sprintf("%v", out["warnings"])
	if !strings.Contains(warnings, "segments between rollups") || !strings.Contains(warnings, "3000") {
		t.Fatalf("the save is not warned of 3000 segments between rollups: %v", out["warnings"])
	}

	update := map[string]any{"auth": body["auth"], "config": map[string]any{"flush_max_interval_ms": 1000}}
	status, out = c.rest(http.MethodPut, "/api/v1/admin/webhooks/sources/"+name, jsonBody(t, update))
	if status != http.StatusOK {
		t.Fatalf("changing the source: %d %v", status, out)
	}
	if strings.Contains(fmt.Sprintf("%v", out["warnings"]), "segments between rollups") {
		t.Fatalf("settings implying 300 segments between rollups are warned: %v", out["warnings"])
	}
}
