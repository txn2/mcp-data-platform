package portal

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

const (
	refLogoURI  = "mcp://global/brand/logo.png"
	refChartURI = "mcp://persona/finance/chart.png"
	refAuthor   = "user1@example.com"
)

// refStoreStub is an in-memory AssetResourceRefStore.
type refStoreStub struct {
	byAsset map[string][]portaldomain.AssetResourceRef
}

func newRefStoreStub() *refStoreStub {
	return &refStoreStub{byAsset: map[string][]portaldomain.AssetResourceRef{}}
}

func (s *refStoreStub) Replace(_ context.Context, id string, refs []portaldomain.AssetResourceRef) error {
	s.byAsset[id] = refs
	return nil
}

func (s *refStoreStub) ListByAsset(_ context.Context, id string) ([]portaldomain.AssetResourceRef, error) {
	return s.byAsset[id], nil
}

func (s *refStoreStub) Attach(_ context.Context, ref portaldomain.AssetResourceRef) (bool, error) {
	s.byAsset[ref.AssetID] = append(s.byAsset[ref.AssetID], ref)
	return true, nil
}

func (*refStoreStub) Detach(context.Context, string, string) (bool, error) { return false, nil }

func (s *refStoreStub) ListByResource(_ context.Context, resourceID string, _ int) ([]portaldomain.AssetResourceRef, error) {
	var out []portaldomain.AssetResourceRef
	for _, refs := range s.byAsset {
		for _, ref := range refs {
			if ref.ResourceID == resourceID {
				out = append(out, ref)
			}
		}
	}
	return out, nil
}

func (s *refStoreStub) GetByToken(_ context.Context, id, token string) (*portaldomain.AssetResourceRef, error) {
	for _, ref := range s.byAsset[id] {
		if ref.RefToken == token {
			return &ref, nil
		}
	}
	return nil, nil //nolint:nilnil // interface contract: no such reference is (nil, nil)
}

// resourceStub resolves the global logo and a finance-only chart.
type resourceStub struct{}

func (resourceStub) Get(_ context.Context, id string) (*resource.Resource, error) {
	for _, res := range stubResources() {
		if res.ID == id {
			return res, nil
		}
	}
	return nil, nil //nolint:nilnil // resource.Store reports a missing row as (nil, nil)
}

func (resourceStub) GetByIDs(_ context.Context, ids []string) (map[string]*resource.Resource, error) {
	out := make(map[string]*resource.Resource, len(ids))
	for _, id := range ids {
		for _, res := range stubResources() {
			if res.ID == id {
				out[id] = res
			}
		}
	}
	return out, nil
}

func (resourceStub) GetByURI(_ context.Context, uri string) (*resource.Resource, error) {
	for _, res := range stubResources() {
		if res.URI == uri {
			return res, nil
		}
	}
	return nil, nil //nolint:nilnil // resource.Store reports a missing row as (nil, nil)
}

func stubResources() []*resource.Resource {
	return []*resource.Resource{
		{
			ID: "res-logo", Scope: resource.ScopeGlobal, URI: refLogoURI,
			Filename: "logo.png", MIMEType: "image/png", S3Key: "resources/global/logo.png",
		},
		{
			ID: "res-chart", Scope: resource.ScopePersona, ScopeID: "finance", URI: refChartURI,
			Filename: "chart.png", MIMEType: "image/png", S3Key: "resources/persona/finance/chart.png",
		},
	}
}

// refToolkit builds a toolkit with the reference-declaration path bound, plus
// the stores a save needs.
func refToolkit(t *testing.T) (*Toolkit, *inMemoryAssetStore, *refStoreStub) {
	t.Helper()
	assets := newInMemoryAssetStore()
	refs := newRefStoreStub()
	tk := New(Config{
		Name: "test", AssetStore: assets, VersionStore: newInMemoryVersionStore(),
		S3Client: &mockS3Client{}, S3Bucket: "bucket", BaseURL: "http://example.com",
	})
	tk.SetResourceRefs(assetrefs.NewDeclarer(refs, resourceStub{}, ""))
	return tk, assets, refs
}

// refCtx is an authenticated caller, optionally inside a persona.
func refCtx(persona string) context.Context {
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "user1", UserEmail: refAuthor, SessionID: "sess1", PersonaName: persona,
	})
}

func decodeSave(t *testing.T, result *mcp.CallToolResult) saveAssetOutput {
	t.Helper()
	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var out saveAssetOutput
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	return out
}

func decodeMap(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	out := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	return out
}

// refusalText asserts the result is an error and returns its message.
func refusalText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.True(t, result.IsError)
	return errText(t, result)
}

// TestSaveDeclaresReferencesAndStatesTheGrant is the save-side acceptance
// criterion: the URI stays in the stored content, the reference is recorded,
// and the response names what declaring it gave away.
func TestSaveDeclaresReferencesAndStatesTheGrant(t *testing.T) {
	tk, assets, refs := refToolkit(t)
	body := `<img src="` + refLogoURI + `">`

	result, _, err := tk.handleSaveAsset(refCtx(""), nil, saveAssetInput{
		Name: "Report", Content: body, ContentType: "text/html",
		Resources: []string{refLogoURI},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	out := decodeSave(t, result)
	assert.Equal(t, 1, out.ResourcesReferenced)
	assert.Equal(t, assetrefs.GrantNotice, out.ResourceGrant)

	stored := refs.byAsset[out.AssetID]
	require.Len(t, stored, 1)
	assert.Equal(t, "res-logo", stored[0].ResourceID)
	assert.Equal(t, refLogoURI, stored[0].URI)
	assert.Equal(t, refAuthor, stored[0].DeclaredBy)

	asset, getErr := assets.Get(context.Background(), out.AssetID)
	require.NoError(t, getErr)
	assert.Equal(t, int64(len(body)), asset.SizeBytes, "the asset stores the URI, not the file")
}

// TestSaveWithoutReferencesReportsNothing keeps the response a save already had
// unchanged: a save that declared no resources reads exactly as it did before.
func TestSaveWithoutReferencesReportsNothing(t *testing.T) {
	tk, _, refs := refToolkit(t)

	result, _, err := tk.handleSaveAsset(refCtx(""), nil, saveAssetInput{
		Name: "Report", Content: "<p>plain</p>", ContentType: "text/html",
	})
	require.NoError(t, err)
	out := decodeSave(t, result)

	assert.Zero(t, out.ResourcesReferenced)
	assert.Empty(t, out.ResourceGrant)
	assert.Empty(t, refs.byAsset)
}

// TestSaveRefusedByPermissionCreatesNothing is the acceptance criterion for an
// unreadable resource, and for the ordering that makes it meaningful: the
// refusal names the URI and no asset is left behind.
func TestSaveRefusedByPermissionCreatesNothing(t *testing.T) {
	tk, assets, refs := refToolkit(t)

	result, _, err := tk.handleSaveAsset(refCtx(""), nil, saveAssetInput{
		Name: "Report", Content: `<img src="` + refChartURI + `">`, ContentType: "text/html",
		Resources: []string{refChartURI},
	})
	require.NoError(t, err)

	assert.Contains(t, refusalText(t, result), refChartURI)
	assert.Empty(t, assets.assets, "a refused declaration must leave no asset behind")
	assert.Empty(t, refs.byAsset)
}

// TestSaveAdmitsThePersonaThatOwnsTheResource is the other half: the check
// withholds from outsiders without withholding from the audience.
func TestSaveAdmitsThePersonaThatOwnsTheResource(t *testing.T) {
	tk, _, refs := refToolkit(t)

	result, _, err := tk.handleSaveAsset(refCtx("finance"), nil, saveAssetInput{
		Name: "Report", Content: `<img src="` + refChartURI + `">`, ContentType: "text/html",
		Resources: []string{refChartURI},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	out := decodeSave(t, result)
	assert.Equal(t, 1, out.ResourcesReferenced)
	require.Len(t, refs.byAsset[out.AssetID], 1)
}

// TestSaveRefusesAboveTheCap is the acceptance criterion for the bound: the
// refusal states the cap.
func TestSaveRefusesAboveTheCap(t *testing.T) {
	tk, _, _ := refToolkit(t)
	uris := make([]string, portaldomain.MaxAssetResourceRefs+1)
	for i := range uris {
		uris[i] = refLogoURI
	}

	result, _, err := tk.handleSaveAsset(refCtx(""), nil, saveAssetInput{
		Name: "Report", Content: "<p>x</p>", ContentType: "text/html", Resources: uris,
	})
	require.NoError(t, err)
	assert.Contains(t, refusalText(t, result), "20")
}

// TestDeclarationRefusedWithoutAManagedResourceLayer proves an author is never
// told a reference was recorded when there was nowhere to record it.
func TestDeclarationRefusedWithoutAManagedResourceLayer(t *testing.T) {
	tk := New(Config{
		Name: "test", AssetStore: newInMemoryAssetStore(), VersionStore: newInMemoryVersionStore(),
		S3Client: &mockS3Client{}, S3Bucket: "bucket",
	})

	result, _, err := tk.handleSaveAsset(refCtx(""), nil, saveAssetInput{
		Name: "Report", Content: "<p>x</p>", ContentType: "text/html",
		Resources: []string{refLogoURI},
	})
	require.NoError(t, err)
	assert.Contains(t, refusalText(t, result), "no managed-resource layer")
}

// TestUpdateReplacesReferences proves a save declares the whole list: naming
// one where the asset named another drops the one no longer named.
func TestUpdateReplacesReferences(t *testing.T) {
	tk, _, refs := refToolkit(t)
	saved := decodeSave(t, mustSave(t, tk, []string{refLogoURI}))

	result, _, err := tk.handleUpdate(refCtx("finance"), manageAssetInput{
		Action: "update", AssetID: saved.AssetID, Resources: []string{refChartURI},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	stored := refs.byAsset[saved.AssetID]
	require.Len(t, stored, 1)
	assert.Equal(t, "res-chart", stored[0].ResourceID)
	assert.Equal(t, float64(1), decodeMap(t, result)[fieldResourcesReferenced])
}

// TestUpdateWithEmptyListRemovesEveryReference proves an empty list is a
// decision rather than an absent argument, which is how an author removes one.
func TestUpdateWithEmptyListRemovesEveryReference(t *testing.T) {
	tk, _, refs := refToolkit(t)
	saved := decodeSave(t, mustSave(t, tk, []string{refLogoURI}))

	result, _, err := tk.handleUpdate(refCtx(""), manageAssetInput{
		Action: "update", AssetID: saved.AssetID, Resources: []string{},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Empty(t, refs.byAsset[saved.AssetID])
	assert.Equal(t, float64(0), decodeMap(t, result)[fieldResourcesReferenced])
	assert.NotContains(t, decodeMap(t, result), fieldResourceGrant,
		"removing every reference grants nothing and must say nothing about a grant")
}

// TestUpdateWithoutResourcesLeavesThemAlone is the distinction the whole
// argument turns on: a write that never mentioned resources has decided
// nothing about them.
func TestUpdateWithoutResourcesLeavesThemAlone(t *testing.T) {
	tk, _, refs := refToolkit(t)
	saved := decodeSave(t, mustSave(t, tk, []string{refLogoURI}))
	newName := "Renamed"

	result, _, err := tk.handleUpdate(refCtx(""), manageAssetInput{
		Action: "update", AssetID: saved.AssetID, Name: newName,
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.Len(t, refs.byAsset[saved.AssetID], 1)
	assert.NotContains(t, decodeMap(t, result), fieldResourcesReferenced)
}

// TestUpdateWithOnlyResourcesIsAValidWrite proves declaring references is
// itself a change: an author who only adds a reference must not be told there
// is nothing to update.
func TestUpdateWithOnlyResourcesIsAValidWrite(t *testing.T) {
	tk, _, refs := refToolkit(t)
	saved := decodeSave(t, mustSave(t, tk, nil))

	result, _, err := tk.handleUpdate(refCtx(""), manageAssetInput{
		Action: "update", AssetID: saved.AssetID, Resources: []string{refLogoURI},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Len(t, refs.byAsset[saved.AssetID], 1)
}

// TestUpdateRefusedByPermissionChangesNothing proves the declaration is
// validated before any write: a refused update leaves the metadata alone.
func TestUpdateRefusedByPermissionChangesNothing(t *testing.T) {
	tk, assets, refs := refToolkit(t)
	saved := decodeSave(t, mustSave(t, tk, []string{refLogoURI}))

	result, _, err := tk.handleUpdate(refCtx(""), manageAssetInput{
		Action: "update", AssetID: saved.AssetID, Name: "Renamed",
		Resources: []string{refChartURI},
	})
	require.NoError(t, err)
	assert.Contains(t, refusalText(t, result), refChartURI)

	asset, getErr := assets.Get(context.Background(), saved.AssetID)
	require.NoError(t, getErr)
	assert.Equal(t, "Report", asset.Name, "the metadata write must not have run")
	require.Len(t, refs.byAsset[saved.AssetID], 1)
	assert.Equal(t, "res-logo", refs.byAsset[saved.AssetID][0].ResourceID)
}

// TestUpdateNothingToDoStillNamesResources keeps the empty-update message
// truthful now that resources are one of the things an update can carry.
func TestUpdateNothingToDoStillNamesResources(t *testing.T) {
	tk, _, _ := refToolkit(t)
	saved := decodeSave(t, mustSave(t, tk, nil))

	result, _, err := tk.handleUpdate(refCtx(""), manageAssetInput{
		Action: "update", AssetID: saved.AssetID,
	})
	require.NoError(t, err)
	assert.Contains(t, refusalText(t, result), "resources")
}

// mustSave saves one HTML asset declaring uris and returns the result.
func mustSave(t *testing.T, tk *Toolkit, uris []string) *mcp.CallToolResult {
	t.Helper()
	persona := ""
	for _, u := range uris {
		if u == refChartURI {
			persona = "finance"
		}
	}
	result, _, err := tk.handleSaveAsset(refCtx(persona), nil, saveAssetInput{
		Name: "Report", Content: "<h1>Q4</h1>", ContentType: "text/html", Resources: uris,
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	return result
}

// TestRefClaimsFromAnUnauthenticatedCall proves a call carrying no identity
// resolves as nobody, which reaches only global resources -- the set an
// unauthenticated reader already sees.
func TestRefClaimsFromAnUnauthenticatedCall(t *testing.T) {
	claims := refClaims(context.Background())
	assert.Empty(t, claims.Sub)
	assert.Empty(t, claims.Personas)

	withCtx := refClaims(refCtx("finance"))
	assert.Equal(t, "user1", withCtx.Sub)
	assert.Equal(t, []string{"finance"}, withCtx.Personas)
}

// TestPatchDeclaresTheReferenceItWrites is why the patch action takes the
// argument: an agent that patches in an <img> pointing at a managed resource
// declares it in the same call, so the URI resolves on the first render rather
// than on a follow-up update.
func TestPatchDeclaresTheReferenceItWrites(t *testing.T) {
	tk, _, refs := refToolkit(t)
	saved := decodeSave(t, mustSave(t, tk, nil))
	tk.s3Client = &mockS3Client{getBody: []byte("<h1>Q4</h1>"), getCT: "text/html"}

	result, _, err := tk.handlePatch(refCtx(""), manageAssetInput{
		Action: "patch", AssetID: saved.AssetID,
		Edits: []textpatch.Edit{{
			Op: textpatch.OpInsertAfter, Find: "</h1>", Text: `<img src="` + refLogoURI + `">`,
		}},
		Resources: []string{refLogoURI},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	assert.Equal(t, float64(1), decodeMap(t, result)[fieldResourcesReferenced])
	assert.Equal(t, assetrefs.GrantNotice, decodeMap(t, result)[fieldResourceGrant])
	require.Len(t, refs.byAsset[saved.AssetID], 1)
}

// TestPatchRefusedByPermissionLeavesContentAlone proves the declaration is
// checked before the patch is applied, so a refused reference costs no version.
func TestPatchRefusedByPermissionLeavesContentAlone(t *testing.T) {
	tk, _, refs := refToolkit(t)
	saved := decodeSave(t, mustSave(t, tk, nil))
	tk.s3Client = &mockS3Client{getBody: []byte("<h1>Q4</h1>"), getCT: "text/html"}

	result, _, err := tk.handlePatch(refCtx(""), manageAssetInput{
		Action: "patch", AssetID: saved.AssetID,
		Edits:     []textpatch.Edit{{Op: textpatch.OpInsertAfter, Find: "</h1>", Text: "x"}},
		Resources: []string{refChartURI},
	})
	require.NoError(t, err)
	assert.Contains(t, refusalText(t, result), refChartURI)
	assert.Empty(t, refs.byAsset[saved.AssetID])
}

// TestPatchWithoutResourcesLeavesThemAlone keeps an ordinary patch --
// overwhelmingly the common case -- exactly as it was.
func TestPatchWithoutResourcesLeavesThemAlone(t *testing.T) {
	tk, _, refs := refToolkit(t)
	saved := decodeSave(t, mustSave(t, tk, []string{refLogoURI}))
	tk.s3Client = &mockS3Client{getBody: []byte("<h1>Q4</h1>"), getCT: "text/html"}

	result, _, err := tk.handlePatch(refCtx(""), manageAssetInput{
		Action: "patch", AssetID: saved.AssetID,
		Edits: []textpatch.Edit{{Op: textpatch.OpInsertAfter, Find: "</h1>", Text: "x"}},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	assert.NotContains(t, decodeMap(t, result), fieldResourcesReferenced)
	assert.Len(t, refs.byAsset[saved.AssetID], 1)
}

// TestApplyRefsReportsAStoreFailureAsAFault proves a declaration the store
// could not record is reported, and reported as the platform failing rather
// than as a decision about what the author declared -- an author told they may
// not reference a file would go looking for a permission they already hold.
func TestApplyRefsReportsAStoreFailureAsAFault(t *testing.T) {
	tk, _, _ := refToolkit(t)
	tk.SetResourceRefs(assetrefs.NewDeclarer(failingRefStore{}, resourceStub{}, ""))

	result, _, err := tk.handleSaveAsset(refCtx(""), nil, saveAssetInput{
		Name: "Report", Content: "<p>x</p>", ContentType: "text/html",
		Resources: []string{refLogoURI},
	})
	require.NoError(t, err)

	msg := refusalText(t, result)
	assert.Contains(t, msg, "could not check the declared resource references")
	assert.NotContains(t, msg, "cannot read", "a storage fault is not a permission decision")
}

// failingRefStore accepts a read and refuses the write, which is the shape of a
// database that goes away mid-save.
type failingRefStore struct{}

func (failingRefStore) Replace(context.Context, string, []portaldomain.AssetResourceRef) error {
	return assert.AnError
}

func (failingRefStore) ListByAsset(context.Context, string) ([]portaldomain.AssetResourceRef, error) {
	return nil, nil
}

func (failingRefStore) Attach(context.Context, portaldomain.AssetResourceRef) (bool, error) {
	return false, assert.AnError
}

func (failingRefStore) Detach(context.Context, string, string) (bool, error) {
	return false, assert.AnError
}

func (failingRefStore) ListByResource(context.Context, string, int) ([]portaldomain.AssetResourceRef, error) {
	return nil, nil
}

func (failingRefStore) GetByToken(context.Context, string, string) (*portaldomain.AssetResourceRef, error) {
	return nil, nil //nolint:nilnil // interface contract: no such reference is (nil, nil)
}
