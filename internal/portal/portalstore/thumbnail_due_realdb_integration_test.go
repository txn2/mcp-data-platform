//go:build integration

package portalstore

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// The renderer reads only due assets and collections (#2084). These run the
// clear, the claim and the sweep against the migrated schema and its
// triggers, and assert which rows are due after each write.

func collectionDue(t *testing.T, db *sql.DB, id string) bool {
	t.Helper()
	var due bool
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT COALESCE(thumbnail_due_at <= now(), FALSE) FROM portal_collections WHERE id = $1`, id).Scan(&due))
	return due
}

// TestAssetThumbnailDue_RealDB_ClearClaimAndSweep: a drawn asset is due once
// after its tile is recorded and then cleared; a type nothing draws is
// cleared; a sweep marks every asset once per rule.
func TestAssetThumbnailDue_RealDB_ClearClaimAndSweep(t *testing.T) {
	db := testdb.New(t)
	assets := &postgresAssetStore{db: db}
	ctx := context.Background()

	seedPendingAsset(t, db, assets, "a_drawn", "text/html", 100, 1, current(1))
	seedPendingAsset(t, db, assets, "a_owed", "text/html", 100, 1, thumbState{})
	seedPendingAsset(t, db, assets, "a_office", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", 100, 1, thumbState{})

	cleared, err := assets.ClearThumbnailWork(ctx, testRenderer, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(2), cleared, "the drawn asset and the type nothing draws leave the due set")
	claimed, err := assets.ClaimThumbnailWork(ctx, testRenderer, time.Minute, 100)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	assert.Equal(t, "a_owed", claimed[0].ID)

	pending, waiting, err := assets.ThumbnailBacklog(ctx, testRenderer)
	require.NoError(t, err)
	assert.Equal(t, [2]int64{0, 1}, [2]int64{pending, waiting}, "the claimed asset is waiting")

	marked, err := assets.SweepThumbnailWork(ctx, testRenderer)
	require.NoError(t, err)
	assert.Equal(t, int64(3), marked)
	marked, err = assets.SweepThumbnailWork(ctx, testRenderer)
	require.NoError(t, err)
	assert.Zero(t, marked, "the same rule is not swept twice")
	marked, err = assets.SweepThumbnailWork(ctx, testRenderer+1)
	require.NoError(t, err)
	assert.Equal(t, int64(3), marked, "a new renderer generation sweeps again")
}

// TestCollectionThumbnailDue_RealDB_MembersMakeItDue: a collection comes due
// when it is created, when its sections or items change, and when a member's
// tile is drawn again; a current mosaic is cleared; a collection deleted with
// its sections and items deletes cleanly.
func TestCollectionThumbnailDue_RealDB_MembersMakeItDue(t *testing.T) {
	db := testdb.New(t)
	assets := &postgresAssetStore{db: db}
	colls, ok := NewPostgresCollectionStore(db, nil).(*postgresCollectionStore)
	require.True(t, ok)
	ctx := context.Background()

	seedPendingAsset(t, db, assets, "a_member", "text/html", 100, 1, current(1))
	require.NoError(t, colls.Insert(ctx, portaldomain.Collection{ID: "c_due", OwnerID: pendingOwner, OwnerEmail: "u@example.com", Name: "c"}))
	require.True(t, collectionDue(t, db, "c_due"), "a new collection is due")

	cleared, err := colls.ClearCollectionThumbnailWork(ctx, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(1), cleared, "an empty collection with no mosaic is owed nothing")
	require.False(t, collectionDue(t, db, "c_due"))

	require.NoError(t, colls.SetSections(ctx, "c_due", []portaldomain.CollectionSection{{
		ID: "s_due", Items: []portaldomain.CollectionItem{{ID: "i_due", AssetID: "a_member"}},
	}}))
	require.True(t, collectionDue(t, db, "c_due"), "adding a member makes the collection due")
	work, err := colls.ClaimCollectionThumbnailWork(ctx, time.Minute, 100)
	require.NoError(t, err)
	require.Len(t, work, 1)
	require.NoError(t, colls.RecordCollectionThumbnail(ctx, "c_due", "portal/collections/c_due/thumbnail.png", work[0].Source))
	cleared, err = colls.ClearCollectionThumbnailWork(ctx, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(1), cleared, "a mosaic composed from its current source leaves the due set")

	_, err = db.ExecContext(ctx, `UPDATE portal_assets SET thumbnail_dark_version = 2 WHERE id = 'a_member'`)
	require.NoError(t, err)
	require.True(t, collectionDue(t, db, "c_due"), "a member's redrawn tile makes the collection due")
	work, err = colls.ClaimCollectionThumbnailWork(ctx, time.Minute, 100)
	require.NoError(t, err)
	require.Len(t, work, 1)

	pending, waiting, err := colls.CollectionThumbnailBacklog(ctx)
	require.NoError(t, err)
	assert.Equal(t, [2]int64{0, 1}, [2]int64{pending, waiting})

	marked, err := colls.SweepCollectionThumbnailWork(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), marked)

	_, err = db.ExecContext(ctx, `DELETE FROM portal_collections WHERE id = 'c_due'`)
	require.NoError(t, err, "deleting a collection cascades through its sections and items")
}

// TestAssetThumbnailClaim_RealDB_ScaleIsTheDueRows is the asset half of
// #2084's measurement: 20,000 settled assets (THUMBNAIL_DUE_BENCH_ROWS to seed
// more and time each statement), and the idle claim, clear and backlog read
// the partial index and not the table.
func TestAssetThumbnailClaim_RealDB_ScaleIsTheDueRows(t *testing.T) {
	rows, bench := 20000, false
	if v := os.Getenv("THUMBNAIL_DUE_BENCH_ROWS"); v != "" {
		bench = true
		n, err := strconv.Atoi(v)
		require.NoError(t, err)
		rows = n
	}
	db := testdb.New(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO portal_assets (id, owner_id, owner_email, name, description, content_type, s3_bucket, s3_key, size_bytes, tags, provenance, current_version)
		SELECT 'bench_' || g, $2, 'u@example.com', 'a' || g, '',
		       (ARRAY['text/html','image/png','application/pdf','text/csv','application/zip'])[1 + g % 5],
		       'portal-assets', 'k/' || g || '/content', 1000, '{}', '{}', 1
		  FROM generate_series(1, $1) g`, rows, pendingOwner)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE portal_assets SET thumbnail_due_at = NULL`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `VACUUM ANALYZE portal_assets`)
	require.NoError(t, err)

	claim, claimArgs, err := buildThumbnailClaim(testRenderer, time.Minute, 4)
	require.NoError(t, err)
	clear, clearArgs, err := buildThumbnailClear(testRenderer, 1000)
	require.NoError(t, err)
	backlog, backlogArgs, err := buildThumbnailBacklog(testRenderer)
	require.NoError(t, err)
	for name, stmt := range map[string]struct {
		q    string
		args []any
	}{"claim": {claim, claimArgs}, "clear": {clear, clearArgs}, "backlog": {backlog, backlogArgs}} {
		plan, elapsed := explainAnalyze(t, db, stmt.q, stmt.args)
		t.Logf("idle %s over %d assets: %v\n%s", name, rows, elapsed, plan)
		assert.Contains(t, plan, "idx_portal_assets_thumbnail_due", "the idle %s does not read the partial index", name)
		assert.NotContains(t, plan, "Seq Scan on portal_assets", "the idle %s reads the table", name)
		// Timed only as the benchmark: under verify's load the server's
		// clock says more about the machine than about the plan.
		if bench {
			assert.Less(t, elapsed, 5*time.Millisecond, "the idle %s took %v", name, elapsed)
		}
	}
}

// explainAnalyze runs stmt under EXPLAIN (ANALYZE, BUFFERS) in a transaction
// it rolls back, and returns the plan and the execution time PostgreSQL
// reports.
func explainAnalyze(t *testing.T, db *sql.DB, stmt string, args []any) (string, time.Duration) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+stmt, args...)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var lines []string
	var elapsed time.Duration
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		lines = append(lines, line)
		if ms, ok := strings.CutPrefix(strings.TrimSpace(line), "Execution Time: "); ok {
			f, err := strconv.ParseFloat(strings.TrimSuffix(ms, " ms"), 64)
			require.NoError(t, err)
			elapsed = time.Duration(f * float64(time.Millisecond))
		}
	}
	require.NoError(t, rows.Err())
	return strings.Join(lines, "\n"), elapsed
}
