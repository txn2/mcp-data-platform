package exportadapters

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// --- graphql adapter tests ---

func TestGraphQLExporter_InsertAsset(t *testing.T) {
	store := &stubAssetStore{}
	adapter := NewGraphQLExporter(store, nil, nil, "", nil)

	err := adapter.InsertExportAsset(context.Background(), graphqlkit.ExportAsset{
		ID:      "a1",
		OwnerID: "u1",
		Name:    "datasets dump",
		Tags:    []string{"catalog"},
		Provenance: graphqlkit.ExportProvenance{
			UserID:    "u1",
			SessionID: "s1",
			ToolCalls: []graphqlkit.ExportProvenanceCall{{
				ToolName:  "graphql_export",
				Timestamp: "2026-01-01T00:00:00Z",
				Parameters: map[string]any{
					"connection": "erp",
					"query":      "{ dataset(urn: \"u\") { urn } }",
				},
			}},
		},
		IdempotencyKey: "key1",
	})
	require.NoError(t, err)
	require.NotNil(t, store.inserted)
	assert.Equal(t, "a1", store.inserted.ID)
	require.Len(t, store.inserted.Provenance.Captures, 1)
	require.Len(t, store.inserted.Provenance.Captures[0].Calls, 1)
	call := store.inserted.Provenance.Captures[0].Calls[0]
	// A GraphQL call's address is one URL, so what a reader needs is the
	// document rather than a request line.
	assert.Equal(t, portal.ProvenanceKindGraphQL, call.Kind)
	assert.Equal(t, "graphql_export", call.Tool)
	assert.Equal(t, "erp", call.Connection)
	assert.Contains(t, call.Statement, "dataset")
}

func TestGraphQLExporter_InsertAssetError(t *testing.T) {
	store := &stubAssetStore{insertErr: errors.New("db down")}
	adapter := NewGraphQLExporter(store, nil, nil, "", nil)

	err := adapter.InsertExportAsset(context.Background(), graphqlkit.ExportAsset{ID: "a1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting export asset")
}

func TestGraphQLExporter_InsertAssetWithNoRecordedCall(t *testing.T) {
	store := &stubAssetStore{}
	adapter := NewGraphQLExporter(store, nil, nil, "", nil)

	err := adapter.InsertExportAsset(context.Background(), graphqlkit.ExportAsset{ID: "a1"})
	require.NoError(t, err)
	require.NotNil(t, store.inserted)
	assert.Empty(t, store.inserted.Provenance.Captures)
}

func TestGraphQLExporter_GetByIdempotencyKey(t *testing.T) {
	store := &stubAssetStore{getByKey: &portal.Asset{ID: "a1", SizeBytes: 99}}
	adapter := NewGraphQLExporter(store, nil, nil, "", nil)

	ref, err := adapter.GetByIdempotencyKey(context.Background(), "u1", "key1")
	require.NoError(t, err)
	assert.Equal(t, "a1", ref.ID)
	assert.Equal(t, int64(99), ref.SizeBytes)
}

func TestGraphQLExporter_GetByIdempotencyKeyError(t *testing.T) {
	store := &stubAssetStore{getByKeyErr: errors.New("not found")}
	adapter := NewGraphQLExporter(store, nil, nil, "", nil)

	_, err := adapter.GetByIdempotencyKey(context.Background(), "u1", "key1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "looking up export idempotency key")
}

func TestGraphQLExporter_CreateVersion(t *testing.T) {
	store := &stubVersionStore{}
	adapter := NewGraphQLExporter(nil, store, nil, "", nil)

	n, err := adapter.CreateExportVersion(context.Background(), graphqlkit.ExportVersion{
		ID: "v1", AssetID: "a1", S3Key: "key", S3Bucket: "b",
		ContentType: "application/json", SizeBytes: 100,
		CreatedBy: "alice@example.com", ChangeSummary: "Initial export",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.NotNil(t, store.created)
	assert.Equal(t, "v1", store.created.ID)
	assert.Equal(t, "Initial export", store.created.ChangeSummary)
}

func TestGraphQLExporter_CreateVersionError(t *testing.T) {
	store := &stubVersionStore{createErr: errors.New("db down")}
	adapter := NewGraphQLExporter(nil, store, nil, "", nil)

	_, err := adapter.CreateExportVersion(context.Background(), graphqlkit.ExportVersion{AssetID: "a1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating export version")
}

func TestGraphQLExporter_CreatePublicShare(t *testing.T) {
	store := &stubShareStore{}
	adapter := NewGraphQLExporter(nil, nil, store, "https://platform.example.com", nil)

	url, err := adapter.CreatePublicShare(context.Background(), "a1", "alice@example.com")
	require.NoError(t, err)
	assert.Contains(t, url, "https://platform.example.com/portal/view/")
	require.NotNil(t, store.inserted)
	assert.Equal(t, "a1", store.inserted.AssetID)
}
