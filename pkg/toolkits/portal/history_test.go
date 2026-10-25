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

// A caller an asset is shared with reads its history only when their persona
// lets its people read earlier versions (#2226, #2290): with it off,
// list_versions and diff refuse a viewer and an editor alike; with it on, both
// are answered. The owner always is.
func TestHistory_ThePersonaSettingDecides(t *testing.T) {
	store := newInMemoryAssetStore()
	vs := newInMemoryVersionStore()
	shares := &fakeShareStore{}
	allowed := false
	tk := New(Config{
		Name: "test", AssetStore: store, VersionStore: vs, ShareStore: shares, S3Client: &mockS3Client{}, S3Bucket: "b",
		HistoryAllowed: func(p string) bool { return allowed && p == "readers" },
	})
	owner := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "owner"})
	require.NoError(t, store.Insert(owner, portal.Asset{ID: "a1", OwnerID: "owner", CurrentVersion: 2, ContentType: "text/markdown"}))

	reader := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "reader", UserEmail: "r@example.com", PersonaName: "readers"})
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
	assert.True(t, call(reader, "list_versions").IsError, "an editor is refused while the setting is off")

	allowed = true
	assert.False(t, call(reader, "list_versions").IsError, "an editor lists the versions")
	shares.assetShare.Permission = portal.PermissionViewer
	assert.False(t, call(reader, "list_versions").IsError, "so does a viewer")

	shares.assetShare = nil
	shares.assetViaColl = portal.PermissionViewer
	assert.False(t, call(reader, "list_versions").IsError, "a viewer through a collection lists them")

	shares.assetViaColl = ""
	res := call(reader, "list_versions")
	assert.True(t, res.IsError, "a caller with no access is refused before the history check")
	assert.False(t, call(owner, "list_versions").IsError, "the owner lists them")
}
