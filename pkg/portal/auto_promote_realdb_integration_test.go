//go:build integration

package portal

// Real-Postgres test for the public-link auto-promote path: a signed-in viewer
// opening a public link gets a derived viewer share, the derivation is
// idempotent, an existing editor is never downgraded, and the owner gets no
// derived share at all.

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

const realdbPromoteOwner = "550e8400-e29b-41d4-a716-446655440000"

func seedPromoteAsset(t *testing.T, db *sql.DB, id, owner string) {
	t.Helper()
	store := NewPostgresAssetStore(db, nil)
	require.NoError(t, store.Insert(context.Background(), Asset{
		ID: id, OwnerID: owner, OwnerEmail: "owner@example.com", Name: id,
		ContentType: "text/markdown", S3Bucket: "b", S3Key: "k", Tags: []string{}, CurrentVersion: 1,
	}))
}

func TestRealDB_AutoPromoteCreatesAndDoesNotDowngrade(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	seedPromoteAsset(t, db, "asset_promo", realdbPromoteOwner)
	shareStore := NewPostgresShareStore(db)
	h := &Handler{deps: Deps{ShareStore: shareStore}}

	viewer := &User{UserID: "viewer1", Email: "viewer1@example.com"}

	// First public-link login → derived viewer share with origin=public_link_login.
	assert.True(t, h.autoPromoteViewer(ctx, promoteTarget{targetTypeAsset, "asset_promo", realdbPromoteOwner, "owner@example.com"}, viewer))
	got, err := shareStore.GetActiveShareForTarget(ctx, targetTypeAsset, "asset_promo", viewer.UserID, viewer.Email)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, PermissionViewer, got.Permission)
	assert.Equal(t, OriginPublicLinkLogin, got.Origin)

	// Idempotent: a second visit does not add a duplicate, and still reports the
	// asset as one this viewer opens in their own portal (#1473).
	assert.True(t, h.autoPromoteViewer(ctx, promoteTarget{targetTypeAsset, "asset_promo", realdbPromoteOwner, "owner@example.com"}, viewer))
	shares, err := shareStore.ListByAsset(ctx, "asset_promo")
	require.NoError(t, err)
	assert.Len(t, shares, 1)

	// An existing editor must not be downgraded.
	editor := &User{UserID: "editor1", Email: "editor1@example.com"}
	require.NoError(t, shareStore.Insert(ctx, &Share{
		ID: "share_editor", AssetID: "asset_promo", Token: "tok_editor", CreatedBy: "owner@example.com",
		SharedWithUserID: editor.UserID, SharedWithEmail: editor.Email, Permission: PermissionEditor,
	}))
	assert.True(t, h.autoPromoteViewer(ctx, promoteTarget{targetTypeAsset, "asset_promo", realdbPromoteOwner, "owner@example.com"}, editor))
	editorShare, err := shareStore.GetActiveShareForTarget(ctx, targetTypeAsset, "asset_promo", editor.UserID, editor.Email)
	require.NoError(t, err)
	require.NotNil(t, editorShare)
	assert.Equal(t, PermissionEditor, editorShare.Permission)

	// The owner gets no derived share.
	owner := &User{UserID: realdbPromoteOwner, Email: "owner@example.com"}
	assert.True(t, h.autoPromoteViewer(ctx, promoteTarget{targetTypeAsset, "asset_promo", realdbPromoteOwner, "owner@example.com"}, owner))
	ownerShare, err := shareStore.GetActiveShareForTarget(ctx, targetTypeAsset, "asset_promo", owner.UserID, owner.Email)
	require.NoError(t, err)
	assert.Nil(t, ownerShare)
}
