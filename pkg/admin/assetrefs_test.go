package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

const (
	adminRefLogoURI   = "mcp://global/brand/logo.png"
	adminRefLogoToken = "tok-logo"
	adminRefBaseURL   = "https://platform.example.com"
	adminRefBody      = `<h1>Q4</h1><img src="` + adminRefLogoURI + `">`
)

// mockAdminRefStore is an in-memory AssetResourceRefStore for the console's
// content reads.
type mockAdminRefStore struct {
	refs    []portaldomain.AssetResourceRef
	listErr error
}

func (*mockAdminRefStore) Replace(context.Context, string, []portaldomain.AssetResourceRef) error {
	return nil
}

func (m *mockAdminRefStore) ListByAsset(context.Context, string) ([]portaldomain.AssetResourceRef, error) {
	return m.refs, m.listErr
}

func (*mockAdminRefStore) GetByToken(context.Context, string, string) (*portaldomain.AssetResourceRef, error) {
	return nil, nil //nolint:nilnil // interface contract: no such reference is (nil, nil)
}

func adminRefHandler(refs portaldomain.AssetResourceRefStore, versions portal.VersionStore) *Handler {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", Name: "Q4 Report", ContentType: "text/html",
		S3Bucket: "b", S3Key: "k", Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}
	return NewHandler(Deps{
		AssetStore:    &mockAdminAssetStore{getAsset: asset},
		ShareStore:    &mockAdminShareStore{},
		VersionStore:  versions,
		S3Client:      &mockAdminS3Client{getData: []byte(adminRefBody), getCT: "text/html"},
		S3Bucket:      "test-bucket",
		ResourceRefs:  refs,
		PublicBaseURL: adminRefBaseURL,
	}, nil)
}

func declaredRefs() *mockAdminRefStore {
	return &mockAdminRefStore{refs: []portaldomain.AssetResourceRef{{
		AssetID: "a1", ResourceID: "res-logo", URI: adminRefLogoURI, RefToken: adminRefLogoToken,
	}}}
}

func adminGet(t *testing.T, h *Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
	return rec
}

// TestAdminAssetContentRewritesReferences is why the console is on the rewrite
// path at all: an administrator is unrestricted by design, and an asset that
// renders for its owner but shows broken images for the operator reviewing it
// is a defect in the console.
func TestAdminAssetContentRewritesReferences(t *testing.T) {
	rec := adminGet(t, adminRefHandler(declaredRefs(), nil), "/api/v1/admin/assets/a1/content")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), adminRefBaseURL+"/portal/refs/a1/"+adminRefLogoToken)
	assert.NotContains(t, rec.Body.String(), adminRefLogoURI)
}

// TestAdminVersionContentRewritesReferences covers the console's other content
// read, the version history one.
func TestAdminVersionContentRewritesReferences(t *testing.T) {
	versions := &mockAdminVersionStore{getVersion: &portal.AssetVersion{
		AssetID: "a1", Version: 1, S3Bucket: "b", S3Key: "v1", ContentType: "text/html",
	}}
	rec := adminGet(t, adminRefHandler(declaredRefs(), versions),
		"/api/v1/admin/assets/a1/versions/1/content")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "/portal/refs/a1/"+adminRefLogoToken)
}

// TestAdminContentServedAsStoredWhenReferencesAreUnavailable proves the console
// degrades the way the portal does: a reference-store fault takes the images
// off the page, not the page itself.
func TestAdminContentServedAsStoredWhenReferencesAreUnavailable(t *testing.T) {
	broken := &mockAdminRefStore{listErr: errors.New("database unavailable")}

	rec := adminGet(t, adminRefHandler(broken, nil), "/api/v1/admin/assets/a1/content")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), adminRefLogoURI)
}

// TestAdminContentWithoutAReferenceStore covers a deployment with no database:
// content is served exactly as it was before references existed.
func TestAdminContentWithoutAReferenceStore(t *testing.T) {
	rec := adminGet(t, adminRefHandler(nil, nil), "/api/v1/admin/assets/a1/content")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, adminRefBody, rec.Body.String())
}

// TestAdminServeRefsIgnoresAnEmptyAssetID guards the helper against a caller
// with no asset to rewrite against.
func TestAdminServeRefsIgnoresAnEmptyAssetID(t *testing.T) {
	h := adminRefHandler(declaredRefs(), nil)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	assert.Equal(t, []byte(adminRefBody),
		h.serveRefs(req, "", "text/html", []byte(adminRefBody)))
}
