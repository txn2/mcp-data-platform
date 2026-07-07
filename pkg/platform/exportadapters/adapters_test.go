package exportadapters

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// --- stubs ---

type stubAssetStore struct {
	portal.AssetStore
	inserted    *portal.Asset
	insertErr   error
	getByKey    *portal.Asset
	getByKeyErr error
}

func (s *stubAssetStore) Insert(_ context.Context, asset portal.Asset) error {
	s.inserted = &asset
	return s.insertErr
}

func (s *stubAssetStore) GetByIdempotencyKey(_ context.Context, _, _ string) (*portal.Asset, error) {
	if s.getByKey != nil {
		return s.getByKey, nil
	}
	return nil, s.getByKeyErr
}

type stubVersionStore struct {
	portal.VersionStore
	created   *portal.AssetVersion
	createErr error
}

func (s *stubVersionStore) CreateVersion(_ context.Context, v portal.AssetVersion) (int, error) {
	s.created = &v
	if s.createErr != nil {
		return 0, s.createErr
	}
	return 1, nil
}

type stubShareStore struct {
	portal.ShareStore
	inserted  *portal.Share
	insertErr error
}

func (s *stubShareStore) Insert(_ context.Context, share portal.Share) error {
	s.inserted = &share
	return s.insertErr
}

// --- trino adapter tests ---

func TestTrinoExporter_InsertAsset(t *testing.T) {
	store := &stubAssetStore{}
	adapter := NewTrinoExporter(store, nil, nil, "")

	err := adapter.InsertExportAsset(context.Background(), trinokit.ExportAsset{
		ID:      "a1",
		OwnerID: "u1",
		Name:    "Test",
		Tags:    []string{"tag1"},
		Provenance: trinokit.ExportProvenance{
			UserID:    "u1",
			SessionID: "s1",
			ToolCalls: []trinokit.ExportProvenanceCall{
				{ToolName: "trino_query", Timestamp: "2026-01-01T00:00:00Z"},
			},
		},
		IdempotencyKey: "key1",
	})
	require.NoError(t, err)
	require.NotNil(t, store.inserted)
	assert.Equal(t, "a1", store.inserted.ID)
	assert.Equal(t, "key1", store.inserted.IdempotencyKey)
	assert.Len(t, store.inserted.Provenance.ToolCalls, 1)
}

func TestTrinoExporter_InsertAssetError(t *testing.T) {
	store := &stubAssetStore{insertErr: fmt.Errorf("db error")}
	adapter := NewTrinoExporter(store, nil, nil, "")

	err := adapter.InsertExportAsset(context.Background(), trinokit.ExportAsset{ID: "a1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting export asset")
}

func TestTrinoExporter_GetByIdempotencyKey(t *testing.T) {
	store := &stubAssetStore{getByKey: &portal.Asset{ID: "a1", SizeBytes: 999}}
	adapter := NewTrinoExporter(store, nil, nil, "")

	ref, err := adapter.GetByIdempotencyKey(context.Background(), "u1", "key1")
	require.NoError(t, err)
	assert.Equal(t, "a1", ref.ID)
	assert.Equal(t, int64(999), ref.SizeBytes)
}

func TestTrinoExporter_GetByIdempotencyKeyNotFound(t *testing.T) {
	store := &stubAssetStore{getByKeyErr: fmt.Errorf("not found")}
	adapter := NewTrinoExporter(store, nil, nil, "")

	_, err := adapter.GetByIdempotencyKey(context.Background(), "u1", "key1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "looking up export idempotency key")
}

func TestTrinoExporter_CreateVersion(t *testing.T) {
	store := &stubVersionStore{}
	adapter := NewTrinoExporter(nil, store, nil, "")

	n, err := adapter.CreateExportVersion(context.Background(), trinokit.ExportVersion{
		AssetID: "a1", S3Key: "key", S3Bucket: "b", ContentType: "text/csv",
		SizeBytes: 100, CreatedBy: "alice@example.com", ChangeSummary: "test",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.NotNil(t, store.created)
	assert.Equal(t, "a1", store.created.AssetID)
}

func TestTrinoExporter_CreateVersionError(t *testing.T) {
	store := &stubVersionStore{createErr: fmt.Errorf("db error")}
	adapter := NewTrinoExporter(nil, store, nil, "")

	_, err := adapter.CreateExportVersion(context.Background(), trinokit.ExportVersion{AssetID: "a1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating export version")
}

func TestTrinoExporter_CreatePublicShare(t *testing.T) {
	store := &stubShareStore{}
	adapter := NewTrinoExporter(nil, nil, store, "https://example.com")

	url, err := adapter.CreatePublicShare(context.Background(), "a1", "alice@example.com")
	require.NoError(t, err)
	assert.Contains(t, url, "https://example.com/portal/view/")
	require.NotNil(t, store.inserted)
	assert.Equal(t, "a1", store.inserted.AssetID)
	assert.Equal(t, "alice@example.com", store.inserted.CreatedBy)
	assert.NotEmpty(t, store.inserted.Token)
	assert.NotEmpty(t, store.inserted.NoticeText)
}

func TestTrinoExporter_CreatePublicShareNoBaseURL(t *testing.T) {
	store := &stubShareStore{}
	adapter := NewTrinoExporter(nil, nil, store, "")

	url, err := adapter.CreatePublicShare(context.Background(), "a1", "alice@example.com")
	require.NoError(t, err)
	// Empty baseURL → empty share URL. The share row IS still inserted (token
	// exists in DB) so the caller can compute the URL later.
	assert.Empty(t, url)
	assert.NotNil(t, store.inserted)
}

func TestTrinoExporter_CreatePublicShareInsertError(t *testing.T) {
	store := &stubShareStore{insertErr: fmt.Errorf("db error")}
	adapter := NewTrinoExporter(nil, nil, store, "https://example.com")

	_, err := adapter.CreatePublicShare(context.Background(), "a1", "alice@example.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting export share")
}

func TestConvertTrinoProvenanceCalls(t *testing.T) {
	calls := convertTrinoProvenanceCalls([]trinokit.ExportProvenanceCall{
		{ToolName: "trino_query", Timestamp: "2026-01-01T00:00:00Z", Parameters: map[string]any{"sql": "SELECT 1"}},
	})
	require.Len(t, calls, 1)
	assert.Equal(t, "trino_query", calls[0].ToolName)
	assert.Equal(t, "SELECT 1", calls[0].Parameters["sql"])
}

// --- api-gateway adapter tests ---

func TestAPIExporter_InsertAsset(t *testing.T) {
	store := &stubAssetStore{}
	adapter := NewAPIExporter(store, nil, nil, "")

	err := adapter.InsertExportAsset(context.Background(), apigatewaykit.ExportAsset{
		ID:      "a1",
		OwnerID: "u1",
		Name:    "items dump",
		Tags:    []string{"crm", "weekly"},
		Provenance: apigatewaykit.ExportProvenance{
			UserID:    "u1",
			SessionID: "s1",
			ToolCalls: []apigatewaykit.ExportProvenanceCall{
				{ToolName: "api_export", Timestamp: "2026-01-01T00:00:00Z"},
			},
		},
		IdempotencyKey: "key1",
	})
	require.NoError(t, err)
	require.NotNil(t, store.inserted)
	assert.Equal(t, "a1", store.inserted.ID)
	assert.Equal(t, "key1", store.inserted.IdempotencyKey)
	assert.Len(t, store.inserted.Provenance.ToolCalls, 1)
	assert.Equal(t, "api_export", store.inserted.Provenance.ToolCalls[0].ToolName)
}

func TestAPIExporter_InsertAssetError(t *testing.T) {
	store := &stubAssetStore{insertErr: fmt.Errorf("db down")}
	adapter := NewAPIExporter(store, nil, nil, "")

	err := adapter.InsertExportAsset(context.Background(), apigatewaykit.ExportAsset{ID: "a1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting export asset")
}

func TestAPIExporter_GetByIdempotencyKey(t *testing.T) {
	store := &stubAssetStore{getByKey: &portal.Asset{ID: "a1", SizeBytes: 1234}}
	adapter := NewAPIExporter(store, nil, nil, "")

	ref, err := adapter.GetByIdempotencyKey(context.Background(), "u1", "key1")
	require.NoError(t, err)
	assert.Equal(t, "a1", ref.ID)
	assert.Equal(t, int64(1234), ref.SizeBytes)
}

func TestAPIExporter_GetByIdempotencyKeyError(t *testing.T) {
	store := &stubAssetStore{getByKeyErr: fmt.Errorf("not found")}
	adapter := NewAPIExporter(store, nil, nil, "")

	_, err := adapter.GetByIdempotencyKey(context.Background(), "u1", "key1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "looking up export idempotency key")
}

func TestAPIExporter_CreateVersion(t *testing.T) {
	store := &stubVersionStore{}
	adapter := NewAPIExporter(nil, store, nil, "")

	n, err := adapter.CreateExportVersion(context.Background(), apigatewaykit.ExportVersion{
		ID: "v1", AssetID: "a1", S3Key: "key", S3Bucket: "b",
		ContentType: "application/json", SizeBytes: 100,
		CreatedBy: "alice@example.com", ChangeSummary: "Exported from API endpoint",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.NotNil(t, store.created)
	assert.Equal(t, "v1", store.created.ID)
	assert.Equal(t, "a1", store.created.AssetID)
	assert.Equal(t, "Exported from API endpoint", store.created.ChangeSummary)
}

func TestAPIExporter_CreateVersionError(t *testing.T) {
	store := &stubVersionStore{createErr: fmt.Errorf("db down")}
	adapter := NewAPIExporter(nil, store, nil, "")

	_, err := adapter.CreateExportVersion(context.Background(), apigatewaykit.ExportVersion{AssetID: "a1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating export version")
}

func TestAPIExporter_CreatePublicShare(t *testing.T) {
	store := &stubShareStore{}
	adapter := NewAPIExporter(nil, nil, store, "https://platform.example.com")

	url, err := adapter.CreatePublicShare(context.Background(), "a1", "alice@example.com")
	require.NoError(t, err)
	assert.Contains(t, url, "https://platform.example.com/portal/view/")
	require.NotNil(t, store.inserted)
	assert.Equal(t, "a1", store.inserted.AssetID)
	assert.Equal(t, "alice@example.com", store.inserted.CreatedBy)
	assert.NotEmpty(t, store.inserted.Token)
	assert.NotEmpty(t, store.inserted.NoticeText)
}

func TestAPIExporter_CreatePublicShareNoBaseURL(t *testing.T) {
	store := &stubShareStore{}
	adapter := NewAPIExporter(nil, nil, store, "")

	url, err := adapter.CreatePublicShare(context.Background(), "a1", "alice@example.com")
	require.NoError(t, err)
	assert.Empty(t, url)
	assert.NotNil(t, store.inserted)
}

func TestAPIExporter_CreatePublicShareInsertError(t *testing.T) {
	store := &stubShareStore{insertErr: fmt.Errorf("db error")}
	adapter := NewAPIExporter(nil, nil, store, "https://x")

	_, err := adapter.CreatePublicShare(context.Background(), "a1", "alice@example.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting export share")
}

func TestConvertAPIProvenanceCalls(t *testing.T) {
	calls := []apigatewaykit.ExportProvenanceCall{
		{ToolName: "api_export", Timestamp: "2026-01-01T00:00:00Z", Parameters: map[string]any{"k": "v"}},
		{ToolName: "api_invoke_endpoint", Timestamp: "2026-01-01T00:00:01Z"},
	}
	got := convertAPIProvenanceCalls(calls)
	require.Len(t, got, 2)
	assert.Equal(t, "api_export", got[0].ToolName)
	assert.Equal(t, "v", got[0].Parameters["k"])
	assert.Equal(t, "api_invoke_endpoint", got[1].ToolName)
}

func TestConvertAPIProvenanceCalls_Empty(t *testing.T) {
	got := convertAPIProvenanceCalls(nil)
	assert.Empty(t, got)
}
