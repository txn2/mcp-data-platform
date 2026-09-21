package scriptexec

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// draftStores are the stores a draft handle writes through, kept so a test
// can read what was written.
type draftStores struct {
	assets   *fakeAssets
	versions *fakeVersionStore
	lander   *fakeLander
}

// draftHandle is an execution handle over the writer harness's stores, the
// shape a composition root hands a draft (#1822).
func draftHandle(t *testing.T) (*Handle, draftStores) {
	t.Helper()
	st := draftStores{assets: newFakeAssets(), versions: newFakeVersionStore(), lander: newFakeLander()}
	return &Handle{export: ExportDeps{
		Assets: st.assets, Versions: st.versions, S3: newFakeS3(), Bucket: "assets", Prefix: "portal",
		Lander: st.lander,
	}}, st
}

// janeDraft is a draft of the saved script executableState describes, run by
// its owner.
func janeDraft(sc *script.Script) Draft {
	return Draft{
		Script: sc, RunID: "dpx_draft", Email: "jane@example.com", Subject: "sub-jane",
		Roles: []string{"analyst"}, Caller: &fakeCaller{},
	}
}

func TestDraftExporter_NothingToWriteThrough(t *testing.T) {
	var none *Handle
	assert.Nil(t, none.DraftExporter(Draft{Script: &script.Script{Name: "x"}}),
		"a deployment with nowhere to keep runs has no writer, and its drafts preview")
	h, _ := draftHandle(t)
	assert.Nil(t, h.DraftExporter(Draft{}), "a draft with no script has nothing to file outputs under")
}

// TestDraftExporter_WritesALibraryOutputForThePersonDrafting: the file lands
// in the library of the person running the draft, acting for them as a run
// acts for its author, and its version says a draft wrote it.
func TestDraftExporter_WritesALibraryOutputForThePersonDrafting(t *testing.T) {
	h, st := draftHandle(t)
	lander := st.lander
	sc, _, _ := executableState()
	w := h.DraftExporter(janeDraft(sc))
	require.NotNil(t, w)

	written, err := w.Export(context.Background(), libraryRequest("orders", "datasets/orders.csv"))
	require.NoError(t, err)
	assert.Equal(t, "mcp:resource:res-datasets/orders.csv", written.ResourceRef)
	require.Len(t, lander.claims, 1)
	assert.Equal(t, "jane@example.com", lander.claims[0].OnBehalfOf)
	assert.Equal(t, []string{"analyst"}, lander.claims[0].Roles)
	assert.Equal(t, "daily draft, run dpx_draft", lander.landed[0].ChangeSummary)

	_, err = w.Export(context.Background(), libraryRequest("orders", "datasets/orders.csv"))
	require.Error(t, err, "a draft refuses a second write of one output as a run does")
}

// TestDraftExporter_VersionsASavedScriptsPortalOutput: a saved script's portal
// output is a version of its own asset, and the version says a draft wrote it.
func TestDraftExporter_VersionsASavedScriptsPortalOutput(t *testing.T) {
	h, st := draftHandle(t)
	assets, versions := st.assets, st.versions
	sc, _, _ := executableState()
	written, err := h.DraftExporter(janeDraft(sc)).Export(context.Background(), csvRequest("daily-sales"))
	require.NoError(t, err)
	require.NotEmpty(t, written.AssetID)
	require.Len(t, assets.byKey, 1)
	require.Len(t, versions.created, 1)
	assert.Equal(t, "daily draft, run dpx_draft", versions.created[0].ChangeSummary)
}

// TestDraftExporter_RefusesThePortalForAnUnsavedScript: a script with no id
// has no asset series, so the portal output and a data-region refresh are
// refused naming the remedy, while the library is open to it.
func TestDraftExporter_RefusesThePortalForAnUnsavedScript(t *testing.T) {
	h, st := draftHandle(t)
	assets, lander := st.assets, st.lander
	w := h.DraftExporter(janeDraft(&script.Script{Name: "brand-new", OwnerEmail: "jane@example.com"}))

	_, err := w.Export(context.Background(), csvRequest("daily-sales"))
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "not saved yet") && strings.Contains(err.Error(), `"resources"`), err.Error())
	_, err = w.PublishData(context.Background(), scriptrun.PublishRequest{Name: "daily-sales", Data: map[string]any{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not saved yet")
	assert.Empty(t, assets.byKey, "nothing was filed")

	_, err = w.Export(context.Background(), libraryRequest("orders", "datasets/orders.csv"))
	require.NoError(t, err)
	assert.Len(t, lander.landed, 1)
}

// TestDraftExporter_PublishDataOfASavedScriptReachesTheWriter: a saved
// script's refresh goes to the writer, which answers as it does for a run.
func TestDraftExporter_PublishDataOfASavedScriptReachesTheWriter(t *testing.T) {
	h, _ := draftHandle(t)
	sc, _, _ := executableState()
	_, err := h.DraftExporter(janeDraft(sc)).PublishData(context.Background(),
		scriptrun.PublishRequest{Name: "never-exported", Data: map[string]any{"a": 1}})
	require.Error(t, err, "a refresh of an output never exported is refused, as a run refuses it")
	assert.NotContains(t, err.Error(), "not saved yet")
}

// Verify the fake stores still satisfy the portal contracts the draft writer
// is built over.
var (
	_ portal.AssetStore   = (*fakeAssets)(nil)
	_ portal.VersionStore = (*fakeVersionStore)(nil)
)
