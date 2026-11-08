//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/sqltag"
)

// TestPlatformQueryIDs_RealDB is #2536 against PostgreSQL: the toolkit's
// claims statement and ledger probe, first run without the platform's tag the
// way a release before #2528 ran them, keep that untagged text in
// pg_stat_statements while every tagged run is added to the same entry, and
// PlatformQueryIDs names the query id of each entry, so the health report can
// leave them out.
func TestPlatformQueryIDs_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		rt := realTarget(t)
		tk := newRealToolkit(t, rt.platform, map[string]map[string]any{"target": rt.target})
		target, err := sql.Open("postgres", fmt.Sprintf("host=%v port=%v user=%v password=%v dbname=%v sslmode=disable",
			rt.target[cfgKeyHost], rt.target[cfgKeyPort], rt.target[cfgKeyUser], rt.target[cfgKeyPassword], rt.target[cfgKeyDatabase]))
		require.NoError(t, err)
		t.Cleanup(func() { _ = target.Close() })
		_, err = target.Exec(`CREATE EXTENSION pg_stat_statements`)
		require.NoError(t, err)
		// This database's entries only: the servers are shared by the tests
		// of every package running at once.
		_, err = target.Exec(`SELECT pg_stat_statements_reset(0, (SELECT oid FROM pg_database WHERE datname = current_database()), 0)`)
		require.NoError(t, err)

		untagged := func(s string) string { return strings.TrimSpace(strings.TrimPrefix(s, sqltag.Platform)) }
		_, err = target.Exec(untagged(claimsSQL), "u", "e", "p", "s")
		require.NoError(t, err)
		_, err = target.Exec(untagged(ledgerExistsSQL), "platform.migrations")
		require.NoError(t, err)
		// Every call through the toolkit runs the claims statement, tagged.
		queryOK(t, tk, Statement{Connection: "target", SQL: "SELECT 1"})

		ctx := context.Background()
		ids, err := tk.PlatformQueryIDs(ctx, "target", nil)
		require.NoError(t, err)
		require.NotEmpty(t, ids)

		rows, err := target.Query(`SELECT queryid::text, query, calls FROM pg_stat_statements
			WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
			AND (query LIKE '%set_config%' OR query LIKE '%to_regclass%') AND query NOT LIKE '%EXPLAIN%'`)
		require.NoError(t, err)
		defer rows.Close()
		entries := 0
		for rows.Next() {
			var id, query string
			var calls int64
			require.NoError(t, rows.Scan(&id, &query, &calls))
			entries++
			assert.NotContains(t, query, sqltag.Platform, "the entry kept its first, untagged text")
			assert.True(t, slices.Contains(ids, id), "the query id of %q is among the platform's: %v", query, ids)
			if strings.Contains(query, "set_config") {
				assert.GreaterOrEqual(t, calls, int64(2), "the tagged run was added to the untagged entry")
			}
		}
		require.NoError(t, rows.Err())
		assert.Equal(t, 2, entries)

		_, err = tk.PlatformQueryIDs(ctx, "nope", nil)
		require.ErrorIs(t, err, ErrConnectionNotFound)
	})
}
