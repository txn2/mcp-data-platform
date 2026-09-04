package portal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// An asset read carried every provenance capture, and a capture is appended on
// every write: the read of a dashboard a script refreshes hourly was 881 KB and
// growing (#1623). These pin the two halves of the fix on this surface -- the
// read carries the newest captures and says how many there are, and the page
// route reaches the rest under the asset's own authorization.

func provenanceCaptures(n int) []ProvenanceCapture {
	out := make([]ProvenanceCapture, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, ProvenanceCapture{Tool: "manage_asset", Version: i})
	}
	return out
}

// newestFirst is the order the store hands captures back in.
func newestFirst(cs []ProvenanceCapture) []ProvenanceCapture {
	out := make([]ProvenanceCapture, 0, len(cs))
	for i := len(cs) - 1; i >= 0; i-- {
		out = append(out, cs[i])
	}
	return out
}

func TestGetAsset_CarriesTheNewestCapturesAndTheirTotal(t *testing.T) {
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Dashboard", Tags: []string{},
		Provenance: Provenance{Captures: provenanceCaptures(50)},
	}
	h := newTestHandler(&mockAssetStore{getAsset: asset}, &mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var got assetResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Len(t, got.Provenance.Captures, portaldomain.ProvenanceCapturesInline)
	assert.Equal(t, 50, got.Provenance.CapturesTotal)
	assert.Equal(t, 50, got.Provenance.Captures[len(got.Provenance.Captures)-1].Version,
		"the newest capture is still the last one, so a reader that leads with it is unaffected")
}

// An asset with a single capture reads exactly as it did before the bound
// existed: every capture, and no total to explain a truncation that did not
// happen.
func TestGetAsset_ASingleCaptureReadsUnchanged(t *testing.T) {
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Report", Tags: []string{},
		Provenance: Provenance{SessionID: "dps_abc", Captures: provenanceCaptures(1)},
	}
	h := newTestHandler(&mockAssetStore{getAsset: asset}, &mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var got assetResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Len(t, got.Provenance.Captures, 1)
	assert.Zero(t, got.Provenance.CapturesTotal)
	assert.Equal(t, "dps_abc", got.Provenance.SessionID)
}

// A copy carries the source's whole provenance into its own row -- what the
// content was built from did not change -- and its response is bounded like
// every other single asset read.
func TestCopyAsset_ResponseIsBounded(t *testing.T) {
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Dashboard", ContentType: "text/markdown", Tags: []string{},
		Provenance: Provenance{Captures: provenanceCaptures(50)},
	}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{}, &mockS3Client{getData: []byte("# body")}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST",
		"/api/v1/portal/assets/a1/copy", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var got Asset
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Len(t, got.Provenance.Captures, portaldomain.ProvenanceCapturesInline)
	assert.Equal(t, 50, got.Provenance.CapturesTotal)
}

func TestListAssetProvenance_PagesNewestFirst(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", Name: "Dashboard", Tags: []string{}}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset, provenancePage: newestFirst(provenanceCaptures(50))},
		&mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET",
		"/api/v1/portal/assets/a1/provenance?offset=20&limit=20", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var got provenancePageResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, 50, got.Total)
	assert.Equal(t, 20, got.Offset)
	require.Len(t, got.Captures, 20)
	assert.Equal(t, 30, got.Captures[0].Version, "the page after the newest twenty")
	assert.Equal(t, 11, got.Captures[19].Version)
}

// The limit is clamped before the store sees it, so a caller asking for the
// whole history gets a page.
func TestListAssetProvenance_ClampsTheLimit(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", Name: "Dashboard", Tags: []string{}}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset, provenancePage: newestFirst(provenanceCaptures(500))},
		&mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET",
		"/api/v1/portal/assets/a1/provenance?limit=5000", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var got provenancePageResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, portaldomain.MaxProvenancePageSize, got.Limit)
	assert.Len(t, got.Captures, portaldomain.MaxProvenancePageSize)
}

// The page is part of the asset's record, so it is authorized exactly as the
// asset is.
func TestListAssetProvenance_Authorization(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "someone-else", Name: "Dashboard", Tags: []string{}}

	t.Run("a stranger is refused", func(t *testing.T) {
		h := newTestHandler(
			&mockAssetStore{getAsset: asset, provenancePage: provenanceCaptures(3)},
			&mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"},
		)
		req := httptest.NewRequestWithContext(context.Background(), "GET",
			"/api/v1/portal/assets/a1/provenance", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("a collection share reaches it", func(t *testing.T) {
		h := newTestHandler(
			&mockAssetStore{getAsset: asset, provenancePage: provenanceCaptures(3)},
			&mockShareStore{collAssetPerm: PermissionViewer},
			&mockS3Client{}, &User{UserID: "u1", Email: "u@example.com"},
		)
		req := httptest.NewRequestWithContext(context.Background(), "GET",
			"/api/v1/portal/assets/a1/provenance", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestListAssetProvenance_MissingAsset(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{getErr: errors.New("not found")},
		&mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"},
	)
	req := httptest.NewRequestWithContext(context.Background(), "GET",
		"/api/v1/portal/assets/missing/provenance", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// The route requires an identity for the reason every asset read does: it
// answers with an asset's own record.
func TestListAssetProvenance_Unauthenticated(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{getAsset: &Asset{ID: "a1", OwnerID: "u1", Tags: []string{}}},
		&mockShareStore{}, &mockS3Client{}, nil,
	)
	req := httptest.NewRequestWithContext(context.Background(), "GET",
		"/api/v1/portal/assets/a1/provenance", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// A deleted asset is gone rather than forbidden: the reader is told which of
// the two it is.
func TestListAssetProvenance_DeletedAsset(t *testing.T) {
	deleted := time.Now()
	h := newTestHandler(
		&mockAssetStore{getAsset: &Asset{ID: "a1", OwnerID: "u1", Tags: []string{}, DeletedAt: &deleted}},
		&mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"},
	)
	req := httptest.NewRequestWithContext(context.Background(), "GET",
		"/api/v1/portal/assets/a1/provenance", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusGone, w.Code)
}

// A share lookup that failed is not a denial: the reader is told the platform
// could not decide, not that they may not look.
func TestListAssetProvenance_ShareCheckFailure(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{getAsset: &Asset{ID: "a1", OwnerID: "someone-else", Tags: []string{}}},
		&mockShareStore{listByAssetE: errors.New("db down")},
		&mockS3Client{}, &User{UserID: "u1"},
	)
	req := httptest.NewRequestWithContext(context.Background(), "GET",
		"/api/v1/portal/assets/a1/provenance", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListAssetProvenance_ReadFailure(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", Name: "Dashboard", Tags: []string{}}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset, provenanceErr: errors.New("connection reset")},
		&mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"},
	)
	req := httptest.NewRequestWithContext(context.Background(), "GET",
		"/api/v1/portal/assets/a1/provenance", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
