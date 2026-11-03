//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

// admin is the context of an administrator's call.
func admin() context.Context {
	return mcpcontext.WithCaller(context.Background(), mcpcontext.Caller{UserID: "u1", Email: "admin@example.com", IsAdmin: true})
}

func newSchemaTarget(t *testing.T, extra map[string]any) (*Toolkit, *memStore) {
	t.Helper()
	platform, _, target := realTarget(t)
	for k, v := range extra {
		target[k] = v
	}
	tk := newRealToolkit(t, platform, map[string]map[string]any{"target": target})
	store := &memStore{}
	tk.SetMigrationStore(store)
	return tk, store
}

// schema runs one manage_schema call and decodes its answer, failing the
// test on an error result unless wantErr.
func schema(t *testing.T, ctx context.Context, tk *Toolkit, in schemaInput, wantErr bool) map[string]any {
	t.Helper()
	if in.Connection == "" {
		in.Connection = "target"
	}
	res, _, err := tk.handleSchema(ctx, &mcp.CallToolRequest{}, in)
	if err != nil {
		t.Fatal(err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError != wantErr {
		t.Fatalf("%s: isError = %v, want %v: %s", in.Command, res.IsError, wantErr, text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decoding %s: %v", text, err)
	}
	return out
}

var threeMigrations = []schemaInput{
	{Command: schemaAdd, Version: 1, Name: "orders", Up: "CREATE TABLE orders (id int PRIMARY KEY, total numeric(10,2) NOT NULL)", Down: "DROP TABLE orders"},
	{Command: schemaAdd, Version: 2, Name: "items", Up: "CREATE TABLE items (order_id int REFERENCES orders(id), sku text NOT NULL, qty int NOT NULL CHECK (qty > 0));\nCOMMENT ON TABLE items IS 'order lines'", Down: "DROP TABLE items"},
	{Command: schemaAdd, Version: 3, Name: "index", Up: "CREATE INDEX items_sku ON items (sku)", Down: "DROP INDEX items_sku"},
}

func TestMigrationsApply_RealDB(t *testing.T) {
	tk, store := newSchemaTarget(t, nil)
	ctx := admin()
	for _, m := range threeMigrations {
		schema(t, ctx, tk, m, false)
	}
	status := schema(t, ctx, tk, schemaInput{Command: schemaStatus}, false)
	if n := len(status["pending"].([]any)); n != 3 {
		t.Fatalf("pending = %d, want 3", n)
	}
	out := schema(t, ctx, tk, schemaInput{Command: schemaApply}, false)
	if n := len(out["done"].([]any)); n != 3 {
		t.Fatalf("applied = %d", n)
	}
	status = schema(t, ctx, tk, schemaInput{Command: schemaStatus}, false)
	if len(status["pending"].([]any)) != 0 || len(status["applied"].([]any)) != 3 || status["current_version"] != float64(3) {
		t.Fatalf("status after apply = %v", status)
	}
	if n := count(t, tk, "mcp_schema.migrations"); n != "3" {
		t.Fatalf("ledger rows = %s", n)
	}
	applied := status["applied"].([]any)[0].(map[string]any)
	if applied["applied_by"] != "admin@example.com" {
		t.Fatalf("applied_by = %v", applied["applied_by"])
	}
	if store.eventsOf(EventApplied) != 3 || store.eventsOf(EventAdded) != 3 {
		t.Fatalf("events = %+v", store.events)
	}

	described := schema(t, ctx, tk, schemaInput{Command: schemaDescribe}, false)
	tables := described["tables"].([]any)
	if len(tables) != 2 {
		t.Fatalf("tables = %v", tables)
	}
	items := tables[0].(map[string]any)
	if items["name"] != "items" || items["comment"] != "order lines" || len(items["columns"].([]any)) != 3 ||
		len(items["indexes"].([]any)) != 1 || len(items["constraints"].([]any)) != 2 {
		t.Fatalf("items = %v", items)
	}
	qty := items["columns"].([]any)[2].(map[string]any)
	if qty["name"] != "qty" || qty["type"] != "integer" || qty["nullable"] != false {
		t.Fatalf("qty = %v", qty)
	}

	// An applied migration is immutable.
	edit := threeMigrations[0]
	edit.Up = "CREATE TABLE orders (id bigint PRIMARY KEY)"
	refused := schema(t, ctx, tk, edit, true)
	if !strings.Contains(refused["error"].(string), "immutable") {
		t.Fatalf("editing an applied migration: %v", refused)
	}

	// down reverts the latest.
	out = schema(t, ctx, tk, schemaInput{Command: schemaDown}, false)
	if n := len(out["done"].([]any)); n != 1 {
		t.Fatalf("reverted = %d", n)
	}
	if n := count(t, tk, "mcp_schema.migrations"); n != "2" {
		t.Fatalf("ledger rows after down = %s", n)
	}
	got := queryOK(t, tk, Statement{Connection: "target", SQL: "SELECT count(*) FROM pg_indexes WHERE indexname = 'items_sku'"})
	if b, _ := json.Marshal(got.Rows[0][0]); string(b) != "0" {
		t.Fatal("the reverted index is still there")
	}
}

func TestPlanRollsBack_RealDB(t *testing.T) {
	tk, _ := newSchemaTarget(t, nil)
	ctx := admin()
	schema(t, ctx, tk, threeMigrations[0], false)
	schema(t, ctx, tk, schemaInput{Command: schemaApply}, false)
	schema(t, ctx, tk, schemaInput{Command: schemaAdd, Version: 2, Name: "note", Up: "ALTER TABLE orders ADD COLUMN note text"}, false)
	out := schema(t, ctx, tk, schemaInput{Command: schemaPlan}, false)
	plan := out["plan"].(map[string]any)
	changed, _ := json.Marshal(plan["changed"])
	locks, _ := json.Marshal(plan["locks"])
	if !strings.Contains(string(changed), "public.orders.note") {
		t.Fatalf("changed = %s", changed)
	}
	if !strings.Contains(string(locks), `{"mode":"AccessExclusiveLock","relation":"public.orders"}`) {
		t.Fatalf("locks = %s", locks)
	}
	cols := queryOK(t, tk, Statement{Connection: "target", SQL: "SELECT count(*) FROM information_schema.columns WHERE table_name = 'orders' AND column_name = 'note'"})
	if b, _ := json.Marshal(cols.Rows[0][0]); string(b) != "0" {
		t.Fatal("the planned column is there after the plan")
	}
}

func TestApplyStopsOnAnAlteredLedger_RealDB(t *testing.T) {
	tk, _ := newSchemaTarget(t, nil)
	ctx := admin()
	schema(t, ctx, tk, threeMigrations[0], false)
	schema(t, ctx, tk, schemaInput{Command: schemaApply}, false)
	execOK(t, tk, Statement{Connection: "target", SQL: "UPDATE mcp_schema.migrations SET checksum = 'tampered' WHERE version = 1"})
	schema(t, ctx, tk, threeMigrations[1], false)
	out := schema(t, ctx, tk, schemaInput{Command: schemaApply}, true)
	if msg := out["error"].(string); !strings.Contains(msg, "version 1") || !strings.Contains(msg, "checksum") {
		t.Fatalf("apply over an altered ledger: %s", msg)
	}
	exists := queryOK(t, tk, Statement{Connection: "target", SQL: "SELECT to_regclass('public.items') IS NULL"})
	if exists.Rows[0][0] != true {
		t.Fatal("a migration ran after drift was found")
	}
}

func TestConcurrentAppliesApplyEachVersionOnce_RealDB(t *testing.T) {
	tk, _ := newSchemaTarget(t, nil)
	ctx := admin()
	for _, m := range threeMigrations {
		schema(t, ctx, tk, m, false)
	}
	var wg sync.WaitGroup
	results := make([]*mcp.CallToolResult, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], _, _ = tk.handleSchema(ctx, &mcp.CallToolRequest{}, schemaInput{Command: schemaApply, Connection: "target", Schema: DefaultSchema})
		}()
	}
	wg.Wait()
	total := 0
	for _, r := range results {
		if r.IsError {
			t.Fatalf("an apply failed: %v", r.Content[0].(*mcp.TextContent).Text)
		}
		var out map[string]any
		_ = json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &out)
		total += len(out["done"].([]any))
	}
	if total != 3 {
		t.Fatalf("the two applies applied %d migrations between them, want 3", total)
	}
	got := queryOK(t, tk, Statement{Connection: "target", SQL: "SELECT count(*), count(DISTINCT version) FROM mcp_schema.migrations"})
	if b, _ := json.Marshal(got.Rows[0]); string(b) != "[3,3]" {
		t.Fatalf("ledger = %s", b)
	}
}

func TestFailedMigrationLeavesNothing_RealDB(t *testing.T) {
	tk, store := newSchemaTarget(t, nil)
	ctx := admin()
	schema(t, ctx, tk, schemaInput{Command: schemaAdd, Version: 1, Name: "half", Up: "CREATE TABLE a (id int);\nCREATE TABLE a (id int)"}, false)
	out := schema(t, ctx, tk, schemaInput{Command: schemaApply}, true)
	if msg := out["error"].(string); !strings.Contains(msg, "migration 1") || !strings.Contains(msg, "42P07") {
		t.Fatalf("a failing migration: %s", msg)
	}
	exists := queryOK(t, tk, Statement{Connection: "target", SQL: "SELECT to_regclass('public.a') IS NULL"})
	if exists.Rows[0][0] != true {
		t.Fatal("the first statement of the failed migration stayed")
	}
	if n := count(t, tk, "mcp_schema.migrations"); n != "0" {
		t.Fatalf("ledger rows = %s", n)
	}
	if store.eventsOf(EventFailed) != 1 {
		t.Fatalf("events = %+v", store.events)
	}
}

func TestNonTransactionalMigrationRunsAlone_RealDB(t *testing.T) {
	tk, _ := newSchemaTarget(t, nil)
	ctx := admin()
	schema(t, ctx, tk, threeMigrations[0], false)
	no := false
	schema(t, ctx, tk, schemaInput{Command: schemaAdd, Version: 2, Name: "concurrent index", Up: "CREATE INDEX CONCURRENTLY orders_total ON orders (total)", Transactional: &no}, false)
	plan := schema(t, ctx, tk, schemaInput{Command: schemaPlan}, false)["plan"].(map[string]any)
	if u, _ := json.Marshal(plan["unplanned"]); string(u) != "[2]" {
		t.Fatalf("unplanned = %s", u)
	}
	schema(t, ctx, tk, schemaInput{Command: schemaApply}, false)
	got := queryOK(t, tk, Statement{Connection: "target", SQL: "SELECT count(*) FROM pg_indexes WHERE indexname = 'orders_total'"})
	if b, _ := json.Marshal(got.Rows[0][0]); string(b) != "1" {
		t.Fatal("the concurrent index was not built")
	}
}

func TestApplyNeedsTheGrant_RealDB(t *testing.T) {
	tk, _ := newSchemaTarget(t, map[string]any{cfgKeyApplyPersonas: []any{"builder"}})
	analyst := mcpcontext.WithPersona(mcpcontext.WithCaller(context.Background(), mcpcontext.Caller{UserID: "u2"}), "analyst")
	schema(t, analyst, tk, threeMigrations[0], false)
	schema(t, analyst, tk, schemaInput{Command: schemaPlan}, false)
	out := schema(t, analyst, tk, schemaInput{Command: schemaApply}, true)
	if !strings.Contains(out["error"].(string), "schema_apply_personas") {
		t.Fatalf("apply without the grant: %v", out)
	}
	builder := mcpcontext.WithPersona(context.Background(), "builder")
	schema(t, builder, tk, schemaInput{Command: schemaApply}, false)
}

func TestStatementCacheFollowsAnApply_RealDB(t *testing.T) {
	tk, _ := newSchemaTarget(t, map[string]any{cfgKeyMaxConns: 1})
	ctx := admin()
	schema(t, ctx, tk, threeMigrations[0], false)
	schema(t, ctx, tk, schemaInput{Command: schemaApply}, false)
	q := Statement{Connection: "target", SQL: "SELECT * FROM orders"}
	if n := len(queryOK(t, tk, q).Columns); n != 2 {
		t.Fatalf("columns = %d", n)
	}
	schema(t, ctx, tk, schemaInput{Command: schemaAdd, Version: 2, Name: "note", Up: "ALTER TABLE orders ADD COLUMN note text"}, false)
	schema(t, ctx, tk, schemaInput{Command: schemaApply}, false)
	if n := len(queryOK(t, tk, q).Columns); n != 3 {
		t.Fatalf("columns after the apply = %d, want 3 on the first call", n)
	}
}
