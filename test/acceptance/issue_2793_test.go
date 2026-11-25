//go:build integration

package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// Issue #2793: a site whose _build.json asks for a type check has each release
// checked with TypeScript after it publishes, in the background: a check, not
// a gate. The compiler (tsc.wasm, fetched once from the npm registry at a
// pinned version, verified and kept in the platform's bucket) runs in a
// worker process under a memory limit, reads the declaration files of the
// packages the site's lockfile locks, and keeps each environment's
// incremental state. Every error is reported with its file, line, column and
// code.
//
// Wire forms: manage_site's command, site, name and environment are strings,
// release a number, and files a list of objects whose path and content are
// strings, each sent in that one form; the portal typecheck routes are a GET
// and a POST with no body. The started process reads a YAML configuration
// whose sites.typecheck.memory_limit is a number. No parameter touched admits
// a second form.

// The site checked: one entry importing tiny-invariant, whose declarations
// come from the lockfile's tarball.
const (
	build2793 = `{
  "entries": ["src/app.ts"],
  "imports": {"tiny-invariant": "https://esm.sh/tiny-invariant@1.3.3"},
  "typecheck": {}
}`
	tsconfig2793 = `{"compilerOptions":{"strict":true,"target":"es2022","module":"esnext","moduleResolution":"bundler","noEmit":true},"include":["src"]}`
	lock2793     = `{
  "name": "acc-2793", "version": "1.0.0", "lockfileVersion": 3, "requires": true,
  "packages": {
    "": {"name": "acc-2793", "version": "1.0.0", "dependencies": {"tiny-invariant": "1.3.3"}},
    "node_modules/tiny-invariant": {
      "version": "1.3.3",
      "resolved": "https://registry.npmjs.org/tiny-invariant/-/tiny-invariant-1.3.3.tgz",
      "integrity": "sha512-+FbBPE1o9QAYvviau/qC5SE3caw21q3xkvWKBtja5vgqOWIHHJ3ioaq1VPfn/Szqctz2bU/oYeKd9/z5BL+PVg=="
    }
  }
}`
	// broken2793 has two errors: a string where a number is declared, and a
	// number where tiny-invariant's declarations take a message, which only a
	// check that read those declarations finds.
	broken2793 = "import invariant from \"tiny-invariant\";\n\nexport const total: number = \"seven\";\ninvariant(total > 0, 42);\n"
	fixed2793  = "import invariant from \"tiny-invariant\";\n\nexport const total: number = 7;\ninvariant(total > 0, \"total is positive\");\n"
	index2793  = `<!doctype html><html><body><script type="module" src="/src/app.js"></script></body></html>`
)

func files2793(app string) []map[string]any {
	return []map[string]any{
		{"path": "_build.json", "content": build2793},
		{"path": "tsconfig.json", "content": tsconfig2793},
		{"path": "package-lock.json", "content": lock2793},
		{"path": "src/app.ts", "content": app},
		{"path": "index.html", "content": index2793},
	}
}

// publish2793 publishes files to a site's dev and returns the release number
// and what the publish said of the check.
func publish2793(t *testing.T, c *client, site string, files []map[string]any) (int, map[string]any) {
	t.Helper()
	out := c.call("manage_site", map[string]any{"command": "publish", "site": site, "note": "acceptance 2793", "files": files})
	rel, _ := out["release"].(map[string]any)
	n := number2588(rel["number"])
	if n == 0 {
		t.Fatalf("the publish stored no release: %v", out)
	}
	tc, _ := out["typecheck"].(map[string]any)
	return n, tc
}

// check2793 waits, up to bound, for the newest check of a release to finish
// and returns it.
func check2793(t *testing.T, c *client, site string, release int, after float64, bound time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(bound)
	for {
		_, view := c.rest(http.MethodGet, fmt.Sprintf("/api/v1/portal/sites/%s/releases/%d/typecheck", site, release), nil)
		check, _ := view["check"].(map[string]any)
		id, _ := check["id"].(float64)
		status, _ := check["status"].(string)
		if check != nil && id > after && status != "queued" && status != "running" {
			return check
		}
		if time.Now().After(deadline) {
			t.Fatalf("release %d's check did not finish within %s: %v", release, bound, view)
		}
		time.Sleep(2 * time.Second)
	}
}

// result2793 is a finished check's result.
func result2793(check map[string]any) map[string]any {
	r, _ := check["result"].(map[string]any)
	return r
}

// TestIssue2793_ErrorsAreReportedWithFileLineColumnAndCode: a release with two
// type errors, one only tiny-invariant's declarations reveal, is checked after
// it publishes and reports both.
func TestIssue2793_ErrorsAreReportedWithFileLineColumnAndCode(t *testing.T) {
	c := connect(t)
	site := newSite2817(t, c)
	release, queued := publish2793(t, c, site, files2793(broken2793))
	if queued == nil || (queued["status"] != "queued" && queued["status"] != "running") {
		t.Fatalf("the publish did not queue a check: %v", queued)
	}
	check := check2793(t, c, site, release, 0, 6*time.Minute)
	res := result2793(check)
	if check["status"] != "errors" {
		t.Fatalf("the check = %v", check)
	}
	raw, _ := json.Marshal(res["diagnostics"])
	var diags []struct {
		File   string `json:"file"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
		Code   string `json:"code"`
	}
	_ = json.Unmarshal(raw, &diags)
	codes := []string{}
	for _, d := range diags {
		if d.File != "src/app.ts" || d.Line == 0 || d.Column == 0 {
			t.Errorf("a diagnostic does not name its place: %+v", d)
		}
		codes = append(codes, d.Code)
	}
	if !slices.Contains(codes, "TS2322") || !slices.Contains(codes, "TS2345") {
		t.Errorf("the check found %v; want TS2322 and the TS2345 only tiny-invariant's declarations reveal", codes)
	}
	if res["packages"] != float64(1) || len(asSlice(res["missing"])) != 0 {
		t.Errorf("the lockfile's package was not read: packages %v missing %v", res["packages"], res["missing"])
	}
	_, sums := c.rest(http.MethodGet, "/api/v1/portal/sites/"+site+"/typechecks", nil)
	if !strings.Contains(asJSON(sums), fmt.Sprintf(`"%d":{"errors":%d,"status":"errors"}`, release, len(diags))) {
		t.Errorf("the releases list does not carry the check: %v", sums)
	}
}

// TestIssue2793_OnlyTheSitesManagersReadItsChecks: a check's errors quote the
// release's source, so a caller who does not manage the site is refused both
// routes.
func TestIssue2793_OnlyTheSitesManagersReadItsChecks(t *testing.T) {
	c := connect(t)
	site := newSite2817(t, c)
	release, _ := publish2793(t, c, site, files2793(fixed2793))
	other := connectAs(t, "acme-analyst-key")
	for _, path := range []string{
		"/api/v1/portal/sites/" + site + "/typechecks",
		fmt.Sprintf("/api/v1/portal/sites/%s/releases/%d/typecheck", site, release),
	} {
		if status, body := other.rest(http.MethodGet, path, nil); status != http.StatusForbidden && status != http.StatusNotFound {
			t.Errorf("GET %s by a caller who does not manage the site = %d: %v", path, status, body)
		}
	}
}

// asSlice is a JSON array, nil for anything else.
func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

// TestIssue2793_AFixedReleaseIsCleanAndARecheckFetchesNothing: the fixed
// release is clean; checking it again through manage_site fetches no package,
// and the environment's incremental state is kept.
func TestIssue2793_AFixedReleaseIsCleanAndARecheckFetchesNothing(t *testing.T) {
	c := connect(t)
	site := newSite2817(t, c)
	release, _ := publish2793(t, c, site, files2793(fixed2793))
	first := check2793(t, c, site, release, 0, 6*time.Minute)
	if first["status"] != "clean" {
		t.Fatalf("the fixed release = %v", first)
	}
	again := c.call("manage_site", map[string]any{"command": "typecheck", "site": site, "release": release})
	if !strings.Contains(asJSON(again), `"queued"`) {
		t.Fatalf("typecheck did not queue a check: %v", again)
	}
	id, _ := first["id"].(float64)
	second := check2793(t, c, site, release, id, 3*time.Minute)
	if second["status"] != "clean" || result2793(second)["fetched"] != float64(0) {
		t.Errorf("the re-check = %v; want clean with nothing fetched", second)
	}
	var n int
	if err := devDB(t).QueryRowContext(context.Background(), `SELECT count(*) FROM site_typecheck_state s JOIN sites x ON x.id = s.site_id
		WHERE x.name = $1 AND s.environment = 'dev' AND length(s.tsbuildinfo) > 0`, site).Scan(&n); err != nil || n != 1 {
		t.Errorf("dev's incremental state: %d rows, %v", n, err)
	}
}

// TestIssue2793_ASiteThatAsksForNoCheckGetsNone: a compiled site whose build
// file asks for no type check is never checked.
func TestIssue2793_ASiteThatAsksForNoCheckGetsNone(t *testing.T) {
	c := connect(t)
	site := newSite2817(t, c)
	_, queued := publish2793(t, c, site, compiled2774(buildFile2774))
	if queued != nil {
		t.Errorf("a site asking for no check had one queued: %v", queued)
	}
	_, sums := c.rest(http.MethodGet, "/api/v1/portal/sites/"+site+"/typechecks", nil)
	if rel, _ := sums["releases"].(map[string]any); len(rel) != 0 {
		t.Errorf("the site lists checks: %v", sums)
	}
}

// issue2793Config is a deployment on its own database with a type check memory
// limit no compiler fits in; the first %s is the address.
const issue2793Config = `
server:
  name: acceptance-2793
  transport: http
  address: "%%s"
auth:
  api_keys:
    enabled: true
    keys:
      - key: "acceptance-2793-key"
        name: "acceptance-2793"
        roles: ["admin"]
personas:
  admin:
    display_name: Admin
    roles: ["admin"]
    tools:
      allow: ["*"]
    connections:
      allow: ["*"]
database:
  dsn: "%s"
  max_open_conns: 5
toolkits:
  s3:
    enabled: true
    instances:
      acc-2793:
        enabled: true
        endpoint: "http://localhost:%s"
        region: "us-east-1"
        access_key_id: "dev-access-key"
        secret_access_key: "dev-secret-key"
        use_path_style: true
    default: acc-2793
portal:
  s3_connection: acc-2793
  s3_bucket: portal-assets
  public_base_url: "https://acceptance-2793.localhost"
sites:
  pages_domain: pages.localhost
  typecheck:
    memory_limit: 16777216
scripts:
  worker:
    enabled: false
notifications:
  enabled: false
thumbnails:
  enabled: false
`

// TestIssue2793_ACheckOverItsMemoryLimitIsReportedAndTheReplicaServes: on a
// deployment whose limit no compiler fits in, the check reports over_limit
// and the platform keeps answering.
func TestIssue2793_ACheckOverItsMemoryLimitIsReportedAndTheReplicaServes(t *testing.T) {
	db := fmt.Sprintf("acceptance_2793_%d", time.Now().UnixNano())
	devPSQL(t, "mcp_platform", "CREATE DATABASE "+db)
	t.Cleanup(func() { devPSQL(t, "mcp_platform", "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)") })
	dsn := "postgres://platform:platform_secret@localhost:" + devPostgresPort() + "/" + db + "?sslmode=disable"
	p := startPlatform(t, fmt.Sprintf(issue2793Config, dsn, devS3Port2616()))
	p.serving(t, 3*time.Minute)
	c := connectAt(t, p.base, "acceptance-2793-key")

	site := newSite2817(t, c)
	release, queued := publish2793(t, c, site, files2793(fixed2793))
	if queued == nil {
		t.Fatal("the publish queued no check")
	}
	check := check2793(t, c, site, release, 0, 6*time.Minute)
	if check["status"] != "over_limit" {
		t.Fatalf("the check = %v; want over_limit", check)
	}
	if msg, _ := result2793(check)["message"].(string); !strings.Contains(msg, "sites.typecheck.memory_limit") {
		t.Errorf("the result does not name the setting: %q", msg)
	}
	if info := c.call("platform_info", map[string]any{}); info["agent_instructions"] == nil {
		t.Errorf("the platform stopped answering after the check: %v", info)
	}
}
