package assetrefs_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
	refs.byAsset[testAssetID] = []portaldomain.AssetResourceRef{{
		AssetID: testAssetID, ResourceID: "res-logo", URI: logoURI, RefToken: logoToken,
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
	refs.byAsset[testAssetID][0].ResourceID = "res-deleted-long-ago"
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

// TestServeNotReadyWithoutAManagedResourceLayer covers a deployment that has
// references recorded but no resource layer wired.
func TestServeNotReadyWithoutAManagedResourceLayer(t *testing.T) {
	server := assetrefs.New(assetrefs.Deps{Refs: loadedRefs()})
	require.False(t, server.Ready())

	mux := http.NewServeMux()
	mux.Handle("GET "+assetrefs.PathPrefix+"{id}/{ref}", server)
	rec := get(t, mux, assetrefs.PathPrefix+testAssetID+"/"+logoToken)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestReadyRequiresEveryDependency pins which absences make the route
// unregisterable, so a deployment missing one does not answer 503 to a reader
// who could otherwise have been told the path does not exist.
func TestReadyRequiresEveryDependency(t *testing.T) {
	full := readyDeps(loadedRefs(), logoBlobs())
	tests := map[string]assetrefs.Deps{
		"no refs store":  {Resources: full.Resources, Blobs: full.Blobs},
		"no resources":   {Refs: full.Refs, Blobs: full.Blobs},
		"no blob client": {Refs: full.Refs, Resources: full.Resources},
	}
	for name, deps := range tests {
		t.Run(name, func(t *testing.T) {
			assert.False(t, assetrefs.New(deps).Ready())
		})
	}
	assert.True(t, assetrefs.New(full).Ready())

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
