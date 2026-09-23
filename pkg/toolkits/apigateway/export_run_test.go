package apigateway

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecordExportAsset_ANamedExportInARunVersionsOneAsset is #1854 for
// api_export: the first run creates the asset under the script's key, the
// next writes its second version.
func TestRecordExportAsset_ANamedExportInARunVersionsOneAsset(t *testing.T) {
	assets, versions := &fakeExportAssetStore{}, &fakeExportVersionStore{}
	deps := &ExportDeps{AssetStore: assets, VersionStore: versions, S3Bucket: "b"}
	uc := &ExportUserContext{UserID: "script:daily", RunOutputKey: func(n string) string { return "script:s1:" + n }}
	p := persistExportArgs{deps: deps, uc: uc, in: exportInput{Name: "orders"}}

	id, v, err := recordExportAsset(context.Background(), p, exportObject{assetID: "a1", s3Key: "k1"}, ExportProvenance{})
	require.NoError(t, err)
	assert.Equal(t, "a1", id)
	assert.Equal(t, 1, v)
	require.Len(t, assets.inserted, 1)
	assert.Equal(t, "script:s1:orders", assets.inserted[0].IdempotencyKey)

	assets.idempLookups = map[string]*ExportAssetRef{"script:daily:script:s1:orders": {ID: "a1"}}
	id, v, err = recordExportAsset(context.Background(), p, exportObject{assetID: "a2", s3Key: "k2"}, ExportProvenance{})
	require.NoError(t, err)
	assert.Equal(t, "a1", id)
	assert.Equal(t, 2, v)
	assert.Len(t, assets.inserted, 1, "no second asset")

	versions.createErr = errors.New("db down")
	_, _, err = recordExportAsset(context.Background(), p, exportObject{assetID: "a3"}, ExportProvenance{})
	require.ErrorContains(t, err, "api_export")
	assert.Empty(t, runOutputKey(uc, exportInput{Name: "x", IdempotencyKey: "mine"}))
}
