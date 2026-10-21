package portal

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// A caller whose access to an asset is viewer reads its current version and
// not its history (#2226): list_versions and diff refuse, an editor's share
// and the owner are answered.
func TestHistory_AViewerIsRefusedListVersionsAndDiff(t *testing.T) {
	store := newInMemoryAssetStore()
	vs := newInMemoryVersionStore()
	shares := &fakeShareStore{}
	tk := New(Config{Name: "test", AssetStore: store, VersionStore: vs, ShareStore: shares, S3Client: &mockS3Client{}, S3Bucket: "b"})
	owner := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "owner"})
	require.NoError(t, store.Insert(owner, portal.Asset{ID: "a1", OwnerID: "owner", CurrentVersion: 2, ContentType: "text/markdown"}))

	reader := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "reader", UserEmail: "r@example.com"})
	call := func(ctx context.Context, action string) *mcp.CallToolResult {
		res, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{Action: action, AssetID: "a1"})
		require.NoError(t, err)
		return res
	}
	text := func(res *mcp.CallToolResult) string {
		tc, _ := res.Content[0].(*mcp.TextContent)
		return tc.Text
	}

	shares.assetShare = &portal.Share{AssetID: "a1", SharedWithUserID: "reader", Permission: portal.PermissionViewer}
	for _, action := range []string{"list_versions", "diff"} {
		res := call(reader, action)
		assert.True(t, res.IsError, action)
		assert.True(t, strings.Contains(text(res), "earlier versions"), "%s: %s", action, text(res))
	}

	shares.assetShare.Permission = portal.PermissionEditor
	assert.False(t, call(reader, "list_versions").IsError, "an editor lists the versions")

	shares.assetShare = nil
	shares.assetViaColl = portal.PermissionEditor
	assert.False(t, call(reader, "list_versions").IsError, "an editor through a collection lists them")

	shares.assetViaColl = ""
	res := call(reader, "list_versions")
	assert.True(t, res.IsError, "a caller with no access is refused before the history check")
	assert.False(t, call(owner, "list_versions").IsError, "the owner lists them")
}
