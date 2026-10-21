//go:build integration

package callrecord

import (
	"context"
	"testing"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// outcomeCatalogSize is #2231's catalog: the admin list took 36 seconds to
// filter about 95,000 records by outcome.
const outcomeCatalogSize = 100_000

// outcomeBound is how long one filtered page and its count may take.
const outcomeBound = time.Second

// outcomeAttempts is how many times each is measured; the fastest counts.
const outcomeAttempts = 3

// seedOutcomeCatalog writes outcomeCatalogSize records: every seventeenth
// failed, pairs of reads over one target in one session (the newer supersedes
// the older), a memory record citing every seven-hundredth, and assets citing
// others.
func seedOutcomeCatalog(ctx context.Context, t *testing.T, store *PostgresStore) {
	t.Helper()
	_, err := store.db.ExecContext(ctx, `
		INSERT INTO call_records (event_id, kind, tool_name, connection, statement, targets,
		                          user_id, session_id, success, created_at)
		SELECT 'evt-' || g, 'sql', 'trino_query', 'wh', 'SELECT ' || (g / 2),
		       jsonb_build_array('trino:wh:t' || (g / 2)), 'u' || (g % 7), 's-' || (g / 20),
		       g % 17 <> 0, now() - make_interval(secs => g)
		  FROM generate_series(1, $1) AS g`, outcomeCatalogSize)
	require.NoError(t, err)
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO memory_records (id, content, metadata)
		SELECT 'mem-' || g, 'cites a call', jsonb_build_object('sources', jsonb_build_array('mcp:call:evt-' || (g * 700)))
		  FROM generate_series(1, $1 / 700) AS g`, outcomeCatalogSize)
	require.NoError(t, err)
	// Assets cite calls four ways: a save that named its call, an export that
	// streamed one, a capture that swept a call in without naming it (cited
	// set, not satisfied: the rule still decides), and a deleted asset.
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO portal_assets (id, owner_id, name, content_type, s3_bucket, s3_key, provenance, deleted_at)
		SELECT 'asset-' || g || '-' || k.n, 'u0', 'cites a call', 'text/html', 'b', 'k' || g || k.n,
		       jsonb_build_object('captures', jsonb_build_array(jsonb_build_object(
		           'tool', k.tool, 'explicit', k.explicit,
		           'event_ids', jsonb_build_array('evt-' || (g * 900 + k.n)),
		           'calls', jsonb_build_array(jsonb_build_object('event_id', 'evt-' || (g * 900 + k.n), 'cited', k.explicit))))),
		       CASE WHEN k.deleted THEN now() END
		  FROM generate_series(1, 60) AS g,
		       (VALUES (1, 'save_asset', true, false), (3, 'trino_export', true, false),
		               (5, 'save_asset', false, false), (7, 'save_asset', true, true)) AS k(n, tool, explicit, deleted)`)
	require.NoError(t, err)
	_, err = store.db.ExecContext(ctx, `ANALYZE call_records; ANALYZE memory_records; ANALYZE portal_assets`)
	require.NoError(t, err)
}

// unguardedOutcomes counts the catalog by outcome with the satisfied rule
// asked of every record, the read before #2231, so the guarded read is held to
// deciding every record the same way.
func unguardedOutcomes(ctx context.Context, t *testing.T, store *PostgresStore) map[string]int {
	t.Helper()
	inner := sq.Select(storedColumns...).
		Column(satisfiedByCase("?")+" AS satisfied_by", callReferencePrefix()).
		Column(supersededExpr).
		Column(reuseCountExpr).
		From("call_records r")
	query, args, err := psq.Select("o.outcome", "COUNT(*)").
		FromSelect(outcomeOver(inner), "o").GroupBy("o.outcome").ToSql()
	require.NoError(t, err)
	rows, err := store.db.QueryContext(ctx, query, args...)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var outcome string
		var n int
		require.NoError(t, rows.Scan(&outcome, &n))
		out[outcome] = n
	}
	require.NoError(t, rows.Err())
	return out
}

// TestCallCatalogRealDBFiltersEachOutcomeWithinASecond is #2231: on a catalog
// of 100,000 records, a page of each outcome and its count answer within a
// second, for the whole catalog (the admin list) and for one caller's records
// (the portal's), and each outcome counts what the rule asked of every record
// counts.
func TestCallCatalogRealDBFiltersEachOutcomeWithinASecond(t *testing.T) {
	ctx := context.Background()
	store := NewPostgresStore(testdb.New(t), Config{})
	seedOutcomeCatalog(ctx, t, store)
	want := unguardedOutcomes(ctx, t, store)
	for _, outcome := range Outcomes {
		require.Positive(t, want[outcome], "the seed holds no %s record", outcome)
	}

	for _, scope := range []struct{ name, userID string }{{"admin", ""}, {"portal", "u0"}} {
		for _, outcome := range Outcomes {
			f := Filter{UserID: scope.userID, Outcome: outcome, Limit: 50}
			// The best of three: this runs beside the rest of the real-database
			// suite, and one sample that lands on another package's burst
			// measures that package rather than this query.
			var (
				page  []Record
				count int
				took  time.Duration
			)
			for attempt := range outcomeAttempts {
				start := time.Now()
				var err error
				page, err = store.List(ctx, f)
				require.NoError(t, err)
				count, err = store.Count(ctx, f)
				require.NoError(t, err)
				if d := time.Since(start); attempt == 0 || d < took {
					took = d
				}
			}
			t.Logf("%s %s: %d records, page and count in %s (best of %d)", scope.name, outcome, count, took, outcomeAttempts)
			assert.Less(t, took, outcomeBound, "%s list filtered to %s", scope.name, outcome)
			require.NotEmpty(t, page, "%s %s", scope.name, outcome)
			for _, rec := range page {
				assert.Equal(t, outcome, rec.Outcome)
			}
			if scope.userID == "" {
				assert.Equal(t, want[outcome], count, "%s records counted", outcome)
			}
		}
	}
}
