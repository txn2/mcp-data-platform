package portal

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// saveAndGet runs save_asset through the real handler and returns the stored
// asset, so these tests assert on what the viewer will actually read rather
// than on the detection function in isolation.
func saveAndGet(t *testing.T, store *inMemoryAssetStore, input saveAssetInput) *portal.Asset {
	t.Helper()

	tk := New(Config{
		Name: "test", AssetStore: store, S3Client: &mockS3Client{},
		S3Bucket: "b", S3Prefix: "assets/", BaseURL: "http://example.com",
	})
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "user1", UserEmail: "user1@example.com", SessionID: "sess1",
	})

	result, _, err := tk.handleSaveAsset(ctx, nil, input)
	require.NoError(t, err)
	require.False(t, result.IsError, "save_asset failed: %+v", result.Content)

	var out saveAssetOutput
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))

	asset, getErr := store.Get(context.Background(), out.AssetID)
	require.NoError(t, getErr)
	return asset
}

// TestSaveAssetDetectsContentType is the save_asset half of issue #1007:
// an agent that saved structured content under a catch-all type gets the asset
// stored under the type the viewer needs.
func TestSaveAssetDetectsContentType(t *testing.T) {
	tests := []struct {
		name         string
		declared     string
		content      string
		wantType     string
		wantDeclared string
	}{
		{
			name:         "json declared text/plain is reclassified",
			declared:     "text/plain",
			content:      `{"results":[{"id":1,"name":"acme"}],"total":1}`,
			wantType:     "application/json",
			wantDeclared: "text/plain",
		},
		{
			name:         "json declared octet-stream is reclassified",
			declared:     "application/octet-stream",
			content:      `[{"id":1},{"id":2}]`,
			wantType:     "application/json",
			wantDeclared: "application/octet-stream",
		},
		{
			name:     "specific declaration is preserved",
			declared: "text/markdown",
			content:  "# Report\n\nSome prose.\n",
			wantType: "text/markdown",
		},
		{
			name:     "html declaration is preserved",
			declared: "text/html",
			content:  "<div>Hello</div>",
			wantType: "text/html",
		},
		{
			name:         "csv declared text/plain is reclassified",
			declared:     "text/plain",
			content:      "id,name\n1,acme\n2,globex\n3,initech\n",
			wantType:     "text/csv",
			wantDeclared: "text/plain",
		},
		{
			name:     "prose declared text/plain stays text/plain",
			declared: "text/plain",
			content:  "Just some notes about the quarter.\n",
			wantType: "text/plain",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newInMemoryAssetStore()
			asset := saveAndGet(t, store, saveAssetInput{
				Name: "asset", Content: tt.content, ContentType: tt.declared,
			})

			assert.Equal(t, tt.wantType, asset.ContentType)
			assert.Equal(t, tt.wantDeclared, asset.Provenance.DeclaredContentType,
				"provenance should record the declaration only when detection replaced it")
		})
	}
}

// TestSaveAssetNeverUpgradesToActiveType is the security rule at the write
// path: content that sniffs as HTML but was declared text/plain must be stored
// as text/plain, or the viewer would render it as markup.
func TestSaveAssetNeverUpgradesToActiveType(t *testing.T) {
	store := newInMemoryAssetStore()
	asset := saveAndGet(t, store, saveAssetInput{
		Name:        "not-really-a-page",
		Content:     "<!DOCTYPE html>\n<html><body><script>alert(1)</script></body></html>",
		ContentType: "text/plain",
	})

	assert.Equal(t, "text/plain", asset.ContentType)
}

// TestSaveAssetKeyExtensionFollowsDetectedType proves the storage key names
// the family the asset was actually stored as, not the one it was declared as.
func TestSaveAssetKeyExtensionFollowsDetectedType(t *testing.T) {
	store := newInMemoryAssetStore()
	asset := saveAndGet(t, store, saveAssetInput{
		Name: "export", Content: `{"a":1,"b":2}`, ContentType: "application/octet-stream",
	})

	assert.True(t, strings.HasSuffix(asset.S3Key, ".json"), "s3 key = %q", asset.S3Key)
}

// TestUpdateContentMovesAssetType covers manage_asset update: replacement
// content that resolves to a different type has to move the asset row too, or
// the viewer keeps rendering the new content under the old family.
func TestUpdateContentMovesAssetType(t *testing.T) {
	store := newInMemoryAssetStore()
	tk := New(Config{
		Name: "test", AssetStore: store, VersionStore: newLinkedVersionStore(store), S3Client: &mockS3Client{},
		S3Bucket: "b", S3Prefix: "assets/",
	})
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "user1", UserEmail: "user1@example.com",
	})

	require.NoError(t, store.Insert(ctx, portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "thing",
		ContentType: "application/octet-stream", S3Bucket: "b", S3Key: "assets/user1/a1/content.bin",
	}))

	result, _, err := tk.handleUpdate(ctx, manageAssetInput{
		Action: actionUpdate, AssetID: "a1", Content: `{"rows":[1,2,3]}`,
	})
	require.NoError(t, err)
	require.False(t, result.IsError, "%+v", result.Content)

	asset, getErr := store.Get(ctx, "a1")
	require.NoError(t, getErr)
	assert.Equal(t, "application/json", asset.ContentType)
}

// TestUpdateContentPreservesSpecificType is the other half: editing a JSON
// asset through the source editor must not re-derive a different type.
func TestUpdateContentPreservesSpecificType(t *testing.T) {
	store := newInMemoryAssetStore()
	tk := New(Config{
		Name: "test", AssetStore: store, VersionStore: newLinkedVersionStore(store), S3Client: &mockS3Client{},
		S3Bucket: "b", S3Prefix: "assets/",
	})
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "user1", UserEmail: "user1@example.com",
	})

	require.NoError(t, store.Insert(ctx, portal.Asset{
		ID: "a1", OwnerID: "user1", Name: "thing",
		ContentType: "application/json", S3Bucket: "b", S3Key: "assets/user1/a1/content.json",
	}))

	result, _, err := tk.handleUpdate(ctx, manageAssetInput{
		Action: actionUpdate, AssetID: "a1", Content: `{"rows":[4,5,6]}`,
	})
	require.NoError(t, err)
	require.False(t, result.IsError, "%+v", result.Content)

	asset, getErr := store.Get(ctx, "a1")
	require.NoError(t, getErr)
	assert.Equal(t, "application/json", asset.ContentType)
}
