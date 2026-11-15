//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2647: each site's traffic and latency, recorded by the
// platform with no Prometheus, by environment and by replica, in the portal
// route behind the Traffic view, the Sites list and manage_site get. Nothing a
// visitor's request carried reaches a row or an answer. The dev stack runs no
// Prometheus backend for the portal, so every criterion here also holds
// criterion 7.
//
// Wire forms: GET /api/v1/portal/sites/{site}/traffic takes range and
// environment as query strings, range absent (24h) and present (1h);
// GET /api/v1/admin/sites/traffic/replicas takes range; manage_site get takes
// command and site as literal tools/call strings. A request to a site is a GET
// naming the hostname in Host with a distinctive path, query and User-Agent.

const (
	path2647  = "/acc2647-distinct-path-7c1e"
	query2647 = "acc2647q=distinct-query-9b2d"
	agent2647 = "acc2647-distinct-agent/4.2"
)

// fixtureReads2647 is the answered read the fixture makes of the domain once
// it serves, before the criterion's own requests.
const fixtureReads2647 = 1

// trafficWait bounds how long a criterion waits for a flush: a replica adds
// its counts once a minute.
const trafficWait = 4 * time.Minute

// trafficSite2647 is a site whose prod serves at a public domain, sent 50
// requests that answer and 10 that do not.
func trafficSite2647(t *testing.T) (*site2604, string) {
	t.Helper()
	s, domain := offlineSite2631(t)
	for range 50 {
		onHost(t, domain, http.MethodGet, "/", map[string]string{"User-Agent": agent2647}, "")
	}
	for range 10 {
		onHost(t, domain, http.MethodGet, path2647+"?"+query2647, map[string]string{"User-Agent": agent2647}, "")
	}
	return s, domain
}

// trafficOf polls the site's traffic route until ok accepts it.
func trafficOf(t *testing.T, s *site2604, key, query string, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(trafficWait)
	for {
		status, got := s.rest(t, key, http.MethodGet, "/traffic"+query, nil)
		if status == http.StatusOK && ok(got) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("the site's traffic did not arrive within %s: %d %v", trafficWait, status, got["_raw"])
		}
		time.Sleep(5 * time.Second)
	}
}

func classOf(m map[string]any, class string) float64 {
	classes, _ := m["classes"].(map[string]any)
	n, _ := classes[class].(float64)
	return n
}

// TestIssue2647_TheTrafficViewShowsProdsRequests: after 50 answered requests
// and 10 to a path that does not exist, the route behind the Traffic view
// shows 50 2xx and 10 4xx for prod within a flush, with p50, p95 and p99; the
// Sites list shows the site's 24-hour count and p95; manage_site get returns
// the 24-hour summary for prod.
func TestIssue2647_TheTrafficViewShowsProdsRequests(t *testing.T) {
	s, _ := trafficSite2647(t)
	got := trafficOf(t, s, s.key, "?range=1h", func(m map[string]any) bool {
		envs, _ := m["environments"].(map[string]any)
		prod, _ := envs["prod"].(map[string]any)
		return classOf(prod, "2xx") >= 50 && classOf(prod, "4xx") >= 10
	})
	envs, _ := got["environments"].(map[string]any)
	prod, _ := envs["prod"].(map[string]any)
	// The fixture reads the domain once it answers (served2518): one more 2xx.
	if classOf(prod, "2xx") != 50+fixtureReads2647 || classOf(prod, "4xx") != 10 {
		t.Errorf("prod counted %v 2xx and %v 4xx, want %d and 10", classOf(prod, "2xx"), classOf(prod, "4xx"), 50+fixtureReads2647)
	}
	for _, p := range []string{"p50_ms", "p95_ms", "p99_ms"} {
		if v, _ := prod[p].(float64); v <= 0 {
			t.Errorf("prod has no %s: %v", p, prod)
		}
	}
	if series, _ := got["series"].([]any); len(series) == 0 {
		t.Errorf("the 1h series is empty")
	}
	day := trafficOf(t, s, s.key, "", func(m map[string]any) bool { return true })
	if day["range"] != "24h" {
		t.Errorf("the default range is %v", day["range"])
	}

	deadline := time.Now().Add(trafficWait)
	for {
		status, list := rest2559(t, s.key, http.MethodGet, portal2559, nil)
		want2559(t, "reading the Sites list", status, http.StatusOK, list)
		if n, p95 := listedTraffic2647(list, s.name); n >= 60 && p95 > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the Sites list does not show the site's 24 hours: %v", list["_raw"])
		}
		time.Sleep(10 * time.Second)
	}

	read := connectAs(t, s.key).call("manage_site", map[string]any{"command": "get", "site": s.name})
	traffic, _ := read["traffic"].(map[string]any)
	tenvs, _ := traffic["environments"].(map[string]any)
	tprod, _ := tenvs["prod"].(map[string]any)
	if n, _ := tprod["requests"].(float64); n != 60+fixtureReads2647 {
		t.Errorf("manage_site get: prod's 24 hours are %v requests, want %d: %v", n, 60+fixtureReads2647, traffic)
	}
}

// listedTraffic2647 is one site's traffic in the Sites list.
func listedTraffic2647(list map[string]any, name string) (requests, p95 float64) {
	sites, _ := list["sites"].([]any)
	for _, raw := range sites {
		st, _ := raw.(map[string]any)
		if st["name"] != name {
			continue
		}
		tr, _ := st["traffic"].(map[string]any)
		requests, _ = tr["requests_24h"].(float64)
		p95, _ = tr["p95_ms"].(float64)
	}
	return requests, p95
}

// TestIssue2647_ReplicasAndNoVisitorData: the per-replica view names each
// replica that answered, with its own figures; someone who cannot view the
// site gets its refusal from the traffic route; the distinctive path, query
// and user agent appear in no traffic row and no traffic answer.
func TestIssue2647_ReplicasAndNoVisitorData(t *testing.T) {
	s, _ := trafficSite2647(t)
	trafficOf(t, s, s.key, "?range=1h", func(m map[string]any) bool {
		total, _ := m["total"].(map[string]any)
		n, _ := total["requests"].(float64)
		return n >= 60
	})
	status, reps := rest2559(t, keyAdmin2604, http.MethodGet, "/api/v1/admin/sites/traffic/replicas?range=1h", nil)
	want2559(t, "reading the replicas", status, http.StatusOK, reps)
	rows, _ := reps["replicas"].([]any)
	if len(rows) == 0 {
		t.Fatalf("no replica is listed: %v", reps["_raw"])
	}
	for _, raw := range rows {
		r, _ := raw.(map[string]any)
		if fmt.Sprint(r["instance"]) == "" || r["requests"] == nil {
			t.Errorf("a replica row names no instance or count: %v", r)
		}
	}
	// A public domain lets anyone view what it serves, so the refusal is read
	// on a site whose environments are all private.
	private := newSite2604(t, keyOwner2604, "acc2647-private-")
	if status, got := private.rest(t, keyPeer2604, http.MethodGet, "/traffic", nil); status != http.StatusForbidden {
		t.Errorf("someone who cannot view the site read its traffic: %d %v", status, got["_raw"])
	}

	_, view := s.rest(t, s.key, http.MethodGet, "/traffic?range=1h", nil)
	answers := fmt.Sprint(view["_raw"], reps["_raw"])
	db := devDB(t)
	var stored string
	if err := db.QueryRow(`SELECT COALESCE(string_agg(t::text, ' '), '') FROM site_traffic t`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var replicaRows string
	if err := db.QueryRow(`SELECT COALESCE(string_agg(t::text, ' '), '') FROM site_traffic_replicas t`).Scan(&replicaRows); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{path2647, query2647, agent2647, "distinct-path", "distinct-query", "distinct-agent"} {
		if strings.Contains(stored+replicaRows, secret) || strings.Contains(answers, secret) {
			t.Errorf("%q reached a traffic row or answer", secret)
		}
	}
}

// TestIssue2647_RecordingOffRecordsNothing: with sites.traffic turned off in
// Admin > Settings > Sites, a site's requests write no row and the view says
// recording is off.
func TestIssue2647_RecordingOffRecordsNothing(t *testing.T) {
	status, settings := rest2559(t, keyAdmin2604, http.MethodGet, "/api/v1/admin/settings/sites", nil)
	want2559(t, "reading the sites settings", status, http.StatusOK, settings)
	saved, _ := settings["settings"].(map[string]any)
	if saved == nil {
		saved = settings
	}
	off := map[string]any{}
	for k, v := range saved {
		if !strings.HasPrefix(k, "_") && k != "updated_by" && k != "updated_at" {
			off[k] = v
		}
	}
	off["traffic_enabled"] = false
	status, got := rest2559(t, keyAdmin2604, http.MethodPut, "/api/v1/admin/settings/sites", off)
	want2559(t, "turning traffic off", status, http.StatusOK, got)
	t.Cleanup(func() {
		off["traffic_enabled"] = true
		_, _ = rest2559(t, keyAdmin2604, http.MethodPut, "/api/v1/admin/settings/sites", off)
	})
	time.Sleep(10 * time.Second) // every replica reads a saved setting within its refresh interval

	s, domain := offlineSite2631(t)
	for range 20 {
		onHost(t, domain, http.MethodGet, "/", nil, "")
	}
	time.Sleep(sitesFlushWait2647)
	_, view := s.rest(t, s.key, http.MethodGet, "/traffic?range=1h", nil)
	if view["recording"] != false {
		t.Errorf("the view does not say recording is off: %v", view["_raw"])
	}
	var rows int
	if err := devDB(t).QueryRow(`SELECT count(*) FROM site_traffic t JOIN sites s ON s.id = t.subject WHERE s.name = $1`, s.name).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("%d traffic rows were written with recording off", rows)
	}
}

// sitesFlushWait2647 covers more than one flush of every replica.
const sitesFlushWait2647 = 130 * time.Second
