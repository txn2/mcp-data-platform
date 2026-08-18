//go:build integration

package portal

// Real-Postgres test for the manage_asset sharing actions (#1280). The unit
// tests prove the actions against a fake store; this proves the row they write
// is one the portal's own read path resolves — the recipient's shared-with-me
// listing, which is where the person the agent shared with actually finds the
// asset — and that revoke_share takes it back out.
//
// Both halves run against one container: testdb.New starts a Postgres per call,
// and the round-trip gate runs every package's RealDB tests together, so a
// second container here is load the suite pays for nothing.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

func TestRealDB_ShareActions(t *testing.T) {
	db := testdb.New(t)
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "owner_1280", UserEmail: "owner1280@example.com",
	})

	assets := portal.NewPostgresAssetStore(db)
	shares := portal.NewPostgresShareStore(db)
	seed := func(id, name string) {
		t.Helper()
		require.NoError(t, assets.Insert(ctx, portal.Asset{
			ID: id, OwnerID: "owner_1280", OwnerEmail: "owner1280@example.com",
			Name: name, ContentType: "text/markdown", S3Bucket: "b", S3Key: "k",
			Tags: []string{}, CurrentVersion: 1,
		}))
	}
	seed("asset_1280", "Q3 Revenue")
	seed("asset_1280b", "Public")

	tk := New(Config{
		Name: "test", AssetStore: assets, ShareStore: shares,
		BaseURL: "https://platform.example.com",
	})

	t.Run("share reaches the recipient's listing and revoke takes it back", func(t *testing.T) {
		created := decodeResult(t, callManage(ctx, t, tk, manageAssetInput{
			Action: actionShare, AssetID: "asset_1280", Recipient: "Recipient@Example.com",
		}))
		shareID, ok := created["share_id"].(string)
		require.True(t, ok)
		assert.Equal(t, "recipient@example.com", created["shared_with"])

		// The recipient finds the asset by the address the share was addressed
		// to, with no user id of their own yet — the same lookup the portal's
		// shared-with-me page performs.
		shared, total, err := shares.ListSharedWithUser(ctx, "recipient_1280", "recipient@example.com", 10, 0)
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		require.Len(t, shared, 1)
		assert.Equal(t, "asset_1280", shared[0].Asset.ID)
		assert.Equal(t, portal.PermissionViewer, shared[0].Permission)

		// And the owner sees the grant they made.
		listed := decodeResult(t, callManage(ctx, t, tk, manageAssetInput{
			Action: actionListShares, AssetID: "asset_1280",
		}))
		assert.Equal(t, float64(1), listed[fieldTotal])

		revoked := callManage(ctx, t, tk, manageAssetInput{Action: actionRevokeShare, ShareID: shareID})
		assert.False(t, revoked.IsError, resultText(t, revoked))

		_, total, err = shares.ListSharedWithUser(ctx, "recipient_1280", "recipient@example.com", 10, 0)
		require.NoError(t, err)
		assert.Zero(t, total, "a revoked share no longer reaches the recipient")

		after := decodeResult(t, callManage(ctx, t, tk, manageAssetInput{
			Action: actionListShares, AssetID: "asset_1280",
		}))
		assert.Equal(t, float64(0), after[fieldTotal])
	})

	// The clock the viewer gate reads is written to the row, not just reported
	// back to the agent.
	t.Run("public link stores its expiry", func(t *testing.T) {
		created := decodeResult(t, callManage(ctx, t, tk, manageAssetInput{
			Action: actionShare, AssetID: "asset_1280b",
			AccessMode: string(portal.AccessModePublic), ExpiresIn: "24h",
		}))
		shareID, ok := created["share_id"].(string)
		require.True(t, ok)

		stored, err := shares.GetByID(ctx, shareID)
		require.NoError(t, err)
		require.NotNil(t, stored)
		assert.Equal(t, portal.AccessModePublic, stored.AccessMode)
		assert.Equal(t, portal.PermissionViewer, stored.Permission)
		require.NotNil(t, stored.ExpiresAt)
		assert.Empty(t, stored.SharedWithEmail)
		assert.Equal(t, portal.OriginExplicit, stored.Origin)
	})
}
