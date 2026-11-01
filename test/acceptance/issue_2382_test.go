//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/lib/pq" // the dev stack's database, read for the gate's record
)

// Issue #2382: the search-first gate remembers a search for the life of the
// platform session, not the 30-minute transport idle window. A session that
// searched, paused past that window and queries again under the same handle
// is not refused SEARCH_REQUIRED.
//
// Wire forms: search's intent and purpose and trino_query's sql are strings,
// sent as JSON strings; limit is sent as a JSON number.

func TestIssue2382_SearchOutlivesTheIdleWindow(t *testing.T) {
	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	start := time.Now().Add(-time.Second)
	c := connect(t) // calls platform_info, then search

	// The search's record, keyed on the caller (a schema-read record carries
	// a unit separator and is left out).
	var key string
	var lifetime time.Duration
	var seconds float64
	err = db.QueryRowContext(ctx, `
		SELECT session_id, EXTRACT(EPOCH FROM (expires_at - discovered_at))
		FROM search_gate_discovery
		WHERE discovered_at >= $1 AND session_id LIKE 'user:%' AND strpos(session_id, chr(31)) = 0
		ORDER BY discovered_at DESC LIMIT 1`, start).Scan(&key, &seconds)
	if err != nil {
		t.Fatalf("no search record written by the search: %v", err)
	}
	lifetime = time.Duration(seconds * float64(time.Second))
	if lifetime <= 30*time.Minute {
		t.Fatalf("the search is remembered for %s, no longer than the 30-minute idle window", lifetime)
	}

	// Thirty-one minutes of idleness, applied to the record rather than waited
	// for: the record is what the gate reads.
	if _, err := db.ExecContext(ctx, `
		UPDATE search_gate_discovery
		SET discovered_at = discovered_at - interval '31 minutes', expires_at = expires_at - interval '31 minutes'
		WHERE session_id = $1`, key); err != nil {
		t.Fatal(err)
	}
	res, text, err := c.callRaw("trino_query", map[string]any{"connection": conn1918, "sql": "SELECT 1", "purpose": "Acceptance: checking a search outlives the idle window."})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("a query 31 minutes after the search, under the same session, was refused: %s", text)
	}
}
