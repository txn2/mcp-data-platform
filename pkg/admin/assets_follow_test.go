package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// An administrator's content write moves the asset's head exactly as the
// owner's does, so the tables registered over its file follow it (#1536).

type followRecorder struct {
	asked  []string
	answer []string
}

func (f *followRecorder) hook(_ context.Context, id string, version int) []string {
	f.asked = append(f.asked, id+"@"+strconv.Itoa(version))
	return f.answer
}

func followingAdminHandler(follow *followRecorder, versions *mockAdminVersionStore, s3 *mockAdminS3Client) *Handler {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/csv",
		S3Bucket: "b", S3Key: "k", CurrentVersion: 1,
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	return NewHandler(Deps{
		AssetStore:     &mockAdminAssetStore{getAsset: asset},
		ShareStore:     &mockAdminShareStore{},
		VersionStore:   versions,
		S3Client:       s3,
		S3Bucket:       "test-bucket",
		OnAssetRevised: follow.hook,
	}, nil)
}

func TestUpdateAdminAssetContentFollowsTheTablesOverTheFile(t *testing.T) {
	follow := &followRecorder{answer: []string{"scratch.uploads.t on scratch now reads version 2."}}
	h := followingAdminHandler(follow, &mockAdminVersionStore{createVersion: 2}, &mockAdminS3Client{})

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1/content",
		strings.NewReader("a,b\n1,2\n"))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp statusResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "updated", resp.Status)
	assert.Equal(t, []string{"scratch.uploads.t on scratch now reads version 2."}, resp.Tables)
	assert.Equal(t, []string{"a1@2"}, follow.asked)
}

func TestRevertAdminVersionFollowsTheTablesOverTheFile(t *testing.T) {
	follow := &followRecorder{answer: []string{"scratch.uploads.t on scratch is pinned"}}
	ver := &portal.AssetVersion{ID: "v1", AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "b", ContentType: "text/csv"}
	h := followingAdminHandler(follow,
		&mockAdminVersionStore{getVersion: ver, createVersion: 3},
		&mockAdminS3Client{getData: []byte("a,b\n"), getCT: "text/csv"})

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/admin/assets/a1/versions/1/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var result map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Equal(t, float64(3), result["version"])
	assert.Equal(t, []any{"scratch.uploads.t on scratch is pinned"}, result["tables"])
	assert.Equal(t, []string{"a1@3"}, follow.asked)
}
