package portal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

const (
	refLogoURI   = "mcp://global/brand/logo.png"
	refLogoToken = "tok-logo"
	refBaseURL   = "https://example.com"
	refBucket    = "managed-resources"
)

// mockRefStore is an in-memory AssetResourceRefStore. GetByToken reports no
// such reference as (nil, nil), which is the Postgres store's contract.
type mockRefStore struct {
	byAsset  map[string][]portaldomain.AssetResourceRef
	listErr  error
	replaced map[string][]portaldomain.AssetResourceRef
}

func newMockRefStore() *mockRefStore {
	return &mockRefStore{
		byAsset:  map[string][]portaldomain.AssetResourceRef{},
		replaced: map[string][]portaldomain.AssetResourceRef{},
	}
}

func (m *mockRefStore) Replace(_ context.Context, id string, refs []portaldomain.AssetResourceRef) error {
	m.replaced[id] = refs
	m.byAsset[id] = refs
	return nil
}

func (m *mockRefStore) ListByAsset(_ context.Context, id string) ([]portaldomain.AssetResourceRef, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.byAsset[id], nil
}

func (m *mockRefStore) Attach(_ context.Context, ref portaldomain.AssetResourceRef) (bool, error) {
	for _, existing := range m.byAsset[ref.AssetID] {
		if existing.ResourceID == ref.ResourceID {
			return false, nil
		}
	}
	m.byAsset[ref.AssetID] = append(m.byAsset[ref.AssetID], ref)
	return true, nil
}

func (m *mockRefStore) Detach(_ context.Context, assetID, resourceID string) (bool, error) {
	kept := make([]portaldomain.AssetResourceRef, 0, len(m.byAsset[assetID]))
	found := false
	for _, ref := range m.byAsset[assetID] {
		if ref.ResourceID == resourceID {
			found = true
			continue
		}
		kept = append(kept, ref)
	}
	m.byAsset[assetID] = kept
	return found, nil
}

func (m *mockRefStore) ListByResource(_ context.Context, resourceID string, _ int) ([]portaldomain.AssetResourceRef, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	var out []portaldomain.AssetResourceRef
	for _, refs := range m.byAsset {
		for _, ref := range refs {
			if ref.ResourceID == resourceID {
				out = append(out, ref)
			}
		}
	}
	return out, nil
}

func (m *mockRefStore) GetByToken(_ context.Context, id, token string) (*portaldomain.AssetResourceRef, error) {
	for _, ref := range m.byAsset[id] {
		if ref.RefToken == token {
			return &ref, nil
		}
	}
	return nil, nil //nolint:nilnil // interface contract: no such reference is (nil, nil)
}

// mockResourceReader serves the global logo and a finance-only chart.
type mockResourceReader struct {
	byID   map[string]*resource.Resource
	getErr error
}

func newMockResourceReader() *mockResourceReader {
	return &mockResourceReader{byID: map[string]*resource.Resource{
		"res-logo": {
			ID: "res-logo", Scope: resource.ScopeGlobal, Filename: "logo.png",
			MIMEType: "image/png", S3Key: "resources/global/logo.png", URI: refLogoURI,
			UpdatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		},
		"res-chart": {
			ID: "res-chart", Scope: resource.ScopePersona, ScopeID: "finance",
			Filename: "chart.png", MIMEType: "image/png",
			S3Key: "resources/persona/finance/chart.png", URI: "mcp://persona/finance/chart.png",
		},
	}}
}

func (m *mockResourceReader) Get(_ context.Context, id string) (*resource.Resource, error) {
	res, ok := m.byID[id]
	if !ok {
		return nil, nil //nolint:nilnil // resource.Store reports a missing row as (nil, nil)
	}
	return res, nil
}

func (m *mockResourceReader) GetByIDs(_ context.Context, ids []string) (map[string]*resource.Resource, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	out := make(map[string]*resource.Resource, len(ids))
	for _, id := range ids {
		if res, ok := m.byID[id]; ok {
			out[id] = res
		}
	}
	return out, nil
}

func (m *mockResourceReader) GetByURI(_ context.Context, uri string) (*resource.Resource, error) {
	for _, res := range m.byID {
		if res.URI == uri {
			return res, nil
		}
	}
	return nil, nil //nolint:nilnil // resource.Store reports a missing row as (nil, nil)
}

// refFixture wires a handler over one HTML asset whose content names the logo
// by its mcp:// URI and which has declared that reference.
type refFixture struct {
	handler *Handler
	refs    *mockRefStore
	s3      *mockS3Client
	asset   *Asset
}

const refAssetBody = `<h1>Q4</h1><img src="` + refLogoURI + `">`

func newRefFixture(t *testing.T, user *User, declared bool) *refFixture {
	t.Helper()
	asset := &Asset{
		ID: "a1", OwnerID: "u1", OwnerEmail: "owner@example.com", Name: "Q4 Report",
		ContentType: "text/html", S3Bucket: "test-bucket", S3Key: "k", CurrentVersion: 1,
	}
	refs := newMockRefStore()
	if declared {
		refs.byAsset[asset.ID] = []portaldomain.AssetResourceRef{{
			AssetID: asset.ID, ResourceID: "res-logo", URI: refLogoURI, RefToken: refLogoToken,
		}}
	}
	s3 := &mockS3Client{getData: []byte(refAssetBody), getCT: "text/html"}
	blobs := &mockS3Client{getData: []byte("PNGBYTES")}

	h := NewHandler(Deps{
		AssetStore:       &mockAssetStore{getAsset: asset},
		ShareStore:       &mockShareStore{},
		S3Client:         s3,
		S3Bucket:         "test-bucket",
		PublicBaseURL:    refBaseURL,
		RateLimit:        RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
		ResourceRefs:     refs,
		ResourceReader:   newMockResourceReader(),
		ResourceBlobs:    blobs,
		ResourceS3Bucket: refBucket,
	}, testAuthMiddleware(user))

	return &refFixture{handler: h, refs: refs, s3: s3, asset: asset}
}

// refRequest is a bare inbound request, for exercising the rewrite helper
// directly rather than through a route.
func refRequest(t *testing.T) *http.Request {
	t.Helper()
	return httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
}

func (f *refFixture) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
	return rec
}

// TestAssetContentRewritesDeclaredReference is acceptance criterion 1 through
// the real handler: opening the asset in the portal yields a working image URL,
// and what is stored is untouched.
func TestAssetContentRewritesDeclaredReference(t *testing.T) {
	f := newRefFixture(t, &User{UserID: "u1"}, true)

	rec := f.get(t, "/api/v1/portal/assets/a1/content")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), refBaseURL+"/portal/refs/a1/"+refLogoToken)
	assert.NotContains(t, rec.Body.String(), refLogoURI)
	assert.Equal(t, refAssetBody, string(f.s3.getData), "the stored content must keep the mcp:// URI")
}

// TestAssetContentLeavesUndeclaredReferenceAlone is acceptance criterion 3
// through the handler: the grant is the declaration, not a string in the body.
func TestAssetContentLeavesUndeclaredReferenceAlone(t *testing.T) {
	f := newRefFixture(t, &User{UserID: "u1"}, false)

	rec := f.get(t, "/api/v1/portal/assets/a1/content")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), refLogoURI)
	assert.NotContains(t, rec.Body.String(), "/portal/refs/")
}

// TestRewrittenURLIsAbsoluteWithoutAConfiguredBaseURL is the deployment case
// the fallback exists for: portal.public_base_url unset, and the URL still has
// to name a host, because the frame the image loads in cannot resolve a
// relative path against its blob: document.
func TestRewrittenURLIsAbsoluteWithoutAConfiguredBaseURL(t *testing.T) {
	f := newRefFixture(t, &User{UserID: "u1"}, true)
	f.handler.deps.PublicBaseURL = ""

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"http://reports.example.com/api/v1/portal/assets/a1/content", http.NoBody)
	req.Header.Set("X-Forwarded-Proto", "https")
	f.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "https://reports.example.com/portal/refs/a1/"+refLogoToken)
}

// TestReferenceRouteServesWithoutASession is the property the rewritten URL
// depends on: the request below carries no session, exactly as an <img> inside
// a sandboxed blob: frame does.
func TestReferenceRouteServesWithoutASession(t *testing.T) {
	f := newRefFixture(t, nil, true)

	rec := f.get(t, assetrefs.PathPrefix+"a1/"+refLogoToken)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "PNGBYTES", rec.Body.String())
}

// TestReferenceRouteUnregisteredWithoutAResourceLayer proves a deployment with
// no managed resources serves the prefix as an unknown path rather than as a
// route that answers 503 to every reader.
func TestReferenceRouteUnregisteredWithoutAResourceLayer(t *testing.T) {
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{},
		ShareStore: &mockShareStore{},
		RateLimit:  RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, testAuthMiddleware(&User{UserID: "u1"}))
	require.Nil(t, h.refMux)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		assetrefs.PathPrefix+"a1/tok", http.NoBody))
	assert.NotEqual(t, http.StatusServiceUnavailable, rec.Code)
}

// TestServeRefsDegradesWhenReferencesCannotBeRead proves a reference-store
// outage takes the images off the page and not the page itself. Refusing the
// read would remove a whole report over a logo.
func TestServeRefsDegradesWhenReferencesCannotBeRead(t *testing.T) {
	f := newRefFixture(t, &User{UserID: "u1"}, true)
	f.refs.listErr = assert.AnError

	rec := f.get(t, "/api/v1/portal/assets/a1/content")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), refLogoURI, "content is served as stored")
}

// TestServeRefsNoOpWithoutAStore covers the deployment with no database: every
// content read serves exactly what it did before references existed.
func TestServeRefsNoOpWithoutAStore(t *testing.T) {
	h := NewHandler(Deps{AssetStore: &mockAssetStore{}, ShareStore: &mockShareStore{}}, nil)

	assert.Equal(t, []byte(refAssetBody),
		h.serveRefs(refRequest(t), "a1", "text/html", []byte(refAssetBody)))
}

// TestServeRefsIgnoresAnEmptyAssetID guards the helper against a caller with no
// asset to rewrite against, which could only produce URLs naming no asset.
func TestServeRefsIgnoresAnEmptyAssetID(t *testing.T) {
	f := newRefFixture(t, &User{UserID: "u1"}, true)

	assert.Equal(t, []byte(refAssetBody),
		f.handler.serveRefs(refRequest(t), "", "text/html", []byte(refAssetBody)))
}

// TestRefRateLimitScalesWithTheCap is why the reference route has a limiter of
// its own: one page view can legitimately fetch up to the cap's worth of
// references, which the viewer's per-page-view bucket would answer 429 and
// blank every image on the page.
func TestRefRateLimitScalesWithTheCap(t *testing.T) {
	got := refRateLimit(RateLimitConfig{RequestsPerMinute: 60, BurstSize: 10})
	assert.Equal(t, 60*portaldomain.MaxAssetResourceRefs, got.RequestsPerMinute)
	assert.Equal(t, 10*portaldomain.MaxAssetResourceRefs, got.BurstSize)

	// A field left unset stays unset so viewerlimit applies its own default
	// before sizing the global backstop from it.
	zero := refRateLimit(RateLimitConfig{})
	assert.Zero(t, zero.RequestsPerMinute)
	assert.Zero(t, zero.BurstSize)
}

// TestCopyCarriesOnlyReferencesTheCopierCanRead is the acceptance criterion for
// copying: a copy must not silently carry a grant its new owner did not earn.
func TestCopyCarriesOnlyReferencesTheCopierCanRead(t *testing.T) {
	f := newRefFixture(t, &User{UserID: "u2", Email: "other@example.com"}, true)
	f.refs.byAsset["a1"] = append(f.refs.byAsset["a1"], portaldomain.AssetResourceRef{
		AssetID: "a1", ResourceID: "res-chart", URI: "mcp://persona/finance/chart.png",
		RefToken: "tok-chart", Position: 1,
	})

	f.handler.copyRefs(t.Context(), "a1", "copy1", &User{UserID: "u2", Email: "other@example.com"})

	carried := f.refs.replaced["copy1"]
	require.Len(t, carried, 1, "the finance chart must not follow a copier who cannot read it")
	assert.Equal(t, "res-logo", carried[0].ResourceID)
	assert.Equal(t, "other@example.com", carried[0].DeclaredBy)
	assert.NotEqual(t, refLogoToken, carried[0].RefToken,
		"a copy is a separate grant and gets a token of its own")
}

// TestCopyCarriesNothingWhenNothingIsReadable proves a copier who can read none
// of the references still gets the asset, with the mcp:// URIs left in its
// content resolving to nothing.
func TestCopyCarriesNothingWhenNothingIsReadable(t *testing.T) {
	f := newRefFixture(t, nil, false)
	f.refs.byAsset["a1"] = []portaldomain.AssetResourceRef{{
		AssetID: "a1", ResourceID: "res-chart", URI: "mcp://persona/finance/chart.png",
		RefToken: "tok-chart",
	}}

	f.handler.copyRefs(t.Context(), "a1", "copy1", &User{UserID: "u2", Email: "other@example.com"})

	assert.NotContains(t, f.refs.replaced, "copy1")
}

// TestCopyRefsNoOpCases covers every input that must leave the copy alone.
func TestCopyRefsNoOpCases(t *testing.T) {
	user := &User{UserID: "u2", Email: "other@example.com"}

	t.Run("no reference store", func(t *testing.T) {
		h := NewHandler(Deps{AssetStore: &mockAssetStore{}, ShareStore: &mockShareStore{}}, nil)
		h.copyRefs(t.Context(), "a1", "copy1", user) // must not panic
	})
	t.Run("no user", func(t *testing.T) {
		f := newRefFixture(t, nil, true)
		f.handler.copyRefs(t.Context(), "a1", "copy1", nil)
		assert.NotContains(t, f.refs.replaced, "copy1")
	})
	t.Run("source has none", func(t *testing.T) {
		f := newRefFixture(t, nil, false)
		f.handler.copyRefs(t.Context(), "a1", "copy1", user)
		assert.NotContains(t, f.refs.replaced, "copy1")
	})
	t.Run("source read fails", func(t *testing.T) {
		f := newRefFixture(t, nil, true)
		f.refs.listErr = assert.AnError
		f.handler.copyRefs(t.Context(), "a1", "copy1", user)
		assert.NotContains(t, f.refs.replaced, "copy1")
	})
}

// TestResourceClaimsUsesTheResolvedPersona proves the copy check judges a
// person by the persona the portal's own gate judges them by, rather than by
// roles this package would otherwise interpret itself.
func TestResourceClaimsUsesTheResolvedPersona(t *testing.T) {
	f := newRefFixture(t, nil, true)
	f.handler.deps.PersonaResolver = func(roles []string) *PersonaInfo {
		if strings.Contains(strings.Join(roles, ","), "fin") {
			return &PersonaInfo{Name: "finance"}
		}
		return nil
	}

	claims := f.handler.resourceClaims(&User{UserID: "u2", Email: "other@example.com", Roles: []string{"fin"}})
	assert.Equal(t, []string{"finance"}, claims.Personas)

	none := f.handler.resourceClaims(&User{UserID: "u3", Email: "n@example.com"})
	assert.Empty(t, none.Personas)
}

// publicRefFixture wires a public share over the same HTML asset, with the
// reference declared.
func publicRefFixture(t *testing.T) *Handler {
	t.Helper()
	now := time.Now()
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Q4 Report", ContentType: "text/html",
		S3Bucket: "test-bucket", S3Key: "k", Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}
	refs := newMockRefStore()
	refs.byAsset["a1"] = []portaldomain.AssetResourceRef{{
		AssetID: "a1", ResourceID: "res-logo", URI: refLogoURI, RefToken: refLogoToken,
	}}

	h := NewHandler(Deps{
		AssetStore:       &mockAssetStore{getAsset: asset},
		ShareStore:       &mockShareStore{getByTokenRes: &Share{AccessMode: AccessModePublic, ID: "s1", AssetID: "a1", Token: "tok1"}},
		S3Client:         &mockS3Client{getData: []byte(refAssetBody), getCT: "text/html"},
		S3Bucket:         "test-bucket",
		PublicBaseURL:    refBaseURL,
		RateLimit:        RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
		ResourceRefs:     refs,
		ResourceReader:   newMockResourceReader(),
		ResourceBlobs:    &mockS3Client{getData: []byte("PNGBYTES")},
		ResourceS3Bucket: refBucket,
	}, nil)
	return h
}

func publicGet(t *testing.T, h *Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
	return rec
}

// TestPublicShareViewerRendersTheReference is acceptance criterion 2: opening
// the asset through a public share link, signed out, resolves the image. The
// viewer embeds the content in the page, so the rewrite has to reach the
// embedded copy and not only the raw-content endpoint.
func TestPublicShareViewerRendersTheReference(t *testing.T) {
	h := publicRefFixture(t)

	rec := publicGet(t, h, "/portal/view/tok1")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "/portal/refs/a1/"+refLogoToken)
	assert.NotContains(t, rec.Body.String(), refLogoURI)
}

// TestPublicShareContentEndpointRewrites covers the other public path: the raw
// content a share serves for download and for the binary families.
func TestPublicShareContentEndpointRewrites(t *testing.T) {
	h := publicRefFixture(t)

	rec := publicGet(t, h, "/portal/view/tok1/content")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "/portal/refs/a1/"+refLogoToken)
}

// TestAnonymousReaderLoadsTheReferencedFile closes the loop on criterion 2: the
// URL the share page just rendered is fetched with no session at all, which is
// exactly what the reader's browser does next.
func TestAnonymousReaderLoadsTheReferencedFile(t *testing.T) {
	h := publicRefFixture(t)

	rec := publicGet(t, h, assetrefs.PathPrefix+"a1/"+refLogoToken)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "PNGBYTES", rec.Body.String())
}

// TestVersionContentRewritesAgainstCurrentReferences pins what a version read
// is rewritten against. The references belong to the asset, not to the version,
// so history is read with the asset's current ones; a version naming a resource
// the asset no longer references renders that image missing.
func TestVersionContentRewritesAgainstCurrentReferences(t *testing.T) {
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Q4 Report", ContentType: "text/html",
		S3Bucket: "test-bucket", S3Key: "k", CurrentVersion: 2,
	}
	refs := newMockRefStore()
	refs.byAsset["a1"] = []portaldomain.AssetResourceRef{{
		AssetID: "a1", ResourceID: "res-logo", URI: refLogoURI, RefToken: refLogoToken,
	}}
	h := NewHandler(Deps{
		AssetStore:       &mockAssetStore{getAsset: asset},
		ShareStore:       &mockShareStore{},
		VersionStore:     &mockVersionStore{getVersion: &AssetVersion{AssetID: "a1", Version: 1, S3Bucket: "test-bucket", S3Key: "v1", ContentType: "text/html"}},
		S3Client:         &mockS3Client{getData: []byte(refAssetBody), getCT: "text/html"},
		S3Bucket:         "test-bucket",
		PublicBaseURL:    refBaseURL,
		RateLimit:        RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
		ResourceRefs:     refs,
		ResourceReader:   newMockResourceReader(),
		ResourceBlobs:    &mockS3Client{getData: []byte("PNGBYTES")},
		ResourceS3Bucket: refBucket,
	}, testAuthMiddleware(&User{UserID: "u1"}))

	rec := publicGet(t, h, "/api/v1/portal/assets/a1/versions/1/content")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "/portal/refs/a1/"+refLogoToken)
}

// TestCopyCarriesNothingWhenResourcesCannotBeRead proves a copy never falls
// back to carrying references it could not check: a failed resource read
// leaves the copy with none rather than with all of them.
func TestCopyCarriesNothingWhenResourcesCannotBeRead(t *testing.T) {
	f := newRefFixture(t, nil, true)
	reader := newMockResourceReader()
	reader.getErr = assert.AnError
	f.handler.deps.ResourceReader = reader

	f.handler.copyRefs(t.Context(), "a1", "copy1", &User{UserID: "u2", Email: "other@example.com"})

	assert.NotContains(t, f.refs.replaced, "copy1")
}
