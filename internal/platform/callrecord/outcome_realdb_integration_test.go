//go:build integration

package callrecord

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	sq "github.com/Masterminds/squirrel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// outcomeCatalogSize is #2231's catalog: the admin list took 36 seconds to
// filter about 95,000 records by outcome.
const outcomeCatalogSize = 100_000

// The bounds below are on work the database reports, not on time: a one-second
// bound held when the package ran alone and failed under make verify's load
// (#2249). Neither count changes with the machine's load.
const (
	// outcomeProbeShare is the most times a page and its count may probe the
	// artifacts the satisfied rule reads (memory_records, portal_assets), as a
	// share of the records in scope. Before #2231 the rule was asked of every
	// record, a probe or two each (188,102 on this catalog); now it is asked
	// of the cited records and a page's, at most 0.05 of them.
	outcomeProbeShare = 0.25
	// outcomeRowsPerRecord is the most rows a page and its count may examine,
	// per record in the whole catalog. The supersession pass reads the catalog
	// once in order, about ten rows a record across the plan's nodes; a
	// supersession probed pair by pair, which ran for minutes on 103,000
	// records with no session, is billions. It is measured against the
	// catalog rather than the unguarded read, whose plan, and so its rows,
	// changes between runs.
	outcomeRowsPerRecord = 50
)

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

// unguardedQuery counts the catalog by outcome with the satisfied rule asked
// of every record: the read before #2231.
func unguardedQuery(t *testing.T) (string, []any) {
	t.Helper()
	inner := sq.Select(storedColumns...).
		Column(satisfiedByCase("?")+" AS satisfied_by", callReferencePrefix()).
		Column(supersededExpr).
		Column(reuseCountExpr).
		From("call_records r")
	query, args, err := psq.Select("o.outcome", "COUNT(*)").
		FromSelect(outcomeOver(inner), "o").GroupBy("o.outcome").ToSql()
	require.NoError(t, err)
	return query, args
}

// unguardedOutcomes runs unguardedQuery, so the guarded read is held to
// deciding every record the same way.
func unguardedOutcomes(ctx context.Context, t *testing.T, store *PostgresStore) map[string]int {
	t.Helper()
	query, args := unguardedQuery(t)
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

// readWork is what one statement cost the database: the rows its nodes
// examined, how many times it probed the artifacts the satisfied rule reads,
// and whether it was compiled.
type readWork struct {
	rows   float64
	probes float64
	jit    bool
}

func (w readWork) add(o readWork) readWork {
	return readWork{rows: w.rows + o.rows, probes: w.probes + o.probes, jit: w.jit || o.jit}
}

// planNode is the part of an EXPLAIN (ANALYZE, FORMAT JSON) node the work is
// read from.
type planNode struct {
	Relation       string     `json:"Relation Name"`
	Rows           float64    `json:"Actual Rows"`
	Loops          float64    `json:"Actual Loops"`
	Filtered       float64    `json:"Rows Removed by Filter"`
	JoinFiltered   float64    `json:"Rows Removed by Join Filter"`
	RecheckDropped float64    `json:"Rows Removed by Index Recheck"`
	Plans          []planNode `json:"Plans"`
}

// examined is the rows a node and its children produced or discarded, each
// counted once per loop. A per-record probe is loops; a pairwise comparison
// the planner hashed is rows removed by the join filter.
func (n planNode) examined() float64 {
	total := (n.Rows + n.Filtered + n.JoinFiltered + n.RecheckDropped) * n.Loops
	for _, c := range n.Plans {
		total += c.examined()
	}
	return total
}

// ruleProbes is the times a node and its children read memory_records or
// portal_assets, the tables the satisfied rule asks of a record.
func (n planNode) ruleProbes() float64 {
	var total float64
	if n.Relation == "memory_records" || n.Relation == "portal_assets" {
		total = n.Loops
	}
	for _, c := range n.Plans {
		total += c.ruleProbes()
	}
	return total
}

// explainRead runs a statement under EXPLAIN ANALYZE in the transaction the
// store reads in, so a read compiled with JIT (#2231's second and a half) is
// seen as the store would run it.
func explainRead(ctx context.Context, t *testing.T, store *PostgresStore, query string, args []any) readWork {
	t.Helper()
	var raw []byte
	require.NoError(t, store.withoutJIT(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "EXPLAIN (ANALYZE, FORMAT JSON) "+query, args...).Scan(&raw)
	}))
	var plans []struct {
		Plan planNode        `json:"Plan"`
		JIT  json.RawMessage `json:"JIT"`
	}
	require.NoError(t, json.Unmarshal(raw, &plans))
	require.Len(t, plans, 1)
	top := plans[0].Plan
	return readWork{rows: top.examined(), probes: top.ruleProbes(), jit: len(plans[0].JIT) > 0}
}

// TestCallCatalogRealDBFiltersEachOutcomeWithinASecond is #2231: on a catalog
// of 100,000 records, where the admin list took 36 seconds to filter by
// outcome, a page of each outcome and its count, for the whole catalog (the
// admin list) and for one caller's records (the portal's), ask the satisfied
// rule of a few records rather than every one, examine a bounded number of
// rows per catalog record, and are not compiled with JIT; and each outcome
// counts what the rule asked of every record counts.
func TestCallCatalogRealDBFiltersEachOutcomeWithinASecond(t *testing.T) {
	ctx := context.Background()
	store := NewPostgresStore(testdb.New(t), Config{})
	seedOutcomeCatalog(ctx, t, store)
	want := unguardedOutcomes(ctx, t, store)
	for _, outcome := range Outcomes {
		require.Positive(t, want[outcome], "the seed holds no %s record", outcome)
	}

	rowBound := float64(outcomeRowsPerRecord * outcomeCatalogSize)
	for _, scope := range []struct{ name, userID string }{{"admin", ""}, {"portal", "u0"}} {
		total, err := store.Count(ctx, Filter{UserID: scope.userID})
		require.NoError(t, err)
		for _, outcome := range Outcomes {
			f := Filter{UserID: scope.userID, Outcome: outcome, Limit: 50}
			pageQuery, pageArgs, err := listPage(f).ToSql()
			require.NoError(t, err)
			countSQL, countArgs, err := countQuery(f).ToSql()
			require.NoError(t, err)
			work := explainRead(ctx, t, store, pageQuery, pageArgs).add(explainRead(ctx, t, store, countSQL, countArgs))
			t.Logf("%s %s: page and count probe %.0f times (%.3f of the records) and examine %.0f rows (%.1f a catalog record)",
				scope.name, outcome, work.probes, work.probes/float64(total), work.rows, work.rows/outcomeCatalogSize)
			assert.LessOrEqual(t, work.probes, outcomeProbeShare*float64(total), "%s %s: rule probes", scope.name, outcome)
			assert.LessOrEqual(t, work.rows, rowBound, "%s %s: rows examined", scope.name, outcome)
			assert.False(t, work.jit, "%s %s was compiled with JIT", scope.name, outcome)

			records, err := store.List(ctx, f)
			require.NoError(t, err)
			require.NotEmpty(t, records, "%s %s", scope.name, outcome)
			for _, rec := range records {
				assert.Equal(t, outcome, rec.Outcome)
			}
			if scope.userID == "" {
				n, err := store.Count(ctx, f)
				require.NoError(t, err)
				assert.Equal(t, want[outcome], n, "%s records counted", outcome)
			}
		}
	}
}
