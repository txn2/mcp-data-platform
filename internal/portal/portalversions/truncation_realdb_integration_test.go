//go:build integration

package portalversions

// Real-Postgres test of how a version moves the truncated mark (#2057). The
// mark is the asset's _sys-truncated tag and its truncation metadata keys,
// added and stripped by jsonb operators in CreateVersion's UPDATE; sqlmock
// matches that statement as a string and would pass any operator, so only a
// real database shows the tag and keys actually follow the content.

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/exporttrunc"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

func TestCreateVersion_RealDB_MovesTheTruncatedMarkWithTheContent(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	store := NewPostgres(db, nil, nil, nil)
	id := seedAsset(t, db, nil)
	// A label the owner set, and metadata a script run recorded: neither is
	// the truncation mark's, and neither may be touched by it.
	_, err := db.ExecContext(ctx,
		`UPDATE portal_assets SET tags = '["sales"]', metadata = '{"run_id":"r1"}' WHERE id = $1`, id)
	require.NoError(t, err)

	cut := exporttrunc.Report{
		Truncated: true, LimitApplied: 100, LimitSource: exporttrunc.SourceDeployment, LimitUnit: exporttrunc.UnitRows,
	}
	write := func(metadata map[string]any) {
		t.Helper()
		_, err := store.CreateVersion(ctx, portaldomain.AssetVersion{
			ID: uuid.New().String(), AssetID: id, S3Key: "k/" + uuid.New().String(), S3Bucket: "portal-assets",
			ContentType: "text/csv", SizeBytes: 10, CreatedBy: "u@example.com", Metadata: metadata,
		})
		require.NoError(t, err)
	}

	write(cut.Metadata())
	tags, meta := assetMark(t, db, id)
	assert.ElementsMatch(t, []string{"sales", exporttrunc.Tag}, tags, "a truncated version tags the asset")
	assert.Equal(t, true, meta[exporttrunc.MetaTruncated])
	assert.InDelta(t, 100, meta[exporttrunc.MetaLimitApplied], 0)

	write(cut.Metadata())
	tags, _ = assetMark(t, db, id)
	assert.ElementsMatch(t, []string{"sales", exporttrunc.Tag}, tags, "a second truncated version does not tag it twice")

	// A portal upload records no metadata: the mark goes, the rest stays.
	_, err = db.ExecContext(ctx, `UPDATE portal_assets SET metadata = metadata || '{"run_id":"r1"}' WHERE id = $1`, id)
	require.NoError(t, err)
	write(nil)
	tags, meta = assetMark(t, db, id)
	assert.Equal(t, []string{"sales"}, tags, "a version that records no cut untags the asset")
	assert.Equal(t, map[string]any{"run_id": "r1"}, meta, "and strips only the truncation keys")

	write(cut.Metadata())
	write(map[string]any{"run_id": "r2"})
	tags, meta = assetMark(t, db, id)
	assert.Equal(t, []string{"sales"}, tags, "a version with other metadata untags the asset")
	assert.Equal(t, map[string]any{"run_id": "r2"}, meta)
}

// assetMark reads the asset's tags and metadata as stored.
func assetMark(t *testing.T, db *sql.DB, id string) (tags []string, metadata map[string]any) {
	t.Helper()
	var rawTags, rawMeta []byte
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT tags, metadata FROM portal_assets WHERE id = $1`, id).Scan(&rawTags, &rawMeta))
	require.NoError(t, json.Unmarshal(rawTags, &tags))
	require.NoError(t, json.Unmarshal(rawMeta, &metadata))
	return tags, metadata
}
