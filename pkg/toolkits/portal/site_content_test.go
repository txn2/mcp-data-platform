package portal

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

type recordedSiteContent struct {
	in   SiteContentInput
	seen int
}

func (r *recordedSiteContent) Handle(_ context.Context, in SiteContentInput) (*mcp.CallToolResult, any, error) {
	r.in, r.seen = in, r.seen+1
	return &mcp.CallToolResult{}, nil, nil
}

func firstText(res *mcp.CallToolResult) string {
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

func TestManageAsset_ASiteReferenceGoesToTheSiteLayer(t *testing.T) {
	seen := &recordedSiteContent{}
	tk := &Toolkit{siteContent: seen}
	edits := []textpatch.Edit{{}}
	_, _, err := tk.handleManageAsset(context.Background(), nil, manageAssetInput{
		Action: actionPatch, AssetID: "mcp:site:shop/default/index.html", BaseVersion: 4, DryRun: true, ChangeSummary: "s",
		Find: "f", Pattern: "p", Section: "h", Selector: "#a", Occurrence: "1", LineStart: 2, LineEnd: 3, ContextBytes: 9, Limit: 5, Offset: 6,
		FromVersion: 7, ToVersion: 8, Edits: edits,
	})
	require.NoError(t, err)
	require.Equal(t, 1, seen.seen)
	assert.Equal(t, SiteContentInput{
		Action: actionPatch, Target: "mcp:site:shop/default/index.html", BaseVersion: 4, DryRun: true, ChangeSummary: "s",
		Find: "f", Pattern: "p", Section: "h", Selector: "#a", Occurrence: "1", LineStart: 2, LineEnd: 3, ContextBytes: 9, Limit: 5, Offset: 6,
		FromVersion: 7, ToVersion: 8, Edits: edits,
	}, seen.in, "every argument the content actions read reaches the site layer")
}

func TestSetSiteContent(t *testing.T) {
	seen := &recordedSiteContent{}
	tk := &Toolkit{}
	tk.SetSiteContent(seen)
	_, _, err := tk.handleManageAsset(context.Background(), nil, manageAssetInput{Action: actionOutline, AssetID: "mcp:site:shop/default"})
	require.NoError(t, err)
	assert.Equal(t, 1, seen.seen, "what is bound is what answers")
}

func TestManageAsset_AnAssetIDNeverReachesTheSiteLayer(t *testing.T) {
	seen := &recordedSiteContent{}
	tk := &Toolkit{siteContent: seen, actions: map[string]manageActionHandler{}}
	res, _, err := tk.handleManageAsset(context.Background(), nil, manageAssetInput{Action: "nope", AssetID: "ast_1"})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Zero(t, seen.seen)
}

func TestManageAsset_ASiteReferenceWithNoSitesOrWithAssetArguments(t *testing.T) {
	ctx := context.Background()
	const ref = "mcp:site:shop/default/index.html"

	res, _, err := (&Toolkit{}).handleManageAsset(ctx, nil, manageAssetInput{Action: actionGetContent, AssetID: ref})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, firstText(res), "sites are not available")

	seen := &recordedSiteContent{}
	tk := &Toolkit{siteContent: seen}
	for _, in := range []manageAssetInput{
		{Action: actionPatch, AssetID: ref, References: []string{"mcp:asset:a"}},
		{Action: actionPatch, AssetID: ref, Sources: []string{"mcp:call:a"}},
	} {
		res, _, err := tk.handleManageAsset(ctx, nil, in)
		require.NoError(t, err)
		assert.True(t, res.IsError)
		assert.Contains(t, firstText(res), "apply to assets")
	}
	assert.Zero(t, seen.seen)
}
