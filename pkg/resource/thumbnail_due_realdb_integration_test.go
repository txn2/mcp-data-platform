//go:build integration

package resource

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// The renderer reads only due rows (#2084). These run the claim, the clear,
// the hold and the sweep against the migrated schema and its trigger, and
// assert which rows are due after each.

func dueAt(t *testing.T, db *sql.DB, id string) (sql.NullTime, bool) {
	t.Helper()
	var due sql.NullTime
	var past bool
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT thumbnail_due_at, COALESCE(thumbnail_due_at <= now(), FALSE) FROM resources WHERE id = $1`, id).Scan(&due, &past))
	return due, past
}

func insertDue(t *testing.T, store Store, id, mime string) *Resource {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, store.Insert(ctx, Resource{
		ID: id, Scope: ScopeGlobal, Path: "due", Filename: id, DisplayName: id,
		MIMEType: mime, SizeBytes: 100, S3Key: "resources/" + id, URI: "mcp://global/due/" + id,
	}))
	r, err := store.Get(ctx, id)
	require.NoError(t, err)
	return r
}

// TestResourceThumbnailDue_RealDB_OnlyDueRowsAreRead: an inserted file is
// due; a claim carries its lease in the due time; a recorded tile, a recorded
// failure and a type nothing draws are each due once and then cleared; a hold
// keeps the file out until it passes; and a content change makes it due again.
func TestResourceThumbnailDue_RealDB_OnlyDueRowsAreRead(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	work := store.(ThumbnailWork)
	ctx := context.Background()

	png := insertDue(t, store, "res_due_png", "image/png")
	zip := insertDue(t, store, "res_due_zip", "application/zip")
	if _, past := dueAt(t, db, png.ID); !past {
		t.Fatal("an inserted file is not due")
	}

	cleared, err := work.ClearThumbnailWork(ctx, thumbnailTestRenderer, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(1), cleared, "the zip, which nothing draws, leaves the due set; the png stays")
	if due, _ := dueAt(t, db, "res_due_zip"); due.Valid {
		t.Error("a type nothing draws is still due")
	}
	// A read stamps last_read_at, which the rule never reads: the file is
	// neither marked due nor anyone woken.
	require.NoError(t, store.(ReadTracker).TouchRead(ctx, zip.ID, time.Now()))
	if due, _ := dueAt(t, db, zip.ID); due.Valid {
		t.Error("a read made the file due")
	}

	claimed, err := work.ClaimThumbnailWork(ctx, thumbnailTestRenderer, time.Hour, 100)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	if due, past := dueAt(t, db, png.ID); !due.Valid || past {
		t.Errorf("a claimed file's due time is not its lease end: %v", due)
	}

	require.NoError(t, work.SetThumbnail(ctx, png.ID, ThumbnailCapture{
		Variant: ThumbnailVariantLight, S3Key: "k/png.png", CapturedAt: png.UpdatedAt, Renderer: thumbnailTestRenderer,
	}))
	require.NoError(t, work.HoldThumbnailWork(ctx, png.ID, 0, 0))
	cleared, err = work.ClearThumbnailWork(ctx, thumbnailTestRenderer, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(1), cleared, "a drawn file is owed nothing and leaves the due set")
	pending, waiting, err := store.(interface {
		ThumbnailBacklog(context.Context, int) (int64, int64, error)
	}).ThumbnailBacklog(ctx, thumbnailTestRenderer)
	require.NoError(t, err)
	assert.Equal(t, [2]int64{0, 0}, [2]int64{pending, waiting})

	// A content change makes it due, through the trigger alone.
	_, err = db.ExecContext(ctx, `UPDATE resources SET updated_at = now() + interval '1 second' WHERE id = $1`, png.ID)
	require.NoError(t, err)
	claimed, err = work.ClaimThumbnailWork(ctx, thumbnailTestRenderer, time.Hour, 100)
	require.NoError(t, err)
	require.Len(t, claimed, 1, "a changed file is claimed again")

	// A failure is due once, then cleared until the file changes.
	require.NoError(t, work.RecordThumbnailFailure(ctx, png.ID, "cannot draw", time.Now().Add(time.Hour)))
	cleared, err = work.ClearThumbnailWork(ctx, thumbnailTestRenderer, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(1), cleared, "a failed file leaves the due set")

	// A hold keeps a file out until it passes.
	held := insertDue(t, store, "res_due_held", "image/png")
	claimed, err = work.ClaimThumbnailWork(ctx, thumbnailTestRenderer, time.Minute, 100)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.NoError(t, work.HoldThumbnailWork(ctx, held.ID, time.Hour, 1))
	claimed, err = work.ClaimThumbnailWork(ctx, thumbnailTestRenderer, time.Minute, 100)
	require.NoError(t, err)
	assert.Empty(t, claimed, "a held file is claimed before its hold passes")
	cleared, err = work.ClearThumbnailWork(ctx, thumbnailTestRenderer, 100)
	require.NoError(t, err)
	assert.Zero(t, cleared, "a held file is not due, so it is not read")
	_, waiting, err = store.(interface {
		ThumbnailBacklog(context.Context, int) (int64, int64, error)
	}).ThumbnailBacklog(ctx, thumbnailTestRenderer)
	require.NoError(t, err)
	assert.Equal(t, int64(1), waiting, "a held file is counted as waiting")
}

// TestResourceThumbnailSweep_RealDB_OncePerRule: a sweep marks every row due
// and records the rule; the same rule marks nothing again; a new renderer
// generation is a new rule and marks every row once more.
func TestResourceThumbnailSweep_RealDB_OncePerRule(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	work := store.(ThumbnailWork)
	ctx := context.Background()
	for i := range 5 {
		insertDue(t, store, fmt.Sprintf("res_sweep_%d", i), "application/zip")
	}
	_, err := db.ExecContext(ctx, `UPDATE resources SET thumbnail_due_at = NULL`)
	require.NoError(t, err)

	countDue := func() int {
		var n int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM resources WHERE thumbnail_due_at IS NOT NULL`).Scan(&n))
		return n
	}
	// The UPDATE above set the column, so the trigger left it alone.
	require.Zero(t, countDue())

	marked, err := work.SweepThumbnailWork(ctx, thumbnailTestRenderer)
	require.NoError(t, err)
	assert.Equal(t, int64(5), marked)
	assert.Equal(t, 5, countDue())

	_, err = work.ClearThumbnailWork(ctx, thumbnailTestRenderer, 100)
	require.NoError(t, err)
	marked, err = work.SweepThumbnailWork(ctx, thumbnailTestRenderer)
	require.NoError(t, err)
	assert.Zero(t, marked, "the same rule is not swept twice")
	assert.Zero(t, countDue())

	marked, err = work.SweepThumbnailWork(ctx, thumbnailTestRenderer+1)
	require.NoError(t, err)
	assert.Equal(t, int64(5), marked, "a new renderer generation sweeps again")
}

// TestResourceThumbnailClaim_RealDB_ScaleIsTheDueRows is #2084's measurement.
// It seeds 20,000 resources, all settled, and asserts the idle claim, clear
// and backlog read the partial index and not the table; then makes 1,000 of
// them due and asserts the claim reads them through the index; then sweeps
// once under a new rule. With THUMBNAIL_DUE_BENCH_ROWS set (500000 for the
// figures in docs/server/configuration.md) it seeds that many and also holds
// each idle statement under 5 ms.
func TestResourceThumbnailClaim_RealDB_ScaleIsTheDueRows(t *testing.T) {
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
		INSERT INTO resources (id, scope, path, filename, display_name, description, mime_type, size_bytes, s3_key, uri, uploader_sub, uploader_email)
		SELECT 'bench_' || g, 'global', 'bench', 'f' || g || '.html', 'f' || g, '',
		       (ARRAY['text/html','image/png','application/pdf','text/csv','application/zip'])[1 + g % 5],
		       1000, 'resources/bench/' || g, 'mcp://global/bench/f' || g, 'u', 'u@example.com'
		  FROM generate_series(1, $1) g`, rows)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE resources SET thumbnail_due_at = NULL`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `VACUUM ANALYZE resources`)
	require.NoError(t, err)

	claim, claimArgs := buildThumbnailClaim(thumbnailTestRenderer, time.Minute, 4)
	clear, clearArgs := buildThumbnailClear(thumbnailTestRenderer, 1000)
	backlog, backlogArgs := buildThumbnailBacklog(thumbnailTestRenderer)

	for name, stmt := range map[string]struct {
		q    string
		args []any
	}{"claim": {claim, claimArgs}, "clear": {clear, clearArgs}, "backlog": {backlog, backlogArgs}} {
		plan, elapsed := explain(t, db, stmt.q, stmt.args)
		t.Logf("idle %s over %d rows: %v\n%s", name, rows, elapsed, plan)
		assert.Contains(t, plan, "idx_resources_thumbnail_due", "the idle %s does not read the partial index", name)
		assert.NotContains(t, plan, "Seq Scan on resources", "the idle %s reads the table", name)
		// Timed only as the benchmark: under verify's load the server's
		// clock says more about the machine than about the plan.
		if bench {
			assert.Less(t, elapsed, 5*time.Millisecond, "the idle %s took %v", name, elapsed)
		}
	}

	_, err = db.ExecContext(ctx, `UPDATE resources SET thumbnail_due_at = now() - interval '1 second'
		WHERE id IN (SELECT id FROM resources ORDER BY id LIMIT 1000)`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `VACUUM ANALYZE resources`)
	require.NoError(t, err)
	plan, elapsed := explain(t, db, claim, claimArgs)
	t.Logf("claim with 1000 due over %d rows: %v\n%s", rows, elapsed, plan)
	assert.Contains(t, plan, "idx_resources_thumbnail_due")
	assert.NotContains(t, plan, "Seq Scan on resources")

	// A renderer-generation bump marks every row once, in chunks, and not
	// again on the next start.
	work := NewPostgresStore(db).(ThumbnailWork)
	start := time.Now()
	marked, err := work.SweepThumbnailWork(ctx, thumbnailTestRenderer+1)
	require.NoError(t, err)
	t.Logf("sweep of %d rows: %d marked in %v", rows, marked, time.Since(start))
	assert.Equal(t, int64(rows), marked)
	marked, err = work.SweepThumbnailWork(ctx, thumbnailTestRenderer+1)
	require.NoError(t, err)
	assert.Zero(t, marked, "a restart under the same rule sweeps nothing")
}

// explain runs stmt under EXPLAIN (ANALYZE, BUFFERS) in a transaction it
// rolls back, and returns the plan and the execution time PostgreSQL reports.
func explain(t *testing.T, db *sql.DB, stmt string, args []any) (string, time.Duration) {
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
