//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2536: pg_stat_statements keeps the text of an entry's first
// execution and gives a statement the same query id with or without a
// comment, so a platform statement first run by a release before #2528 keeps
// its untagged text while every tagged run is added to it. On a database
// where the toolkit's per-call claims statement and the health report's
// extensions read were first run untagged, the health route, called twice,
// ranks neither, and ranks the application's statement.
//
// Wire forms: the health route takes the connection in its path and no body.
// No parameter admits a second form.

// The two platform statements as a release before #2528 sent them: the
// toolkit's claims statement and the health report's extensions read,
// without the platform's comment.
const (
	untaggedClaims2536     = "SELECT set_config('mcp.user_id', $1, true), set_config('mcp.email', $2, true), set_config('mcp.persona', $3, true), set_config('mcp.source', $4, true)"
	untaggedExtensions2536 = "SELECT extname AS name, extversion AS version FROM pg_extension ORDER BY extname"
)

func TestIssue2536_StatementsRecordedBeforeTheTagAreLeftOut(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	d := newDB2528(t, admin)
	app := d.open(t, d.role, d.password)
	// This database's entries only; the connection save's probe ran tagged
	// statements already.
	if _, err := d.open(t, "orders_app", "orders_secret").Exec(
		`SELECT pg_stat_statements_reset(0, (SELECT oid FROM pg_database WHERE datname = current_database()), 0)`); err != nil {
		t.Fatalf("resetting the database's statement statistics: %v", err)
	}
	if _, err := app.Exec(untaggedClaims2536, "u", "e", "p", "s"); err != nil {
		t.Fatalf("running the untagged claims statement: %v", err)
	}
	if _, err := app.Exec(untaggedExtensions2536); err != nil {
		t.Fatalf("running the untagged extensions read: %v", err)
	}
	if _, err := app.Exec("SELECT count(*) FROM items WHERE sku = 'acc-2536-app'"); err != nil {
		t.Fatal(err)
	}

	health(t, admin, d.conn)
	report := health(t, admin, d.conn)

	// The premise: both entries kept their untagged text and the tagged runs
	// were added to them.
	rows, err := app.Query(`SELECT query, calls FROM pg_stat_statements
		WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
		AND (query LIKE '%set_config%' OR query LIKE '%FROM pg_extension%') AND query NOT LIKE '%EXPLAIN%'`)
	if err != nil {
		t.Fatalf("reading pg_stat_statements: %v", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort close after read
	kept := 0
	for rows.Next() {
		var query string
		var calls int64
		if err := rows.Scan(&query, &calls); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(query, "mcp-data-platform") {
			t.Errorf("an entry carries the tag; it was not first recorded untagged: %q", query)
			continue
		}
		kept++
		if calls < 2 {
			t.Errorf("the platform's tagged runs were not added to the untagged entry (%d calls): %q", calls, query)
		}
	}
	if kept != 2 {
		t.Fatalf("pg_stat_statements holds %d untagged platform entries, want 2", kept)
	}

	statements, _ := report["statements"].(map[string]any)
	sawApp := false
	for _, s := range asList(statements["top"]) {
		q := fmt.Sprint(s["query"])
		if strings.Contains(q, "set_config") || strings.Contains(q, "FROM pg_extension") {
			t.Errorf("the ranking lists a platform statement recorded before the tag: %q", q)
		}
		sawApp = sawApp || strings.Contains(q, "FROM items WHERE sku")
	}
	if !sawApp {
		t.Errorf("the application's statement is not ranked: %v", statements["top"])
	}
}
