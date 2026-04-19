package platform

import (
	"context"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/registry"
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

func TestWireTrinoExport_ToolAppearsInToolList(t *testing.T) {
	// This is the integration test that proves trino_export actually registers
	// when portal + trino are both configured.

	// Create a real trino toolkit
	tk, err := trinokit.New("test", trinokit.Config{
		Host: "localhost",
		User: "test",
	})
	require.NoError(t, err)
	defer tk.Close() //nolint:errcheck // test cleanup

	// Verify trino_export is NOT in the tool list before wiring
	assert.NotContains(t, tk.Tools(), "trino_export")

	// Create a platform with portal stores and wire export
	p := &Platform{
		config: &Config{
			Portal: PortalConfig{
				S3Bucket: "test-bucket",
				S3Prefix: "exports",
			},
		},
		portalAssetStore:   portal.NewNoopAssetStore(),
		portalVersionStore: portal.NewNoopVersionStore(),
		portalShareStore:   portal.NewNoopShareStore(),
		portalS3Client:     &noopS3Client{},
		toolkitRegistry:    newTestRegistry(tk),
	}

	p.wireTrinoExport()

	// Verify trino_export IS in the tool list after wiring
	assert.Contains(t, tk.Tools(), "trino_export")

	// Verify it registers on an MCP server
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	tk.RegisterTools(server)
}

// noopS3Client implements portal.S3Client for testing.
type noopS3Client struct{}

func (*noopS3Client) PutObject(_ context.Context, _, _ string, _ []byte, _ string) error {
	return nil
}

func (*noopS3Client) GetObject(_ context.Context, _, _ string) (data []byte, contentType string, err error) { //nolint:gocritic // named for clarity
	return nil, "", nil
}
func (*noopS3Client) DeleteObject(_ context.Context, _, _ string) error { return nil }
func (*noopS3Client) Close() error                                      { return nil }

// newTestRegistry creates a registry with a single toolkit.
func newTestRegistry(tk *trinokit.Toolkit) *registry.Registry {
	r := registry.NewRegistry()
	_ = r.Register(tk) //nolint:errcheck // test setup
	return r
}

func TestWireTrinoExport_WithMultiConnectionToolkit(t *testing.T) {
	// Mirror the real deployment: multi-connection trino created via NewMulti
	multiTk, err := trinokit.NewMulti(trinokit.MultiConfig{
		DefaultConnection: "acme",
		Instances: map[string]trinokit.Config{
			"acme": {Host: "localhost", User: "test", Port: 8080},
		},
	})
	require.NoError(t, err)
	defer multiTk.Close() //nolint:errcheck // test cleanup

	assert.NotContains(t, multiTk.Tools(), "trino_export")

	p := &Platform{
		config: &Config{
			Portal: PortalConfig{
				S3Bucket: "portal-assets",
				S3Prefix: "artifacts",
			},
		},
		portalAssetStore:   portal.NewNoopAssetStore(),
		portalVersionStore: portal.NewNoopVersionStore(),
		portalShareStore:   portal.NewNoopShareStore(),
		portalS3Client:     &noopS3Client{},
		toolkitRegistry:    newTestRegistry(multiTk),
	}

	p.wireTrinoExport()

	assert.Contains(t, multiTk.Tools(), "trino_export",
		"trino_export must appear in tool list when portal+trino are both configured")
}

func TestWireTrinoExport_SkipsWhenExplicitlyDisabled(t *testing.T) {
	tk, err := trinokit.New("test", trinokit.Config{Host: "localhost", User: "test"})
	require.NoError(t, err)
	defer tk.Close() //nolint:errcheck // test cleanup

	disabled := false
	p := &Platform{
		config: &Config{
			Portal: PortalConfig{
				Export:   PortalExportConfig{Enabled: &disabled},
				S3Bucket: "b",
			},
		},
		portalAssetStore:   portal.NewNoopAssetStore(),
		portalVersionStore: portal.NewNoopVersionStore(),
		portalShareStore:   portal.NewNoopShareStore(),
		portalS3Client:     &noopS3Client{},
		toolkitRegistry:    newTestRegistry(tk),
	}

	p.wireTrinoExport()
	assert.NotContains(t, tk.Tools(), "trino_export")
}

func TestWireTrinoExport_SkipsWhenNoPortalS3(t *testing.T) {
	tk, err := trinokit.New("test", trinokit.Config{Host: "localhost", User: "test"})
	require.NoError(t, err)
	defer tk.Close() //nolint:errcheck // test cleanup

	p := &Platform{
		config:           &Config{},
		portalAssetStore: portal.NewNoopAssetStore(),
		// portalS3Client is nil — no S3 configured
		toolkitRegistry: newTestRegistry(tk),
	}

	p.wireTrinoExport()

	// trino_export should NOT appear because S3 is missing
	assert.NotContains(t, tk.Tools(), "trino_export")
}

func TestWireTrinoExport_SkipsWhenNoTrino(_ *testing.T) {
	p := &Platform{
		config:             &Config{Portal: PortalConfig{S3Bucket: "b"}},
		portalAssetStore:   portal.NewNoopAssetStore(),
		portalVersionStore: portal.NewNoopVersionStore(),
		portalShareStore:   portal.NewNoopShareStore(),
		portalS3Client:     &noopS3Client{},
		toolkitRegistry:    registry.NewRegistry(), // empty — no trino
	}

	p.wireTrinoExport()
	// No panic, no error — just silently skips
}

func TestTrinoExportRegistersViaFullPlatformInit(t *testing.T) {
	// This test mirrors the real deployment: toolkit registry has trino,
	// portal has S3 + database. Exercises initPortal → wireTrinoExport
	// with a real toolkit registry (same path as the production code).
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	// Build the toolkit registry with trino + s3 (same as initRegistries)
	reg := registry.NewRegistry()
	registry.RegisterBuiltinFactories(reg)
	loader := registry.NewLoader(reg)
	err = loader.LoadFromMap(map[string]any{
		"trino": map[string]any{
			"enabled": true,
			"instances": map[string]any{
				"test": map[string]any{
					"host": "localhost",
					"user": "test",
					"port": 8080,
				},
			},
			"default": "test",
		},
		"s3": map[string]any{
			"enabled": true,
			"instances": map[string]any{
				"test": map[string]any{
					"endpoint":   "http://localhost:9000",
					"region":     "us-east-1",
					"access_key": "test",
					"secret_key": "test",
				},
			},
		},
	})
	require.NoError(t, err)

	// Verify trino loaded
	trinoToolkits := reg.GetByKind("trino")
	require.NotEmpty(t, trinoToolkits, "trino toolkit should be in registry")

	// Build platform with db + registry + portal config (same state as after initRegistries + initDatabase)
	p := &Platform{
		config: &Config{
			Toolkits: map[string]any{
				"s3": map[string]any{
					"enabled": true,
					"instances": map[string]any{
						"test": map[string]any{
							"endpoint":          "http://localhost:9000",
							"region":            "us-east-1",
							"access_key_id":     "test",
							"secret_access_key": "test",
						},
					},
				},
			},
			Portal: PortalConfig{
				S3Connection: "test",
				S3Bucket:     "portal-assets",
				S3Prefix:     "artifacts",
			},
		},
		db:              db,
		toolkitRegistry: reg,
	}

	// Run initPortal — this creates stores and calls wireTrinoExport
	err = p.initPortal()
	require.NoError(t, err)

	// Check trino_export appeared
	var exportTools []string
	for _, tk := range reg.GetByKind("trino") {
		exportTools = append(exportTools, tk.Tools()...)
	}

	assert.Contains(t, exportTools, "trino_export",
		"trino_export must appear after initPortal when db + trino + s3 are configured. Tools found: %v", exportTools)

	// Also verify it registers on the MCP server (simulating Start → RegisterAllTools)
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	reg.RegisterAllTools(server)
}

func TestConvertProvenanceCalls(t *testing.T) {
	calls := convertProvenanceCalls([]trinokit.ExportProvenanceCall{
		{ToolName: "trino_query", Timestamp: "2026-01-01T00:00:00Z", Parameters: map[string]any{"sql": "SELECT 1"}},
	})
	require.Len(t, calls, 1)
	assert.Equal(t, "trino_query", calls[0].ToolName)
	assert.Equal(t, "SELECT 1", calls[0].Parameters["sql"])
}
