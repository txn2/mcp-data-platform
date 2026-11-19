package portal

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/assetbulk"
	"github.com/txn2/mcp-data-platform/internal/portal/assetsimilar"
	"github.com/txn2/mcp-data-platform/internal/portal/assettags"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

func bulkFixture(t *testing.T) (*Toolkit, *inMemoryAssetStore, context.Context) {
	t.Helper()
	assets := newInMemoryAssetStore()
	assets.assets["mine1"] = portal.Asset{ID: "mine1", OwnerID: "user1", OwnerEmail: "user1@example.com", Name: "Mine one", Tags: []string{"Finance", "q3"}}
	assets.assets["mine2"] = portal.Asset{ID: "mine2", OwnerID: "user1", OwnerEmail: "user1@example.com", Name: "Mine two", Tags: []string{}}
	assets.assets["theirs"] = portal.Asset{ID: "theirs", OwnerID: "user2", OwnerEmail: "bob@example.com", Name: "Hidden report", Tags: []string{"secret"}}
	tk, ctx := newAllowlistToolkitOver(t, assets)
	return tk, assets, ctx
}

func decodeBulk(t *testing.T, res *mcp.CallToolResult) bulkResult {
	t.Helper()
	require.False(t, res.IsError, "%v", res.Content)
	var out bulkResult
	require.NoError(t, json.Unmarshal([]byte(bulkText(t, res)), &out))
	return out
}

func TestManageAssetBulk_AddTagsAppliesToOwnAndSkipsHidden(t *testing.T) {
	tk, assets, ctx := bulkFixture(t)
	res, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{
		Action: actionUpdate, AssetIDs: []string{"mine1", "mine2", "theirs", "gone", "mine1"}, AddTags: []string{"finance", "board"},
	})
	require.NoError(t, err)
	out := decodeBulk(t, res)
	assert.Equal(t, 4, out.Requested, "a repeated id is acted on once")
	assert.Equal(t, []string{"mine1", "mine2"}, out.Applied)
	require.Len(t, out.Skipped, 2)
	for _, s := range out.Skipped {
		assert.Equal(t, assetbulk.ReasonHidden, s.Reason)
	}
	text := bulkText(t, res)
	assert.NotContains(t, text, "Hidden report", "nothing of an asset the caller cannot open is returned")
	assert.NotContains(t, text, "secret")
	assert.Equal(t, []string{"Finance", "q3", "board"}, assets.assets["mine1"].Tags, "the asset's own spelling is kept")
	assert.Equal(t, []string{"finance", "board"}, assets.assets["mine2"].Tags)
	assert.Equal(t, []string{"secret"}, assets.assets["theirs"].Tags, "nothing changes on an asset the caller cannot open")
}

func TestManageAssetBulk_RemoveTagsOnOneAsset(t *testing.T) {
	tk, assets, ctx := bulkFixture(t)
	res, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{Action: actionUpdate, AssetID: "mine1", RemoveTags: []string{"FINANCE", "q3"}})
	require.NoError(t, err)
	out := decodeBulk(t, res)
	assert.Equal(t, []string{"mine1"}, out.Applied)
	assert.Equal(t, []string{}, assets.assets["mine1"].Tags, "removing every tag leaves an empty list")
}

func TestManageAssetBulk_DeleteReportsTheHandlersRefusal(t *testing.T) {
	tk, assets, ctx := bulkFixture(t)
	shared := assets.assets["theirs"]
	shared.Name = "Shared report"
	assets.assets["theirs"] = shared
	// Readable through a share, not deletable by a non-owner.
	tk.shareStore = &fakeShareStore{assetShare: &portal.Share{ID: "s1", Permission: portal.PermissionViewer}}
	res, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{Action: actionDelete, AssetIDs: []string{"mine2", "theirs"}})
	require.NoError(t, err)
	out := decodeBulk(t, res)
	assert.Equal(t, []string{"mine2"}, out.Applied)
	require.Len(t, out.Skipped, 1)
	assert.Equal(t, "theirs", out.Skipped[0].AssetID)
	assert.NotEqual(t, assetbulk.ReasonHidden, out.Skipped[0].Reason, "an asset the caller can open reports the handler's own refusal")
	assert.Contains(t, out.Message, "1 skipped")
}

func TestManageAssetBulk_Refusals(t *testing.T) {
	tk, _, ctx := bulkFixture(t)
	tooMany := make([]string, assetbulk.MaxAssets+1)
	for i := range tooMany {
		tooMany[i] = "a" + strings.Repeat("x", i)
	}
	for name, in := range map[string]manageAssetInput{
		"both":      {Action: actionDelete, AssetID: "mine1", AssetIDs: []string{"mine2"}},
		"action":    {Action: actionRevert, AssetIDs: []string{"mine1"}},
		"too many":  {Action: actionDelete, AssetIDs: tooMany},
		"content":   {Action: actionUpdate, AssetIDs: []string{"mine1"}, Content: "x"},
		"tags both": {Action: actionUpdate, AssetIDs: []string{"mine1"}, Tags: []string{"a"}, AddTags: []string{"b"}},
	} {
		t.Run(name, func(t *testing.T) {
			res, _, err := tk.handleManageAsset(ctx, nil, in)
			require.NoError(t, err)
			assert.True(t, res.IsError)
		})
	}
}

func TestWantsBulk(t *testing.T) {
	assert.True(t, wantsBulk(manageAssetInput{AssetIDs: []string{"a"}}))
	assert.True(t, wantsBulk(manageAssetInput{Action: actionUpdate, AssetID: "a", AddTags: []string{"t"}}))
	assert.False(t, wantsBulk(manageAssetInput{Action: actionUpdate, AssetID: "a", Tags: []string{"t"}}))
	assert.False(t, wantsBulk(manageAssetInput{Action: actionGet, AssetID: "a", AddTags: []string{"t"}}))
}

func TestSkipReason(t *testing.T) {
	assert.Equal(t, "refused", skipReason(&mcp.CallToolResult{}))
	assert.Equal(t, "no", skipReason(&mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: " no "}}}))
}

type fakeTagLister struct {
	page assettags.Page
	err  error
}

func (f fakeTagLister) List(context.Context, assettags.Query) (assettags.Page, error) {
	return f.page, f.err
}

type fakeSimilar struct {
	got     assetsimilar.NearestQuery
	matches []assetsimilar.Match
	err     error
}

func (f *fakeSimilar) Nearest(_ context.Context, q assetsimilar.NearestQuery) ([]assetsimilar.Match, error) {
	f.got = q
	return f.matches, f.err
}

type fixedEmbedder struct{ fail bool }

func (f fixedEmbedder) Embed(context.Context, string) ([]float32, error) {
	if f.fail {
		return nil, errors.New("down")
	}
	return []float32{0.1, 0.2}, nil
}

func (f fixedEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		v, err := f.Embed(ctx, texts[i])
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func (fixedEmbedder) Dimension() int { return 2 }
func (fixedEmbedder) Kind() string   { return "fixed" }

func TestSaveAsset_ReportsExistingTagsAndSimilarAssets(t *testing.T) {
	tk, _, ctx := bulkFixture(t)
	tk.embedder = fixedEmbedder{}
	similar := &fakeSimilar{matches: []assetsimilar.Match{{AssetID: "mine1", Name: "Mine one", OwnerEmail: "user1@example.com", Similarity: 0.97}}}
	tk.SetSaveHints(fakeTagLister{page: assettags.Page{Tags: []assettags.Tag{{Tag: "finance", Count: 4}}}}, similar)

	res, _, err := tk.handleSaveAsset(ctx, nil, saveAssetInput{Name: "Mine one", Content: "# Q3", ContentType: "text/markdown", Tags: []string{"Finance"}})
	require.NoError(t, err)
	require.False(t, res.IsError, "%v", res.Content)
	var out saveAssetOutput
	require.NoError(t, json.Unmarshal([]byte(bulkText(t, res)), &out))
	assert.Equal(t, []assettags.Variant{{Tag: "finance", Count: 4, CloseTo: "Finance"}}, out.ExistingTags)
	assert.Equal(t, similar.matches, out.SimilarAssets)
	assert.Equal(t, []string{out.AssetID}, similar.got.Exclude, "the asset just saved is never its own duplicate")
	require.NotNil(t, similar.got.Reach)
	assert.Contains(t, out.Message, "Existing tags close to the ones given")
	assert.Contains(t, out.Message, "same work as")
}

func TestSaveAsset_HintsNeverRefuseTheSave(t *testing.T) {
	tk, assets, ctx := bulkFixture(t)
	tk.embedder = fixedEmbedder{fail: true}
	tk.SetSaveHints(fakeTagLister{err: errors.New("db down")}, &fakeSimilar{err: errors.New("db down")})
	res, _, err := tk.handleSaveAsset(ctx, nil, saveAssetInput{Name: "N", Content: "# x", ContentType: "text/markdown", Tags: []string{"Finance"}})
	require.NoError(t, err)
	require.False(t, res.IsError)
	var out saveAssetOutput
	require.NoError(t, json.Unmarshal([]byte(bulkText(t, res)), &out))
	assert.Empty(t, out.ExistingTags)
	assert.Empty(t, out.SimilarAssets)
	assert.Contains(t, assets.assets, out.AssetID, "the asset is saved whatever the lookups do")

	tk.embedder = fixedEmbedder{}
	res, _, err = tk.handleSaveAsset(ctx, nil, saveAssetInput{Name: "N", Content: "# x", ContentType: "text/markdown"})
	require.NoError(t, err)
	require.False(t, res.IsError)
}

func TestSaveAsset_NoHintsWithoutCallerOrConfig(t *testing.T) {
	tk, _, _ := bulkFixture(t)
	out := saveAssetOutput{}
	tk.addSaveHints(context.Background(), &portal.Asset{ID: "a", Tags: []string{"x"}}, &out)
	assert.Empty(t, out.Message, "no configured hints, no notice")
}

func TestHintNotice(t *testing.T) {
	assert.Empty(t, hintNotice(nil, nil))
}

// bulkText is a result's first text block, failing the test when it has none.
func bulkText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, res.Content)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok, "the first content block is not text")
	return tc.Text
}
