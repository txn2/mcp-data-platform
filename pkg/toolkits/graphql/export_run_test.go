package graphql

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPersist_ANamedExportInARunVersionsOneAsset is #1854 for graphql_export.
func TestPersist_ANamedExportInARunVersionsOneAsset(t *testing.T) {
	assets := &fakeAssets{}
	deps := &ExportDeps{AssetStore: assets, VersionStore: assets, S3Client: newFakeBlobs(), S3Bucket: "b"}
	uc := &ExportUserContext{UserID: "script:daily", RunOutputKey: func(n string) string { return "script:s1:" + n }}
	in := exportInput{Name: "orders"}

	first, err := (&Toolkit{}).persist(context.Background(), deps, uc, in, persisted{payload: []byte(`{"data":{}}`)})
	require.NoError(t, err)
	id := first.assetID
	assert.Equal(t, 1, first.version)
	require.Len(t, assets.inserted, 1)
	assert.Equal(t, "script:s1:orders", assets.inserted[0].IdempotencyKey)

	assets.existing = &ExportAssetRef{ID: id}
	again, err := (&Toolkit{}).persist(context.Background(), deps, uc, in, persisted{payload: []byte(`{"data":{}}`)})
	require.NoError(t, err)
	assert.Equal(t, id, again.assetID)
	assert.Equal(t, 2, again.version)
	assert.Len(t, assets.inserted, 1, "no second asset")
	assert.Empty(t, runOutputKey(uc, exportInput{Name: "x", IdempotencyKey: "mine"}))
}

// TestPersist_NoUploadIsLeftWithoutARow covers #1903: a write that records no
// row naming the upload deletes it.
func TestPersist_NoUploadIsLeftWithoutARow(t *testing.T) {
	run := &ExportUserContext{UserID: "script:daily", RunOutputKey: func(n string) string { return "script:s1:" + n }}
	for _, tc := range []struct {
		name     string
		assets   *fakeAssets
		uc       *ExportUserContext
		wantErr  bool
		wantKept int
	}{
		{"the insert fails", &fakeAssets{insertErr: errors.New("db down")}, &ExportUserContext{UserID: "u1"}, true, 0},
		{"a run's version on the existing asset fails", &fakeAssets{existing: &ExportAssetRef{ID: "first"}, versionErr: errors.New("db down")}, run, true, 0},
		{"a run's first asset is inserted and its version fails", &fakeAssets{versionErr: errors.New("db down")}, run, true, 1},
		{"a stored export", &fakeAssets{}, &ExportUserContext{UserID: "u1"}, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			blobs := newFakeBlobs()
			deps := &ExportDeps{AssetStore: tc.assets, VersionStore: tc.assets, S3Client: blobs, S3Bucket: "b"}
			_, err := (&Toolkit{}).persist(context.Background(), deps, tc.uc, exportInput{Name: "orders"}, persisted{payload: []byte(`{"data":{}}`)})
			assert.Equal(t, tc.wantErr, err != nil, "error: %v", err)
			assert.Len(t, blobs.objects, tc.wantKept)
		})
	}
}

// A delete that fails is logged and does not change the call's outcome; with
// no storage client there is nothing to delete.
func TestDiscardExport(t *testing.T) {
	blobs := newFakeBlobs()
	blobs.objects["b/k"] = []byte("x")
	blobs.err = errors.New("denied")
	discardExport(context.Background(), &ExportDeps{S3Client: blobs, S3Bucket: "b"}, "k")
	assert.Len(t, blobs.objects, 1)
	discardExport(context.Background(), &ExportDeps{}, "k")
}
