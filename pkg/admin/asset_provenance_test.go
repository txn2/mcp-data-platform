package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// The administrator's read of an asset carried every capture, which on a
// dashboard a script refreshes hourly was 881 KB (#1623). It now carries the
// newest of them and says how many the asset holds; the twin route below
// reaches the rest.

func adminCaptures(n int) []portal.ProvenanceCapture {
	out := make([]portal.ProvenanceCapture, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, portal.ProvenanceCapture{Tool: "manage_asset", Version: i})
	}
	return out
}

func adminNewestFirst(cs []portal.ProvenanceCapture) []portal.ProvenanceCapture {
	out := make([]portal.ProvenanceCapture, 0, len(cs))
	for i := len(cs) - 1; i >= 0; i-- {
		out = append(out, cs[i])
	}
	return out
}

func TestGetAdminAsset_CarriesTheNewestCapturesAndTheirTotal(t *testing.T) {
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", Name: "Dashboard", ContentType: "text/html", Tags: []string{},
		Provenance: portal.Provenance{Captures: adminCaptures(333)},
	}
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, &mockAdminS3Client{})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp portal.Asset
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Len(t, resp.Provenance.Captures, portaldomain.ProvenanceCapturesInline)
	assert.Equal(t, 333, resp.Provenance.CapturesTotal)
	assert.Equal(t, 333, resp.Provenance.Captures[len(resp.Provenance.Captures)-1].Version)
}

func TestListAdminAssetProvenance_PagesNewestFirst(t *testing.T) {
	asset := &portal.Asset{ID: "a1", OwnerID: "u1", Name: "Dashboard", Tags: []string{}}
	h := newAdminTestHandler(
		&mockAdminAssetStore{getAsset: asset, provenancePage: adminNewestFirst(adminCaptures(50))},
		&mockAdminShareStore{}, &mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET",
		"/api/v1/admin/assets/a1/provenance?offset=20&limit=20", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var got adminProvenancePageResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	assert.Equal(t, 50, got.Total)
	assert.Equal(t, 20, got.Offset)
	assert.Equal(t, 20, got.Limit)
	require.Len(t, got.Captures, 20)
	assert.Equal(t, 30, got.Captures[0].Version)
}

func TestListAdminAssetProvenance_DefaultsAndClamps(t *testing.T) {
	asset := &portal.Asset{ID: "a1", OwnerID: "u1", Name: "Dashboard", Tags: []string{}}
	h := newAdminTestHandler(
		&mockAdminAssetStore{getAsset: asset, provenancePage: adminNewestFirst(adminCaptures(500))},
		&mockAdminShareStore{}, &mockAdminS3Client{},
	)

	for query, want := range map[string]int{
		"":            portaldomain.DefaultProvenancePageSize,
		"?limit=5000": portaldomain.MaxProvenancePageSize,
	} {
		req := httptest.NewRequestWithContext(context.Background(), "GET",
			"/api/v1/admin/assets/a1/provenance"+query, http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		var got adminProvenancePageResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
		assert.Equal(t, want, got.Limit, "query %q", query)
		assert.Len(t, got.Captures, want)
	}
}

// A missing asset is a 404 and a failure to read its captures is not reported
// as one.
func TestListAdminAssetProvenance_MissingAssetAndReadFailure(t *testing.T) {
	missing := newAdminTestHandler(
		&mockAdminAssetStore{getErr: errors.New("not found")},
		&mockAdminShareStore{}, &mockAdminS3Client{},
	)
	req := httptest.NewRequestWithContext(context.Background(), "GET",
		"/api/v1/admin/assets/missing/provenance", http.NoBody)
	w := httptest.NewRecorder()
	missing.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)

	broken := newAdminTestHandler(
		&mockAdminAssetStore{
			getAsset:      &portal.Asset{ID: "a1", OwnerID: "u1", Tags: []string{}},
			provenanceErr: errors.New("connection reset"),
		},
		&mockAdminShareStore{}, &mockAdminS3Client{},
	)
	req = httptest.NewRequestWithContext(context.Background(), "GET",
		"/api/v1/admin/assets/a1/provenance", http.NoBody)
	w = httptest.NewRecorder()
	broken.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
