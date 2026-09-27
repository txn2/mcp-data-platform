//go:build integration

package assetbucket

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestRun_RealDB_NamesThePortalBucketOnRowsNamingNone is #1931's read-path
// criterion at the rows: a legacy asset and its version gain the portal
// bucket, a row naming another bucket keeps it, and a second run writes
// nothing.
func TestRun_RealDB_NamesThePortalBucketOnRowsNamingNone(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	const owner = "550e8400-e29b-41d4-a716-446655440931"
	for _, q := range []string{
		`INSERT INTO portal_assets (id, owner_id, owner_email, name, content_type, s3_bucket, s3_key, size_bytes) VALUES
			('legacy', '` + owner + `', 'u@example.com', 'legacy', 'text/html', '', 'u/legacy/content.html', 1),
			('other', '` + owner + `', 'u@example.com', 'other', 'text/html', 'elsewhere', 'u/other/content.html', 1)`,
		`INSERT INTO portal_asset_versions (id, asset_id, version, s3_key, s3_bucket, content_type, size_bytes) VALUES
			('legacy-v1', 'legacy', 1, 'u/legacy/content.html', '', 'text/html', 1),
			('other-v1', 'other', 1, 'u/other/content.html', 'elsewhere', 'text/html', 1)`,
	} {
		_, err := db.ExecContext(ctx, q)
		require.NoError(t, err)
	}

	assets, versions, err := Run(ctx, db, "portal-assets")
	require.NoError(t, err)
	assert.Equal(t, [2]int64{1, 1}, [2]int64{assets, versions})

	bucketOf := func(q string) string {
		var b string
		require.NoError(t, db.QueryRowContext(ctx, q).Scan(&b))
		return b
	}
	assert.Equal(t, "portal-assets", bucketOf(`SELECT s3_bucket FROM portal_assets WHERE id = 'legacy'`))
	assert.Equal(t, "portal-assets", bucketOf(`SELECT s3_bucket FROM portal_asset_versions WHERE id = 'legacy-v1'`))
	assert.Equal(t, "elsewhere", bucketOf(`SELECT s3_bucket FROM portal_assets WHERE id = 'other'`))
	assert.Equal(t, "elsewhere", bucketOf(`SELECT s3_bucket FROM portal_asset_versions WHERE id = 'other-v1'`))

	assets, versions, err = Run(ctx, db, "portal-assets")
	require.NoError(t, err)
	assert.Zero(t, assets+versions, "a second run writes nothing")
}
