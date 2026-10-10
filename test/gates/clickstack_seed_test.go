package gates

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// deployments/observability/clickstack/seed/seed.py applies the dashboards,
// saved searches and alerts to HyperDX by name (#1901). These run it against
// a fake of the HyperDX API holding objects in memory, the way the API stores
// them: an id and timestamps added to what was sent, list responses that
// leave an alert's chart out.

const seedDir = "deployments/observability/clickstack/seed"

// fakeHyperDX is the part of HyperDX's API the seed calls.
type fakeHyperDX struct {
	mu      sync.Mutex
	objects map[string][]map[string]any // by kind
	writes  map[string]int              // "POST dashboards" -> count
	nextID  int
	key     string
	// limitEvery refuses every n-th API call with a 429, as HyperDX's
	// per-key limiter does past its 100 a minute; zero never.
	limitEvery int
	calls      int
}

// newFakeHyperDX starts the fake. limitEvery is set before the server serves
// a request: the seed reaches it from another process, which gives the race
// detector no ordering between a write here and the handler's read.
func newFakeHyperDX(t *testing.T, limitEvery int) (*fakeHyperDX, *httptest.Server) {
	t.Helper()
	f := &fakeHyperDX{objects: map[string][]map[string]any{}, writes: map[string]int{}, key: "fake-access-key", limitEvery: limitEvery}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeHyperDX) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.serveSession(w, r) {
		return
	}
	f.serveAPI(w, r)
}

// serveSession answers the health check and the sign-up, sign-in and account
// routes, which take no API key. It reports whether it answered.
func (f *fakeHyperDX) serveSession(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/health":
		_, _ = w.Write([]byte(`{"data":"ok"}`))
		return true
	case "/register/password":
		w.WriteHeader(http.StatusOK)
		return true
	case "/login/password":
		http.SetCookie(w, &http.Cookie{Name: "connect.sid", Value: "s1", Domain: "localhost", Path: "/"})
		w.Header().Set("Location", "http://localhost:8080/")
		w.WriteHeader(http.StatusSeeOther)
		return true
	case "/me":
		if c, err := r.Cookie("connect.sid"); err != nil || c.Value != "s1" {
			w.WriteHeader(http.StatusUnauthorized)
			return true
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"accessKey": f.key})
		return true
	}
	return false
}

// serveAPI answers /api/v2 as HyperDX stores objects.
func (f *fakeHyperDX) serveAPI(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+f.key {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.calls++
	if f.limitEvery > 0 && f.calls%f.limitEvery == 0 {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v2/"), "/")
	kind := parts[0]
	if kind == "sources" {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"id": "src-log", "kind": "log", "name": "Logs", "connection": "conn-1"},
			{"id": "src-trace", "kind": "trace", "name": "Traces", "connection": "conn-1"},
			{"id": "src-metric", "kind": "metric", "name": "Metrics", "connection": "conn-1"},
		}})
		return
	}
	switch {
	case r.Method == http.MethodGet && len(parts) == 1:
		out := make([]map[string]any, 0, len(f.objects[kind]))
		for _, o := range f.objects[kind] {
			listed := map[string]any{}
			for k, v := range o {
				if kind == "alerts" && k == "chartConfig" {
					continue // the list endpoint omits it
				}
				listed[k] = v
			}
			out = append(out, listed)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
	case r.Method == http.MethodGet:
		for _, o := range f.objects[kind] {
			if o["id"] == parts[1] {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": o})
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodPost || r.Method == http.MethodPut:
		f.write(w, r, kind, parts)
	case r.Method == http.MethodDelete && len(parts) == 2:
		f.delete(w, kind, parts[1])
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// write stores a created or replaced object, refusing what HyperDX refuses.
func (f *fakeHyperDX) write(w http.ResponseWriter, r *http.Request, kind string, parts []string) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// HyperDX's dashboard update names every filter by id; its create
	// takes none and gives each one.
	if kind == "dashboards" {
		for _, raw := range asList(body["filters"]) {
			filter, _ := raw.(map[string]any)
			_, has := filter["id"]
			if r.Method == http.MethodPut && !has {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"Body validation failed: filters.0.id: Required"}`))
				return
			}
			if r.Method == http.MethodPost {
				f.nextID++
				filter["id"] = fmt.Sprintf("filter-%d", f.nextID)
			}
		}
	}
	// An alert has no dashboard, so no variables: HyperDX fails its every
	// evaluation on a $__filter it cannot resolve (UnknownVariableError).
	if kind == "alerts" {
		cfg, _ := body["chartConfig"].(map[string]any)
		if sql, _ := cfg["sqlTemplate"].(string); strings.Contains(sql, "$__filter(") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"message":"alert %v references a dashboard variable"}`, body["name"])
			return
		}
	}
	f.writes[r.Method+" "+kind]++
	body["updatedAt"] = fmt.Sprintf("t%d", f.nextID)
	if r.Method == http.MethodPost {
		f.nextID++
		body["id"] = fmt.Sprintf("%s-%d", kind, f.nextID)
		f.objects[kind] = append(f.objects[kind], body)
	} else {
		for i, o := range f.objects[kind] {
			if o["id"] == parts[1] {
				body["id"] = parts[1]
				f.objects[kind][i] = body
			}
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": body})
}

// delete removes one object.
func (f *fakeHyperDX) delete(w http.ResponseWriter, kind, id string) {
	f.writes["DELETE "+kind]++
	kept := f.objects[kind][:0]
	for _, o := range f.objects[kind] {
		if o["id"] != id {
			kept = append(kept, o)
		}
	}
	f.objects[kind] = kept
	_, _ = w.Write([]byte(`{}`))
}

// wrote is how many writes of one kind ("POST dashboards") the fake took,
// read under its lock: the handler writes the count on the server's
// goroutine.
func (f *fakeHyperDX) wrote(what string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes[what]
}

// allWrites is every write count, for a failure message and a comparison.
func (f *fakeHyperDX) allWrites() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fmt.Sprint(f.writes)
}

// asList is v as a list, or nil.
func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// runSeed runs seed.py over dir against base with env.
func runSeed(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "python3", append([]string{"-I", filepath.Join(dir, "seed.py"), "--dir", dir, "--wait", "5"}, args...)...) //nolint:gosec // test runs the repository's own script
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("seed.py: %v\n%s", err, out)
	}
	return string(out)
}

// copySeed copies the seed directory into a temporary one a test may edit.
func copySeed(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "seed")
	require(t, os.CopyFS(dst, os.DirFS(filepath.Join("..", "..", seedDir))))
	return dst
}

// definitionCounts is how many dashboards, saved searches and alerts the
// directory defines.
func definitionCounts(t *testing.T, dir string) (dashboards, searches, alerts int) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "dashboards", "*.json"))
	require(t, err)
	var list []any
	b, err := os.ReadFile(filepath.Join(dir, "saved-searches.json")) //nolint:gosec // test fixture path
	require(t, err)
	require(t, json.Unmarshal(b, &list))
	searches = len(list)
	b, err = os.ReadFile(filepath.Join(dir, "alerts.json")) //nolint:gosec // test fixture path
	require(t, err)
	require(t, json.Unmarshal(b, &list))
	return len(files), searches, len(list)
}

// TestSeed_IdempotentByName: the first run creates every definition, a second
// over the same directory writes nothing, and a changed definition is
// replaced in place, not created again.
func TestSeed_IdempotentByName(t *testing.T) {
	fake, srv := newFakeHyperDX(t, 0)
	dir := copySeed(t)
	env := []string{"HYPERDX_API_URL=" + srv.URL, "HYPERDX_API_KEY=" + fake.key, "HYPERDX_ALERT_WEBHOOK_URL=http://hooks.example.com/alert"}
	dashboards, searches, alerts := definitionCounts(t, dir)

	out := runSeed(t, dir, env)
	if fake.wrote("POST dashboards") != dashboards || fake.wrote("POST saved-searches") != searches ||
		fake.wrote("POST alerts") != alerts || fake.wrote("POST webhooks") != 1 {
		t.Fatalf("first run wrote %v, want %d dashboards, %d searches, %d alerts, 1 webhook\n%s", fake.allWrites(), dashboards, searches, alerts, out)
	}

	before := fake.allWrites()
	out = runSeed(t, dir, env)
	if !strings.Contains(out, "seed: 0 writes") || fake.allWrites() != before {
		t.Fatalf("second run wrote: %v\n%s", fake.allWrites(), out)
	}

	path := filepath.Join(dir, "dashboards", "02-tool-calls.json")
	b, err := os.ReadFile(path) //nolint:gosec // the test's own copy
	require(t, err)
	edited := []byte(strings.Replace(string(b), `"Calls by tool"`, `"Calls per tool"`, 1))
	require(t, os.WriteFile(path, edited, 0o600)) //nolint:gosec // the test's own copy
	out = runSeed(t, dir, env)
	if fake.wrote("PUT dashboards") != 1 || fake.wrote("POST dashboards") != dashboards {
		t.Fatalf("an edited dashboard is replaced once: %v\n%s", fake.allWrites(), out)
	}
	if !strings.Contains(out, "updated dashboards: MCP Platform: Tool calls") {
		t.Fatalf("the report names the update:\n%s", out)
	}
}

// TestSeed_RemovesWhatItNoLongerDefines: an alert the definitions renamed is
// removed under its old name, and an object an operator made without the
// seed's tag is left alone.
func TestSeed_RemovesWhatItNoLongerDefines(t *testing.T) {
	fake, srv := newFakeHyperDX(t, 0)
	dir := copySeed(t)
	env := []string{"HYPERDX_API_URL=" + srv.URL, "HYPERDX_API_KEY=" + fake.key, "HYPERDX_ALERT_WEBHOOK_URL=http://hooks.example.com/alert"}
	runSeed(t, dir, env)
	fake.mu.Lock()
	fake.objects["alerts"] = append(fake.objects["alerts"],
		map[string]any{"id": "old", "name": "An alert this bundle once shipped", "tags": []any{"mcp-data-platform"}},
		map[string]any{"id": "mine", "name": "An operator's own alert", "tags": []any{"team"}})
	fake.mu.Unlock()
	out := runSeed(t, dir, env)
	if fake.wrote("DELETE alerts") != 1 || !strings.Contains(out, "removed alerts: An alert this bundle once shipped") {
		t.Fatalf("deletes %v\n%s", fake.allWrites(), out)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, o := range fake.objects["alerts"] {
		if o["id"] == "old" {
			t.Fatal("the retired alert is still there")
		}
	}
}

// TestSeed_WaitsOutTheRateLimit: a 429 is waited out and the call retried,
// so a run larger than HyperDX's per-minute limit still completes.
func TestSeed_WaitsOutTheRateLimit(t *testing.T) {
	fake, srv := newFakeHyperDX(t, 7)
	dir := copySeed(t)
	env := []string{"HYPERDX_API_URL=" + srv.URL, "HYPERDX_API_KEY=" + fake.key, "HYPERDX_ALERT_WEBHOOK_URL=http://hooks.example.com/alert"}
	dashboards, _, alerts := definitionCounts(t, dir)
	runSeed(t, dir, env)
	if fake.wrote("POST dashboards") != dashboards || fake.wrote("POST alerts") != alerts {
		t.Fatalf("a rate-limited run wrote %v", fake.allWrites())
	}
	if out := runSeed(t, dir, env); !strings.Contains(out, "seed: 0 writes") {
		t.Fatalf("a refused write was counted or repeated:\n%s", out)
	}
}

// TestSeed_AlertsNeedAChannel: with no webhook URL the dashboards and saved
// searches are applied and the alerts skipped, saying so.
func TestSeed_AlertsNeedAChannel(t *testing.T) {
	fake, srv := newFakeHyperDX(t, 0)
	dir := copySeed(t)
	out := runSeed(t, dir, []string{"HYPERDX_API_URL=" + srv.URL, "HYPERDX_API_KEY=" + fake.key, "HYPERDX_ALERT_WEBHOOK_URL="})
	if fake.wrote("POST alerts") != 0 || fake.wrote("POST webhooks") != 0 || fake.wrote("POST dashboards") == 0 {
		t.Fatalf("writes %v", fake.allWrites())
	}
	if !strings.Contains(out, "HYPERDX_ALERT_WEBHOOK_URL is not set") {
		t.Fatalf("the skip is reported:\n%s", out)
	}
}

// TestSeed_Bootstrap: on a fresh evaluation install the seed creates the first
// user, signs in, and reads its key, carrying the session cookie by hand
// (HyperDX scopes it to Domain=localhost).
func TestSeed_Bootstrap(t *testing.T) {
	fake, srv := newFakeHyperDX(t, 0)
	dir := copySeed(t)
	runSeed(t, dir, []string{
		"HYPERDX_API_URL=" + srv.URL, "HYPERDX_API_KEY=", "HYPERDX_ALERT_WEBHOOK_URL=",
		"HYPERDX_SEED_EMAIL=admin@example.com", "HYPERDX_SEED_PASSWORD=pw",
	}, "--bootstrap")
	if fake.wrote("POST dashboards") == 0 {
		t.Fatalf("nothing applied after bootstrap: %v", fake.allWrites())
	}
}

// TestSeed_RenderEveryQuery: every tile and alert renders to SQL with no
// placeholder left, reads the dashboard's time range, and carries the fleet
// filter unless it reads a fixed window of its own.
func TestSeed_RenderEveryQuery(t *testing.T) {
	dir := filepath.Join("..", "..", seedDir)
	out := runSeed(t, dir, nil, "--render")
	_, _, alerts := definitionCounts(t, dir)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	tiles, gotAlerts := 0, 0
	for _, line := range lines {
		var r struct{ What, Name, SQL string }
		require(t, json.Unmarshal([]byte(line), &r))
		if r.What == "alert" {
			gotAlerts++
		} else {
			tiles++
		}
		for _, leftover := range []string{"{{", "{a}", "{b}", "$__", "$deployment"} {
			if strings.Contains(r.SQL, leftover) {
				t.Errorf("%s %q: %q left in %s", r.What, r.Name, leftover, r.SQL)
			}
		}
		if !strings.Contains(r.SQL, "now()") {
			t.Errorf("%s %q reads no time range: %s", r.What, r.Name, r.SQL)
		}
	}
	if gotAlerts != alerts || tiles < 50 {
		t.Fatalf("rendered %d tiles and %d alerts (want %d alerts)", tiles, gotAlerts, alerts)
	}
}

// TestSeed_DashboardGrid: every tile fits HyperDX's 24-column grid and no two
// tiles of a dashboard overlap.
func TestSeed_DashboardGrid(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", seedDir, "dashboards", "*.json"))
	require(t, err)
	if len(files) != 8 {
		t.Fatalf("%d dashboards, want 8", len(files))
	}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Clean(f)) //nolint:gosec // the repository's own definitions
		require(t, err)
		var d struct {
			Name  string
			Tiles []struct {
				Name       string
				X, Y, W, H int
			}
		}
		require(t, json.Unmarshal(b, &d))
		used := map[[2]int]string{}
		for _, tl := range d.Tiles {
			if tl.X < 0 || tl.W < 1 || tl.X+tl.W > 24 || tl.H < 1 {
				t.Errorf("%s / %s is off the grid: x=%d w=%d h=%d", d.Name, tl.Name, tl.X, tl.W, tl.H)
			}
			for x := tl.X; x < tl.X+tl.W; x++ {
				for y := tl.Y; y < tl.Y+tl.H; y++ {
					if other, ok := used[[2]int{x, y}]; ok {
						t.Errorf("%s: %s overlaps %s", d.Name, tl.Name, other)
					}
					used[[2]int{x, y}] = tl.Name
				}
			}
		}
	}
}
