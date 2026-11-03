//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// Acceptance for #2404: an application database's schema is built from
// versioned migrations manage_schema keeps, plans, applies and reports. Each
// criterion works in a schema of its own on the dev stack's `orders`
// application database (postgres-target), so a run leaves the fixture tables
// alone and a second run starts clean.
//
// Wire forms: manage_schema takes command, connection, schema, name, up and
// down as strings, version and to_version as integers and transactional as a
// boolean; every call below sends them as those JSON types, and no parameter
// of the tool admits a second form. manage_script takes command, name and
// source as strings and allow_writes as a boolean; search takes intent and
// purpose as strings; the admin migrations routes take a path and a schema
// query parameter.

const (
	conn2404 = "orders"
	// owner2404 is a collaborator: every tool, and not the admin persona.
	owner2404 = "acme-owner-key"
)

// schema2404 is a fresh schema name for one criterion.
func schema2404(prefix string) string { return "acc2404_" + prefix + "_" + unique1579() }

// threeMigrations2404 are the migrations most criteria apply.
func threeMigrations2404() []map[string]any {
	return []map[string]any{
		{"version": 1, "name": "orders", "up": "CREATE TABLE shop_orders (id bigint PRIMARY KEY, total numeric(10,2) NOT NULL)", "down": "DROP TABLE shop_orders"},
		{"version": 2, "name": "order items", "up": "CREATE TABLE shop_items (order_id bigint NOT NULL REFERENCES shop_orders (id), sku text NOT NULL, qty int NOT NULL CHECK (qty > 0));\nCOMMENT ON TABLE shop_items IS 'one line of an order'", "down": "DROP TABLE shop_items"},
		{"version": 3, "name": "sku index", "up": "CREATE INDEX shop_items_sku ON shop_items (sku)", "down": "DROP INDEX shop_items_sku"},
	}
}

// schemaCall is one manage_schema call.
func schemaCall(c *client, schema string, args map[string]any) map[string]any {
	params := map[string]any{"connection": conn2404, "schema": schema}
	for k, v := range args {
		params[k] = v
	}
	return c.call("manage_schema", params)
}

// schemaRefused is a manage_schema call expected to fail, returning its text.
func schemaRefused(t *testing.T, c *client, schema string, args map[string]any) string {
	t.Helper()
	params := map[string]any{"connection": conn2404, "schema": schema}
	for k, v := range args {
		params[k] = v
	}
	res, text, err := c.callRaw("manage_schema", params)
	if err != nil {
		t.Fatalf("manage_schema %v: %v", args["command"], err)
	}
	if !res.IsError {
		t.Fatalf("manage_schema %v succeeded and was expected to fail: %s", args["command"], text)
	}
	return text
}

// addAll adds migrations to schema.
func addAll(c *client, schema string, migrations []map[string]any) {
	for _, m := range migrations {
		args := map[string]any{"command": "add"}
		for k, v := range m {
			args[k] = v
		}
		schemaCall(c, schema, args)
	}
}

// sqlCount counts rows on the orders database with a draft's query.
func sqlCount(t *testing.T, c *client, sql string) string {
	t.Helper()
	out := draft2428(c, "acceptance-2404-count", `
def main():
    """Counts."""
    print(platform.query("`+sql+`", connection="`+conn2404+`")["rows"][0]["n"])
`, false)
	if out["status"] != "succeeded" {
		t.Fatalf("counting with %q failed: %v", sql, out)
	}
	log, _ := out["log"].(string)
	return strings.TrimSpace(log)
}

func TestIssue2404_ThreeMigrationsAreAppliedAndRecorded(t *testing.T) {
	admin := connect(t)
	schema := schema2404("apply")
	addAll(admin, schema, threeMigrations2404())
	before := schemaCall(admin, schema, map[string]any{"command": "status"})
	if pending, _ := before["pending"].([]any); len(pending) != 3 {
		t.Fatalf("status before apply: %v", before)
	}
	applied := schemaCall(admin, schema, map[string]any{"command": "apply"})
	if done, _ := applied["done"].([]any); len(done) != 3 {
		t.Fatalf("apply: %v", applied)
	}
	status := schemaCall(admin, schema, map[string]any{"command": "status"})
	if pending, _ := status["pending"].([]any); len(pending) != 0 {
		t.Errorf("status after apply still lists pending migrations: %v", status)
	}
	if status["current_version"] != float64(3) {
		t.Errorf("current_version = %v, want 3", status["current_version"])
	}
	if n := sqlCount(t, admin, "SELECT count(*) AS n FROM mcp_schema.migrations WHERE schema_name = '"+schema+"'"); n != "3" {
		t.Errorf("the ledger holds %s rows for the schema, want 3", n)
	}
	if n := sqlCount(t, admin, "SELECT count(*) AS n FROM information_schema.tables WHERE table_schema = '"+schema+"'"); n != "2" {
		t.Errorf("the schema holds %s tables, want 2", n)
	}
	history := schemaCall(admin, schema, map[string]any{"command": "history"})
	raw, _ := json.Marshal(history["events"])
	if strings.Count(string(raw), `"event":"applied"`) != 3 {
		t.Errorf("history does not record three applies: %s", raw)
	}
}

func TestIssue2404_PlanListsTheTableAndItsLockAndRollsBack(t *testing.T) {
	admin := connect(t)
	schema := schema2404("plan")
	addAll(admin, schema, threeMigrations2404()[:1])
	schemaCall(admin, schema, map[string]any{"command": "apply"})
	schemaCall(admin, schema, map[string]any{"command": "add", "version": 2, "name": "order note", "up": "ALTER TABLE shop_orders ADD COLUMN note text"})
	out := schemaCall(admin, schema, map[string]any{"command": "plan", "to_version": 2})
	plan, _ := out["plan"].(map[string]any)
	changed, _ := json.Marshal(plan["changed"])
	locks, _ := json.Marshal(plan["locks"])
	if !strings.Contains(string(changed), schema+".shop_orders.note") {
		t.Errorf("plan does not list the new column: %s", changed)
	}
	if !strings.Contains(string(locks), `{"mode":"AccessExclusiveLock","relation":"`+schema+`.shop_orders"}`) {
		t.Errorf("plan does not list the table's lock: %s", locks)
	}
	if n := sqlCount(t, admin, "SELECT count(*) AS n FROM information_schema.columns WHERE table_schema = '"+schema+"' AND table_name = 'shop_orders' AND column_name = 'note'"); n != "0" {
		t.Errorf("the planned column is there after the plan")
	}

	// The portal's connection page reads the same plan, and the migrations
	// with their state.
	status, report := admin.rest(http.MethodGet, "/api/v1/admin/connection-instances/appdb/"+conn2404+"/migrations?schema="+schema, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET migrations answered %d: %v", status, report)
	}
	states := []string{}
	for _, m := range report["migrations"].([]any) {
		states = append(states, m.(map[string]any)["state"].(string))
	}
	if strings.Join(states, ",") != "applied,pending" {
		t.Errorf("the page reads states %v, want applied,pending", states)
	}
	status, portalPlan := admin.rest(http.MethodPost, "/api/v1/admin/connection-instances/appdb/"+conn2404+"/migrations/plan?schema="+schema, http.NoBody)
	if status != http.StatusOK || !strings.Contains(asJSON(portalPlan), schema+".shop_orders.note") {
		t.Errorf("POST migrations/plan answered %d: %v", status, portalPlan)
	}
}

func TestIssue2404_AnAppliedMigrationIsImmutableAndAnAlteredLedgerStopsTheApply(t *testing.T) {
	admin := connect(t)
	schema := schema2404("immutable")
	migrations := threeMigrations2404()
	addAll(admin, schema, migrations[:1])
	schemaCall(admin, schema, map[string]any{"command": "apply"})

	edited := map[string]any{"command": "add", "version": 1, "name": "orders", "up": "CREATE TABLE shop_orders (id bigint PRIMARY KEY)"}
	if text := schemaRefused(t, admin, schema, edited); !strings.Contains(text, "immutable") {
		t.Errorf("editing an applied migration: %s", text)
	}

	tamper := draft2428(admin, "acceptance-2404-tamper", `
def main():
    """Alters the ledger by hand."""
    platform.execute("UPDATE mcp_schema.migrations SET checksum = 'edited by hand' WHERE schema_name = :s AND version = 1", connection="`+conn2404+`", params={"s": "`+schema+`"})
`, true)
	if tamper["status"] != "succeeded" {
		t.Fatalf("altering the ledger failed: %v", tamper)
	}
	addAll(admin, schema, migrations[1:2])
	text := schemaRefused(t, admin, schema, map[string]any{"command": "apply"})
	if !strings.Contains(text, "version 1") || !strings.Contains(text, "checksum") {
		t.Errorf("apply over an altered ledger does not stop naming the version: %s", text)
	}
	if n := sqlCount(t, admin, "SELECT count(*) AS n FROM information_schema.tables WHERE table_schema = '"+schema+"' AND table_name = 'shop_items'"); n != "0" {
		t.Errorf("a migration ran after the drift was found")
	}
}

func TestIssue2404_TwoConcurrentAppliesApplyEachVersionOnce(t *testing.T) {
	admin := connect(t)
	schema := schema2404("concurrent")
	addAll(admin, schema, threeMigrations2404())
	clients := []*client{connect(t), connect(t)}
	results := make([]string, 2)
	failed := make([]bool, 2)
	var wg sync.WaitGroup
	for i, c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, text, err := c.callRaw("manage_schema", map[string]any{"command": "apply", "connection": conn2404, "schema": schema})
			results[i], failed[i] = text, err != nil || res.IsError
		}()
	}
	wg.Wait()
	applied := 0
	for i, text := range results {
		if failed[i] {
			if !strings.Contains(text, "migration lock") {
				t.Errorf("apply %d failed for a reason other than the lock: %s", i, text)
			}
			continue
		}
		var out map[string]any
		_ = json.Unmarshal([]byte(text), &out)
		done, _ := out["done"].([]any)
		applied += len(done)
	}
	if applied != 3 {
		t.Errorf("the two applies applied %d migrations between them, want 3: %v", applied, results)
	}
	if n := sqlCount(t, admin, "SELECT count(*) AS n FROM mcp_schema.migrations WHERE schema_name = '"+schema+"'"); n != "3" {
		t.Errorf("the ledger holds %s rows, want each version once", n)
	}
}

func TestIssue2404_AFailingMigrationLeavesNoChangeAndNoLedgerRow(t *testing.T) {
	admin := connect(t)
	schema := schema2404("fails")
	schemaCall(admin, schema, map[string]any{"command": "add", "version": 1, "name": "half", "up": "CREATE TABLE half_done (id int)"})
	schemaCall(admin, schema, map[string]any{"command": "apply"})
	// A row the live table holds and a new database does not: migration 2
	// applies from zero, which add checks (#2405), and fails on this data.
	seeded := draft2428(admin, "acceptance-2404-seed", `
def main():
    """Writes a row migration 2's check refuses."""
    platform.execute("INSERT INTO `+schema+`.half_done VALUES (1)", connection="`+conn2404+`")
`, true)
	if seeded["status"] != "succeeded" {
		t.Fatalf("seeding the live row failed: %v", seeded)
	}
	schemaCall(admin, schema, map[string]any{
		"command": "add", "version": 2, "name": "check", "transactional": true,
		"up": "INSERT INTO half_done VALUES (2);\nALTER TABLE half_done ADD CONSTRAINT half_done_big CHECK (id > 1)",
	})
	text := schemaRefused(t, admin, schema, map[string]any{"command": "apply"})
	if !strings.Contains(text, "migration 2") || !strings.Contains(text, "23514") {
		t.Errorf("the failing migration's error: %s", text)
	}
	if n := sqlCount(t, admin, "SELECT count(*) AS n FROM "+schema+".half_done"); n != "1" {
		t.Errorf("the failed migration left %s rows, want the one seeded", n)
	}
	if n := sqlCount(t, admin, "SELECT count(*) AS n FROM mcp_schema.migrations WHERE schema_name = '"+schema+"'"); n != "1" {
		t.Errorf("the ledger holds %s rows, want migration 1 alone", n)
	}
	history := schemaCall(admin, schema, map[string]any{"command": "history", "limit": 10})
	if raw, _ := json.Marshal(history["events"]); !strings.Contains(string(raw), `"event":"failed"`) {
		t.Errorf("history does not record the failure: %s", raw)
	}
}

func TestIssue2404_DescribeListsTheSchemaAndSearchDoesNot(t *testing.T) {
	admin := connect(t)
	schema := schema2404("describe")
	addAll(admin, schema, threeMigrations2404())
	schemaCall(admin, schema, map[string]any{"command": "apply"})
	out := schemaCall(admin, schema, map[string]any{"command": "describe"})
	tables, _ := out["tables"].([]any)
	byName := map[string]map[string]any{}
	for _, raw := range tables {
		tb := raw.(map[string]any)
		byName[tb["name"].(string)] = tb
	}
	items := byName["shop_items"]
	if items == nil || byName["shop_orders"] == nil {
		t.Fatalf("describe lists %v", tables)
	}
	cols := []string{}
	for _, raw := range items["columns"].([]any) {
		col := raw.(map[string]any)
		cols = append(cols, col["name"].(string)+" "+col["type"].(string))
	}
	if strings.Join(cols, ", ") != "order_id bigint, sku text, qty integer" {
		t.Errorf("shop_items columns = %v", cols)
	}
	definitions := []string{}
	for _, raw := range items["constraints"].([]any) {
		definitions = append(definitions, raw.(map[string]any)["definition"].(string))
	}
	if items["comment"] != "one line of an order" || !strings.Contains(asJSON(items["indexes"]), "shop_items_sku") ||
		!strings.Contains(strings.Join(definitions, "; "), "CHECK ((qty > 0))") {
		t.Errorf("shop_items = %v", items)
	}

	res, text, err := admin.callRaw("search", map[string]any{"intent": "shop_items shop_orders order lines sku qty", "purpose": "Acceptance #2404: an application's tables appear in no search result."})
	if err != nil || res.IsError {
		t.Fatalf("search: %v %s", err, text)
	}
	if strings.Contains(text, "shop_items") || strings.Contains(text, "shop_orders") || strings.Contains(text, schema) {
		t.Errorf("search returns an application table: %s", text)
	}
}

func TestIssue2404_ApplyNeedsTheGrant(t *testing.T) {
	owner := connectAs(t, owner2404)
	schema := schema2404("grant")
	addAll(owner, schema, threeMigrations2404()[:1])
	plan := schemaCall(owner, schema, map[string]any{"command": "plan"})
	if plan["rolled_back"] != true {
		t.Fatalf("plan as a collaborator: %v", plan)
	}
	text := schemaRefused(t, owner, schema, map[string]any{"command": "apply"})
	if !strings.Contains(text, "schema_apply_personas") {
		t.Errorf("apply as a collaborator: %s", text)
	}
	admin := connect(t)
	if applied := schemaCall(admin, schema, map[string]any{"command": "apply"}); len(applied["done"].([]any)) != 1 {
		t.Errorf("apply as admin: %v", applied)
	}
}

func TestIssue2404_AMigrationCannotEndItsOwnTransaction(t *testing.T) {
	admin := connect(t)
	schema := schema2404("commit")
	text := schemaRefused(t, admin, schema, map[string]any{
		"command": "add", "version": 1, "name": "sneaky",
		"up": "CREATE TABLE kept (id int); COMMIT; CREATE TABLE after_commit (id int)",
	})
	if !strings.Contains(text, "statement 2 is COMMIT") {
		t.Errorf("a migration holding COMMIT: %s", text)
	}
	if status := schemaCall(admin, schema, map[string]any{"command": "status"}); len(status["pending"].([]any)) != 0 {
		t.Errorf("the refused migration was kept: %v", status)
	}
	// A function body's COMMIT is the function's, not the migration's.
	schemaCall(admin, schema, map[string]any{
		"command": "add", "version": 1, "name": "procedure",
		"up": "CREATE PROCEDURE tidy() LANGUAGE plpgsql AS $$ BEGIN COMMIT; END $$",
	})
}

func TestIssue2404_APersonaThatDeniesTheConnectionIsRefused(t *testing.T) {
	ingest := connectAs(t, ingestKey2428)
	res, text, err := ingest.callRaw("manage_schema", map[string]any{"command": "status", "connection": conn2404, "schema": "public"})
	if err != nil {
		t.Fatalf("manage_schema: %v", err)
	}
	if !res.IsError || !strings.Contains(text, "connection not allowed") {
		t.Errorf("manage_schema on a connection the persona denies: %s", text)
	}
}
