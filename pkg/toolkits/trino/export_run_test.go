package trino

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runUser is the user context a script run's call carries: the platform
// hands the export the script's output identity for a name (#1854).
func runUser() *ExportUserContext {
	return &ExportUserContext{
		UserID: "script:daily", UserEmail: "jane@example.com",
		RunOutputKey: func(name string) string { return "script:s1:" + name },
	}
}

// TestStoreAsset_ANamedExportInARunVersionsOneAsset: the first run creates the
// asset under the script's key; a run that finds it writes its next version
// and creates no asset.
func TestStoreAsset_ANamedExportInARunVersionsOneAsset(t *testing.T) {
	assets, versions := &mockExportAssetStore{}, &mockExportVersionStore{}
	tk := newTestExportToolkit(assets, versions, &mockExportS3Client{})
	in := exportInput{Name: "daily", Format: "csv"}
	asset := ExportAsset{ID: "new-1", OwnerID: "script:daily", S3Key: "k1", ContentType: "text/csv"}

	got, hit, errRes := tk.storeAsset(context.Background(), tk.exportDeps, asset, in, runUser())
	require.Nil(t, hit)
	require.Nil(t, errRes)
	assert.Equal(t, "new-1", got.assetID)
	assert.Equal(t, 1, got.version)
	require.NotNil(t, assets.inserted)
	assert.Equal(t, "script:s1:daily", assets.inserted.IdempotencyKey)

	assets.inserted, assets.idempotencyHit = nil, &ExportAssetRef{ID: "new-1"}
	versions.versionNum = 2
	got, _, errRes = tk.storeAsset(context.Background(), tk.exportDeps, ExportAsset{ID: "new-2", S3Key: "k2"}, in, runUser())
	require.Nil(t, errRes)
	assert.Equal(t, "new-1", got.assetID, "the next run writes onto the first run's asset")
	assert.Equal(t, 2, got.version)
	assert.Nil(t, assets.inserted, "no second asset")
	assert.Equal(t, "k2", versions.created.S3Key)
}

// TestStoreAsset_OutsideARunNothingChanges keeps every other call a new asset,
// and an explicit idempotency key its own meaning.
func TestStoreAsset_OutsideARunNothingChanges(t *testing.T) {
	assets := &mockExportAssetStore{}
	tk := newTestExportToolkit(assets, &mockExportVersionStore{}, &mockExportS3Client{})
	user := runUser()
	user.RunOutputKey = nil
	got, _, errRes := tk.storeAsset(context.Background(), tk.exportDeps, ExportAsset{ID: "a"}, exportInput{Name: "x"}, user)
	require.Nil(t, errRes)
	assert.Equal(t, 1, got.version)
	assert.Empty(t, assets.inserted.IdempotencyKey)

	assert.Empty(t, runOutputKey(runUser(), exportInput{Name: "x", IdempotencyKey: "mine"}))
	assert.Empty(t, runOutputKey(runUser(), exportInput{}))
}

func TestStoreAsset_AFailedVersionFailsTheExport(t *testing.T) {
	versions := &mockExportVersionStore{createErr: errors.New("db down")}
	tk := newTestExportToolkit(&mockExportAssetStore{}, versions, &mockExportS3Client{})
	_, _, errRes := tk.storeAsset(context.Background(), tk.exportDeps, ExportAsset{ID: "a"}, exportInput{Name: "daily"}, runUser())
	require.NotNil(t, errRes)
	assert.True(t, errRes.IsError)
}
