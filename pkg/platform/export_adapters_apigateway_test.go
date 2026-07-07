package platform

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// The api-gateway export adapters live in the exportadapters subpackage,
// where their pass-through conversion is unit-tested. The tests below cover
// the platform-side wiring: the guards that decide whether api_export is
// registered, and the happy-path assembly that lands SetExportDeps on the
// toolkit.

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

func (*apiNoopS3Client) PutObjectStream(_ context.Context, _, _ string, _ io.Reader, _ string) (int64, error) {
	return 0, nil
}

func (*apiNoopS3Client) GetObject(_ context.Context, _, _ string) (data []byte, contentType string, err error) { //nolint:gocritic // named for clarity
	return nil, "", nil
}
func (*apiNoopS3Client) DeleteObject(_ context.Context, _, _ string) error { return nil }
func (*apiNoopS3Client) Close() error                                      { return nil }
