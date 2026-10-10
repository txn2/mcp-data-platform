//go:build integration

package capacity

// Real-Postgres tests for the capacity service (#1899): the catalog reads,
// the reference queries over the real schema, and the usage rows' save, add
// and read-back. sqlmock matches statements as strings; these run them.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
)

// TestSampleTables_RealDB reads a size for the partitioned audit table and
// every HNSW index the migrations create, and a transaction id age.
func TestSampleTables_RealDB(t *testing.T) {
	db := testdb.New(t)
	got, err := SampleTables(context.Background(), db)
	require.NoError(t, err)
	require.True(t, got.Known)

	sizes := map[string]int64{}
	for _, tc := range got.Tables {
		sizes[tc.Table] = tc.Bytes
	}
	assert.Positive(t, sizes["audit_logs"], "the partitioned audit table is summed over its partitions")
	assert.Len(t, got.Tables, len(Tables), "every listed table exists in the migrated schema")

	indexes := map[string]bool{}
	for _, i := range got.VectorIndexes {
		indexes[i.Index] = true
	}
	for _, want := range []string{"idx_prompts_embedding_hnsw", "idx_memory_records_embedding_hnsw", "idx_resources_embedding_hnsw"} {
		assert.True(t, indexes[want], "missing %s in %v", want, indexes)
	}
	assert.Positive(t, got.TransactionIDAge)
}

// TestScan_RealDB runs the reference queries over the real schema: a resource
// row whose object is gone is dangling, an object with no row is an orphan,
// and the result saved and read back is what every replica reports.
func TestScan_RealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO resources (id, scope, scope_id, path, filename, display_name, description, mime_type, size_bytes, s3_key, uri, uploader_sub, uploader_email)
		VALUES ('res_kept', 'global', NULL, 'p', 'f.csv', 'f', '', 'text/csv', 4, 'resources/global/global/res_kept/f.csv', 'mcp://global/p/f.csv', 'u', 'u@example.com'),
		       ('res_gone', 'global', NULL, 'q', 'g.csv', 'g', '', 'text/csv', 4, 'resources/global/global/res_gone/g.csv', 'mcp://global/q/g.csv', 'u', 'u@example.com')`)
	require.NoError(t, err)

	old := time.Now().Add(-time.Hour)
	walker := fakeWalker{objects: map[string][]s3adapter.WalkedObject{
		"r": {
			{Key: "resources/global/global/res_kept/f.csv", Size: 4, LastModified: old},
			{Key: "resources/global/global/stray/h.csv", Size: 9, LastModified: old},
		},
		"p": {{Key: "artifacts/u/a/content.html", Size: 2, LastModified: old}},
	}}
	s := &Scanner{DB: db, Layout: testLayout, Walkers: map[string]Walker{"r": walker, "p": walker}}
	res, err := s.Scan(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.Orphaned[observability.StoragePurposeResources])
	assert.Equal(t, int64(1), res.Dangling[observability.StoragePurposeResources])
	assert.Equal(t, int64(1), res.Orphaned[observability.StoragePurposePortalAssets])
	require.NoError(t, res.Save(ctx, db))

	r := NewRecorder(testLayout)
	r.RecordObject("r", "resources/global/global/new/n.csv", 1, 100)
	require.NoError(t, r.Flush(ctx, db))

	got, err := ReadStorage(ctx, db, map[string]string{"r": observability.StorageBackendSeaweedFS, "p": observability.StorageBackendSeaweedFS})
	require.NoError(t, err)
	assert.Contains(t, got.Usage, observability.BucketUsage{
		Bucket: "r", Purpose: observability.StoragePurposeResources, Backend: observability.StorageBackendSeaweedFS, Bytes: 113, Objects: 3,
	}, "the listing's two objects plus the write recorded since")
	assert.True(t, got.ScanKnown)
	assert.Equal(t, int64(3), got.ScanObjects)
	assert.Contains(t, got.Reconcile, observability.ReconcileCount{Purpose: observability.StoragePurposeResources, Orphaned: 1, Dangling: 1})

	// A write that lands while a listing runs survives the listing's save,
	// and a purpose the listing no longer finds keeps only what it gained.
	snapshot, err := readUsage(ctx, db, snapshotQuery)
	require.NoError(t, err)
	r.RecordObject("r", "resources/global/global/during/d.csv", 1, 7)
	require.NoError(t, r.Flush(ctx, db))
	relisted := ScanResult{
		Usage:    map[usageKey]usageDelta{{"r", observability.StoragePurposeResources}: {objects: 2, bytes: 13}},
		Snapshot: snapshot, Buckets: []string{"r", "p"},
	}
	require.NoError(t, relisted.Save(ctx, db))
	got, err = ReadStorage(ctx, db, nil)
	require.NoError(t, err)
	assert.Contains(t, got.Usage, observability.BucketUsage{
		Bucket: "r", Purpose: observability.StoragePurposeResources, Backend: observability.StorageBackendOther, Bytes: 20, Objects: 3,
	}, "the listing's two objects plus the one written while it ran")

	// A bucket the deployment no longer lists loses its rows.
	require.NoError(t, ScanResult{Usage: map[usageKey]usageDelta{}, Snapshot: map[usageKey]usageDelta{}, Buckets: []string{"p"}}.Save(ctx, db))
	got, err = ReadStorage(ctx, db, nil)
	require.NoError(t, err)
	for _, u := range got.Usage {
		assert.NotEqual(t, "r", u.Bucket, "a row for an unlisted bucket survived: %+v", u)
	}

	// The portal reference query runs over the real schema.
	s = &Scanner{DB: db, Layout: testLayout, Walkers: map[string]Walker{"p": walker}}
	_, err = s.Scan(ctx)
	require.NoError(t, err)
}

// TestScanLock_RealDB shows the listing is skipped while another session
// holds its lock.
func TestScanLock_RealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	holder, err := db.Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = holder.Close() }()
	_, err = holder.ExecContext(ctx, "SELECT pg_advisory_lock($1)", scanLockKey)
	require.NoError(t, err)

	s := newService(Sources{DB: db, Layout: testLayout, Walkers: map[string]Walker{"p": fakeWalker{}}}, Config{})
	ran, err := s.locked(ctx, func(context.Context) error { return nil })
	require.NoError(t, err)
	assert.False(t, ran)

	_, err = holder.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", scanLockKey)
	require.NoError(t, err)
	ran, err = s.locked(ctx, func(context.Context) error { return nil })
	require.NoError(t, err)
	assert.True(t, ran)
}
