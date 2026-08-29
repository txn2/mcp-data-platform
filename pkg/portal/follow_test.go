package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A content write moves the asset's head, so the tables registered over its
// file follow it (#1536). What these hold is that both of this handler's
// version writers -- the editor's PUT and a revert -- hand the version they
// wrote to the hook and carry its answer in the response, and that a
// deployment with no registrar answers exactly as before.

// followRecorder stands in for the registrar's hook: it records what it was
// asked about and answers with a fixed report.
type followRecorder struct {
	asked  []string
	answer []string
}

func (f *followRecorder) hook(_ context.Context, id string, version int) []string {
	f.asked = append(f.asked, id+"@"+strconv.Itoa(version))
	return f.answer
}

func TestUpdateAssetContentFollowsTheTablesOverTheFile(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/csv", S3Bucket: "b", S3Key: "k", CurrentVersion: 1}
	follow := &followRecorder{answer: []string{"scratch.uploads.t on scratch now reads version 2."}}
	h := newTestHandlerWithVersions(&mockAssetStore{getAsset: asset}, &mockShareStore{},
		&mockVersionStore{createVersion: 2}, &mockS3Client{}, &User{UserID: "u1"})
	h.deps.OnAssetRevised = follow.hook

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/content",
		strings.NewReader("a,b\n1,2\n"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp statusResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "updated", resp.Status)
	assert.Equal(t, []string{"scratch.uploads.t on scratch now reads version 2."}, resp.Tables)
	assert.Equal(t, []string{"a1@2"}, follow.asked, "the hook is given the version the write produced")
}

func TestRevertToVersionFollowsTheTablesOverTheFile(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", CurrentVersion: 2}
	targetVer := &AssetVersion{ID: "v1", AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "b", ContentType: "text/csv"}
	follow := &followRecorder{answer: []string{"scratch.uploads.t on scratch is pinned"}}
	h := newTestHandlerWithVersions(&mockAssetStore{getAsset: asset}, &mockShareStore{},
		&mockVersionStore{getVersion: targetVer, createVersion: 3},
		&mockS3Client{getData: []byte("a,b\n"), getCT: "text/csv"}, &User{UserID: "u1"})
	h.deps.OnAssetRevised = follow.hook

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/versions/1/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var result map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Equal(t, float64(3), result["version"])
	assert.Equal(t, []any{"scratch.uploads.t on scratch is pinned"}, result["tables"])
	assert.Equal(t, []string{"a1@3"}, follow.asked)
}

// TestContentWritesWithoutARegistrarSayNothingAboutTables: the field is absent
// rather than empty, so a deployment that cannot register tables answers as
// it always did.
func TestContentWritesWithoutARegistrarSayNothingAboutTables(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/csv", S3Bucket: "b", S3Key: "k", CurrentVersion: 1}
	h := newTestHandlerWithVersions(&mockAssetStore{getAsset: asset}, &mockShareStore{},
		&mockVersionStore{createVersion: 2}, &mockS3Client{}, &User{UserID: "u1"})

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/content",
		strings.NewReader("a,b\n1,2\n"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "tables")
}
