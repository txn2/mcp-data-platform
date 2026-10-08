package exportadapters

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/exporttrunc"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// latestVersionStore answers GetLatest, the read an idempotency hit on a
// truncated asset makes.
type latestVersionStore struct {
	stubVersionStore
	latest    *portal.AssetVersion
	latestErr error
	reads     int
}

func (s *latestVersionStore) GetLatest(context.Context, string) (*portal.AssetVersion, error) {
	s.reads++
	return s.latest, s.latestErr
}

var cutMeta = map[string]any{exporttrunc.MetaTruncated: true, exporttrunc.MetaLimitApplied: float64(100)}

// Each exporter hands the version's metadata to the store, which is how the
// asset comes to carry the cut (#2057).
func TestExportersPassTheVersionMetadata(t *testing.T) {
	ctx := context.Background()
	versions := &stubVersionStore{}
	_, err := NewTrinoExporter(nil, versions, nil, "", nil).CreateExportVersion(ctx, trinokit.ExportVersion{AssetID: "a", Metadata: cutMeta})
	require.NoError(t, err)
	assert.Equal(t, cutMeta, versions.created.Metadata)

	versions = &stubVersionStore{}
	_, err = NewAPIExporter(nil, versions, nil, "", nil).CreateExportVersion(ctx, apigatewaykit.ExportVersion{AssetID: "a", Metadata: cutMeta})
	require.NoError(t, err)
	assert.Equal(t, cutMeta, versions.created.Metadata)

	versions = &stubVersionStore{}
	_, err = NewGraphQLExporter(nil, versions, nil, "", nil).CreateExportVersion(ctx, graphqlkit.ExportVersion{AssetID: "a", Metadata: cutMeta})
	require.NoError(t, err)
	assert.Equal(t, cutMeta, versions.created.Metadata)
}

// An idempotency hit on a tagged asset reads the cut off its current version;
// an untagged one reads nothing; a failed read reports no cut rather than
// failing the hit.
func TestIdempotencyHitReadsTheCut(t *testing.T) {
	ctx := context.Background()
	tagged := &portal.Asset{ID: "a1", Tags: []string{"sales", exporttrunc.Tag}}
	versions := &latestVersionStore{latest: &portal.AssetVersion{Metadata: cutMeta}}

	ref, err := NewTrinoExporter(&stubAssetStore{getByKey: tagged}, versions, nil, "", nil).GetByIdempotencyKey(ctx, "u", "k")
	require.NoError(t, err)
	assert.Equal(t, cutMeta, ref.Metadata)

	apiRef, err := NewAPIExporter(&stubAssetStore{getByKey: tagged}, versions, nil, "", nil).GetByIdempotencyKey(ctx, "u", "k")
	require.NoError(t, err)
	assert.Equal(t, cutMeta, apiRef.Metadata)

	gqlRef, err := NewGraphQLExporter(&stubAssetStore{getByKey: tagged}, versions, nil, "", nil).GetByIdempotencyKey(ctx, "u", "k")
	require.NoError(t, err)
	assert.Equal(t, cutMeta, gqlRef.Metadata)

	reads := versions.reads
	ref, err = NewTrinoExporter(&stubAssetStore{getByKey: &portal.Asset{ID: "a2"}}, versions, nil, "", nil).GetByIdempotencyKey(ctx, "u", "k")
	require.NoError(t, err)
	assert.Nil(t, ref.Metadata)
	assert.Equal(t, reads, versions.reads, "an untagged asset costs no version read")

	failing := &latestVersionStore{latestErr: errors.New("db down")}
	ref, err = NewTrinoExporter(&stubAssetStore{getByKey: tagged}, failing, nil, "", nil).GetByIdempotencyKey(ctx, "u", "k")
	require.NoError(t, err, "the hit stands")
	assert.Nil(t, ref.Metadata)
}
