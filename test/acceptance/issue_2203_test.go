//go:build integration

package acceptance

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2203: a fresh install's setup was fitted as growth and
// shown as a fill date. A store's first day is now left out of every fit, a
// 30-day rate waits for two weeks of history after it, and each tick is
// sampled by one replica: the other finds the sample and skips.
//
// Wire forms: this ticket adds no tool parameter. GET
// /api/v1/admin/storage takes no parameter; GET /api/v1/admin/storage/history
// takes store and days as query strings, the one form a query has.

// issue2203Interval is the dev stack's storage sampling interval, the
// default.
const issue2203Interval = 15 * time.Minute

// issue2203Point is one stored sample as the history route answers it.
type issue2203Point struct {
	At   time.Time `json:"at"`
	Used int64     `json:"used_bytes"`
}

// issue2203Report is the part of the storage report the criteria read.
type issue2203Report struct {
	GeneratedAt time.Time `json:"generated_at"`
	Stores      []struct {
		Store       string   `json:"store"`
		Used        int64    `json:"used_bytes"`
		History     float64  `json:"history_hours"`
		Rate7d      *float64 `json:"rate_7d_bytes_per_day"`
		Rate30d     *float64 `json:"rate_30d_bytes_per_day"`
		Min7d       float64  `json:"rate_7d_min_history_hours"`
		Min30d      float64  `json:"rate_30d_min_history_hours"`
		ExhaustedAt *string  `json:"exhaustion_at"`
	} `json:"stores"`
}

// issue2203Get reads one admin route as the suite's administrator.
func issue2203Get(t *testing.T, c *client, path string, into any) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, c.base+path, http.NoBody) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: HTTP %d: %s", path, res.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("GET %s: %v\n%s", path, err, raw)
	}
}

// issue2203Slope is the least-squares slope of used bytes over the points at
// or after from, in bytes per day; ok is false with fewer than two.
func issue2203Slope(points []issue2203Point, from time.Time) (slope float64, ok bool) {
	var in []issue2203Point
	for _, p := range points {
		if !p.At.Before(from) {
			in = append(in, p)
		}
	}
	if len(in) < 2 {
		return 0, false
	}
	x := func(p issue2203Point) float64 { return p.At.Sub(in[0].At).Hours() / 24 }
	var mx, my float64
	for _, p := range in {
		mx += x(p)
		my += float64(p.Used)
	}
	n := float64(len(in))
	mx, my = mx/n, my/n
	var sxx, sxy float64
	for _, p := range in {
		sxx += (x(p) - mx) * (x(p) - mx)
		sxy += (x(p) - mx) * (float64(p.Used) - my)
	}
	return sxy / sxx, sxx > 0
}

// TestIssue2203_TheFitLeavesTheStoresFirstDayOut: the database's weekly rate
// on the admin storage report is the fit of its samples from the end of its
// first day, not from its first sample; each rate names the history it needs
// counting that first day; and a store with less than that has no rate.
func TestIssue2203_TheFitLeavesTheStoresFirstDayOut(t *testing.T) {
	c := connect(t)
	var report issue2203Report
	issue2203Get(t, c, "/api/v1/admin/storage", &report)
	var history struct {
		Points []issue2203Point `json:"points"`
	}
	issue2203Get(t, c, "/api/v1/admin/storage/history?store=database&days=30", &history)

	for _, s := range report.Stores {
		if s.Store != "database" {
			continue
		}
		if s.Min7d != 48 || s.Min30d != 360 {
			t.Errorf("a store whose whole history is kept needs 48 h for a weekly rate and 360 h for a monthly one; the report says %v and %v", s.Min7d, s.Min30d)
		}
		if s.History < s.Min7d {
			if s.Rate7d != nil || s.ExhaustedAt != nil {
				t.Errorf("%.1f h of history, short of %v, and the report gives a rate %v and a fill date %v", s.History, s.Min7d, s.Rate7d, s.ExhaustedAt)
			}
			return
		}
		if s.History < s.Min30d && s.Rate30d != nil {
			t.Errorf("%.1f h of history, short of %v, and the report gives a monthly rate", s.History, s.Min30d)
		}
		var points []issue2203Point
		for _, p := range history.Points {
			if p.At.Before(report.GeneratedAt) {
				points = append(points, p)
			}
		}
		points = append(points, issue2203Point{At: report.GeneratedAt, Used: s.Used})
		from := points[0].At.Add(24 * time.Hour)
		if week := report.GeneratedAt.Add(-7 * 24 * time.Hour); week.After(from) {
			from = week
		}
		want, ok := issue2203Slope(points, from)
		if !ok || s.Rate7d == nil {
			t.Fatalf("the report's weekly rate %v, and %d samples after the first day", s.Rate7d, len(points))
		}
		withSetup, _ := issue2203Slope(points, points[0].At)
		t.Logf("database: weekly rate %.0f B/day reported, %.0f B/day fitted from the end of the first day, %.0f B/day fitted from the first sample", *s.Rate7d, want, withSetup)
		if math.Abs(*s.Rate7d-want) > math.Max(math.Abs(want)*0.01, 1) {
			t.Errorf("the weekly rate is %.0f B/day; fitted from the end of the store's first day it is %.0f", *s.Rate7d, want)
		}
		return
	}
	t.Fatal("the storage report names no database store")
}

// issue2203Scrape reads one replica's metrics and returns the value of the
// unlabeled series name, and of name with the given label text.
func issue2203Scrape(t *testing.T, url, line string) float64 {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, _ := io.ReadAll(res.Body)
	for l := range strings.SplitSeq(string(raw), "\n") {
		if strings.HasPrefix(l, line+" ") {
			v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(l, line)), 64)
			if err == nil {
				return v
			}
		}
	}
	return 0
}

// TestIssue2203_EachTickIsSampledByOneReplica: since the replicas started,
// no two database samples lie within one sampling tick of each other, and a
// replica that found the other's sample counted its pass as skipped. Each
// replica runs a pass as it starts, so two replicas started together sampled
// the same tick twice before.
func TestIssue2203_EachTickIsSampledByOneReplica(t *testing.T) {
	c := connect(t)
	urls := metricsURLs()
	if len(urls) < 2 {
		t.Fatal("this criterion reads both replicas' metrics: run `make dev` with two replicas, or set ACCEPTANCE_METRICS_URL to both")
	}
	var started time.Time
	var skipped float64
	for _, u := range urls {
		start := time.Unix(int64(issue2203Scrape(t, u, "process_start_time_seconds")), 0)
		if started.IsZero() || start.Before(started) {
			started = start
		}
		skipped += issue2203Scrape(t, u, `background_loop_iterations_total{loop="storage_history",result="skipped"}`)
	}
	var history struct {
		Points []issue2203Point `json:"points"`
	}
	issue2203Get(t, c, "/api/v1/admin/storage/history?store=database&days=1", &history)
	var since []issue2203Point
	for _, p := range history.Points {
		if !p.At.Before(started) {
			since = append(since, p)
		}
	}
	for i := 1; i < len(since); i++ {
		if gap := since[i].At.Sub(since[i-1].At); gap < issue2203Interval*9/10 {
			t.Errorf("samples at %s and %s, %s apart: one tick sampled twice", since[i-1].At, since[i].At, gap)
		}
	}
	if skipped < 1 {
		t.Errorf("no replica skipped a pass since %s; with two replicas each starting with a pass, one finds the other's sample", started)
	}
	t.Logf("%d database samples since the replicas started at %s; %v passes skipped", len(since), started, skipped)
}
