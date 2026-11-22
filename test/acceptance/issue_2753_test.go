//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2753, the v1.166.4 hardening batch:
//
//   - #2750: a password a DO block gives a role through SQL it passes as a
//     string (EXECUTE '...', EXECUTE format(... %L ...), a concatenation,
//     dblink_exec) is refused by password_literal and reaches no sink; a body
//     that builds a role statement at run time is refused by role_named.
//   - #2751: renaming a site environment moves its tile and removing one
//     deletes it, so the tile's object is reclaimed.
//   - #2752: a collection item its owner cannot open, held from before the add
//     rule, is removed by the sweep; what the owner can open stays in order.
//
// Wire forms: manage_schema takes command, connection, schema, name and up as
// strings and version as an integer; the SQL is one string, sent once per body
// form as a literal tools/call param. manage_site set_environment takes site,
// environment and rename_to, and remove_environment site and environment, each
// as a literal string param. manage_asset create_collection takes name as a
// string and sections as an array of objects; get_collection takes
// collection_id as a string. GET /api/v1/portal/collections/{id} and GET
// /api/v1/portal/assets/{id} have no body; POST
// /api/v1/portal/collections/{id}/shares takes shared_with_email and
// permission as strings and notify as a boolean.

// forms2750 are the ways a body passes a role's password as SQL in a string,
// each with the object the refusal names.
func forms2750(k *canary2363) []struct{ label, up, object string } {
	return []struct{ label, up, object string }{
		{"execute literal", "DO $$\nBEGIN\n  EXECUTE 'ALTER USER acc2753_nobody WITH PASSWORD ''" + k.plant("execute-literal") + "''';\nEND\n$$", "ALTER USER"},
		{"format", "DO $$ BEGIN EXECUTE format('ALTER ROLE %I PASSWORD %L', 'acc2753_nobody', '" + k.plant("format") + "'); END $$", "SQL built at run time"},
		{"concatenation", "DO $$ BEGIN EXECUTE 'ALTER ROLE acc2753_nobody PASSWORD ' || quote_literal('" + k.plant("concatenation") + "'); END $$", "SQL built at run time"},
		{"dblink_exec", "DO $$ BEGIN PERFORM dblink_exec('dbname=x', 'ALTER ROLE acc2753_nobody PASSWORD ''" + k.plant("dblink") + "'''); END $$", "ALTER ROLE"},
	}
}

// TestIssue2753_APasswordInDynamicSQLIsRefusedAndReachesNoSink is #2750: each
// body form is refused by the migration review's password_literal rule naming
// the statement, nothing is
// saved, and no form of any of the passwords (as written, base64, hex, URL-
// and JSON-escaped) is found in the platform database (the stored migrations,
// audit_logs.parameters and the call catalog among its tables), the replicas'
// logs and traces, or any response the run read, the refusals included.
func TestIssue2753_APasswordInDynamicSQLIsRefusedAndReachesNoSink(t *testing.T) {
	admin := connectFor(t, 10*time.Minute)
	k := newCanary2363(t)
	schema := schema2404(t, "dyn")
	for i, f := range forms2750(k) {
		ok, text := k.call(admin, "manage_schema", map[string]any{
			"command": "add", "connection": conn2404, "schema": schema, "version": i + 1,
			"name": "acceptance 2753 " + f.label, "up": f.up,
		})
		credential := strings.Contains(text, "writes a credential") || strings.Contains(text, "gives a role a password")
		if ok || !credential || !strings.Contains(text, "statement 1 ("+f.object+")") {
			t.Errorf("%s: want a password_literal refusal naming %q, got ok=%v %s", f.label, f.object, ok, firstRunes(text, 600))
		}
	}
	status := schemaCall(admin, schema, map[string]any{"command": "status"})
	if pending, _ := status["pending"].([]any); len(pending) != 0 {
		t.Errorf("a refused migration was saved: %v", status)
	}
	// The control proves the search reads the audit rows: a migration with no
	// credential is stored and audited as sent, the control in its comment.
	added := schemaCall(admin, schema, map[string]any{
		"command": "add", "version": 9, "name": "acceptance 2753 control",
		"up": "CREATE TABLE acc2753_t (id int);\nCOMMENT ON TABLE acc2753_t IS '" + k.control + "'",
	})
	if added["status"] != "pending" {
		t.Errorf("the control migration did not add: %v", added)
	}
	k.awaitWriters(admin)
	// The audit rows read back through the admin route: the control's row
	// holds it as sent, and the refused calls' rows are searched with it.
	audit := "/api/v1/admin/audit/events?per_page=200&tool_name=manage_schema&session_id=" + admin.sessionID
	for attempt := 0; attempt < 40; attempt++ {
		if _, raw := k.rest(admin, http.MethodGet, audit, nil); strings.Contains(string(raw), k.control) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	k.searchDatabase()
	k.searchFiles()
	k.searchResponses()
	if !k.found["response"] {
		t.Error("the control value was not found in any response; the search would miss a leak there")
	}
}

// TestIssue2753_ARoleStatementBuiltAtRunTimeIsRefused is #2750's role-naming
// half: a grant in an EXECUTE literal is refused naming its roles, and a
// grant built at run time, which cannot be read, is refused as one.
func TestIssue2753_ARoleStatementBuiltAtRunTimeIsRefused(t *testing.T) {
	admin := connect(t)
	schema := schema2404(t, "role")
	for _, c := range []struct{ label, up, want string }{
		{"literal", "DO $$ BEGIN EXECUTE 'GRANT SELECT ON acc2753_t TO acc2753_other'; END $$", "(acc2753_other) names role acc2753_other"},
		{"built", "DO $$ BEGIN EXECUTE format('GRANT SELECT ON %I TO %I', 'acc2753_t', 'acc2753_other'); END $$", "(SQL built at run time) builds a statement at run time that names a role"},
	} {
		text := schemaRefused(t, admin, schema, map[string]any{"command": "add", "version": 1, "name": "acceptance 2753 " + c.label, "up": c.up})
		if !strings.Contains(text, c.want) {
			t.Errorf("%s: want the role_named refusal %q: %s", c.label, c.want, firstRunes(text, 600))
		}
	}
}

// siteTiles2751 is each environment's tile_key for the site named.
func siteTiles2751(t *testing.T, db *sql.DB, site string) map[string]string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT t.environment, t.tile_key FROM site_tiles t JOIN sites s ON s.id = t.site_id WHERE s.name = $1`, site)
	if err != nil {
		t.Fatalf("reading site_tiles: %v", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var env, key string
		if err := rows.Scan(&env, &key); err != nil {
			t.Fatal(err)
		}
		out[env] = key
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestIssue2753_AnEnvironmentsTileFollowsItsRenameAndRemoval is #2751 through
// manage_site: stage's tile row moves to qa when stage is renamed, and is
// deleted when qa is removed; dev's tile is untouched.
func TestIssue2753_AnEnvironmentsTileFollowsItsRenameAndRemoval(t *testing.T) {
	db := devDB(t)
	s := newSite2604(t, keyOwner2604, "acc2753-")
	s.setEnv(t, s.key, "stage", map[string]any{"after": "dev"})
	key := "sites/acc2753/" + s.name + "/stage.webp"
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO site_tiles (site_id, environment, tile_key, renderer, drawn_release)
		SELECT id, 'stage', $2, 1, 0 FROM sites WHERE name = $1
		ON CONFLICT (site_id, environment) DO UPDATE SET tile_key = EXCLUDED.tile_key`, s.name, key); err != nil {
		t.Fatalf("recording stage's tile: %v", err)
	}
	before := siteTiles2751(t, db, s.name)
	if before["stage"] != key {
		t.Fatalf("stage has no tile to follow: %v", before)
	}

	agent := connectAs(t, s.key)
	agent.call("manage_site", map[string]any{"command": "set_environment", "site": s.name, "environment": "stage", "rename_to": "qa"})
	after := siteTiles2751(t, db, s.name)
	if after["qa"] != key || after["stage"] != "" {
		t.Errorf("the tile did not move with the rename: %v", after)
	}

	agent.call("manage_site", map[string]any{"command": "remove_environment", "site": s.name, "environment": "qa"})
	removed := siteTiles2751(t, db, s.name)
	if _, ok := removed["qa"]; ok {
		t.Errorf("a removed environment kept its tile row: %v", removed)
	}
	if removed["dev"] != before["dev"] {
		t.Errorf("dev's tile changed: %v, was %v", removed, before)
	}
	var referenced int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM site_tiles WHERE tile_key = $1`, key).Scan(&referenced); err != nil {
		t.Fatal(err)
	}
	if referenced != 0 {
		t.Errorf("the removed environment's tile object is still referenced %d time(s)", referenced)
	}
}

// insertItem2752 puts assetID at the end of the collection's first section
// directly in the database, as a deployment before the add rule holds it.
func insertItem2752(t *testing.T, db *sql.DB, collectionID, assetID string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO portal_collection_items (id, section_id, asset_id, position)
		SELECT 'acc2753-' || md5(random()::text), cs.id, $2, 99
		  FROM portal_collection_sections cs WHERE cs.collection_id = $1
		 ORDER BY cs.position LIMIT 1`, collectionID, assetID); err != nil {
		t.Fatalf("inserting a pre-fix collection item: %v", err)
	}
}

// awaitRemoval2752 waits for the sweep to record the pair's removal, polling
// to a bound: the dev stack runs the retention sweeps every 30 seconds.
func awaitRemoval2752(t *testing.T, db *sql.DB, collectionID, assetID string) {
	t.Helper()
	deadline := time.Now().Add(150 * time.Second)
	for {
		var n int
		if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM portal_collection_item_removals WHERE collection_id = $1 AND asset_id = $2`,
			collectionID, assetID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the sweep did not remove %s from %s within 150s", assetID, collectionID)
		}
		time.Sleep(2 * time.Second)
	}
}

// collectionAssets2752 is the asset ids of the collection in order, and the
// raw response, as the caller reads it through the portal and manage_asset.
func collectionAssets2752(t *testing.T, c *client, collectionID string) (portal []string, raw string) {
	t.Helper()
	status, got := c.rest(http.MethodGet, "/api/v1/portal/collections/"+collectionID, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the collection: %d %v", status, got)
	}
	sections, _ := got["sections"].([]any)
	for _, s := range sections {
		m, _ := s.(map[string]any)
		items, _ := m["items"].([]any)
		for _, it := range items {
			im, _ := it.(map[string]any)
			portal = append(portal, fmt.Sprint(im["asset_id"]))
		}
	}
	tool := c.call("manage_asset", map[string]any{"action": "get_collection", "collection_id": collectionID})
	return portal, asJSON(got) + asJSON(tool)
}

// TestIssue2753_TheSweepRemovesAnItemItsOwnerCannotOpen is #2752: an admin's
// private asset put straight into the owner's collection, which is shared with
// the peer, is removed by the sweep; neither the owner nor the peer then finds
// its name or id in the collection through the portal or manage_asset, and the
// peer cannot open it; the owner's own items stay in order; a later pass
// removes nothing more from the collection.
func TestIssue2753_TheSweepRemovesAnItemItsOwnerCannotOpen(t *testing.T) {
	db := devDB(t)
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	private, privateName := saveReport(t, admin, "2753 private")
	first, _ := saveReport(t, owner, "2753 first")
	second, _ := saveReport(t, owner, "2753 second")

	created := owner.call("manage_asset", map[string]any{
		"action": "create_collection", "name": "acc-2753 " + unique1579(),
		"sections": []any{map[string]any{"title": "Mine", "items": []any{map[string]any{"asset_id": first}, map[string]any{"asset_id": second}}}},
	})
	collID := fmt.Sprint(created["collection_id"])
	t.Cleanup(func() {
		_, _, _ = owner.callRaw("manage_asset", map[string]any{"action": "delete_collection", "collection_id": collID})
	})
	if status, out := owner.rest(http.MethodPost, "/api/v1/portal/collections/"+collID+"/shares", jsonBody(t, map[string]any{
		"shared_with_email": "asset.peer@example.com", "permission": "viewer", "notify": false,
	})); status != http.StatusCreated {
		t.Fatalf("sharing the collection with the peer: %d %v", status, out)
	}

	insertItem2752(t, db, collID, private)
	awaitRemoval2752(t, db, collID, private)

	for who, c := range map[string]*client{"owner": owner, "peer": peer} {
		ids, raw := collectionAssets2752(t, c, collID)
		if strings.Join(ids, ",") != first+","+second {
			t.Errorf("%s reads the collection as %v, want the owner's two items in order", who, ids)
		}
		if strings.Contains(raw, private) || strings.Contains(raw, privateName) {
			t.Errorf("%s still finds the removed asset in the collection: %.400s", who, raw)
		}
	}
	if status, _ := peer.rest(http.MethodGet, "/api/v1/portal/assets/"+private, http.NoBody); status == http.StatusOK {
		t.Error("the peer still opens the admin's private asset")
	}
	if status, _ := admin.rest(http.MethodGet, "/api/v1/portal/assets/"+private, http.NoBody); status != http.StatusOK {
		t.Errorf("the asset itself was touched: the admin reads %d", status)
	}

	// A later pass: a second pre-fix item is removed, which shows the sweep
	// ran again, and the first removal is recorded once.
	other, _ := saveReport(t, admin, "2753 later")
	insertItem2752(t, db, collID, other)
	awaitRemoval2752(t, db, collID, other)
	var removals int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM portal_collection_item_removals WHERE collection_id = $1 AND asset_id = $2`,
		collID, private).Scan(&removals); err != nil {
		t.Fatal(err)
	}
	if removals != 1 {
		t.Errorf("the first removal is recorded %d times after another pass", removals)
	}
	if ids, _ := collectionAssets2752(t, owner, collID); strings.Join(ids, ",") != first+","+second {
		t.Errorf("a later pass changed the owner's items: %v", ids)
	}
}
