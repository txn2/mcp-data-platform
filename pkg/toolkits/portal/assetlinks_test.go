package portal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/portal/contentrefs"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

const linkedTarget = "mcp:asset:ast_data"

// linkToolkit is refToolkit with its version store in hand, so a test can
// assert that a refused write created no version.
func linkToolkit(t *testing.T) (*Toolkit, *inMemoryAssetStore, *inMemoryVersionStore) {
	t.Helper()
	tk, assets, _ := refToolkit(t)
	versions := newInMemoryVersionStore()
	tk.versionStore = versions
	return tk, assets, versions
}

// TestSaveRefusesAReferenceUsedAsALink covers #1875's refusal on save_asset:
// an HTML <a href> and a Markdown [text](ref) link to a reference are refused
// naming the reference, saying links are not supported, and nothing is stored.
func TestSaveRefusesAReferenceUsedAsALink(t *testing.T) {
	for name, in := range map[string]saveAssetInput{
		"html anchor":   {Name: "Report", ContentType: "text/html", Content: `<a href="` + linkedTarget + `">data</a>`},
		"markdown link": {Name: "Notes", ContentType: "text/markdown", Content: "see [the data](" + linkedTarget + ")"},
	} {
		t.Run(name, func(t *testing.T) {
			tk, assets, versions := linkToolkit(t)
			result, _, err := tk.handleSaveAsset(refCtx(""), nil, in)
			require.NoError(t, err)

			msg := refusalText(t, result)
			assert.Contains(t, msg, linkedTarget)
			assert.Contains(t, msg, "links between assets are not supported")
			assert.Contains(t, msg, "name it in text")
			assert.Empty(t, assets.assets, "a refused save creates no asset")
			assert.Empty(t, versions.versions, "a refused save creates no version")
		})
	}
}

// TestSaveReportsAnUndeclaredLoad covers the report: a body loading a
// reference it does not declare saves, and names the reference with the run
// log's consequence.
func TestSaveReportsAnUndeclaredLoad(t *testing.T) {
	tk, _, _ := linkToolkit(t)
	result, _, err := tk.handleSaveAsset(refCtx(""), nil, saveAssetInput{
		Name: "Report", ContentType: "text/html",
		Content:    `<img src="` + linkedTarget + `"><img src="` + refLogoURI + `">`,
		References: []string{refLogoURI},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	out := decodeSave(t, result)
	assert.Equal(t, []string{linkedTarget}, out.UndeclaredReferences)
	assert.Contains(t, out.Message, linkedTarget)
	assert.Contains(t, out.Message, contentrefs.UndeclaredConsequence)
}

// TestSaveWithEveryLoadDeclaredReportsNone keeps a correct save's result as
// it was: no undeclared_references key at all.
func TestSaveWithEveryLoadDeclaredReportsNone(t *testing.T) {
	tk, _, _ := linkToolkit(t)
	result, _, err := tk.handleSaveAsset(refCtx(""), nil, saveAssetInput{
		Name: "Report", ContentType: "text/html",
		Content: `<img src="` + refLogoURI + `">`, References: []string{refLogoURI},
	})
	require.NoError(t, err)
	assert.NotContains(t, decodeMap(t, result), fieldUndeclaredReferences)
}

// TestUpdateRefusesALinkAndWritesNothing covers manage_asset update: the
// refusal comes before the metadata write as well as the content write.
func TestUpdateRefusesALinkAndWritesNothing(t *testing.T) {
	tk, assets, versions := linkToolkit(t)
	saved := decodeSave(t, mustSave(t, tk, nil))
	before := len(versions.versions[saved.AssetID])

	result, _, err := tk.handleUpdate(refCtx(""), manageAssetInput{
		Action: "update", AssetID: saved.AssetID, Name: "Renamed",
		Content: `<p><a href='` + linkedTarget + `'>x</a></p>`,
	})
	require.NoError(t, err)
	assert.Contains(t, refusalText(t, result), linkedTarget)
	assert.Len(t, versions.versions[saved.AssetID], before)
	asset, getErr := assets.Get(context.Background(), saved.AssetID)
	require.NoError(t, getErr)
	assert.Equal(t, "Report", asset.Name, "a refused update changes no metadata either")
}

// TestUpdateJudgesUndeclaredAgainstTheReferencesInEffect proves the report is
// read against what the asset declares after the call: the existing
// declaration when the update passes none, and the new list when it does.
func TestUpdateJudgesUndeclaredAgainstTheReferencesInEffect(t *testing.T) {
	tk, _, _ := linkToolkit(t)
	saved := decodeSave(t, mustSave(t, tk, []string{refLogoURI}))
	body := `<img src="` + refLogoURI + `"><img src="` + linkedTarget + `">`

	kept, _, err := tk.handleUpdate(refCtx(""), manageAssetInput{
		Action: "update", AssetID: saved.AssetID, Content: body,
	})
	require.NoError(t, err)
	require.False(t, kept.IsError)
	assert.Equal(t, []any{linkedTarget}, decodeMap(t, kept)[fieldUndeclaredReferences],
		"the logo is declared already, so only the asset is reported")

	cleared, _, err := tk.handleUpdate(refCtx(""), manageAssetInput{
		Action: "update", AssetID: saved.AssetID, Content: body, References: []string{},
	})
	require.NoError(t, err)
	require.False(t, cleared.IsError)
	assert.Equal(t, []any{refLogoURI, linkedTarget}, decodeMap(t, cleared)[fieldUndeclaredReferences],
		"an empty list clears the declaration, so both are reported")
}

// TestUpdateOfMetadataAloneReportsNothing: an update without content has no
// body to read, and says nothing about references it did not look at.
func TestUpdateOfMetadataAloneReportsNothing(t *testing.T) {
	tk, _, _ := linkToolkit(t)
	saved := decodeSave(t, mustSave(t, tk, nil))
	result, _, err := tk.handleUpdate(refCtx(""), manageAssetInput{
		Action: "update", AssetID: saved.AssetID, Name: "Renamed",
	})
	require.NoError(t, err)
	assert.NotContains(t, decodeMap(t, result), fieldUndeclaredReferences)
}

// TestPatchRefusesALinkItWrites covers manage_asset patch, for a real write
// and a dry run alike: the patched body is what is judged.
func TestPatchRefusesALinkItWrites(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		tk, assets, versions := linkToolkit(t)
		saved := decodeSave(t, mustSave(t, tk, nil))
		before := len(versions.versions[saved.AssetID])
		tk.s3Client = &mockS3Client{getBody: []byte("# Q4\n"), getCT: "text/markdown"}
		asset, err := assets.Get(context.Background(), saved.AssetID)
		require.NoError(t, err)
		asset.ContentType = "text/markdown"
		assets.assets[saved.AssetID] = *asset

		result, _, err := tk.handlePatch(refCtx(""), manageAssetInput{
			Action: "patch", AssetID: saved.AssetID, DryRun: dryRun,
			Edits: []textpatch.Edit{{Op: textpatch.OpInsertAfter, Find: "# Q4", Text: "\n[data](" + linkedTarget + ")"}},
		})
		require.NoError(t, err)
		assert.Contains(t, refusalText(t, result), linkedTarget, "dry_run=%v", dryRun)
		assert.Len(t, versions.versions[saved.AssetID], before)
	}
}

// TestPatchReportsAnUndeclaredLoad covers the report on patch, a dry run
// included, judged against the asset's existing declaration.
func TestPatchReportsAnUndeclaredLoad(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		tk, _, _ := linkToolkit(t)
		saved := decodeSave(t, mustSave(t, tk, []string{refLogoURI}))
		tk.s3Client = &mockS3Client{getBody: []byte(`<h1>Q4</h1><img src="` + refLogoURI + `">`), getCT: "text/html"}

		result, _, err := tk.handlePatch(refCtx(""), manageAssetInput{
			Action: "patch", AssetID: saved.AssetID, DryRun: dryRun,
			Edits: []textpatch.Edit{{Op: textpatch.OpInsertAfter, Find: "</h1>", Text: `<img src="` + linkedTarget + `">`}},
		})
		require.NoError(t, err)
		require.False(t, result.IsError)
		out := decodeMap(t, result)
		assert.Equal(t, []any{linkedTarget}, out[fieldUndeclaredReferences], "dry_run=%v", dryRun)
		assert.Contains(t, out[fieldMessage], contentrefs.UndeclaredConsequence)
	}
}

// TestUndeclaredRefsWhenTheDeclarationCannotBeRead reports nothing rather than
// name a reference undeclared that may be declared.
func TestUndeclaredRefsWhenTheDeclarationCannotBeRead(t *testing.T) {
	tk, _, _ := linkToolkit(t)
	tk.SetContentRefs(newDeclarer(unreadableRefStore{}, newInMemoryAssetStore()))
	assert.Nil(t, tk.undeclaredRefs(refCtx(""), "a1", `<img src="`+linkedTarget+`">`, nil))
	assert.Equal(t, []string{linkedTarget},
		tk.undeclaredRefs(refCtx(""), "a1", `<img src="`+linkedTarget+`">`, []string{}),
		"a passed list needs no read")
}

// unreadableRefStore fails every read of an asset's references.
type unreadableRefStore struct{ failingRefStore }

func (unreadableRefStore) ListByAsset(context.Context, string) ([]assetrefs.Ref, error) {
	return nil, assert.AnError
}
