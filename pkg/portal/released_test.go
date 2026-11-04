package portal

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// heldAsset is an asset an application's release holds at version 1 while
// version 2 is its newest (#2411).
func heldAsset() *Asset {
	now := time.Now()
	return &Asset{
		ID: "a1", OwnerID: "u1", Name: "Storefront", ContentType: "text/plain", S3Bucket: "b", S3Key: "k2",
		CurrentVersion: 2, ReleasedVersion: 1, Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}
}

func heldVersions() *mockVersionStore {
	return &mockVersionStore{getVersion: &AssetVersion{AssetID: "a1", Version: 1, S3Bucket: "b", S3Key: "k1", ContentType: "text/plain"}}
}

// The content route serves the version the release pins, not the newest.
func TestGetAssetContent_ServesTheReleasedVersion(t *testing.T) {
	s3 := &mockS3Client{getData: []byte("released"), getCT: "text/plain"}
	h := newTestHandlerWithVersions(&mockAssetStore{getAsset: heldAsset()}, &mockShareStore{}, heldVersions(), s3, &User{UserID: "u1"})
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "k1", s3.getKey, "the released version's object is read")

	h = newTestHandlerWithVersions(&mockAssetStore{getAsset: heldAsset()}, &mockShareStore{},
		&mockVersionStore{getErr: errors.New("gone")}, s3, &User{UserID: "u1"})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code, "a held version that cannot be read is never answered with the newest")
}

// A public share serves the released version too.
func TestPublicView_ServesTheReleasedVersion(t *testing.T) {
	share := &Share{AccessMode: AccessModePublic, ID: "s1", AssetID: "a1", Token: "tok1", NoticeText: defaultNoticeText}
	s3 := &mockS3Client{getData: []byte("released"), getCT: "text/plain"}
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: heldAsset()}, ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client: s3, S3Bucket: "b", VersionStore: heldVersions(),
	}, nil)
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "k1", s3.getKey)
}
