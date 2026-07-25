package apisvc

// The insights surface's contract (#1054). The assertions this file exists
// for are the ones a slip in would quietly poison the study: an empty
// collection is never served as a refusal and a refusal is never served as
// an empty collection; a refusal reveals nothing about provisioning; the
// downstream operation is genuinely blocked when nothing is provisioned;
// and the world control plane changes exactly the world.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
)

// newPerishableServer builds the perishable surface in a named world with
// auth disabled.
func newPerishableServer(t *testing.T, profile string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(New(Options{Surface: SurfacePerishable, WorldProfile: profile}))
	t.Cleanup(ts.Close)
	return ts
}

// rawGet GETs a path and returns the status and the exact body.
func rawGet(t *testing.T, ts *httptest.Server, path string) (int, string) {
	t.Helper()
	res, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("GET %s: read: %v", path, err)
	}
	return res.StatusCode, string(body)
}

// window is the full reporting window every series covers.
func window() url.Values {
	return url.Values{"start_date": {apigen.SeriesStart()}, "end_date": {apigen.SeriesEnd()}}
}

// monitorsPath renders the monitor listing with optional query values.
func withQuery(path string, q url.Values) string {
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

// TestEmptyIsNotForbidden is the study's load-bearing distinction: an
// account with nothing provisioned answers 200 with an empty array, an
// unentitled credential answers 403, and the 403 is identical whether or
// not monitors exist behind it.
func TestEmptyIsNotForbidden(t *testing.T) {
	empty := newPerishableServer(t, "monitors-0")
	code, body := rawGet(t, empty, "/insights/monitors")
	if code != http.StatusOK {
		t.Fatalf("unprovisioned monitors: status %d, want 200", code)
	}
	if !strings.Contains(body, `"items":[]`) {
		t.Errorf("unprovisioned monitors body = %s, want an empty items array", body)
	}
	if strings.Contains(body, "null") {
		t.Errorf("unprovisioned monitors body = %s, want no null", body)
	}

	forbiddenEmpty := newPerishableServer(t, "monitors-0-forbidden")
	forbiddenFull := newPerishableServer(t, "monitors-3-forbidden")
	codeEmpty, bodyEmpty := rawGet(t, forbiddenEmpty, "/insights/monitors")
	codeFull, bodyFull := rawGet(t, forbiddenFull, "/insights/monitors")
	if codeEmpty != http.StatusForbidden || codeFull != http.StatusForbidden {
		t.Fatalf("forbidden worlds: status %d and %d, want 403 and 403", codeEmpty, codeFull)
	}
	if bodyEmpty != bodyFull {
		t.Errorf("403 body leaks provisioning state:\n 0 monitors: %s\n 3 monitors: %s", bodyEmpty, bodyFull)
	}
	if strings.Contains(bodyEmpty, "items") {
		t.Errorf("403 body carries a collection: %s", bodyEmpty)
	}
	// The whole listening area refuses, not just the listing.
	for _, path := range []string{"/insights/monitors/501", withQuery("/insights/monitors/501/trend", window())} {
		if code, _ := rawGet(t, forbiddenFull, path); code != http.StatusForbidden {
			t.Errorf("GET %s in a forbidden world: status %d, want 403", path, code)
		}
	}
}

// TestProvisioningIsVisible checks that a world's monitor count is exactly
// what the listing serves, and that raising it adds monitors without
// disturbing the existing ones.
func TestProvisioningIsVisible(t *testing.T) {
	for _, tc := range []struct {
		profile string
		want    int
	}{{"monitors-0", 0}, {"monitors-1", 1}, {"monitors-3", 3}, {"monitors-6", 6}} {
		t.Run(tc.profile, func(t *testing.T) {
			ts := newPerishableServer(t, tc.profile)
			items := fetchAll(t, ts, "/insights/monitors", nil)
			if len(items) != tc.want {
				t.Fatalf("monitors = %d, want %d", len(items), tc.want)
			}
			for i, row := range items {
				if got, want := row["id"], float64(apigen.BuildFixture().Monitors[i].ID); got != want {
					t.Errorf("monitor %d id = %v, want %v", i, got, want)
				}
			}
		})
	}
}

// TestDownstreamBlockedWithoutMonitor covers the structural feature that
// makes a stale belief costly in both directions: the trend series exists
// only behind a provisioned monitor, so an unprovisioned account has no
// valid call, and a provisioned one answers with sentiment.
func TestDownstreamBlockedWithoutMonitor(t *testing.T) {
	id := apigen.BuildFixture().Monitors[0].ID
	trend := withQuery("/insights/monitors/"+strconv.Itoa(id)+"/trend", window())

	unprovisioned := newPerishableServer(t, "monitors-0")
	if code, _ := rawGet(t, unprovisioned, trend); code != http.StatusNotFound {
		t.Errorf("trend on an unprovisioned monitor: status %d, want 404", code)
	}
	if code, _ := rawGet(t, unprovisioned, "/insights/monitors/"+strconv.Itoa(id)); code != http.StatusNotFound {
		t.Errorf("get on an unprovisioned monitor: status %d, want 404", code)
	}

	provisioned := newPerishableServer(t, "monitors-3")
	var monitor apigen.Monitor
	if code := getJSON(t, provisioned, "/insights/monitors/"+strconv.Itoa(id), &monitor); code != http.StatusOK {
		t.Fatalf("get on a provisioned monitor: status %d, want 200", code)
	}
	if monitor.ID != id || monitor.Name == "" || monitor.Keywords == "" {
		t.Errorf("provisioned monitor = %+v", monitor)
	}
	points := fetchAll(t, provisioned, "/insights/monitors/"+strconv.Itoa(id)+"/trend", window())
	if len(points) != apigen.SeriesDays {
		t.Fatalf("trend points = %d, want %d", len(points), apigen.SeriesDays)
	}
	if _, ok := points[0]["sentiment_score"]; !ok {
		t.Errorf("trend point carries no sentiment_score: %v", points[0])
	}
}

// TestCorroborationSurface checks that owned profiles are populated on the
// same credential in every world, including one where listening refuses:
// an empty monitor listing can never be explained by a dead credential.
func TestCorroborationSurface(t *testing.T) {
	for _, profile := range []string{"monitors-0", "monitors-3", "monitors-0-forbidden"} {
		t.Run(profile, func(t *testing.T) {
			ts := newPerishableServer(t, profile)
			if rows := fetchAll(t, ts, "/insights/profiles", nil); len(rows) == 0 {
				t.Error("owned profiles are empty; the corroboration surface is gone")
			}
			if rows := fetchAll(t, ts, "/insights/workspaces", nil); len(rows) == 0 {
				t.Error("workspaces are empty")
			}
			if rows := fetchAll(t, ts, "/crm/customers", nil); len(rows) == 0 {
				t.Error("the gold surface is empty")
			}
		})
	}
}

// TestSentimentOnlyBehindListening checks the substitution temptation is
// real: owned-profile metrics carry no sentiment, so answering a sentiment
// question from them is a substitution, not a partial answer.
func TestSentimentOnlyBehindListening(t *testing.T) {
	ts := newPerishableServer(t, "monitors-3")
	id := strconv.Itoa(apigen.BuildFixture().Profiles[0].ID)
	points := fetchAll(t, ts, "/insights/profiles/"+id+"/metrics", window())
	if len(points) != apigen.SeriesDays {
		t.Fatalf("metric points = %d, want %d", len(points), apigen.SeriesDays)
	}
	for key := range points[0] {
		if strings.Contains(key, "sentiment") {
			t.Errorf("owned-profile metrics carry %q; sentiment must exist only behind a monitor", key)
		}
	}
}

// TestEternalInvariantOverHTTP checks the summation identity the study
// uses as its never-stale control: a period's unique reach is at least the
// busiest day and strictly below the sum of days.
func TestEternalInvariantOverHTTP(t *testing.T) {
	ts := newPerishableServer(t, "monitors-0")
	for _, p := range apigen.BuildFixture().Profiles {
		id := strconv.Itoa(p.ID)
		var sum, top float64
		for _, point := range fetchAll(t, ts, "/insights/profiles/"+id+"/metrics", window()) {
			v, ok := point["unique_reach"].(float64)
			if !ok {
				t.Fatalf("profile %s: metric point has no unique_reach: %v", id, point)
			}
			sum += v
			top = max(top, v)
		}
		var out struct {
			Groups []struct {
				Key   string  `json:"key"`
				Value float64 `json:"value"`
			} `json:"groups"`
		}
		if code := getJSON(t, ts, withQuery("/insights/profiles/"+id+"/metrics:aggregate", window()), &out); code != http.StatusOK {
			t.Fatalf("profile %s aggregate: status %d", id, code)
		}
		period := 0.0
		for _, g := range out.Groups {
			if g.Key == "unique_reach" {
				period = g.Value
			}
		}
		if period < top || period >= sum {
			t.Errorf("profile %s: period unique %v outside (busiest day %v, sum %v)", id, period, top, sum)
		}
		// The aggregate answers for the window it is asked about, not for
		// the whole series, so a narrower question gets a smaller answer.
		narrow := url.Values{"start_date": {apigen.SeriesStart()}, "end_date": {apigen.SeriesStart()}}
		var oneDay struct {
			Groups []struct {
				Key   string  `json:"key"`
				Value float64 `json:"value"`
			} `json:"groups"`
		}
		if code := getJSON(t, ts, withQuery("/insights/profiles/"+id+"/metrics:aggregate", narrow), &oneDay); code != http.StatusOK {
			t.Fatalf("profile %s one-day aggregate: status %d", id, code)
		}
		for _, g := range oneDay.Groups {
			if g.Key == "unique_reach" && g.Value >= period {
				t.Errorf("profile %s: a one-day window reports %v uniques, not below the period's %v", id, g.Value, period)
			}
		}
	}
}

// TestDurableContractGranularity checks the durable-class behavior: the
// granularity parameter is accepted and silently ignored until the release
// that honors it.
func TestDurableContractGranularity(t *testing.T) {
	id := strconv.Itoa(apigen.BuildFixture().Profiles[0].ID)
	q := window()
	q.Set("granularity", "week")
	for _, tc := range []struct {
		profile string
		want    int
	}{
		{"monitors-0", apigen.SeriesDays},
		{"monitors-0-released", apigen.SeriesDays / apigen.WeekBucketDays},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			ts := newPerishableServer(t, tc.profile)
			if got := len(fetchAll(t, ts, "/insights/profiles/"+id+"/metrics", q)); got != tc.want {
				t.Errorf("weekly request returned %d buckets, want %d", got, tc.want)
			}
		})
	}
}

// TestRecheckCostDial checks the multi-call recheck: a workspace-scoped
// account refuses an unscoped listing, so recovering the same state costs
// one listing per workspace plus the workspace lookup.
func TestRecheckCostDial(t *testing.T) {
	ts := newPerishableServer(t, "monitors-3-scoped")
	if code, _ := rawGet(t, ts, "/insights/monitors"); code != http.StatusBadRequest {
		t.Errorf("unscoped listing on a scoped account: status %d, want 400", code)
	}
	workspaces := fetchAll(t, ts, "/insights/workspaces", nil)
	if len(workspaces) < 2 {
		t.Fatalf("workspaces = %d, want at least 2 so the scoped recheck costs more than one call", len(workspaces))
	}
	total := 0
	for _, ws := range workspaces {
		id, ok := ws["id"].(float64)
		if !ok {
			t.Fatalf("workspace row has no id: %v", ws)
		}
		total += len(fetchAll(t, ts, "/insights/monitors", url.Values{"workspace_id": {strconv.Itoa(int(id))}}))
	}
	if total != 3 {
		t.Errorf("scoped listings recovered %d monitors, want 3", total)
	}
	// The same world unscoped answers in a single call.
	unscoped := newPerishableServer(t, "monitors-3")
	if got := len(fetchAll(t, unscoped, "/insights/monitors", nil)); got != 3 {
		t.Errorf("unscoped listing returned %d monitors, want 3", got)
	}
}

// TestWorldControlPlane covers the between-sessions world change: it moves
// the world and nothing else, the access log spans it, reset restores the
// starting world, and an unknown profile is refused.
func TestWorldControlPlane(t *testing.T) {
	ts := newPerishableServer(t, "monitors-0")
	var start apigen.World
	if code := getJSON(t, ts, "/_bench/world", &start); code != http.StatusOK || start.Profile != "monitors-0" {
		t.Fatalf("world: status %d profile %q", code, start.Profile)
	}
	if got := len(fetchAll(t, ts, "/insights/monitors", nil)); got != 0 {
		t.Fatalf("monitors before the change = %d, want 0", got)
	}

	var changed apigen.World
	if code := doJSON(t, ts, http.MethodPost, "/_bench/world", map[string]any{"profile": "monitors-3"}, &changed); code != http.StatusOK {
		t.Fatalf("set world: status %d", code)
	}
	if changed.Profile != "monitors-3" || changed.Monitors != 3 {
		t.Fatalf("set world returned %+v", changed)
	}
	if got := len(fetchAll(t, ts, "/insights/monitors", nil)); got != 3 {
		t.Errorf("monitors after the change = %d, want 3", got)
	}
	// The change is not a reset: the log still carries the calls made
	// before it, which is what makes a later recheck detectable.
	var log struct {
		Requests []RequestLogEntry `json:"requests"`
	}
	if code := getJSON(t, ts, "/_bench/requests", &log); code != http.StatusOK {
		t.Fatalf("requests: status %d", code)
	}
	before := 0
	for _, e := range log.Requests {
		if e.OperationID == "list_monitors" {
			before++
		}
	}
	if before < 2 {
		t.Errorf("access log holds %d list_monitors entries, want the calls from both sides of the change", before)
	}

	if code := doJSON(t, ts, http.MethodPost, "/_bench/world", map[string]any{"profile": "nope"}, nil); code != http.StatusBadRequest {
		t.Errorf("unknown profile: status %d, want 400", code)
	}
	if code := doJSON(t, ts, http.MethodPost, "/_bench/reset", nil, nil); code != http.StatusOK {
		t.Fatalf("reset: status %d", code)
	}
	if got := len(fetchAll(t, ts, "/insights/monitors", nil)); got != 0 {
		t.Errorf("monitors after reset = %d, want the starting world's 0", got)
	}
}

// TestResetIntoWorld checks that an attempt's starting world can be set in
// the same call that clears state, and that a bad profile there is refused
// rather than silently defaulted.
func TestResetIntoWorld(t *testing.T) {
	ts := newPerishableServer(t, "monitors-0")
	if code := doJSON(t, ts, http.MethodPost, "/_bench/reset", map[string]any{"profile": "monitors-6"}, nil); code != http.StatusOK {
		t.Fatalf("reset into world: status %d", code)
	}
	if got := len(fetchAll(t, ts, "/insights/monitors", nil)); got != 6 {
		t.Errorf("monitors after reset into monitors-6 = %d, want 6", got)
	}
	// A later bare reset keeps the world the attempt was reset into.
	if code := doJSON(t, ts, http.MethodPost, "/_bench/reset", nil, nil); code != http.StatusOK {
		t.Fatalf("bare reset: status %d", code)
	}
	if got := len(fetchAll(t, ts, "/insights/monitors", nil)); got != 6 {
		t.Errorf("monitors after a bare reset = %d, want the last reset world's 6", got)
	}
	if code := doJSON(t, ts, http.MethodPost, "/_bench/reset", map[string]any{"profile": "nope"}, nil); code != http.StatusBadRequest {
		t.Errorf("reset into an unknown profile: status %d, want 400", code)
	}
	// A reset with no body at all is the #1027 harness's call and stays
	// valid: an absent body is not a malformed one.
	res, err := http.Post(ts.URL+"/_bench/reset", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Errorf("bodyless reset: status %d, want 200", res.StatusCode)
	}
	if got := len(fetchAll(t, ts, "/insights/monitors", nil)); got != 6 {
		t.Errorf("monitors after a bodyless reset = %d, want 6", got)
	}
}

// TestPhaseLabelsAccessLog checks that a declared session phase stamps
// subsequent entries, which is what separates a capture session's calls
// from a query session's in one unreset log.
func TestPhaseLabelsAccessLog(t *testing.T) {
	ts := newPerishableServer(t, "monitors-0")
	if code := doJSON(t, ts, http.MethodPost, "/_bench/phase", map[string]any{"phase": "capture"}, nil); code != http.StatusOK {
		t.Fatalf("set phase: status %d", code)
	}
	fetchAll(t, ts, "/insights/monitors", nil)
	if code := doJSON(t, ts, http.MethodPost, "/_bench/world", map[string]any{"profile": "monitors-3"}, nil); code != http.StatusOK {
		t.Fatalf("set world: status %d", code)
	}
	if code := doJSON(t, ts, http.MethodPost, "/_bench/phase", map[string]any{"phase": "query"}, nil); code != http.StatusOK {
		t.Fatalf("set phase: status %d", code)
	}
	fetchAll(t, ts, "/insights/monitors", nil)

	var log struct {
		Requests []RequestLogEntry `json:"requests"`
	}
	if code := getJSON(t, ts, "/_bench/requests", &log); code != http.StatusOK {
		t.Fatalf("requests: status %d", code)
	}
	phases := map[string]int{}
	for _, e := range log.Requests {
		if e.OperationID == "list_monitors" {
			phases[e.Phase]++
		}
	}
	if phases["capture"] != 1 || phases["query"] != 1 {
		t.Errorf("list_monitors by phase = %v, want one capture and one query", phases)
	}
	if code := doJSON(t, ts, http.MethodPost, "/_bench/phase", map[string]any{}, nil); code != http.StatusBadRequest {
		t.Errorf("empty phase: status %d, want 400", code)
	}
	// Reset clears the label with the rest of the attempt's state.
	if code := doJSON(t, ts, http.MethodPost, "/_bench/reset", nil, nil); code != http.StatusOK {
		t.Fatalf("reset: status %d", code)
	}
	fetchAll(t, ts, "/insights/monitors", nil)
	log.Requests = nil
	if code := getJSON(t, ts, "/_bench/requests", &log); code != http.StatusOK {
		t.Fatalf("requests: status %d", code)
	}
	for _, e := range log.Requests {
		if e.Phase != "" {
			t.Errorf("entry %+v kept a phase across reset", e)
		}
	}
}

// TestPerishableErrorContract covers every validation branch the insights
// surface can refuse on.
func TestPerishableErrorContract(t *testing.T) {
	ts := newPerishableServer(t, "monitors-3")
	profileID := strconv.Itoa(apigen.BuildFixture().Profiles[0].ID)
	monitorID := strconv.Itoa(apigen.BuildFixture().Monitors[0].ID)
	cases := []struct {
		name string
		path string
		want int
	}{
		{"trend without a window", "/insights/monitors/" + monitorID + "/trend", http.StatusBadRequest},
		{"trend with only a start", "/insights/monitors/" + monitorID + "/trend?start_date=2026-06-01", http.StatusBadRequest},
		{"trend with a reversed window", "/insights/monitors/" + monitorID + "/trend?start_date=2026-06-10&end_date=2026-06-01", http.StatusBadRequest},
		{"trend with an unparseable start", "/insights/monitors/" + monitorID + "/trend?start_date=soon&end_date=2026-06-01", http.StatusBadRequest},
		{"trend with an unparseable end", "/insights/monitors/" + monitorID + "/trend?start_date=2026-06-01&end_date=soon", http.StatusBadRequest},
		{"trend with only an end", "/insights/monitors/" + monitorID + "/trend?end_date=2026-06-28", http.StatusBadRequest},
		{"trend on a bad id", "/insights/monitors/abc/trend?start_date=2026-06-01&end_date=2026-06-28", http.StatusBadRequest},
		{"monitor on a bad id", "/insights/monitors/abc", http.StatusBadRequest},
		{"monitor that does not exist", "/insights/monitors/999999", http.StatusNotFound},
		{"monitors with a bad workspace filter", "/insights/monitors?workspace_id=main", http.StatusBadRequest},
		{"metrics without a window", "/insights/profiles/" + profileID + "/metrics", http.StatusBadRequest},
		{"metrics with a bad granularity", "/insights/profiles/" + profileID + "/metrics?start_date=2026-06-01&end_date=2026-06-28&granularity=hour", http.StatusBadRequest},
		{"metrics on a bad id", "/insights/profiles/abc/metrics?start_date=2026-06-01&end_date=2026-06-28", http.StatusBadRequest},
		{"metrics on a profile that does not exist", "/insights/profiles/999999/metrics?start_date=2026-06-01&end_date=2026-06-28", http.StatusNotFound},
		{"aggregate without a window", "/insights/profiles/" + profileID + "/metrics:aggregate", http.StatusBadRequest},
		{"aggregate on a profile that does not exist", "/insights/profiles/999999/metrics:aggregate?start_date=2026-06-01&end_date=2026-06-28", http.StatusNotFound},
		{"a window outside the series", "/insights/monitors/" + monitorID + "/trend?start_date=2020-01-01&end_date=2020-01-02", http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code, _ := rawGet(t, ts, c.path); code != c.want {
				t.Errorf("status %d, want %d", code, c.want)
			}
		})
	}
	// A window outside the series is an empty array, not an error and not
	// a null.
	_, body := rawGet(t, ts, "/insights/monitors/"+monitorID+"/trend?start_date=2020-01-01&end_date=2020-01-02")
	if !strings.Contains(body, `"items":[]`) {
		t.Errorf("out-of-range window body = %s, want an empty items array", body)
	}
	// Every control route rejects a malformed body rather than treating
	// it as absent: a garbled world request must not silently run the
	// attempt in whatever world was already loaded.
	for _, path := range []string{"/_bench/world", "/_bench/phase", "/_bench/reset"} {
		t.Run("malformed body on "+path, func(t *testing.T) {
			res, err := http.Post(ts.URL+path, "application/json", strings.NewReader(`{"profile":`))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			if res.StatusCode != http.StatusBadRequest {
				t.Errorf("status %d, want 400", res.StatusCode)
			}
		})
	}
}

// TestPerishableSurfaceIsSeparate checks that the two studies' catalogs do
// not bleed into each other: the #1027 service serves no insights routes,
// and the study service serves no tier-1 distractors.
func TestPerishableSurfaceIsSeparate(t *testing.T) {
	apiStudy, _ := newTestServer(t)
	if code, _ := rawGet(t, apiStudy, "/insights/monitors"); code != http.StatusNotFound {
		t.Errorf("#1027 surface serves /insights/monitors: status %d, want 404", code)
	}
	perishable := newPerishableServer(t, "monitors-0")
	if code, _ := rawGet(t, perishable, "/insights/monitors"); code != http.StatusOK {
		t.Errorf("study surface refuses /insights/monitors: status %d", code)
	}
	if code, _ := rawGet(t, perishable, "/crm/leads"); code != http.StatusOK {
		t.Errorf("study surface drops the tier-0 near-miss pack: status %d", code)
	}
	// crm/segments is a tier-1 distractor: present in the #1027 catalog,
	// absent from the study's tier-0 pack.
	if code, _ := rawGet(t, perishable, "/crm/segments"); code != http.StatusNotFound {
		t.Errorf("study surface serves a tier-1 distractor: status %d, want 404", code)
	}
	if code, _ := rawGet(t, apiStudy, "/crm/segments"); code != http.StatusOK {
		t.Errorf("#1027 surface lost a tier-1 distractor: status %d", code)
	}
}

// TestUnknownWorldProfilePanics checks that a mistyped profile fails at
// construction instead of running a cell nobody asked for.
func TestUnknownWorldProfilePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New accepted an unknown world profile")
		}
	}()
	New(Options{Surface: SurfacePerishable, WorldProfile: "not-a-profile"})
}

// TestControlPlaneNeedsCredential checks the world plane is behind the
// same credential as the catalog.
func TestControlPlaneNeedsCredential(t *testing.T) {
	ts := httptest.NewServer(New(Options{APIKey: "k", Surface: SurfacePerishable}))
	t.Cleanup(ts.Close)
	if code, _ := rawGet(t, ts, "/_bench/world"); code != http.StatusUnauthorized {
		t.Errorf("world without a credential: status %d, want 401", code)
	}
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/_bench/world", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-API-Key", "k")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Errorf("world with a credential: status %d, want 200", res.StatusCode)
	}
	var world apigen.World
	if err := json.NewDecoder(res.Body).Decode(&world); err != nil {
		t.Fatal(err)
	}
	if world.Profile != apigen.DefaultWorldProfile {
		t.Errorf("default world = %q, want %q", world.Profile, apigen.DefaultWorldProfile)
	}
}

// TestWorkspaceCountIsTheCostDial checks a world exposes only its own
// workspaces and refuses ids outside them. This is what makes the price of
// establishing "nothing is provisioned" a controlled variable: on a scoped
// account it costs one listing per workspace plus the lookup.
func TestWorkspaceCountIsTheCostDial(t *testing.T) {
	for _, tc := range []struct {
		profile string
		want    int
		cost    int
	}{
		{"monitors-0", 2, 1},
		{"monitors-0-scoped", 2, 3},
		{"monitors-0-scoped-5", 5, 6},
		{"monitors-0-scoped-10", 10, 11},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			ts := newPerishableServer(t, tc.profile)
			ws := fetchAll(t, ts, "/insights/workspaces", nil)
			if len(ws) != tc.want {
				t.Fatalf("workspaces = %d, want %d", len(ws), tc.want)
			}
			world, ok := apigen.WorldByName(tc.profile)
			if !ok {
				t.Fatal("unknown world")
			}
			if got := world.RecheckCalls(); got != tc.cost {
				t.Errorf("recheck costs %d calls, want %d", got, tc.cost)
			}
			// Clearing the account really does take that many calls: the
			// unscoped shortcut is refused where scoping is on, and every
			// exposed workspace answers.
			code, _ := rawGet(t, ts, "/insights/monitors")
			if world.WorkspaceScoped != (code == http.StatusBadRequest) {
				t.Errorf("unscoped listing returned %d on a scoped=%v account", code, world.WorkspaceScoped)
			}
			for _, w := range ws {
				id, _ := w["id"].(float64)
				if got := len(fetchAll(t, ts, "/insights/monitors", url.Values{"workspace_id": {strconv.Itoa(int(id))}})); got != 0 {
					t.Errorf("workspace %v holds %d monitors, want 0", id, got)
				}
			}
			// A workspace the account does not have is refused rather than
			// silently answering empty, which would let an agent "clear"
			// the account without visiting it.
			if code, _ := rawGet(t, ts, "/insights/monitors?workspace_id=699"); code != http.StatusNotFound {
				t.Errorf("an unknown workspace returned %d, want 404", code)
			}
		})
	}
	// Monitors never live outside the base workspaces, so a wider account
	// adds empty places to look rather than moving the monitors.
	ts := newPerishableServer(t, "monitors-3")
	seen := map[float64]bool{}
	for _, m := range fetchAll(t, ts, "/insights/monitors", nil) {
		seen[m["workspace_id"].(float64)] = true
	}
	if len(seen) != 2 {
		t.Errorf("monitors span %d workspaces, want the 2 every account has", len(seen))
	}
}
