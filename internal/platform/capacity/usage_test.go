package capacity

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func TestRecorder_SortsAndSums(t *testing.T) {
	r := NewRecorder(testLayout)
	r.RecordObject("r", "resources/global/global/r1/f.pdf", 1, 100)
	r.RecordObject("r", "resources/global/global/r2/g.pdf", 1, 50)
	r.RecordObject("r", "resources/global/global/r2/g.pdf", -1, 0)
	r.RecordObject("p", "artifacts/scripts/s/a/run/content.csv", 1, 7)
	r.RecordObject("elsewhere", "x", 1, 9)
	r.RecordObject("r", "resources/global/global/r1/.thumbnail.png", 1, 4096)
	r.RecordObject("p", "artifacts/scripts/s1/tile.png", 1, 4096)
	assert.Equal(t, map[usageKey]usageDelta{
		{"r", observability.StoragePurposeResources}:     {objects: 1, bytes: 150},
		{"p", observability.StoragePurposeScriptOutputs}: {objects: 1, bytes: 7},
	}, r.take(), "a tile is redrawn in place, so its put is left to the listing")
	assert.Empty(t, r.take(), "take empties the recorder")
}

// TestRecorder_FlushAddsAndKeepsFailures adds each delta to its row and keeps
// the one that failed for the next flush; a delta that nets to zero is not
// written.
func TestRecorder_FlushAddsAndKeepsFailures(t *testing.T) {
	db, mock := newMock(t)
	r := NewRecorder(testLayout)
	r.RecordObject("r", "resources/x/y/z/f", 1, 10)
	r.RecordObject("p", "artifacts/u/a/content.html", 1, 5)
	r.RecordObject("p", "artifacts/u/a/content.html", -1, -5)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO storage_usage")).
		WithArgs("r", observability.StoragePurposeResources, int64(10), int64(1)).WillReturnError(errors.New("down"))
	require.ErrorContains(t, r.Flush(context.Background(), db), "adding to storage usage")
	require.NoError(t, mock.ExpectationsWereMet())

	r.RecordObject("r", "resources/x/y/z/g", 1, 1)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO storage_usage")).
		WithArgs("r", observability.StoragePurposeResources, int64(11), int64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, r.Flush(context.Background(), db))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReadStorage(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("SELECT bucket, purpose, bytes, objects FROM storage_usage").
		WillReturnRows(sqlmock.NewRows([]string{"bucket", "purpose", "bytes", "objects"}).AddRow("r", "resources", 10, 2))
	mock.ExpectQuery("SELECT purpose, orphaned, dangling FROM storage_reconciles").
		WillReturnRows(sqlmock.NewRows([]string{"purpose", "orphaned", "dangling"}).AddRow("resources", 1, 0))
	mock.ExpectQuery("SELECT duration_seconds, objects FROM storage_scans").
		WillReturnRows(sqlmock.NewRows([]string{"d", "o"}).AddRow(2.5, 12))
	got, err := ReadStorage(context.Background(), db, map[string]string{"r": "seaweedfs"})
	require.NoError(t, err)
	assert.Equal(t, observability.StorageCapacity{
		Usage:     []observability.BucketUsage{{Bucket: "r", Purpose: "resources", Backend: "seaweedfs", Bytes: 10, Objects: 2}},
		Reconcile: []observability.ReconcileCount{{Purpose: "resources", Orphaned: 1}},
		ScanKnown: true, ScanSeconds: 2.5, ScanObjects: 12,
	}, got)
}

// TestReadStorage_OnlyConfiguredBuckets leaves out a row for a bucket the
// deployment no longer configures.
func TestReadStorage_OnlyConfiguredBuckets(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("FROM storage_usage").WillReturnRows(sqlmock.NewRows([]string{"bucket", "purpose", "bytes", "objects"}).
		AddRow("old", "resources", 5, 1).AddRow("r", "resources", 10, 2))
	mock.ExpectQuery("FROM storage_reconciles").WillReturnRows(sqlmock.NewRows([]string{"purpose", "orphaned", "dangling"}))
	mock.ExpectQuery("FROM storage_scans").WillReturnError(sql.ErrNoRows)
	got, err := ReadStorage(context.Background(), db, map[string]string{"r": ""})
	require.NoError(t, err)
	assert.Equal(t, []observability.BucketUsage{{Bucket: "r", Purpose: "resources", Backend: observability.StorageBackendOther, Bytes: 10, Objects: 2}}, got.Usage)
}

// TestReadStorage_NoScanYet reads a deployment whose first listing has not
// finished: no scan figures, and no error.
func TestReadStorage_NoScanYet(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("FROM storage_usage").WillReturnRows(sqlmock.NewRows([]string{"bucket", "purpose", "bytes", "objects"}))
	mock.ExpectQuery("FROM storage_reconciles").WillReturnRows(sqlmock.NewRows([]string{"purpose", "orphaned", "dangling"}))
	mock.ExpectQuery("FROM storage_scans").WillReturnError(sql.ErrNoRows)
	got, err := ReadStorage(context.Background(), db, nil)
	require.NoError(t, err)
	assert.False(t, got.ScanKnown)
}

func TestReadStorage_Failures(t *testing.T) {
	for i, step := range []string{"FROM storage_usage", "FROM storage_reconciles", "FROM storage_scans"} {
		t.Run(step, func(t *testing.T) {
			db, mock := newMock(t)
			for j, q := range []string{"FROM storage_usage", "FROM storage_reconciles"} {
				if j < i {
					mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows([]string{"a", "b", "c", "d"}[:3+boolInt(j == 0)]))
				}
			}
			mock.ExpectQuery(step).WillReturnError(errors.New("down"))
			_, err := ReadStorage(context.Background(), db, nil)
			require.Error(t, err)
		})
	}
	db, mock := newMock(t)
	mock.ExpectQuery("FROM storage_usage").WillReturnRows(sqlmock.NewRows([]string{"bucket"}).AddRow("only one column"))
	_, err := ReadStorage(context.Background(), db, nil)
	require.ErrorContains(t, err, "reading storage usage")
	db, mock = newMock(t)
	mock.ExpectQuery("FROM storage_usage").WillReturnRows(sqlmock.NewRows([]string{"bucket", "purpose", "bytes", "objects"}))
	mock.ExpectQuery("FROM storage_reconciles").WillReturnRows(sqlmock.NewRows([]string{"purpose"}).AddRow("x"))
	_, err = ReadStorage(context.Background(), db, nil)
	require.ErrorContains(t, err, "reading storage reconcile")
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestSampleTables(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("pg_partition_tree").WithArgs(pq.Array(Tables)).
		WillReturnRows(sqlmock.NewRows([]string{"name", "bytes", "rows"}).AddRow("audit_logs", 8192, 3))
	mock.ExpectQuery("amname = 'hnsw'").
		WillReturnRows(sqlmock.NewRows([]string{"name", "bytes"}).AddRow("idx_prompts_embedding_hnsw", 16384))
	mock.ExpectQuery("datfrozenxid").WillReturnRows(sqlmock.NewRows([]string{"age"}).AddRow(42))
	got, err := SampleTables(context.Background(), db)
	require.NoError(t, err)
	assert.Equal(t, observability.DatabaseCapacity{
		Known:            true,
		Tables:           []observability.TableCapacity{{Table: "audit_logs", Bytes: 8192, Rows: 3}},
		VectorIndexes:    []observability.IndexCapacity{{Index: "idx_prompts_embedding_hnsw", Bytes: 16384}},
		TransactionIDAge: 42,
	}, got)
}

func TestSampleTables_Failures(t *testing.T) {
	steps := []string{"pg_partition_tree", "amname = 'hnsw'", "datfrozenxid"}
	for i, step := range steps {
		t.Run(step, func(t *testing.T) {
			db, mock := newMock(t)
			if i > 0 {
				mock.ExpectQuery(steps[0]).WillReturnRows(sqlmock.NewRows([]string{"name", "bytes", "rows"}))
			}
			if i > 1 {
				mock.ExpectQuery(steps[1]).WillReturnRows(sqlmock.NewRows([]string{"name", "bytes"}))
			}
			mock.ExpectQuery(step).WillReturnError(errors.New("down"))
			got, err := SampleTables(context.Background(), db)
			require.Error(t, err)
			assert.False(t, got.Known)
		})
	}
	for i, step := range steps[:2] {
		db, mock := newMock(t)
		if i > 0 {
			mock.ExpectQuery(steps[0]).WillReturnRows(sqlmock.NewRows([]string{"name", "bytes", "rows"}))
		}
		mock.ExpectQuery(step).WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("short"))
		_, err := SampleTables(context.Background(), db)
		require.Error(t, err, step)
	}
}
