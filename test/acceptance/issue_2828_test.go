//go:build integration

package acceptance

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Issue #2828: a site type check kept wazero's compilation cache, about 177
// MiB, in sites.typecheck.dir for the replica's life beside the laid-out
// compiler, enough to evict a pod whose /tmp is sized for upload spooling; and
// deleting a site left its checks and incremental state behind. After a check
// the directory now holds the compiler and nothing else, and deleting the site
// deletes its checks and state. (The memory default, a quarter of the
// container's limit, is read from the cgroup at start and is unit-tested: the
// dev stack's binary runs in no container.)
//
// Wire forms: manage_site's command, site and note are strings and files a
// list of objects whose path and content are strings; the portal typecheck
// route is a GET with no body. No parameter touched admits a second form.

// dir2828 is the dev stack's sites.typecheck.dir (dev/platform.yaml), from
// this package's directory.
const dir2828 = "../../build/dev-typecheck"

// TestIssue2828_ACheckLeavesOnlyTheCompilerOnDisk: once a check has finished,
// sites.typecheck.dir holds the laid-out compiler and no compilation cache or
// check scratch.
func TestIssue2828_ACheckLeavesOnlyTheCompilerOnDisk(t *testing.T) {
	// Each check waits its turn and compiles the compiler afresh (no
	// compilation cache is kept on disk, #2828), so the connection outlives the
	// default session bound (#1738).
	c := connectFor(t, 20*time.Minute)
	site := newSite2817(t, c)
	release, _ := publish2793(t, c, site, files2830(`["app.ts"]`))
	if check := check2793(t, c, site, release, 0, 6*time.Minute); check["status"] != "errors" {
		t.Fatalf("the check = %v", check)
	}
	entries, err := os.ReadDir(dir2828)
	if err != nil {
		t.Fatalf("reading %s: %v", dir2828, err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
		if e.Name() == "wasm-cache" || strings.HasPrefix(e.Name(), "check-") {
			t.Errorf("%s holds %s after the check", dir2828, e.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(dir2828, "tsc")); err != nil {
		t.Errorf("the laid-out compiler is not kept: %v (holds %v)", err, names)
	}
}

// TestIssue2828_DeletingASiteDeletesItsChecksAndState: a site that was
// checked, then deleted, leaves no check and no incremental state.
func TestIssue2828_DeletingASiteDeletesItsChecksAndState(t *testing.T) {
	// Each check waits its turn and compiles the compiler afresh (no
	// compilation cache is kept on disk, #2828), so the connection outlives the
	// default session bound (#1738).
	c := connectFor(t, 20*time.Minute)
	site := newSite2817(t, c)
	release, _ := publish2793(t, c, site, files2830(`["app.ts"]`))
	check2793(t, c, site, release, 0, 6*time.Minute)
	db := devDB(t)
	var id string
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM sites WHERE name = $1`, site).Scan(&id); err != nil {
		t.Fatalf("the site's id: %v", err)
	}
	count := func() int {
		var n int
		if err := db.QueryRowContext(context.Background(), `SELECT (SELECT count(*) FROM site_typechecks WHERE site_id = $1) +
			(SELECT count(*) FROM site_typecheck_state WHERE site_id = $1)`, id).Scan(&n); err != nil {
			t.Fatalf("counting the site's checks: %v", err)
		}
		return n
	}
	if count() == 0 {
		t.Fatal("the checked site has no check or state to delete")
	}
	c.call("manage_site", map[string]any{"command": "delete", "site": site})
	if n := count(); n != 0 {
		t.Errorf("the deleted site left %d check and state rows", n)
	}
}
