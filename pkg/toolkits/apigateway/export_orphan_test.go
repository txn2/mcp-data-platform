package apigateway

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRecordExportAsset_NoUploadIsLeftWithoutARow covers #1903: a write that
// records no row naming the upload deletes it, and one that does keeps it.
func TestRecordExportAsset_NoUploadIsLeftWithoutARow(t *testing.T) {
	run := &ExportUserContext{UserID: "script:daily", RunOutputKey: func(n string) string { return "script:s1:" + n }}
	existing := map[string]*ExportAssetRef{"script:daily:script:s1:orders": {ID: "first"}}
	for _, tc := range []struct {
		name        string
		assets      *fakeExportAssetStore
		versionErr  error
		uc          *ExportUserContext
		wantErr     bool
		wantDeleted bool
	}{
		{"the insert fails", &fakeExportAssetStore{insertErr: errors.New("db down")}, nil, &ExportUserContext{UserID: "u1"}, true, true},
		{"a run's version on the existing asset fails", &fakeExportAssetStore{idempLookups: existing}, errors.New("db down"), run, true, true},
		{"a run's first asset is inserted and its version fails", &fakeExportAssetStore{}, errors.New("db down"), run, true, false},
		{"a stored export", &fakeExportAssetStore{}, nil, &ExportUserContext{UserID: "u1"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s3 := &fakeExportS3Client{}
			deps := &ExportDeps{AssetStore: tc.assets, VersionStore: &fakeExportVersionStore{createErr: tc.versionErr}, S3Client: s3, S3Bucket: "b"}
			obj := exportObject{assetID: "new-1", s3Key: "exports/u1/new-1/content.json", size: 2, contentType: "application/json"}
			_, _, err := recordExportAsset(context.Background(), persistExportArgs{deps: deps, uc: tc.uc, in: exportInput{Name: "orders"}}, obj, ExportProvenance{})
			assert.Equal(t, tc.wantErr, err != nil, "error: %v", err)
			if tc.wantDeleted {
				assert.Equal(t, []string{obj.s3Key}, s3.deleted)
			} else {
				assert.Empty(t, s3.deleted)
			}
		})
	}
}

// A delete that fails is logged and does not change the call's outcome; with
// no storage client there is nothing to delete.
func TestDiscardExport(t *testing.T) {
	s3 := &fakeExportS3Client{deleteErr: errors.New("denied")}
	discardExport(context.Background(), &ExportDeps{S3Client: s3, S3Bucket: "b"}, "k")
	assert.Equal(t, []string{"k"}, s3.deleted)
	discardExport(context.Background(), &ExportDeps{}, "k")
}
