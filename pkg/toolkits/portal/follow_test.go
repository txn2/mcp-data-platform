package portal

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

// Every content write this toolkit makes moves a file's head, and the tables
// registered over the file follow it (#1536). What these hold: a replacement
// of a managed resource, an edit, a patch and a revert of an asset each hand
// the version they wrote to the registrar and carry its report in their
// result, both as a field and in the message; a save of a NEW asset does
// not, because no table can exist over a file that did not exist; and a
// registration says which rule it was made under.

const followedSentence = "scratch.uploads.analyst_t on scratch now reads version 2."

func decodeFollowResult(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	require.False(t, result.IsError, result.Content)
	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &parsed))
	return parsed
}

func TestReplaceContentFollowsTheTablesOverTheResource(t *testing.T) {
	tk, _ := resourceToolkit(t)
	reg := &fakeTableRegistrar{followed: []string{followedSentence}}
	tk.SetTableRegistrar(reg)

	out := decodeResourceOutput(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionReplace, Reference: "mcp:resource:res1", Content: "day,high\nmon,88\n",
	}))

	assert.Equal(t, []string{followedSentence}, out.Tables)
	assert.Contains(t, out.Message, followedSentence, "the message says it too, for a caller reading only that")
	assert.Equal(t, []string{"resource:res1:2"}, reg.followedFor, "the version the writer recorded")
}

func TestReplaceContentWithoutARegistrarSaysNothingAboutTables(t *testing.T) {
	tk, _ := resourceToolkit(t)
	out := decodeResourceOutput(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionReplace, Reference: "mcp:resource:res1", Content: "day,high\nmon,88\n",
	}))
	assert.Nil(t, out.Tables)
}

// contentToolkit is an asset toolkit with a versioned asset and a registrar
// that reports one followed table.
func contentToolkit(t *testing.T) (*Toolkit, *fakeTableRegistrar, context.Context) {
	t.Helper()
	store := newInMemoryAssetStore()
	vs := newInMemoryVersionStore()
	s3 := &mockS3Client{getBody: []byte("a,b\n1,2\n"), getCT: "text/csv"}
	tk := New(Config{
		Name: "test", AssetStore: store, VersionStore: vs,
		S3Client: s3, S3Bucket: "bucket", S3Prefix: "assets/",
	})
	reg := &fakeTableRegistrar{followed: []string{followedSentence}}
	tk.SetTableRegistrar(reg)
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1"})
	require.NoError(t, store.Insert(ctx, portal.Asset{ID: "a1", OwnerID: "user1", ContentType: "text/csv", CurrentVersion: 1}))
	_, err := vs.CreateVersion(ctx, portal.AssetVersion{
		ID: "v1", AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "bucket", ContentType: "text/csv", SizeBytes: 8,
	})
	require.NoError(t, err)
	return tk, reg, ctx
}

func TestManageAssetContentWritesFollowTheTablesOverTheAsset(t *testing.T) {
	cases := []struct {
		name        string
		input       manageAssetInput
		wantVersion float64
	}{
		{"update", manageAssetInput{Action: "update", AssetID: "a1", Content: "a,b\n3,4\n"}, 2},
		{"patch", manageAssetInput{
			Action: "patch", AssetID: "a1",
			Edits: []textpatch.Edit{{Find: "1,2", Replace: "9,9"}},
		}, 2},
		{"revert", manageAssetInput{Action: "revert", AssetID: "a1", Version: 1}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tk, reg, ctx := contentToolkit(t)

			result, _, err := tk.handleManageAsset(ctx, nil, tc.input)
			require.NoError(t, err)
			parsed := decodeFollowResult(t, result)

			assert.Equal(t, tc.wantVersion, parsed["version"])
			assert.Equal(t, []any{followedSentence}, parsed["tables"])
			assert.Contains(t, parsed["message"], followedSentence)
			assert.Equal(t, []string{"asset:a1:2"}, reg.followedFor)
		})
	}
}

// TestManageAssetUpdateWithoutContentAsksNothing: a metadata-only update
// moves no head, so no table is asked about.
func TestManageAssetUpdateWithoutContentAsksNothing(t *testing.T) {
	tk, reg, ctx := contentToolkit(t)
	result, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{Action: "update", AssetID: "a1", Name: "Renamed"})
	require.NoError(t, err)
	parsed := decodeFollowResult(t, result)
	assert.NotContains(t, parsed, "tables")
	assert.Empty(t, reg.followedFor)
}

// TestSaveAssetWritesTheFirstVersionAndFollowsNothing: a new asset has no
// registration over it, and asking would be a read for nothing.
func TestSaveAssetWritesTheFirstVersionAndFollowsNothing(t *testing.T) {
	store := newInMemoryAssetStore()
	vs := newInMemoryVersionStore()
	tk := New(Config{Name: "test", AssetStore: store, VersionStore: vs, S3Client: &mockS3Client{}, S3Bucket: "bucket"})
	reg := &fakeTableRegistrar{followed: []string{followedSentence}}
	tk.SetTableRegistrar(reg)
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "user1", UserEmail: "u@example.com"})

	result, _, err := tk.handleSaveAsset(ctx, nil, saveAssetInput{Name: "new", Content: "a,b\n", ContentType: "text/csv"})
	require.NoError(t, err)
	require.False(t, result.IsError, result.Content)
	assert.Empty(t, reg.followedFor)
}

func TestRegisterTable_SaysWhichRuleTheTableWasMadeUnder(t *testing.T) {
	reg := &fakeTableRegistrar{}
	tk := tableToolkit(t, reg)

	// Saying nothing registers a table that follows the file.
	result, _, err := tk.handleManageTable(ownerCtx(), nil, manageTableInput{
		Action: tableActionRegister, Reference: assetReference, Connection: "scratch",
	})
	require.NoError(t, err)
	parsed := decodeFollowResult(t, result)
	assert.True(t, reg.lastFollow)
	assert.Equal(t, true, parsed["follow"])
	assert.Contains(t, parsed["message"], "The table follows the file")
	assert.Contains(t, parsed["message"], "follow=false")

	pinned := false
	result, _, err = tk.handleManageTable(ownerCtx(), nil, manageTableInput{
		Action: tableActionRegister, Reference: assetReference, Connection: "scratch", Follow: &pinned,
	})
	require.NoError(t, err)
	parsed = decodeFollowResult(t, result)
	assert.False(t, reg.lastFollow)
	assert.Equal(t, false, parsed["follow"])
	assert.Contains(t, parsed["message"], "pinned to this version of the file")
	assert.Contains(t, parsed["message"], "follow=true")
}
