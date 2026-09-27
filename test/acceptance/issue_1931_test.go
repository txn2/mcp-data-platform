//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"path"
	"strings"
	"testing"
)

// Issue #1931: an asset whose row named no bucket could not be read, and the
// portal purge (#1904) could not remove one. It sent every delete to bucket "", the store answered
// NoSuchBucket, and the row was kept and retried on every sweep. This file
// saves and deletes an asset through the tools and routes a person uses, then
// gives its rows the shape an early release wrote, and asserts the platform's
// own sweep removes it.
//
// The legacy shape and the retention are both set directly in the dev
// database, as #1904's criterion backdates its deletion: no current release
// writes an empty bucket. Resolving the bucket, deleting the objects and
// removing the row are the platform's work.
//
// Wire forms: save_asset's name, content and content_type, and s3_list's
// connection, bucket and prefix are typed strings, so each admits one JSON
// form and is sent as a literal tools/call parameter of it. The portal's
// delete route takes no body.

// issue1931Deleted saves an HTML asset, waits for its tiles, deletes it
// through the portal and returns its id and the directory its objects are in.
func issue1931Deleted(t *testing.T, c *client, title string) (id, dir string) {
	t.Helper()
	id = saveAsset1789(t, c, "text/html", "<!DOCTYPE html><html><body><h1>"+title+"</h1></body></html>")
	row := awaitAssetTile1787(t, c, id, true)
	dir = path.Dir(fmt.Sprint(row["s3_key"])) + "/"
	if status, body := c.rest(http.MethodDelete, "/api/v1/portal/assets/"+id, http.NoBody); status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("DELETE asset %s: HTTP %d %v", id, status, body)
	}
	return id, dir
}

// TestIssue1931_AnAssetRowWithNoBucketIsPurgedFromThePortalBucket is the
// ticket's case: the asset row and its version rows hold an empty bucket. The
// sweep resolves it to the portal bucket, removes every object the asset
// named there, and removes the row.
func TestIssue1931_AnAssetRowWithNoBucketIsPurgedFromThePortalBucket(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	db := issue1904DB(t)
	id, dir := issue1931Deleted(t, c, "legacy row")
	if objects := issue1903List(t, c, issue1903PortalConn, issue1903PortalBucket, dir); len(objects) < 2 {
		t.Fatalf("the asset's directory %s holds %v; want its content and tiles before the purge", dir, objects)
	}

	issue1904Exec(t, db, `UPDATE portal_asset_versions SET s3_bucket = '' WHERE asset_id = $1`, id)
	issue1904Exec(t, db, `UPDATE portal_assets SET s3_bucket = '', deleted_at = NOW() - interval '31 days' WHERE id = $1`, id)

	issue1904Await(t, "the row with no bucket removed", func() bool {
		return issue1904Count(t, db, `SELECT COUNT(*) FROM portal_assets WHERE id = $1`, id) == 0
	})
	if left := issue1903List(t, c, issue1903PortalConn, issue1903PortalBucket, dir); len(left) != 0 {
		t.Errorf("objects left in the portal bucket under the purged asset's directory %s: %v", dir, left)
	}
}

// TestIssue1931_AnAssetInABucketThatNoLongerExistsIsPurged is the third
// expectation: a row naming a bucket the store does not have is not retried
// every sweep for ever. No object can exist in a bucket that does not, so the
// sweep removes the row.
func TestIssue1931_AnAssetInABucketThatNoLongerExistsIsPurged(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	db := issue1904DB(t)
	id, _ := issue1931Deleted(t, c, "retired bucket")

	const retired = "acceptance-1931-retired"
	issue1904Exec(t, db, `UPDATE portal_asset_versions SET s3_bucket = $2 WHERE asset_id = $1`, id, retired)
	issue1904Exec(t, db, `UPDATE portal_assets SET s3_bucket = $2, deleted_at = NOW() - interval '31 days' WHERE id = $1`, id, retired)

	issue1904Await(t, "the row naming a bucket that does not exist removed", func() bool {
		return issue1904Count(t, db, `SELECT COUNT(*) FROM portal_assets WHERE id = $1`, id) == 0
	})
}

// TestIssue1931_AnAssetWhoseRowNamesNoBucketOpens is the read path: a live
// asset whose rows name no bucket could not be opened, since every read takes
// the bucket from the row. The platform names the portal bucket on such rows,
// and the asset's content reads through the portal route.
func TestIssue1931_AnAssetWhoseRowNamesNoBucketOpens(t *testing.T) {
	c := connect(t)
	db := issue1904DB(t)
	const marker = "acceptance 1931 live legacy row"
	id := saveAsset1789(t, c, "text/html", "<!DOCTYPE html><html><body><h1>"+marker+"</h1></body></html>")

	issue1904Exec(t, db, `UPDATE portal_asset_versions SET s3_bucket = '' WHERE asset_id = $1`, id)
	issue1904Exec(t, db, `UPDATE portal_assets SET s3_bucket = '' WHERE id = $1`, id)

	issue1904Await(t, "the portal bucket named on the asset's rows", func() bool {
		return issue1904Count(t, db, `SELECT COUNT(*) FROM portal_assets WHERE id = $1 AND s3_bucket <> ''`, id) == 1 &&
			issue1904Count(t, db, `SELECT COUNT(*) FROM portal_asset_versions WHERE asset_id = $1 AND s3_bucket = ''`, id) == 0
	})
	status, content := c.restText("/api/v1/portal/assets/" + id + "/content")
	if status != http.StatusOK || !strings.Contains(content, marker) {
		t.Fatalf("GET content: HTTP %d, %.200q; want 200 with the saved content", status, content)
	}
}
