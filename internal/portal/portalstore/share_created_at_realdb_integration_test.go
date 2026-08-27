//go:build integration

package portalstore

// Real-Postgres test for the timestamp a share create answers with (#1511).
//
// The insert leaves created_at to the column default and reads it back, so the
// row and the value the handler renders come from one clock. sqlmock can show
// that the statement asks for the column; only the real schema can show that
// the default exists, that RETURNING hands the value back, and that the row
// holds what the caller was told.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

func TestShareInsertReturnsTheStoredCreatedAt_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := &postgresShareStore{db: db}
	ctx := context.Background()

	// portal_shares.asset_id references portal_assets(id), so the share needs
	// a real asset to hang off.
	assets := &postgresAssetStore{db: db}
	require.NoError(t, assets.Insert(ctx, portaldomain.Asset{
		ID: "22222222-2222-2222-2222-222222222222", OwnerID: "550e8400-e29b-41d4-a716-446655440444",
		OwnerEmail: "owner@example.com", Name: "Report", ContentType: "text/markdown",
		S3Bucket: "portal-assets", S3Key: "k/share-created-at/v1/content.md",
		SizeBytes: 10, Tags: []string{}, CurrentVersion: 1,
	}))

	before := time.Now().UTC().Add(-time.Minute)
	share := portaldomain.Share{
		ID:              "11111111-1111-1111-1111-111111111111",
		AssetID:         "22222222-2222-2222-2222-222222222222",
		Token:           "tok-created-at",
		CreatedBy:       "owner@example.com",
		SharedWithEmail: "bob@example.com",
	}
	require.NoError(t, store.Insert(ctx, &share))

	assert.False(t, share.CreatedAt.IsZero(), "Insert must fill CreatedAt from the stored row")
	assert.True(t, share.CreatedAt.After(before), "the stored timestamp must be a real clock reading")

	// The value handed back is the value in the row, not a second reading.
	stored, err := store.GetByID(ctx, share.ID)
	require.NoError(t, err)
	assert.True(t, stored.CreatedAt.Equal(share.CreatedAt),
		"a read of the share must return the timestamp Insert reported: got %s, want %s",
		stored.CreatedAt, share.CreatedAt)
}
