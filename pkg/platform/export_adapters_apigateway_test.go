package platform

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// The trino-side adapter tests in export_adapters_test.go cover the
// shared stubAssetStore / stubVersionStore / stubShareStore types
// — those same stubs work here because they implement the
// portal.* interfaces and the api gateway adapters wrap the same
// portal stores. Each test below exercises an api gateway adapter
// pass-through to confirm the conversion preserves field shape.

func TestAPIExportAssetStoreAdapter_Insert(t *testing.T) {
	store := &stubAssetStore{}
	adapter := &apiExportAssetStoreAdapter{store: store}

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

func TestAPIExportAssetStoreAdapter_InsertError(t *testing.T) {
	store := &stubAssetStore{insertErr: fmt.Errorf("db down")}
	adapter := &apiExportAssetStoreAdapter{store: store}

	err := adapter.InsertExportAsset(context.Background(), apigatewaykit.ExportAsset{ID: "a1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting api_export asset")
}

func TestAPIExportAssetStoreAdapter_GetByIdempotencyKey(t *testing.T) {
	store := &stubAssetStore{getByKey: &portal.Asset{ID: "a1", SizeBytes: 1234}}
	adapter := &apiExportAssetStoreAdapter{store: store}

	ref, err := adapter.GetByIdempotencyKey(context.Background(), "u1", "key1")
	require.NoError(t, err)
	assert.Equal(t, "a1", ref.ID)
	assert.Equal(t, int64(1234), ref.SizeBytes)
}

func TestAPIExportAssetStoreAdapter_GetByIdempotencyKeyError(t *testing.T) {
	store := &stubAssetStore{getByKeyErr: fmt.Errorf("not found")}
	adapter := &apiExportAssetStoreAdapter{store: store}

	_, err := adapter.GetByIdempotencyKey(context.Background(), "u1", "key1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "looking up api_export idempotency key")
}

func TestAPIExportVersionStoreAdapter(t *testing.T) {
	store := &stubVersionStore{}
	adapter := &apiExportVersionStoreAdapter{store: store}

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

func TestAPIExportVersionStoreAdapter_Error(t *testing.T) {
	store := &stubVersionStore{createErr: fmt.Errorf("db down")}
	adapter := &apiExportVersionStoreAdapter{store: store}

	_, err := adapter.CreateExportVersion(context.Background(), apigatewaykit.ExportVersion{AssetID: "a1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating api_export version")
}

func TestAPIExportShareCreatorAdapter(t *testing.T) {
	store := &stubShareStore{}
	adapter := &apiExportShareCreatorAdapter{shareStore: store, baseURL: "https://platform.example.com"}

	url, err := adapter.CreatePublicShare(context.Background(), "a1", "alice@example.com")
	require.NoError(t, err)
	assert.Contains(t, url, "https://platform.example.com/portal/view/")
	require.NotNil(t, store.inserted)
	assert.Equal(t, "a1", store.inserted.AssetID)
	assert.Equal(t, "alice@example.com", store.inserted.CreatedBy)
	assert.NotEmpty(t, store.inserted.Token)
	assert.NotEmpty(t, store.inserted.NoticeText)
}

func TestAPIExportShareCreatorAdapter_NoBaseURL(t *testing.T) {
	store := &stubShareStore{}
	adapter := &apiExportShareCreatorAdapter{shareStore: store, baseURL: ""}

	url, err := adapter.CreatePublicShare(context.Background(), "a1", "alice@example.com")
	require.NoError(t, err)
	// Empty baseURL → empty url; the share row is still inserted so
	// the share token exists in the DB even if the operator hasn't
	// configured a public-base-url for portal.
	assert.Empty(t, url)
	assert.NotNil(t, store.inserted)
}

func TestAPIExportShareCreatorAdapter_InsertError(t *testing.T) {
	store := &stubShareStore{insertErr: fmt.Errorf("db error")}
	adapter := &apiExportShareCreatorAdapter{shareStore: store, baseURL: "https://x"}

	_, err := adapter.CreatePublicShare(context.Background(), "a1", "alice@example.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting api_export share")
}

func TestConvertAPIGatewayProvenanceCalls(t *testing.T) {
	calls := []apigatewaykit.ExportProvenanceCall{
		{ToolName: "api_export", Timestamp: "2026-01-01T00:00:00Z", Parameters: map[string]any{"k": "v"}},
		{ToolName: "api_invoke_endpoint", Timestamp: "2026-01-01T00:00:01Z"},
	}
	got := convertAPIGatewayProvenanceCalls(calls)
	require.Len(t, got, 2)
	assert.Equal(t, "api_export", got[0].ToolName)
	assert.Equal(t, "v", got[0].Parameters["k"])
	assert.Equal(t, "api_invoke_endpoint", got[1].ToolName)
}

func TestConvertAPIGatewayProvenanceCalls_Empty(t *testing.T) {
	got := convertAPIGatewayProvenanceCalls(nil)
	assert.Empty(t, got)
}

// TestWireAPIGatewayExport_DisabledByConfig proves the explicit
// portal.export.enabled=false short-circuits before adapter
// construction. Operators expect the toggle to be effective.
func TestWireAPIGatewayExport_DisabledByConfig(_ *testing.T) {
	disabled := false
	p := &Platform{
		config: &Config{Portal: PortalConfig{Export: PortalExportConfig{Enabled: &disabled}}},
	}
	// Must not panic and must not require S3/asset store.
	p.wireAPIGatewayExport()
}

// TestWireAPIGatewayExport_NoPortalSkips proves the missing-S3 /
// missing-asset-store guards. Without a portal asset store there's
// nothing to write to, so api_export must stay unwired.
func TestWireAPIGatewayExport_NoPortalSkips(_ *testing.T) {
	p := &Platform{
		config: &Config{Portal: PortalConfig{Export: PortalExportConfig{}}},
		// portalS3Client and portalAssetStore both nil.
	}
	// Must not panic; toolkitRegistry stays nil because we never
	// reach the GetByKind call.
	p.wireAPIGatewayExport()
}

// TestWireAPIGatewayExport_ToolAppearsInToolList exercises the
// happy-path wire: portal + api gateway both configured, the
// adapters get assembled, SetExportDeps lands on the toolkit, and
// api_export now appears in tk.Tools(). Mirrors the trino-side
// TestWireTrinoExport_ToolAppearsInToolList test.
func TestWireAPIGatewayExport_ToolAppearsInToolList(t *testing.T) {
	mc, err := apigatewaykit.ParseMultiConfig("api", map[string]map[string]any{
		"crm": {"base_url": "https://api.example.com"},
	})
	require.NoError(t, err)
	tk := apigatewaykit.NewMulti(mc)
	t.Cleanup(func() { _ = tk.Close() })

	// api_export should NOT be in the tool list before wiring.
	assert.NotContains(t, tk.Tools(), "api_export")

	r := registry.NewRegistry()
	require.NoError(t, r.Register(tk))

	p := &Platform{
		config: &Config{
			Portal: PortalConfig{
				S3Bucket:      "exports",
				S3Prefix:      "data",
				PublicBaseURL: "https://platform.example.com",
			},
		},
		portalAssetStore:   portal.NewNoopAssetStore(),
		portalVersionStore: portal.NewNoopVersionStore(),
		portalShareStore:   portal.NewNoopShareStore(),
		portalS3Client:     &apiNoopS3Client{},
		toolkitRegistry:    r,
	}

	p.wireAPIGatewayExport()

	// After wiring, api_export must appear in the tool list.
	assert.Contains(t, tk.Tools(), "api_export")
}

// apiNoopS3Client satisfies portal.S3Client for the wire test —
// no PutObject is exercised here, but the platform's wireAPIGatewayExport
// requires a non-nil portalS3Client to proceed past the guard.
type apiNoopS3Client struct{}

func (*apiNoopS3Client) PutObject(_ context.Context, _, _ string, _ []byte, _ string) error {
	return nil
}

func (*apiNoopS3Client) GetObject(_ context.Context, _, _ string) (data []byte, contentType string, err error) { //nolint:gocritic // named for clarity
	return nil, "", nil
}
func (*apiNoopS3Client) DeleteObject(_ context.Context, _, _ string) error { return nil }
func (*apiNoopS3Client) Close() error                                      { return nil }
