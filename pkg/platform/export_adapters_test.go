package platform

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// --- Adapter unit tests ---

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
	inserted      *portal.Share
	insertErr     error
	getByTokenRes *portal.Share
	getByTokenErr error
}

func (s *stubShareStore) Insert(_ context.Context, share portal.Share) error {
	s.inserted = &share
	return s.insertErr
}

func (s *stubShareStore) GetByToken(_ context.Context, _ string) (*portal.Share, error) {
	return s.getByTokenRes, s.getByTokenErr
}

func TestExportAssetStoreAdapter_Insert(t *testing.T) {
	store := &stubAssetStore{}
	adapter := &exportAssetStoreAdapter{store: store}

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

func TestExportAssetStoreAdapter_InsertError(t *testing.T) {
	store := &stubAssetStore{insertErr: fmt.Errorf("db error")}
	adapter := &exportAssetStoreAdapter{store: store}

	err := adapter.InsertExportAsset(context.Background(), trinokit.ExportAsset{ID: "a1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting export asset")
}

func TestExportAssetStoreAdapter_GetByIdempotencyKey(t *testing.T) {
	store := &stubAssetStore{getByKey: &portal.Asset{ID: "a1", SizeBytes: 999}}
	adapter := &exportAssetStoreAdapter{store: store}

	ref, err := adapter.GetByIdempotencyKey(context.Background(), "u1", "key1")
	require.NoError(t, err)
	assert.Equal(t, "a1", ref.ID)
	assert.Equal(t, int64(999), ref.SizeBytes)
}

func TestExportAssetStoreAdapter_GetByIdempotencyKeyNotFound(t *testing.T) {
	store := &stubAssetStore{getByKeyErr: fmt.Errorf("not found")}
	adapter := &exportAssetStoreAdapter{store: store}

	_, err := adapter.GetByIdempotencyKey(context.Background(), "u1", "key1")
	assert.Error(t, err)
}

func TestExportVersionStoreAdapter(t *testing.T) {
	store := &stubVersionStore{}
	adapter := &exportVersionStoreAdapter{store: store}

	n, err := adapter.CreateExportVersion(context.Background(), trinokit.ExportVersion{
		AssetID: "a1", S3Key: "key", S3Bucket: "b", ContentType: "text/csv",
		SizeBytes: 100, CreatedBy: "alice@example.com", ChangeSummary: "test",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.NotNil(t, store.created)
	assert.Equal(t, "a1", store.created.AssetID)
}

func TestExportVersionStoreAdapter_Error(t *testing.T) {
	store := &stubVersionStore{createErr: fmt.Errorf("db error")}
	adapter := &exportVersionStoreAdapter{store: store}

	_, err := adapter.CreateExportVersion(context.Background(), trinokit.ExportVersion{AssetID: "a1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating export version")
}

func TestExportShareCreatorAdapter(t *testing.T) {
	store := &stubShareStore{}
	adapter := &exportShareCreatorAdapter{shareStore: store, baseURL: "https://example.com"}

	url, err := adapter.CreatePublicShare(context.Background(), "a1", "alice@example.com")
	require.NoError(t, err)
	assert.Contains(t, url, "https://example.com/portal/view/")
	require.NotNil(t, store.inserted)
	assert.Equal(t, "a1", store.inserted.AssetID)
	assert.Equal(t, "alice@example.com", store.inserted.CreatedBy)
	assert.NotEmpty(t, store.inserted.Token)
	assert.NotEmpty(t, store.inserted.NoticeText)
}

func TestExportShareCreatorAdapter_NoBaseURL(t *testing.T) {
	store := &stubShareStore{}
	adapter := &exportShareCreatorAdapter{shareStore: store, baseURL: ""}

	url, err := adapter.CreatePublicShare(context.Background(), "a1", "alice@example.com")
	require.NoError(t, err)
	// Returns just the token when no base URL
	assert.NotEmpty(t, url)
	assert.NotContains(t, url, "http")
}

func TestExportShareCreatorAdapter_InsertError(t *testing.T) {
	store := &stubShareStore{insertErr: fmt.Errorf("db error")}
	adapter := &exportShareCreatorAdapter{shareStore: store, baseURL: "https://example.com"}

	_, err := adapter.CreatePublicShare(context.Background(), "a1", "alice@example.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting share")
}

func TestGenerateShareToken(t *testing.T) {
	token, err := generateShareToken()
	require.NoError(t, err)
	assert.Len(t, token, 64) // 32 bytes = 64 hex chars

	token2, err := generateShareToken()
	require.NoError(t, err)
	assert.NotEqual(t, token, token2)
}

func TestGenerateUUID(t *testing.T) {
	id := generateUUID()
	assert.Contains(t, id, "-")
	// Should be 36 chars: 8-4-4-4-12 = 32 hex + 4 hyphens
	assert.Len(t, id, 36)

	id2 := generateUUID()
	assert.NotEqual(t, id, id2)
}

func TestParseExportConfig(t *testing.T) {
	p := &Platform{config: &Config{
		Portal: PortalConfig{
			Export: PortalExportConfig{
				MaxRows:        50000,
				MaxBytes:       50 * 1024 * 1024,
				DefaultTimeout: "3m",
				MaxTimeout:     "8m",
			},
		},
	}}

	cfg := p.parseExportConfig()
	assert.Equal(t, 50000, cfg.MaxRows)
	assert.Equal(t, int64(50*1024*1024), cfg.MaxBytes)
	assert.Equal(t, 3*60*1e9, float64(cfg.DefaultTimeout))
	assert.Equal(t, 8*60*1e9, float64(cfg.MaxTimeout))
}

func TestParseExportConfigDefaults(t *testing.T) {
	p := &Platform{config: &Config{}}
	cfg := p.parseExportConfig()
	// Zero values — applyExportDefaults fills them in later
	assert.Equal(t, 0, cfg.MaxRows)
}

func TestConvertProvenanceCalls(t *testing.T) {
	calls := convertProvenanceCalls([]trinokit.ExportProvenanceCall{
		{ToolName: "trino_query", Timestamp: "2026-01-01T00:00:00Z", Parameters: map[string]any{"sql": "SELECT 1"}},
	})
	require.Len(t, calls, 1)
	assert.Equal(t, "trino_query", calls[0].ToolName)
	assert.Equal(t, "SELECT 1", calls[0].Parameters["sql"])
}
