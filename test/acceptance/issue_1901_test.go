//go:build integration

package acceptance

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Issue #1901: the ClickStack bundle's dashboards, saved searches and alerts
// are code, applied by a seed job idempotently by name; the fleet overview
// shows two deployments apart and together; a deployment that stops reporting
// fires an alert to a generic webhook; and the Prometheus rule files pass
// promtool.
//
// Wire forms: no tools/call parameter is involved. The seed speaks HyperDX's
// API; the alert reaches a webhook fixture this test serves; the fleet tiles
// run as their rendered SQL in ClickHouse.
//
// Stack: #1900's (the bundle's compose profile, `make dev` pointed at its edge
// collector with DEV_DEPLOYMENT_ID_B=acme-dev-b). CLICKSTACK_COMPOSE_PROJECT
// and CLICKSTACK_ENV_FILE name the compose project and its .env when they are
// not the defaults (mcp-observability, the bundle's .env).

const (
	issue1901Bundle = "../../deployments/observability/clickstack"
	issue1901Seed   = issue1901Bundle + "/seed"
)

// bundleEnv is the compose profile's .env, as a map.
func bundleEnv(t *testing.T) (path string, env map[string]string) {
	t.Helper()
	path = os.Getenv("CLICKSTACK_ENV_FILE")
	if path == "" {
		path = filepath.Join(issue1901Bundle, ".env")
	}
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		t.Fatalf("the bundle's .env (%s): %v. Copy .env.example and start the compose profile", path, err)
	}
	defer f.Close() //nolint:errcheck // read only
	env = map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "="); ok && !strings.HasPrefix(k, "#") {
			env[k] = v
		}
	}
	if env["HYPERDX_SEED_EMAIL"] == "" {
		env["HYPERDX_SEED_EMAIL"] = "admin@example.com" // compose.yaml's default
	}
	return path, env
}

// hyperdxAPI is the bundle's HyperDX API base.
func hyperdxAPI(env map[string]string) string {
	port := env["CLICKSTACK_API_PORT"]
	if port == "" {
		port = "8000"
	}
	return "http://127.0.0.1:" + port
}

// hyperdxKey signs in as the seed's user and reads its personal API key, the
// way the seed's --bootstrap does: HyperDX scopes the session cookie to
// localhost, so it is carried by hand.
func hyperdxKey(t *testing.T, env map[string]string) string {
	t.Helper()
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, env["HYPERDX_SEED_EMAIL"], env["HYPERDX_SEED_PASSWORD"])
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, hyperdxAPI(env)+"/login/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := noRedirect.Do(req)
	if err != nil {
		t.Fatalf("signing in to HyperDX: %v", err)
	}
	_ = res.Body.Close()
	var cookie string
	for _, c := range res.Cookies() {
		if c.Name == "connect.sid" {
			cookie = c.Name + "=" + c.Value
		}
	}
	if cookie == "" {
		t.Fatalf("HyperDX sign-in returned no session (status %d); has the seed bootstrapped its user?", res.StatusCode)
	}
	req, _ = http.NewRequestWithContext(t.Context(), http.MethodGet, hyperdxAPI(env)+"/me", http.NoBody)
	req.Header.Set("Cookie", cookie)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("reading the HyperDX account: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // read below
	var me struct{ AccessKey string }
	if err := json.NewDecoder(res.Body).Decode(&me); err != nil || me.AccessKey == "" {
		t.Fatalf("no access key on the HyperDX account: %v", err)
	}
	return me.AccessKey
}

// hyperdxNames lists the names of one kind of HyperDX object.
func hyperdxNames(t *testing.T, env map[string]string, key, kind string) map[string]bool {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, hyperdxAPI(env)+"/api/v2/"+kind, http.NoBody)
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("listing %s: %v", kind, err)
	}
	defer res.Body.Close() //nolint:errcheck // read below
	var out struct{ Data []struct{ Name string } }
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("listing %s: %v", kind, err)
	}
	names := map[string]bool{}
	for _, d := range out.Data {
		names[d.Name] = true
	}
	return names
}

// definedNames is the names the repository's definitions carry.
func definedNames(t *testing.T) (dashboards, searches []string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(issue1901Seed, "dashboards", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no dashboards under %s: %v", issue1901Seed, err)
	}
	for _, f := range files {
		var d struct{ Name string }
		readJSON1901(t, f, &d)
		dashboards = append(dashboards, d.Name)
	}
	var list []struct{ Name string }
	readJSON1901(t, filepath.Join(issue1901Seed, "saved-searches.json"), &list)
	for _, s := range list {
		searches = append(searches, s.Name)
	}
	return dashboards, searches
}

func readJSON1901(t *testing.T, path string, out any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// runSeedJob runs the compose profile's seed service once and returns its
// output. --no-deps: ClickStack is already up, and the job only talks to it.
func runSeedJob(t *testing.T, envFile string) string {
	t.Helper()
	project := os.Getenv("CLICKSTACK_COMPOSE_PROJECT")
	if project == "" {
		project = "mcp-observability"
	}
	cmd := exec.CommandContext(t.Context(), "docker", "compose", "-p", project, "--env-file", envFile, //nolint:gosec // the test's own stack
		"-f", filepath.Join(issue1901Bundle, "compose.yaml"), "run", "--rm", "--no-deps", "seed")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the seed job: %v\n%s", err, out)
	}
	return string(out)
}

// TestIssue1901_TheSeedJobCreatesEverythingAndASecondRunChangesNothing: on the
// compose profile the seed job creates every dashboard and saved search, and a
// second run changes nothing.
func TestIssue1901_TheSeedJobCreatesEverythingAndASecondRunChangesNothing(t *testing.T) {
	envFile, env := bundleEnv(t)
	first := runSeedJob(t, envFile)
	key := hyperdxKey(t, env)
	dashboards, searches := definedNames(t)
	have := hyperdxNames(t, env, key, "dashboards")
	for _, n := range dashboards {
		if !have[n] {
			t.Errorf("dashboard %q is not in HyperDX after the seed:\n%s", n, first)
		}
	}
	have = hyperdxNames(t, env, key, "saved-searches")
	for _, n := range searches {
		if !have[n] {
			t.Errorf("saved search %q is not in HyperDX after the seed", n)
		}
	}
	if second := runSeedJob(t, envFile); !strings.Contains(second, "seed: 0 writes") {
		t.Fatalf("the second run changed something:\n%s", second)
	}
}

// renderTile is one tile's SQL from the seed's --render, with the given
// dashboard filters selected.
func renderTile(t *testing.T, name string, filters ...string) string {
	t.Helper()
	args := []string{"-I", filepath.Join(issue1901Seed, "seed.py"), "--dir", issue1901Seed, "--render"}
	for _, f := range filters {
		args = append(args, "--filter", f)
	}
	out, err := exec.CommandContext(t.Context(), "python3", args...).Output() //nolint:gosec // the repository's own script
	if err != nil {
		t.Fatalf("seed.py --render: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		var r struct{ Name, SQL string }
		if json.Unmarshal([]byte(line), &r) == nil && r.Name == name {
			return r.SQL
		}
	}
	t.Fatalf("no tile %q in the rendered definitions", name)
	return ""
}

// TestIssue1901_TheFleetOverviewShowsTwoDeploymentsApartAndTogether: the fleet
// overview's tiles, run as HyperDX runs them, show the two instances of the
// fleet test separately (the Deployment filter on either) and together (no
// filter).
func TestIssue1901_TheFleetOverviewShowsTwoDeploymentsApartAndTogether(t *testing.T) {
	reporting := "MCP Platform: Fleet overview / Deployments reporting (last 5 minutes)"
	lastSeen := "MCP Platform: Fleet overview / Deployments: version and last seen"
	if got := clickhouseNumber(t, renderTile(t, reporting)); got < 2 {
		t.Errorf("together: %v deployments reporting, want both", got)
	}
	rows := clickhouse(t, renderTile(t, lastSeen))
	if len(rows) < 2 {
		t.Errorf("together: the last-seen table has %d rows, want a row per deployment: %v", len(rows), rows)
	}
	for _, d := range []string{"acme-dev", issue1900DeploymentB} {
		if got := clickhouseNumber(t, renderTile(t, reporting, "deployment="+d)); got != 1 {
			t.Errorf("filtered to %s: %v deployments reporting, want 1", d, got)
		}
		rows := clickhouse(t, renderTile(t, lastSeen, "deployment="+d))
		if len(rows) != 1 || !strings.HasPrefix(rows[0], d+"\t") {
			t.Errorf("filtered to %s: last-seen rows %v", d, rows)
		}
	}
}

// webhookFixture is a generic webhook receiver HyperDX, in its container,
// reaches through host.docker.internal.
type webhookFixture struct {
	mu     sync.Mutex
	bodies []string
	url    string
}

func startWebhookFixture(t *testing.T) *webhookFixture {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &webhookFixture{url: fmt.Sprintf("http://host.docker.internal:%d/alert", ln.Addr().(*net.TCPAddr).Port)}
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.bodies = append(f.bodies, string(b))
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return f
}

func (f *webhookFixture) received(substr string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.bodies {
		if strings.Contains(b, substr) {
			return true
		}
	}
	return false
}

// seedWithAlerts runs seed.py against HyperDX with the alerts' webhook and,
// when interval is set, every alert evaluated on it.
func seedWithAlerts(t *testing.T, env map[string]string, key, webhook, interval string) {
	t.Helper()
	// Not t.Context(): the restore runs in a cleanup, after it is cancelled.
	cmd := exec.CommandContext(context.Background(), "python3", "-I", filepath.Join(issue1901Seed, "seed.py"), "--dir", issue1901Seed) //nolint:gosec // the repository's own script
	cmd.Env = append(os.Environ(), "HYPERDX_API_URL="+hyperdxAPI(env), "HYPERDX_API_KEY="+key,
		"HYPERDX_ALERT_WEBHOOK_URL="+webhook, "HYPERDX_ALERT_INTERVAL="+interval)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seeding the alerts: %v\n%s", err, out)
	}
}

// replicaBProcesses is the second replica's platform process (make dev's
// build/air-b binary).
func replicaBProcesses(t *testing.T) []int {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "pgrep", "-f", "build/air-b/mcp-data-platform").Output()
	if err != nil {
		t.Fatalf("finding the second replica's process: %v. `make dev` runs it unless DEV_REPLICAS=1", err)
	}
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		var pid int
		if _, err := fmt.Sscan(f, &pid); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// notReporting is the count a "Deployment not reporting" notification
// carries ("<value> exceeds 0"), or -1 when the body is not one.
func notReporting(body string) float64 {
	if !strings.Contains(body, "Deployment not reporting") {
		return -1
	}
	m := notReportingValue.FindStringSubmatch(body)
	if m == nil {
		return -1
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	return v
}

var notReportingValue = regexp.MustCompile(`\\n(\d+(?:\.\d+)?) (?:exceeds|meets or exceeds) 0\\n`)

// since is every notification received after the n-th.
func (f *webhookFixture) since(n int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.bodies[n:]...)
}

func (f *webhookFixture) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.bodies)
}

// TestIssue1901_AStoppedDeploymentFiresTheNotReportingAlert: stopping one
// instance fires the not-reporting alert to a generic webhook, and it is
// silent while both report. The alerts are evaluated every minute for the run
// (HYPERDX_ALERT_INTERVAL) and put back on their own intervals after. The
// second replica is paused (SIGSTOP), so it sends nothing while it keeps its
// state, and resumed when the test ends.
func TestIssue1901_AStoppedDeploymentFiresTheNotReportingAlert(t *testing.T) {
	_, env := bundleEnv(t)
	key := hyperdxKey(t, env)
	hook := startWebhookFixture(t)
	seedWithAlerts(t, env, key, hook.url, "1m")
	t.Cleanup(func() { seedWithAlerts(t, env, key, hook.url, "") })

	// Both deployments reporting: three evaluations, and no notification
	// that says one stopped.
	quiet := time.Now().Add(3*time.Minute + 15*time.Second)
	for time.Now().Before(quiet) {
		time.Sleep(5 * time.Second)
	}
	for _, b := range hook.since(0) {
		if v := notReporting(b); v > 0 {
			t.Fatalf("the not-reporting alert fired (%v) while both deployments report:\n%s", v, b)
		}
	}

	before := hook.count()
	pids := replicaBProcesses(t)
	for _, pid := range pids {
		if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
			t.Fatalf("pausing replica B (%d): %v", pid, err)
		}
	}
	t.Cleanup(func() {
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGCONT)
		}
	})

	deadline := time.Now().Add(8 * time.Minute)
	for time.Now().Before(deadline) {
		for _, b := range hook.since(before) {
			if v := notReporting(b); v >= 1 {
				return
			}
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("no not-reporting alert with a count of at least one reached the webhook in 8 minutes; %d notifications arrived after the pause", hook.count()-before)
}

// TestIssue1901_PromtoolPassesOnTheRuleFiles: `promtool check rules` passes on
// the updated rule files, and the rule unit tests pass.
func TestIssue1901_PromtoolPassesOnTheRuleFiles(t *testing.T) {
	root := "../.."
	test := exec.CommandContext(t.Context(), "bash", "scripts/alert-rules-test.sh")
	test.Dir = root
	if out, err := test.CombinedOutput(); err != nil {
		t.Fatalf("alert rule tests: %v\n%s", err, out)
	}
	dir := t.TempDir()
	for _, f := range []string{"alert-rules.yaml", "recording-rules.yaml"} {
		extract := exec.CommandContext(t.Context(), "python3", "-c", `
import sys
lines = open(sys.argv[1]).read().splitlines()
start = next(i for i, l in enumerate(lines) if l.strip().endswith(".yaml: |")) + 1
indent = len(lines[start]) - len(lines[start].lstrip())
for l in lines[start:]:
    if l.strip() and (len(l) - len(l.lstrip())) < indent:
        break
    print(l[indent:] if l.strip() else "")
`, filepath.Join(root, "deployments/observability", f)) //nolint:gosec // fixed script, repository files
		out, err := extract.Output()
		if err != nil {
			t.Fatalf("extracting %s: %v", f, err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), out, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	check := exec.CommandContext(t.Context(), "docker", "run", "--rm", "-v", dir+":/rules:ro", "--entrypoint", "promtool", //nolint:gosec // the dev stack's pinned Prometheus image
		"prom/prometheus:v3.1.0", "check", "rules", "/rules/alert-rules.yaml", "/rules/recording-rules.yaml")
	if out, err := check.CombinedOutput(); err != nil {
		t.Fatalf("promtool check rules: %v\n%s", err, out)
	}
}
