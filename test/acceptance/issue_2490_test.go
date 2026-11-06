//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// Acceptance for #2490: a migration added with transactional=false is
// reverted the way it is applied. Its down (DROP INDEX CONCURRENTLY, which
// PostgreSQL refuses in a transaction block with SQLSTATE 25001) runs alone,
// the index is gone, its ledger row is deleted, and the migration below it
// reverts as before; a transactional migration whose down runs CONCURRENTLY
// is refused at add, naming transactional=false.
//
// Wire forms: manage_schema takes connection, schema, command, name, up and
// down as strings, version as a JSON number and transactional as a JSON
// boolean.

func TestIssue2490_ANonTransactionalMigrationIsRevertedAlone(t *testing.T) {
	admin := connect(t)
	schema := "acc2490_" + unique1579()
	schemaCall(admin, schema, map[string]any{
		"command": "add", "version": 1, "name": "orders",
		"up": "CREATE TABLE orders (id bigint PRIMARY KEY, status text NOT NULL)", "down": "DROP TABLE orders",
	})
	schemaCall(admin, schema, map[string]any{
		"command": "add", "version": 2, "name": "status index", "transactional": false,
		"up":   "CREATE INDEX CONCURRENTLY IF NOT EXISTS orders_status_idx ON orders (status)",
		"down": "DROP INDEX CONCURRENTLY IF EXISTS " + schema + ".orders_status_idx",
	})
	if applied := schemaCall(admin, schema, map[string]any{"command": "apply"}); len(applied["done"].([]any)) != 2 {
		t.Fatalf("apply: %v", applied)
	}
	if n := sqlCount(t, admin, "SELECT count(*) AS n FROM pg_indexes WHERE schemaname = '"+schema+"' AND indexname = 'orders_status_idx'"); n != "1" {
		t.Fatalf("the concurrent index is not there after apply: %s", n)
	}

	down := schemaCall(admin, schema, map[string]any{"command": "down"})
	if done, _ := down["done"].([]any); len(done) != 1 {
		t.Fatalf("down of the non-transactional migration: %v", down)
	}
	if n := sqlCount(t, admin, "SELECT count(*) AS n FROM pg_indexes WHERE schemaname = '"+schema+"' AND indexname = 'orders_status_idx'"); n != "0" {
		t.Errorf("the index is still there after down: %s", n)
	}
	if status := schemaCall(admin, schema, map[string]any{"command": "status"}); status["current_version"] != float64(1) {
		t.Errorf("the ledger after down: %v", status)
	}
	schemaCall(admin, schema, map[string]any{"command": "down"})
	if status := schemaCall(admin, schema, map[string]any{"command": "status"}); status["current_version"] != float64(0) {
		t.Errorf("the migration below it did not revert: %v", status)
	}
}

func TestIssue2490_ATransactionalDownThatRunsConcurrentlyIsRefusedAtAdd(t *testing.T) {
	admin := connect(t)
	schema := "acc2490_" + unique1579()
	text := schemaRefused(t, admin, schema, map[string]any{
		"command": "add", "version": 1, "name": "bad down",
		"up": "CREATE TABLE things (id int)", "down": "DROP INDEX CONCURRENTLY IF EXISTS things_idx",
	})
	if !strings.Contains(text, "transactional=false") {
		t.Fatalf("the refusal does not name transactional=false: %s", text)
	}
}
