//go:build integration

package acceptance

// Issue #2084: the thumbnail worker's idle poll evaluated its whole owed rule
// on every row of resources, portal_assets and portal_collections, every five
// seconds, on every replica. It now reads only rows marked due, through a
// partial index, and a row becoming due wakes the worker over LISTEN.
//
// These criteria run against the dev stack: documents are saved with the
// tools a user calls, the platform draws their tiles with the renderer beside
// it, and the dev database is read to show each one left the due set once
// drawn and that every replica listens for new work. How the idle claim
// performs at 500,000 rows is measured by the real-database tests
// TestResourceThumbnailClaim_RealDB_ScaleIsTheDueRows and
// TestAssetThumbnailClaim_RealDB_ScaleIsTheDueRows.
//
// Wire forms: save_asset's name, content, content_type and description,
// manage_asset's action, collection_id, name, description and sections, and
// manage_resource's action, filename, display_name, path, description,
// content and content_type are typed strings (sections a typed array of
// objects), so each admits one JSON form and is sent as a literal
// tools/call parameter of that form.

import (
	"database/sql"
	"net/http"
	"testing"
	"time"
)

// settleWait2084 bounds how long a drawn row may stay due: the pass after the
// tile is recorded takes it out, and that pass follows at once.
const settleWait2084 = 30 * time.Second

func devDB2084(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the dev database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// awaitSettled2084 waits until the row id of table is out of the due set.
func awaitSettled2084(t *testing.T, db *sql.DB, table, id string) {
	t.Helper()
	deadline := time.Now().Add(settleWait2084)
	for {
		var due sql.NullTime
		// #nosec G701 -- table is one of three constants in this file
		if err := db.QueryRowContext(t.Context(), `SELECT thumbnail_due_at FROM `+table+` WHERE id = $1`, id).Scan(&due); err != nil {
			t.Fatalf("reading %s %s: %v", table, id, err)
		}
		if !due.Valid {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s %s is still due at %v, %s after its tile was drawn", table, id, due.Time, settleWait2084)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// TestIssue2084_ADrawnDocumentLeavesTheDueSet: an asset, a resource and a
// collection are each due when written, drawn by the platform, and then out
// of the due set, so the next idle pass reads none of them.
func TestIssue2084_ADrawnDocumentLeavesTheDueSet(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	db := devDB2084(t)

	asset := saveAsset1787(t, c, "text/html", `<!DOCTYPE html><html><body style="margin:0;background:#16a34a;height:100vh"></body></html>`)
	awaitAssetTile1787(t, c, asset, true)
	awaitSettled2084(t, db, "portal_assets", asset)

	name := "acceptance-2084-" + unique1787()
	out := c.call("manage_resource", map[string]any{
		"action": "create", "filename": name + ".csv", "display_name": name, "path": "acceptance-2084",
		"description":  "Acceptance #2084: a file drawn once and then out of the due set.",
		"content":      "region,revenue\nwest,41208\neast,38112\n",
		"content_type": "text/csv",
	})
	resID, _ := out["resource_id"].(string)
	if resID == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+resID, http.NoBody) })
	awaitResourceTile1787(t, c, resID, true)
	awaitSettled2084(t, db, "resources", resID)

	out = c.call("manage_asset", map[string]any{
		"action": "create_collection", "name": "acceptance-2084-" + unique1787(),
		"description": "Acceptance #2084: a collection whose mosaic is drawn once.",
		"sections":    []any{map[string]any{"title": "Members", "items": []any{map[string]any{"asset_id": asset}}}},
	})
	coll, _ := out["collection_id"].(string)
	if coll == "" {
		t.Fatalf("create_collection returned no id: %v", out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete_collection", "collection_id": coll})
	})
	deadline := time.Now().Add(tileWait1787)
	for {
		if status, _ := readTile1787(t, c, "/api/v1/portal/collections/"+coll, ""); status == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no tile for collection %s after %s", coll, tileWait1787)
		}
		time.Sleep(time.Second)
	}
	awaitSettled2084(t, db, "portal_collections", coll)
}

// TestIssue2084_TheWorkerListensForNewWork: the platform holds a LISTEN
// connection on thumbnail_due, which the trigger notifies when a row becomes
// due, and the rule every table was last swept under is recorded.
func TestIssue2084_TheWorkerListensForNewWork(t *testing.T) {
	db := devDB2084(t)
	var listeners int
	if err := db.QueryRowContext(t.Context(),
		`SELECT count(*) FROM pg_stat_activity WHERE query = 'LISTEN "thumbnail_due"'`).Scan(&listeners); err != nil {
		t.Fatalf("reading pg_stat_activity: %v", err)
	}
	if listeners == 0 {
		t.Error("no connection listens on thumbnail_due; a new document waits for the poll")
	}
	rows, err := db.QueryContext(t.Context(), `SELECT subject FROM thumbnail_sweeps ORDER BY subject`)
	if err != nil {
		t.Fatalf("reading thumbnail_sweeps: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var swept []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		swept = append(swept, s)
	}
	if len(swept) != 3 {
		t.Errorf("tables swept under the current rule: %v; want portal_assets, portal_collections and resources", swept)
	}
}
