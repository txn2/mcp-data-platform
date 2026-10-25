//go:build integration

package portalstore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// A link changed in place keeps its token and takes the new mode, expiry and
// banner, and a grant's permission changes without a new row (#2291).
func TestShareUpdatesInPlace_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := &postgresShareStore{db: db}
	ctx := context.Background()
	assets := &postgresAssetStore{db: db}
	require.NoError(t, assets.Insert(ctx, portaldomain.Asset{
		ID: "33333333-3333-3333-3333-333333333333", OwnerID: "550e8400-e29b-41d4-a716-446655440555",
		OwnerEmail: "owner@example.com", Name: "Report", ContentType: "text/markdown",
		S3Bucket: "portal-assets", S3Key: "k/share-update/v1/content.md",
		SizeBytes: 10, Tags: []string{}, CurrentVersion: 1,
	}))

	link := portaldomain.Share{
		ID: "44444444-4444-4444-4444-444444444444", AssetID: "33333333-3333-3333-3333-333333333333",
		Token: "tok-link", CreatedBy: "owner@example.com", AccessMode: portaldomain.AccessModeAuthenticated,
		NoticeText: portaldomain.DefaultNoticeText,
	}
	require.NoError(t, store.Insert(ctx, &link))
	exp := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	require.NoError(t, store.UpdateLink(ctx, link.ID, portaldomain.LinkUpdate{
		AccessMode: portaldomain.AccessModePublic, ExpiresAt: &exp, NoticeText: "Board only", HideExpiration: true,
	}))
	got, err := store.GetByID(ctx, link.ID)
	require.NoError(t, err)
	assert.Equal(t, "tok-link", got.Token)
	assert.Equal(t, portaldomain.AccessModePublic, got.AccessMode)
	require.NotNil(t, got.ExpiresAt)
	assert.True(t, got.ExpiresAt.Equal(exp))
	assert.Equal(t, "Board only", got.NoticeText)
	assert.True(t, got.HideExpiration)

	require.NoError(t, store.UpdateLink(ctx, link.ID, portaldomain.LinkUpdate{AccessMode: portaldomain.AccessModeAuthenticated}))
	got, err = store.GetByID(ctx, link.ID)
	require.NoError(t, err)
	assert.Nil(t, got.ExpiresAt, "a link for signed-in people carries no expiry")

	grant := portaldomain.Share{
		ID: "55555555-5555-5555-5555-555555555555", AssetID: "33333333-3333-3333-3333-333333333333",
		Token: "tok-grant", CreatedBy: "owner@example.com", SharedWithEmail: "member@example.com",
	}
	require.NoError(t, store.Insert(ctx, &grant))
	require.NoError(t, store.SetPermission(ctx, grant.ID, portaldomain.PermissionEditor))
	got, err = store.GetByID(ctx, grant.ID)
	require.NoError(t, err)
	assert.Equal(t, portaldomain.PermissionEditor, got.Permission)

	all, err := store.ListByAsset(ctx, "33333333-3333-3333-3333-333333333333")
	require.NoError(t, err)
	assert.Len(t, all, 2, "nothing was added")
}
