//go:build integration

package acceptance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Issue #2833: every site type check compiled the TypeScript compiler's
// WebAssembly again before it checked anything (no compilation cache since
// #2828), about 104 s of a 162 s check at a 0.5 CPU limit, and nothing said
// how a check's time divided. A check now reports compile_ms, the part of
// duration_ms spent compiling the compiler, in its result and its log line;
// and sites.typecheck.compile_cache keeps the compiled compiler in
// sites.typecheck.dir, so the next check reads it rather than compiling it.
// Turning the setting off removes the cache at the next start.
//
// Wire forms: manage_site's command, site and note are strings, release a
// number, and files a list of objects whose path and content are strings;
// the portal typecheck route is a GET with no body. The started process reads
// a YAML configuration whose sites.typecheck.compile_cache is a boolean and
// whose dir is a string. No parameter touched admits a second form.

// issue2833Config is a deployment on its own database whose type checks work
// in a directory the criterion reads, with the compilation cache on or off;
// the first %s is the address.
const issue2833Config = `
server:
  name: acceptance-2833
  transport: http
  address: "%%s"
auth:
  api_keys:
    enabled: true
    keys:
      - key: "acceptance-2833-key"
        name: "acceptance-2833"
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
      acc-2833:
        enabled: true
        endpoint: "http://localhost:%s"
        region: "us-east-1"
        access_key_id: "dev-access-key"
        secret_access_key: "dev-secret-key"
        use_path_style: true
    default: acc-2833
portal:
  s3_connection: acc-2833
  s3_bucket: portal-assets
  public_base_url: "https://acceptance-2833.localhost"
sites:
  pages_domain: pages.localhost
  typecheck:
    dir: "%s"
    compile_cache: %t
scripts:
  worker:
    enabled: false
notifications:
  enabled: false
thumbnails:
  enabled: false
`

// deployment2833 is a database of its own for a started platform, dropped
// when the test ends, and its DSN.
func deployment2833(t *testing.T) string {
	t.Helper()
	db := fmt.Sprintf("acceptance_2833_%d", time.Now().UnixNano())
	devPSQL(t, "mcp_platform", "CREATE DATABASE "+db)
	t.Cleanup(func() { devPSQL(t, "mcp_platform", "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)") })
	return "postgres://platform:platform_secret@localhost:" + devPostgresPort() + "/" + db + "?sslmode=disable"
}

// start2833 starts a platform on dsn whose checks work in dir, keeping the
// compilation cache or not, and connects to it.
func start2833(t *testing.T, dsn, dir string, keep bool) (*platformProcess, *client) {
	t.Helper()
	p := startPlatform(t, fmt.Sprintf(issue2833Config, dsn, devS3Port2616(), dir, keep))
	p.serving(t, 3*time.Minute)
	// Each check compiles the compiler once at least, so the connection
	// outlives the default session bound (#1738).
	return p, connectAtFor(t, p.base, "acceptance-2833-key", 20*time.Minute)
}

// compileMS2833 is a finished check's compile_ms.
func compileMS2833(t *testing.T, check map[string]any) float64 {
	t.Helper()
	res := result2793(check)
	ms, _ := res["compile_ms"].(float64)
	total, _ := res["duration_ms"].(float64)
	if ms <= 0 || ms > total {
		t.Fatalf("the check's compile_ms = %v of duration_ms %v; want a part of it: %v", res["compile_ms"], total, check)
	}
	return ms
}

// cacheFiles2833 is every file of the compilation cache under dir.
func cacheFiles2833(dir string) []string {
	var out []string
	_ = filepath.WalkDir(filepath.Join(dir, "compile-cache"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// TestIssue2833_TheNextCheckReadsTheCompiledCompiler: with compile_cache on,
// the first check compiles the compiler into sites.typecheck.dir, the check
// queued again reads it in a fraction of the time, and both log their
// compile time.
func TestIssue2833_TheNextCheckReadsTheCompiledCompiler(t *testing.T) {
	dir := t.TempDir()
	p, c := start2833(t, deployment2833(t), dir, true)
	site := newSite2817(t, c)
	release, _ := publish2793(t, c, site, files2830(`["app.ts"]`))
	first := check2793(t, c, site, release, 0, 6*time.Minute)
	if first["status"] != "errors" {
		t.Fatalf("the first check = %v", first)
	}
	cold := compileMS2833(t, first)
	cached := cacheFiles2833(dir)
	if len(cached) == 0 {
		t.Fatalf("%s holds no compilation cache after the first check", dir)
	}

	again := c.call("manage_site", map[string]any{"command": "typecheck", "site": site, "release": release})
	if !strings.Contains(asJSON(again), `"queued"`) {
		t.Fatalf("typecheck did not queue a check: %v", again)
	}
	id, _ := first["id"].(float64)
	second := check2793(t, c, site, release, id, 6*time.Minute)
	if second["status"] != "errors" {
		t.Fatalf("the second check = %v", second)
	}
	warm := compileMS2833(t, second)
	t.Logf("compile_ms: first %.0f, second %.0f", cold, warm)
	if warm*4 > cold {
		t.Errorf("the second check spent %.0f ms on the compiler against the first's %.0f; want it read from the cache", warm, cold)
	}
	if got := cacheFiles2833(dir); strings.Join(got, ",") != strings.Join(cached, ",") {
		t.Errorf("the second check wrote the cache again: %v, then %v", cached, got)
	}
	logs := p.out.String()
	if n := strings.Count(logs, `"msg":"site type check finished"`); n != 2 || !strings.Contains(logs, `"compile_ms":`) {
		t.Errorf("want two finished-check log lines carrying compile_ms, found %d", n)
	}
}

// TestIssue2833_TurningTheCacheOffFreesItsDisk: a platform started with
// compile_cache off on a directory a cache was kept in removes it, and its
// checks keep none.
func TestIssue2833_TurningTheCacheOffFreesItsDisk(t *testing.T) {
	dir, dsn := t.TempDir(), deployment2833(t)
	p, c := start2833(t, dsn, dir, true)
	site := newSite2817(t, c)
	release, _ := publish2793(t, c, site, files2830(`["app.ts"]`))
	first := check2793(t, c, site, release, 0, 6*time.Minute)
	if len(cacheFiles2833(dir)) == 0 {
		t.Fatalf("no cache was kept to remove: %v", first)
	}
	p.stop(t)

	_, c = start2833(t, dsn, dir, false)
	if _, err := os.Stat(filepath.Join(dir, "compile-cache")); !os.IsNotExist(err) {
		t.Fatalf("the cache is still in %s with compile_cache off (%v)", dir, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tsc")); err != nil {
		t.Errorf("the laid-out compiler went with the cache: %v", err)
	}
	c.call("manage_site", map[string]any{"command": "typecheck", "site": site, "release": release})
	id, _ := first["id"].(float64)
	second := check2793(t, c, site, release, id, 6*time.Minute)
	compileMS2833(t, second)
	if got := cacheFiles2833(dir); len(got) != 0 {
		t.Errorf("a check with compile_cache off kept a cache: %v", got)
	}
}

// TestIssue2833_ACheckOnTheDevStackReportsItsCompileTime: on a deployment
// keeping no cache, a check's result still says how long the compiler took
// to compile, a part of its whole time.
func TestIssue2833_ACheckOnTheDevStackReportsItsCompileTime(t *testing.T) {
	c := connectFor(t, 20*time.Minute)
	site := newSite2817(t, c)
	release, _ := publish2793(t, c, site, files2830(`["app.ts"]`))
	compileMS2833(t, check2793(t, c, site, release, 0, 6*time.Minute))
}
