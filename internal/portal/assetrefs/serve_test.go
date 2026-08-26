package assetrefs_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

const resourcesBucket = "managed-resources"

// serveFixture wires the serving route over one asset that references the
// global logo, on the mux pattern the portal registers.
func serveFixture(t *testing.T, deps assetrefs.Deps) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("GET "+assetrefs.PathPrefix+"{id}/{ref}", assetrefs.New(deps))
	return mux
}

func loadedRefs() *fakeRefs {
	refs := newFakeRefs()
	refs.byAsset[testAssetID] = []assetrefs.Ref{{
		AssetID: testAssetID, TargetKind: assetrefs.TargetResource,
		TargetID: "res-logo", URI: logoURI, RefToken: logoToken,
	}}
	return refs
}

func readyDeps(refs *fakeRefs, blobs *fakeBlobs) assetrefs.Deps {
	return assetrefs.Deps{
		Refs: refs, Resources: fixtureResources(), Blobs: blobs, Bucket: resourcesBucket,
	}
}

func logoBlobs() *fakeBlobs {
	return &fakeBlobs{byKey: map[string]string{"resources/global/logo.png": "PNGBYTES"}}
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
	return rec
}

// TestServeReturnsResourceBytesWithoutASession is the property the whole design
// turns on: the route authorizes on the token in the path and nothing else, so
// it answers a reader inside a sandboxed frame and an anonymous share viewer
// alike. The request below carries no cookie, no header, and no identity.
func TestServeReturnsResourceBytesWithoutASession(t *testing.T) {
	blobs := logoBlobs()
	h := serveFixture(t, readyDeps(loadedRefs(), blobs))

	rec := get(t, h, assetrefs.PathPrefix+testAssetID+"/"+logoToken)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "PNGBYTES", rec.Body.String())
	assert.Equal(t, resourcesBucket, blobs.bucket,
		"a reference must be read from the managed-resource bucket, not the portal's")
}

// TestServeRefusesTokenFromAnotherAsset proves the token is scoped to the asset
// that declared it: pasted onto another asset's path it resolves to nothing.
func TestServeRefusesTokenFromAnotherAsset(t *testing.T) {
	h := serveFixture(t, readyDeps(loadedRefs(), logoBlobs()))

	rec := get(t, h, assetrefs.PathPrefix+"asset_other/"+logoToken)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotContains(t, rec.Body.String(), "res-logo", "a refusal must disclose nothing")
}

// TestServeRefusesUnknownToken covers the ordinary wrong-token case, and pins
// that it is answered identically to the wrong-asset case above so one cannot
// be probed for the other.
func TestServeRefusesUnknownToken(t *testing.T) {
	h := serveFixture(t, readyDeps(loadedRefs(), logoBlobs()))

	unknown := get(t, h, assetrefs.PathPrefix+testAssetID+"/tok-nope")
	other := get(t, h, assetrefs.PathPrefix+"asset_other/"+logoToken)

	assert.Equal(t, http.StatusNotFound, unknown.Code)
	assert.Equal(t, other.Body.String(), unknown.Body.String())
}

// TestServeDeletedResourceIs404 is the acceptance criterion for a deleted
// resource: the reference stops resolving, and the asset around it is
// unaffected because this is a subresource fetch, not the page.
func TestServeDeletedResourceIs404(t *testing.T) {
	refs := loadedRefs()
	refs.byAsset[testAssetID][0].TargetID = "res-deleted-long-ago"
	h := serveFixture(t, readyDeps(refs, logoBlobs()))

	rec := get(t, h, assetrefs.PathPrefix+testAssetID+"/"+logoToken)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestServeMissingBlobIs404 separates the two ways a file can be gone: the row
// survived but its object did not. It must read as missing, not as a server
// fault, so the viewer renders a broken image rather than an error.
func TestServeMissingBlobIs404(t *testing.T) {
	h := serveFixture(t, readyDeps(loadedRefs(), &fakeBlobs{byKey: map[string]string{}}))

	rec := get(t, h, assetrefs.PathPrefix+testAssetID+"/"+logoToken)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestServeBlobFailureIsNotReportedAsMissing is the other half: a storage
// outage must not tell the reader the file was deleted.
func TestServeBlobFailureIsNotReportedAsMissing(t *testing.T) {
	h := serveFixture(t, readyDeps(loadedRefs(), &fakeBlobs{err: errStore}))

	rec := get(t, h, assetrefs.PathPrefix+testAssetID+"/"+logoToken)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestServeStoreFailureIsRefused proves a reference lookup that fails is not
// treated as a valid token: failing open here would serve a resource to a
// caller whose token was never checked.
func TestServeStoreFailureIsRefused(t *testing.T) {
	refs := loadedRefs()
	refs.listErr = errStore
	h := serveFixture(t, readyDeps(refs, logoBlobs()))

	rec := get(t, h, assetrefs.PathPrefix+testAssetID+"/"+logoToken)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestServeNotReadyWithoutAnyTargetLayer covers a deployment that has
// references recorded but neither a resource layer nor asset storage wired.
func TestServeNotReadyWithoutAnyTargetLayer(t *testing.T) {
	server := assetrefs.New(assetrefs.Deps{Refs: loadedRefs()})
	require.False(t, server.Ready())

	mux := http.NewServeMux()
	mux.Handle("GET "+assetrefs.PathPrefix+"{id}/{ref}", server)
	rec := get(t, mux, assetrefs.PathPrefix+testAssetID+"/"+logoToken)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestReadyRequiresOneWholeTargetLayer pins which absences make the route
// unregisterable, so a deployment missing one does not answer 503 to a reader
// who could otherwise have been told the path does not exist.
//
// One whole kind is enough: a deployment with assets and no managed-resource
// layer serves asset references, and half a kind serves neither.
func TestReadyRequiresOneWholeTargetLayer(t *testing.T) {
	full := readyDeps(loadedRefs(), logoBlobs())
	unready := map[string]assetrefs.Deps{
		"no refs store":          {Resources: full.Resources, Blobs: full.Blobs},
		"resources, no blobs":    {Refs: full.Refs, Resources: full.Resources},
		"blobs, no resources":    {Refs: full.Refs, Blobs: full.Blobs},
		"assets, no blobs":       {Refs: full.Refs, Assets: fixtureAssets()},
		"asset blobs, no assets": {Refs: full.Refs, AssetBlobs: logoBlobs()},
	}
	for name, deps := range unready {
		t.Run(name, func(t *testing.T) {
			assert.False(t, assetrefs.New(deps).Ready())
		})
	}
	assert.True(t, assetrefs.New(full).Ready())
	assert.True(t, assetrefs.New(assetrefs.Deps{
		Refs: full.Refs, Assets: fixtureAssets(), AssetBlobs: logoBlobs(),
	}).Ready(), "asset references serve without a managed-resource layer")

	var nilServer *assetrefs.Server
	assert.False(t, nilServer.Ready(), "a nil server must answer rather than panic")
}

// TestServeRefusesWrites proves the route is a read: it exists to hand a
// browser bytes, and a token that grants a read must never grant anything else.
func TestServeRefusesWrites(t *testing.T) {
	h := serveFixture(t, readyDeps(loadedRefs(), logoBlobs()))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(),
		http.MethodDelete, assetrefs.PathPrefix+testAssetID+"/"+logoToken, http.NoBody))

	// The mux itself refuses a method its pattern does not carry; the handler's
	// own guard covers a caller that reaches it directly.
	assert.NotEqual(t, http.StatusOK, rec.Code)

	direct := httptest.NewRecorder()
	assetrefs.New(readyDeps(loadedRefs(), logoBlobs())).ServeHTTP(direct,
		httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/x", http.NoBody))
	assert.Equal(t, http.StatusMethodNotAllowed, direct.Code)
}

// TestServeWithoutPathValues covers a handler reached on a route that carries
// neither path parameter, which must refuse rather than look anything up.
func TestServeWithoutPathValues(t *testing.T) {
	rec := httptest.NewRecorder()
	assetrefs.New(readyDeps(loadedRefs(), logoBlobs())).ServeHTTP(rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// The asset-reference half of the route (#1488). The fixtures below give the
// referencing asset one reference to another asset whose content lives in the
// portal's own bucket.
const (
	targetAssetID    = "asset_target"
	assetToken       = "tok-asset"
	portalBucket     = "portal-assets"
	targetAssetKey   = "assets/asset_target/content.csv"
	targetAssetBytes = "region,revenue\nwest,42\n"
)

// assetRefDeps wires the route over one asset reference, with the referenced
// asset present unless a test removes it.
func assetRefDeps(t *testing.T, refs *fakeRefs) (assetrefs.Deps, *fakeAssets, *fakeBlobs) {
	t.Helper()
	assets := &fakeAssets{byID: map[string]*portaldomain.Asset{
		targetAssetID: {
			ID: targetAssetID, Name: "Weekly numbers", ContentType: "text/csv",
			S3Bucket: portalBucket, S3Key: targetAssetKey, OwnerID: "u1",
		},
	}}
	blobs := &fakeBlobs{byKey: map[string]string{targetAssetKey: targetAssetBytes}}
	return assetrefs.Deps{
		Refs: refs, Resources: fixtureResources(), Blobs: logoBlobs(), Bucket: resourcesBucket,
		Assets: assets, AssetBlobs: blobs,
	}, assets, blobs
}

// assetRefFixture is a referencing asset holding one asset reference.
func assetRefFixture() *fakeRefs {
	refs := newFakeRefs()
	refs.byAsset[testAssetID] = []assetrefs.Ref{{
		AssetID: testAssetID, TargetKind: assetrefs.TargetAsset,
		TargetID: targetAssetID, URI: "mcp:asset:" + targetAssetID, RefToken: assetToken,
	}}
	return refs
}

// TestServeReturnsTheReferencedAssetsCurrentContent is the acceptance criterion
// for #1488: the URL resolves to the referenced asset's stored content, read on
// this request, with the referencing asset never re-saved. Rewriting the target
// asset's bytes is what a scheduled script does, and the next fetch of the same
// URL answers with the new content.
func TestServeReturnsTheReferencedAssetsCurrentContent(t *testing.T) {
	deps, _, blobs := assetRefDeps(t, assetRefFixture())
	h := serveFixture(t, deps)

	rec := get(t, h, assetrefs.PathPrefix+testAssetID+"/"+assetToken)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, targetAssetBytes, rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/csv")
	assert.Equal(t, portalBucket, blobs.bucket,
		"an asset reference reads from the bucket the asset itself is stored in")

	blobs.byKey[targetAssetKey] = "region,revenue\nwest,99\n"
	again := get(t, h, assetrefs.PathPrefix+testAssetID+"/"+assetToken)
	assert.Equal(t, "region,revenue\nwest,99\n", again.Body.String(),
		"the reference resolves to the current content, not to a copy taken at declaration")
}

// TestServeDeletedAssetIs404 is the acceptance criterion for a deleted target:
// the reference row survives, the URL answers not found, and the page around it
// is untouched because this is a subresource fetch.
func TestServeDeletedAssetIs404(t *testing.T) {
	deps, assets, _ := assetRefDeps(t, assetRefFixture())
	deleted := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	assets.byID[targetAssetID].DeletedAt = &deleted
	h := serveFixture(t, deps)

	rec := get(t, h, assetrefs.PathPrefix+testAssetID+"/"+assetToken)

	assert.Equal(t, http.StatusNotFound, rec.Code)

	delete(assets.byID, targetAssetID)
	gone := get(t, h, assetrefs.PathPrefix+testAssetID+"/"+assetToken)
	assert.Equal(t, http.StatusNotFound, gone.Code, "an asset that no longer exists answers the same")
}

// TestServeResolvesTheTargetsOwnReferences proves a referenced asset is served
// through its own reference list, so a referenced page renders with its
// pictures rather than with dead URIs.
func TestServeResolvesTheTargetsOwnReferences(t *testing.T) {
	refs := assetRefFixture()
	refs.byAsset[targetAssetID] = []assetrefs.Ref{{
		AssetID: targetAssetID, TargetKind: assetrefs.TargetResource,
		TargetID: "res-logo", URI: logoURI, RefToken: logoToken,
	}}
	deps, assets, blobs := assetRefDeps(t, refs)
	assets.byID[targetAssetID].ContentType = "text/html"
	blobs.byKey[targetAssetKey] = `<img src="` + logoURI + `">`
	h := serveFixture(t, deps)

	rec := get(t, h, assetrefs.PathPrefix+testAssetID+"/"+assetToken)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), assetrefs.PathPrefix+targetAssetID+"/"+logoToken)
	assert.NotContains(t, rec.Body.String(), logoURI,
		"the target's own declared URI is rewritten, exactly as it is when the target is opened directly")
}

// TestServeAnswersACycleRatherThanFollowingIt is the acceptance criterion for a
// reference cycle. Each request resolves one level and returns; what the reader
// gets is a URL back to the other asset, which their client may or may not
// follow. Nothing here recurses.
func TestServeAnswersACycleRatherThanFollowingIt(t *testing.T) {
	const backToken = "tok-back"
	refs := assetRefFixture()
	refs.byAsset[targetAssetID] = []assetrefs.Ref{{
		AssetID: targetAssetID, TargetKind: assetrefs.TargetAsset,
		TargetID: testAssetID, URI: "mcp:asset:" + testAssetID, RefToken: backToken,
	}}
	deps, assets, blobs := assetRefDeps(t, refs)
	assets.byID[testAssetID] = &portaldomain.Asset{
		ID: testAssetID, Name: "Dashboard", ContentType: "text/html",
		S3Bucket: portalBucket, S3Key: "assets/dash/content.html", OwnerID: "u1",
	}
	assets.byID[targetAssetID].ContentType = "text/html"
	blobs.byKey[targetAssetKey] = `<a href="mcp:asset:` + testAssetID + `">back</a>`
	blobs.byKey["assets/dash/content.html"] = `<a href="mcp:asset:` + targetAssetID + `">on</a>`
	h := serveFixture(t, deps)

	rec := get(t, h, assetrefs.PathPrefix+testAssetID+"/"+assetToken)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), assetrefs.PathPrefix+targetAssetID+"/"+backToken,
		"the cycle is answered as a URL the reader may follow, not walked here")
}

// TestServeAssetReferenceWithoutAssetStorage covers a deployment serving
// resource references with no asset storage wired: the asset reference answers
// that its storage is not configured, and the resource reference still serves.
func TestServeAssetReferenceWithoutAssetStorage(t *testing.T) {
	refs := assetRefFixture()
	refs.byAsset[testAssetID] = append(refs.byAsset[testAssetID], assetrefs.Ref{
		AssetID: testAssetID, TargetKind: assetrefs.TargetResource,
		TargetID: "res-logo", URI: logoURI, RefToken: logoToken,
	})
	h := serveFixture(t, readyDeps(refs, logoBlobs()))

	assert.Equal(t, http.StatusServiceUnavailable,
		get(t, h, assetrefs.PathPrefix+testAssetID+"/"+assetToken).Code)
	assert.Equal(t, http.StatusOK,
		get(t, h, assetrefs.PathPrefix+testAssetID+"/"+logoToken).Code)
}

// TestServeUnknownTargetKindIs404 covers a row written by a newer version than
// this binary: it is answered as a missing target rather than guessed at.
func TestServeUnknownTargetKindIs404(t *testing.T) {
	refs := assetRefFixture()
	refs.byAsset[testAssetID][0].TargetKind = "collection"
	deps, _, _ := assetRefDeps(t, refs)

	rec := get(t, serveFixture(t, deps), assetrefs.PathPrefix+testAssetID+"/"+assetToken)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestServeAssetBlobFailureIsNotReportedAsMissing is the asset arm's half of
// the distinction the resource arm already draws: a storage outage must not
// tell the reader the asset was deleted.
func TestServeAssetBlobFailureIsNotReportedAsMissing(t *testing.T) {
	deps, _, blobs := assetRefDeps(t, assetRefFixture())
	blobs.err = errStore

	rec := get(t, serveFixture(t, deps), assetrefs.PathPrefix+testAssetID+"/"+assetToken)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestServeMissingAssetObjectIs404 separates the two ways a referenced asset's
// content can be gone: the row survived but its object did not.
func TestServeMissingAssetObjectIs404(t *testing.T) {
	deps, _, blobs := assetRefDeps(t, assetRefFixture())
	delete(blobs.byKey, targetAssetKey)

	rec := get(t, serveFixture(t, deps), assetrefs.PathPrefix+testAssetID+"/"+assetToken)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestServeAssetWithUnreadableOwnReferences serves the referenced asset's
// content as stored when its own reference list cannot be read. An unresolved
// reference costs one picture; refusing the read would cost the whole file.
func TestServeAssetWithUnreadableOwnReferences(t *testing.T) {
	refs := assetRefFixture()
	deps, _, blobs := assetRefDeps(t, refs)
	blobs.byKey[targetAssetKey] = `<img src="` + logoURI + `">`
	// The list the rewrite consults fails while the token still resolves: they
	// are two separate reads in the Postgres store.
	refs.byAssetErr = errStore

	rec := get(t, serveFixture(t, deps), assetrefs.PathPrefix+testAssetID+"/"+assetToken)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), logoURI, "the content is served exactly as stored")
}
