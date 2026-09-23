package graphql

import (
	"context"
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

	first, err := (&Toolkit{}).persist(context.Background(), deps, uc, in, []byte(`{"data":{}}`))
	require.NoError(t, err)
	id := first.assetID
	assert.Equal(t, 1, first.version)
	require.Len(t, assets.inserted, 1)
	assert.Equal(t, "script:s1:orders", assets.inserted[0].IdempotencyKey)

	assets.existing = &ExportAssetRef{ID: id}
	again, err := (&Toolkit{}).persist(context.Background(), deps, uc, in, []byte(`{"data":{}}`))
	require.NoError(t, err)
	assert.Equal(t, id, again.assetID)
	assert.Equal(t, 2, again.version)
	assert.Len(t, assets.inserted, 1, "no second asset")
	assert.Empty(t, runOutputKey(uc, exportInput{Name: "x", IdempotencyKey: "mine"}))
}
