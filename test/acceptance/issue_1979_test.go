//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whlayout"
)

// Acceptance for #1979: the status of every webhook source on one page.
//
// Every criterion reads GET /api/v1/admin/webhooks/status, the one request the
// Webhooks page makes, with `range` as a query string (hour, day). Sources are
// created and deleted through the admin API, and requests reach them as raw
// HTTP bytes to /hooks/{source}. Wire forms: the status route takes no body
// and one query parameter; /hooks/{source} is posted application/json.
//
// A compaction that fails cannot be produced by posting to the receiver: every
// segment the receiver writes is readable. It is written as what a corrupted
// object leaves behind, a segment that is not gzip at the key a receiver
// writes and its window recorded the way a receiver records it, for the
// platform's own compactor to fail on.
//
// Counts reach the database when the receiver flushes them, every ten seconds
// by default, so a criterion about counts or rejections polls the route until
// they arrive rather than waiting a fixed time.

// issue1979Status is the route's response, decoded.
type issue1979Status struct {
	From          time.Time `json:"from"`
	BucketSeconds int       `json:"bucket_seconds"`
	Sources       []struct {
		Name        string           `json:"name"`
		Health      string           `json:"health"`
		LastEventAt *time.Time       `json:"last_event_at"`
		LastHour    map[string]int64 `json:"last_hour"`
		Pending     int              `json:"pending"`
		Failing     int              `json:"failing"`
		LastError   string           `json:"last_error"`
	} `json:"sources"`
	Volume []struct {
		Source  string `json:"source"`
		Outcome string `json:"outcome"`
		Count   int64  `json:"count"`
	} `json:"volume"`
	Rejections []struct {
		Source  string `json:"source"`
		Outcome string `json:"outcome"`
		Reason  string `json:"reason"`
	} `json:"rejections"`
}

// issue1979Read reads the status of every source, and the raw body.
func issue1979Read(t *testing.T, c *client, rangeName string) (issue1979Status, string) {
	t.Helper()
	status, body := c.restText("/api/v1/admin/webhooks/status?range=" + rangeName)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/admin/webhooks/status: status %d: %s", status, body)
	}
	var out issue1979Status
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decoding the status: %v\n%s", err, body)
	}
	return out, body
}

// issue1979Until reads the status until ok holds for it, and fails after
// within.
func issue1979Until(t *testing.T, c *client, within time.Duration, what string, ok func(issue1979Status) bool) issue1979Status {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		st, body := issue1979Read(t, c, "hour")
		if ok(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: not reported within %s:\n%s", what, within, body)
		}
		time.Sleep(2 * time.Second)
	}
}

// issue1979Source finds one source's line.
func issue1979Source(st issue1979Status, name string) (int, bool) {
	for i, s := range st.Sources {
		if s.Name == name {
			return i, true
		}
	}
	return 0, false
}

// TestIssue1979_WithNoSourcesEveryListIsEmpty is criterion 6: with no sources
// configured, every list in the response is []. It runs first in this file,
// after every other criterion that creates a source has deleted its own, so a
// stack nobody created a source on by hand has none.
func TestIssue1979_WithNoSourcesEveryListIsEmpty(t *testing.T) {
	c := connect(t)
	st, _ := issue1979Read(t, c, "hour")
	// A source an earlier acceptance run made and could not delete (every one
	// is named acc<ticket>-...) is residue of that run, not a deployment's, and
	// is removed; any other source means this stack is not the empty case.
	for _, src := range st.Sources {
		if strings.HasPrefix(src.Name, "acc") {
			if code, out := c.rest(http.MethodDelete, "/api/v1/admin/webhooks/sources/"+src.Name, http.NoBody); code != http.StatusNoContent {
				t.Fatalf("removing the earlier run's source %s: status %d: %v", src.Name, code, out)
			}
		}
	}
	st, body := issue1979Read(t, c, "hour")
	if len(st.Sources) != 0 {
		names := make([]string, 0, len(st.Sources))
		for _, s := range st.Sources {
			names = append(names, s.Name)
		}
		t.Fatalf("this criterion needs a stack with no webhook source; it has %v", names)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"sources", "volume", "rejections"} {
		if got := string(raw[key]); got != "[]" {
			t.Errorf("%s is %s with no sources, want []", key, got)
		}
	}
	day, _ := issue1979Read(t, c, "day")
	if len(day.Volume) != 0 || len(day.Rejections) != 0 {
		t.Errorf("the day's series or rejections are not empty with no sources: %+v", day)
	}
	if status, _ := c.restText("/api/v1/admin/webhooks/status?range=week"); status != http.StatusBadRequest {
		t.Errorf("an unknown range answers %d, want 400", status)
	}
}

// TestIssue1979_OneRequestReportsAReceivingAndASilentSource is criteria 1
// and 2: one request returns every source, and of a source that has received
// an event and one that has received none, the silent one is marked so.
func TestIssue1979_OneRequestReportsAReceivingAndASilentSource(t *testing.T) {
	c := connect(t)
	busy := issue1870Name("acc1979-busy")
	quiet := issue1870Name("acc1979-quiet")
	issue1870Create(t, c, issue1870HMAC(busy, "whsec_1979", map[string]any{"event_id_path": "$.id"}))
	issue1870Create(t, c, issue1870HMAC(quiet, "whsec_1979", map[string]any{"event_id_path": "$.id"}))

	res, body := issue1870Signed(t, busy, "whsec_1979", "application/json", []byte(`{"id":"evt-1979-1"}`))
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("the signed request answered %d: %s", res.StatusCode, body)
	}

	st, _ := issue1979Read(t, c, "hour")
	b, okBusy := issue1979Source(st, busy)
	q, okQuiet := issue1979Source(st, quiet)
	if !okBusy || !okQuiet {
		t.Fatalf("one request does not report both sources: %+v", st.Sources)
	}
	if got := st.Sources[b].Health; got != "receiving" {
		t.Errorf("the source that received an event is %q, want receiving", got)
	}
	if st.Sources[b].LastEventAt == nil {
		t.Error("the source that received an event reports no last event")
	}
	if got := st.Sources[q].Health; got != "silent" {
		t.Errorf("the source that received nothing is %q, want silent", got)
	}
	if st.Sources[q].LastEventAt != nil {
		t.Errorf("the source that received nothing reports a last event at %v", st.Sources[q].LastEventAt)
	}
}

// TestIssue1979_ABadSignatureShowsWithItsSourceAndReason is criterion 3.
func TestIssue1979_ABadSignatureShowsWithItsSourceAndReason(t *testing.T) {
	c := connect(t)
	name := issue1870Name("acc1979-forged")
	issue1870Create(t, c, issue1870HMAC(name, "whsec_1979", map[string]any{"event_id_path": "$.id"}))

	forged := []byte(`{"id":"evt-1979-forged"}`)
	res, body := issue1870Post(t, baseURL(), name, "application/json", forged,
		map[string]string{"X-Signature": issue1870Sign("not-the-secret", forged)})
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the forged request answered %d: %s", res.StatusCode, body)
	}

	issue1979Until(t, c, time.Minute, "the forged request's rejection", func(st issue1979Status) bool {
		for _, r := range st.Rejections {
			if r.Source == name && r.Outcome == "unauthorized" && r.Reason == "the signature does not match" {
				return true
			}
		}
		return false
	})
}

// TestIssue1979_AFailedCompactionShowsItsCountAndError is criterion 4.
func TestIssue1979_AFailedCompactionShowsItsCountAndError(t *testing.T) {
	c := connect(t)
	db := issue1870DB(t)
	store := issue1870S3(t)
	name := issue1870Name("acc1979-corrupt")
	issue1870Create(t, c, issue1870HMAC(name, "whsec_1979", map[string]any{
		"event_id_path": "$.id", "compact_every_minutes": 1,
	}))

	window := time.Now().UTC().Truncate(time.Minute).Add(-5 * time.Minute)
	issue1870Put(t, store, whlayout.SegmentKey(name, window, "acceptance-corrupt", 1), []byte("this is not a gzip segment"))
	issue1870MarkSegment(t, db, name, window, time.Minute)

	st := issue1979Until(t, c, 2*time.Minute, "the failed compaction", func(st issue1979Status) bool {
		i, ok := issue1979Source(st, name)
		return ok && st.Sources[i].Failing > 0
	})
	i, _ := issue1979Source(st, name)
	s := st.Sources[i]
	if s.Failing != 1 {
		t.Errorf("failing is %d, want 1", s.Failing)
	}
	if s.Health != "failing" {
		t.Errorf("health is %q, want failing", s.Health)
	}
	if !strings.Contains(s.LastError, "segment") {
		t.Errorf("the last error %q does not name the segment that failed", s.LastError)
	}
	var stored string
	if err := db.QueryRowContext(context.Background(), `SELECT last_error FROM webhook_windows WHERE source = $1 AND window_start = $2`,
		name, window).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if s.LastError != stored {
		t.Errorf("the reported error %q is not the window's %q", s.LastError, stored)
	}
}

// TestIssue1979_TheVolumeTotalsMatchTheCountsTable is criterion 5: the
// volume series over the last hour sums, per source and outcome, to the
// request counts table's rows for the same bound.
func TestIssue1979_TheVolumeTotalsMatchTheCountsTable(t *testing.T) {
	c := connect(t)
	db := issue1870DB(t)
	name := issue1870Name("acc1979-volume")
	issue1870Create(t, c, issue1870HMAC(name, "whsec_1979", map[string]any{"event_id_path": "$.id"}))

	for i := range 3 {
		payload := []byte(`{"id":"evt-1979-volume-` + string(rune('a'+i)) + `"}`)
		if res, body := issue1870Signed(t, name, "whsec_1979", "application/json", payload); res.StatusCode != http.StatusAccepted {
			t.Fatalf("signed request %d answered %d: %s", i, res.StatusCode, body)
		}
	}
	forged := []byte(`{"id":"evt-1979-volume-forged"}`)
	issue1870Post(t, baseURL(), name, "application/json", forged,
		map[string]string{"X-Signature": issue1870Sign("not-the-secret", forged)})

	st := issue1979Until(t, c, time.Minute, "the source's counts", func(st issue1979Status) bool {
		i, ok := issue1979Source(st, name)
		return ok && st.Sources[i].LastHour["accepted"] == 3 && st.Sources[i].LastHour["unauthorized"] == 1
	})

	series := map[string]int64{}
	for _, p := range st.Volume {
		if p.Source == name {
			series[p.Outcome] += p.Count
		}
	}
	table := issue1979Counts(t, db, name, st.From)
	if len(series) != len(table) {
		t.Errorf("the series has outcomes %v, the counts table %v", series, table)
	}
	for outcome, n := range table {
		if series[outcome] != n {
			t.Errorf("%s: the series sums to %d over the hour, the counts table to %d", outcome, series[outcome], n)
		}
	}
	i, _ := issue1979Source(st, name)
	for outcome, n := range st.Sources[i].LastHour {
		if series[outcome] != n {
			t.Errorf("%s: the series sums to %d, the source's last hour to %d", outcome, series[outcome], n)
		}
	}
	if st.BucketSeconds != 60 {
		t.Errorf("the hour is drawn in %d-second buckets, want 60", st.BucketSeconds)
	}
}

// issue1979Counts sums a source's per-minute counts at or after from, by
// outcome, straight from the table the series is read from.
func issue1979Counts(t *testing.T, db *sql.DB, source string, from time.Time) map[string]int64 {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT outcome, SUM(count) FROM webhook_request_counts
		WHERE source = $1 AND minute >= $2 GROUP BY outcome`, source, from)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close() //nolint:errcheck // read below
	out := map[string]int64{}
	for rows.Next() {
		var outcome string
		var n int64
		if err := rows.Scan(&outcome, &n); err != nil {
			t.Fatal(err)
		}
		out[outcome] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
