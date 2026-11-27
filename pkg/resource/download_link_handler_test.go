package resource

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
)

// TestHandleDownloadLink: the portal's Copy link mints a link of the kind
// asked for to a file the caller can read, a single download when none is
// asked for; a kind there is none of, a file the caller cannot see and a
// deployment that cannot mint are refused (#2847).
func TestHandleDownloadLink(t *testing.T) {
	store, s3 := newMockStore(), newMockS3()
	seedResource(store, s3, "res-1", ScopeUser, "someone-else", "someone-else")
	seedResource(store, s3, "res-2", ScopeGlobal, "", "user-123")
	expiresAt := time.Date(2026, 11, 27, 13, 0, 0, 0, time.UTC)
	asked := "unset"
	deps := Deps{
		Store: store, S3Client: s3, S3Bucket: "test-bucket", URIScheme: "mcp",
		DownloadLink: func(res *Resource, expires string) (string, time.Time, error) {
			if expires != "" && expires != "single_download" && expires != "6h" {
				return "", time.Time{}, errors.New("expires is single_download or 6h")
			}
			asked = expires
			return "https://p.example.com/api/v1/resources/downloads/tok/" + res.Filename, expiresAt, nil
		},
	}
	h := NewHandler(deps, memberExtractor, nil)
	get := func(h *Handler, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody))
		return rec
	}

	rec := get(h, "/api/v1/resources/res-2/download-link?expires=6h")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "https://p.example.com/api/v1/resources/downloads/tok/test.csv", out["url"])
	assert.Equal(t, "2026-11-27T13:00:00Z", out["expires_at"])
	assert.Equal(t, "6h", out["expires"])
	assert.Equal(t, "6h", asked)
	rec = get(h, "/api/v1/resources/res-2/download-link")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, asked, "none asked is the minter's default")
	assert.Contains(t, rec.Body.String(), `"expires":"single_download"`)

	rec = get(h, "/api/v1/resources/res-2/download-link?expires=2h")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "single_download or 6h")
	assert.Equal(t, http.StatusNotFound, get(h, "/api/v1/resources/res-1/download-link").Code, "a file the caller cannot read")

	deps.DownloadLink = nil
	assert.Equal(t, http.StatusServiceUnavailable, get(NewHandler(deps, memberExtractor, nil), "/api/v1/resources/res-2/download-link").Code)
}
