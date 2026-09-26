//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path"
	"testing"
	"time"
)

// Issue #1904: rows and objects nothing ever removed. The per-table
// criterion (an expired row and a live row, only the expired one removed) is
// executed against a real database by each sweep's *RealDB test. This file
// executes the same through the running platform: a person deletes an asset
// and forgets a memory through the tools and routes they use, the deletion is
// moved past its retention, and the platform's own sweep removes the rows and
// every object the asset named, while a deletion inside its retention stays.
//
// The retention is wall-clock time, so the deletion is backdated directly in
// the dev database, as #1694's criterion backdates its revocation; everything
// the criterion is about -- finding the expired item, deleting its objects and
// then its rows, and leaving the recent one -- is the platform's own work. The
// dev stack runs the sweeps every 30 seconds (retention.every).
//
// Wire forms: save_asset's name, content and content_type, memory_capture's
// type, content, category and confidence, memory_manage's command and id, and
// s3_list's connection, bucket and prefix are typed strings, so each admits one
// JSON form and is sent as a literal tools/call parameter of it. The portal's
// delete route takes no body.

// issue1904Sweep bounds how long the criterion waits for a sweep that runs
// every 30 seconds to have run.
const issue1904Sweep = 90 * time.Second

func issue1904DB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the dev database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func issue1904Exec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func issue1904Count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var n int
	if err := db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// issue1904Await polls until done reports true or the sweep's bound passes.
func issue1904Await(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(issue1904Sweep)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within %s", what, issue1904Sweep)
		}
		time.Sleep(2 * time.Second)
	}
}

// TestIssue1904_ADeletedAssetIsPurgedWithItsObjectsAfterItsRetention is the
// portal half: the asset row, its version rows and every object it named are
// gone once its deletion is past the retention; an asset deleted today is
// still there.
func TestIssue1904_ADeletedAssetIsPurgedWithItsObjectsAfterItsRetention(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	db := issue1904DB(t)
	expired := saveAsset1789(t, c, "text/html", "<!DOCTYPE html><html><body><h1>expired</h1></body></html>")
	recent := saveAsset1789(t, c, "text/html", "<!DOCTYPE html><html><body><h1>recent</h1></body></html>")
	row := awaitAssetTile1787(t, c, expired, true)
	dir := path.Dir(fmt.Sprint(row["s3_key"])) + "/"
	if objects := issue1903List(t, c, issue1903PortalConn, issue1903PortalBucket, dir); len(objects) < 2 {
		t.Fatalf("the asset's directory %s holds %v; want its content and tiles before the purge", dir, objects)
	}

	for _, id := range []string{expired, recent} {
		if status, body := c.rest(http.MethodDelete, "/api/v1/portal/assets/"+id, http.NoBody); status != http.StatusOK && status != http.StatusNoContent {
			t.Fatalf("DELETE asset %s: HTTP %d %v", id, status, body)
		}
	}
	issue1904Exec(t, db, `UPDATE portal_assets SET deleted_at = NOW() - interval '31 days' WHERE id = $1`, expired)

	issue1904Await(t, "the expired asset's row removed", func() bool {
		return issue1904Count(t, db, `SELECT COUNT(*) FROM portal_assets WHERE id = $1`, expired) == 0
	})
	if n := issue1904Count(t, db, `SELECT COUNT(*) FROM portal_asset_versions WHERE asset_id = $1`, expired); n != 0 {
		t.Errorf("%d version rows of the purged asset remain", n)
	}
	if left := issue1903List(t, c, issue1903PortalConn, issue1903PortalBucket, dir); len(left) != 0 {
		t.Errorf("objects left under the purged asset's directory %s: %v", dir, left)
	}
	if n := issue1904Count(t, db, `SELECT COUNT(*) FROM portal_assets WHERE id = $1 AND deleted_at IS NOT NULL`, recent); n != 1 {
		t.Errorf("the asset deleted today was removed before its retention")
	}
}

// TestIssue1904_AForgottenMemoryIsRemovedAfterItsRetention is the memory
// half: memory_manage forget archives a record, and the sweep removes it once
// the archive is past the retention, leaving one archived today.
func TestIssue1904_AForgottenMemoryIsRemovedAfterItsRetention(t *testing.T) {
	c := connect(t)
	db := issue1904DB(t)
	stamp := time.Now().UnixNano()
	expired := captureMemory1625(t, c, fmt.Sprintf("Acceptance #1904: an old forgotten fact (%d).", stamp), "business_knowledge", nil)
	recent := captureMemory1625(t, c, fmt.Sprintf("Acceptance #1904: a fact forgotten today (%d).", stamp), "business_knowledge", nil)
	for _, id := range []string{expired, recent} {
		c.call("memory_manage", map[string]any{"command": "forget", "id": id})
	}
	issue1904Exec(t, db, `UPDATE memory_records SET updated_at = NOW() - interval '91 days' WHERE id = $1 AND status = 'archived'`, expired)

	issue1904Await(t, "the expired memory record removed", func() bool {
		return issue1904Count(t, db, `SELECT COUNT(*) FROM memory_records WHERE id = $1`, expired) == 0
	})
	if n := issue1904Count(t, db, `SELECT COUNT(*) FROM memory_records WHERE id = $1 AND status = 'archived'`, recent); n != 1 {
		t.Errorf("the memory archived today was removed before its retention")
	}
}
