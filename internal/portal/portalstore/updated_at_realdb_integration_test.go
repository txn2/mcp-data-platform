//go:build integration

package portalstore

// Real-Postgres tests for what moves an asset's updated_at (#1466).
//
// The column is what the portal's Updated sort orders by and what every asset
// card shows. A thumbnail capture used to write the asset row through the same
// update path an edit uses, so a pass that re-captured a library's pending
// thumbnails re-dated every asset in it. sqlmock can show which columns the
// statement sets; only the real schema can show what the stored value is
// afterwards and what order the rows come back in.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

const stampOwner = "550e8400-e29b-41d4-a716-446655440444"

func readUpdatedAt(t *testing.T, db *sql.DB, id string) time.Time {
	t.Helper()
	var got time.Time
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT updated_at FROM portal_assets WHERE id = $1`, id).Scan(&got))
	return got
}

func newStampAsset(id, name string) portaldomain.Asset {
	return portaldomain.Asset{
		ID: id, OwnerID: stampOwner, OwnerEmail: "u@example.com",
		Name: name, ContentType: "text/markdown", S3Bucket: "portal-assets",
		S3Key: "k/" + id + "/v1/content.md", SizeBytes: 100, Tags: []string{}, CurrentVersion: 1,
	}
}

// The headline criterion: an asset nobody has touched since March still reads
// as last changed in March after its thumbnails are captured.
func TestAssetUpdatedAt_RealDB_ACaptureDoesNotRedateTheAsset(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}
	ctx := context.Background()

	march := time.Date(2026, 3, 10, 19, 59, 55, 0, time.UTC)
	require.NoError(t, store.Insert(ctx, newStampAsset("asset_untouched", "March report")))
	stampAsset(t, db, "asset_untouched", march, march)

	lightKey := "k/asset_untouched/v1/.thumbnail.png"
	darkKey := "k/asset_untouched/v1/.thumbnail_dark.png"
	version := 1

	require.NoError(t, store.Update(ctx, "asset_untouched", portaldomain.AssetUpdate{
		ThumbnailS3Key: &lightKey, ThumbnailVersion: &version,
	}))
	assert.WithinDuration(t, march, readUpdatedAt(t, db, "asset_untouched"), time.Second,
		"a light capture is platform state, not a change to the asset")

	require.NoError(t, store.Update(ctx, "asset_untouched", portaldomain.AssetUpdate{
		ThumbnailDarkS3Key: &darkKey, ThumbnailDarkVersion: &version,
	}))
	assert.WithinDuration(t, march, readUpdatedAt(t, db, "asset_untouched"), time.Second,
		"the dark variant runs as its own capture and must not re-date the asset either")

	// The capture did land: the asset is off the refresh queue, which is the
	// whole point of writing those columns.
	stored, err := store.Get(ctx, "asset_untouched")
	require.NoError(t, err)
	assert.Equal(t, lightKey, stored.ThumbnailS3Key)
	assert.Equal(t, darkKey, stored.ThumbnailDarkS3Key)
	assert.Equal(t, 1, stored.ThumbnailVersion)
	assert.Equal(t, 1, stored.ThumbnailDarkVersion)
}

// The other half: everything a person does to an asset still dates it.
func TestAssetUpdatedAt_RealDB_AnAuthoredChangeStillStampsIt(t *testing.T) {
	march := time.Date(2026, 3, 10, 19, 59, 55, 0, time.UTC)

	tests := []struct {
		name   string
		update portaldomain.AssetUpdate
	}{
		{"a rename", portaldomain.AssetUpdate{Name: new("Renamed")}},
		{"a description edit", portaldomain.AssetUpdate{Description: new("what it covers")}},
		{"a tag change", portaldomain.AssetUpdate{Tags: []string{"q4"}}},
		{
			name: "replaced content",
			update: portaldomain.AssetUpdate{
				ContentType: "text/csv", S3Key: "k/asset_edited/v2/content.csv",
				SizeBytes: 200, HasContent: true,
			},
		},
		{"a retention cap", portaldomain.AssetUpdate{MaxVersions: new(5)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testdb.New(t)
			store := &postgresAssetStore{db: db}
			ctx := context.Background()

			require.NoError(t, store.Insert(ctx, newStampAsset("asset_edited", "March report")))
			stampAsset(t, db, "asset_edited", march, march)

			require.NoError(t, store.Update(ctx, "asset_edited", tt.update))
			assert.True(t, readUpdatedAt(t, db, "asset_edited").After(march),
				"an authored change is what the Updated column is for")
		})
	}
}

// Criterion 4: running the pending-thumbnail pass over a library leaves it in
// the order it was in. The captures run newest-first, which is the order a
// worker walking the queue would produce and the order that would invert the
// library if each capture stamped.
func TestAssetUpdatedAt_RealDB_ARecapturePassDoesNotReorderTheLibrary(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}
	ctx := context.Background()

	dates := map[string]time.Time{
		"asset_march":  time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC),
		"asset_june":   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		"asset_august": time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC),
	}
	// Seeded with the legacy visible key, which is what puts a row on the
	// pending list and what made this reachable in the first place.
	for id, when := range dates {
		require.NoError(t, store.Insert(ctx, newStampAsset(id, id)))
		stampAsset(t, db, id, when, when)
		_, err := db.ExecContext(ctx,
			`UPDATE portal_assets SET thumbnail_s3_key = $1, thumbnail_version = 1 WHERE id = $2`,
			"k/"+id+"/v1/thumbnail.png", id)
		require.NoError(t, err)
	}

	before, _, err := store.List(ctx, portaldomain.AssetFilter{Owner: portaldomain.NewAssetOwner(stampOwner, "")})
	require.NoError(t, err)
	require.Equal(t, []string{"asset_august", "asset_june", "asset_march"}, ids(before))

	pending, _, err := store.List(ctx, portaldomain.AssetFilter{Owner: portaldomain.NewAssetOwner(stampOwner, ""), ThumbnailPending: true})
	require.NoError(t, err)
	require.Len(t, pending, 3, "every legacy-key row is due for re-capture")

	version := 1
	for _, a := range pending {
		key := "k/" + a.ID + "/v1/.thumbnail.png"
		require.NoError(t, store.Update(ctx, a.ID, portaldomain.AssetUpdate{
			ThumbnailS3Key: &key, ThumbnailVersion: &version,
		}))
	}

	after, _, err := store.List(ctx, portaldomain.AssetFilter{Owner: portaldomain.NewAssetOwner(stampOwner, "")})
	require.NoError(t, err)
	assert.Equal(t, ids(before), ids(after))
	for _, a := range after {
		assert.WithinDuration(t, dates[a.ID], a.UpdatedAt, time.Second)
	}
}
