package platform

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/portalstore"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// newTestPortalHandle builds a portalstore.Handle over noop stores for the
// export-wiring tests. wireTrinoExport / wireAPIGatewayExport only check that
// the accessors are non-nil and hand them to the export adapters, so noop
// stores are sufficient. s3 may be nil to exercise the database-only guard.
func newTestPortalHandle(s3 portal.S3Client) *portalstore.Handle {
	return portalstore.NewFromStores(portalstore.Stores{
		Asset:    portal.NewNoopAssetStore(),
		Version:  portal.NewNoopVersionStore(),
		Share:    portal.NewNoopShareStore(),
		S3Client: s3,
	}, nil, portalstore.Config{})
}

// stubShareStore is consumed by prompt_shared_serving_test.go.
type stubShareStore struct {
	portal.ShareStore
	inserted      *portal.Share
	insertErr     error
	getByTokenRes *portal.Share
	getByTokenErr error
	promptRefs    []portal.SharedPromptRef
}

func (s *stubShareStore) Insert(_ context.Context, share portal.Share) error {
	s.inserted = &share
	return s.insertErr
}

func (*stubShareStore) ListSharedWithUserSince(_ context.Context, _, _ string, _ time.Time, _ int) ([]portal.SharedTargetRef, error) {
	return nil, nil
}

func (s *stubShareStore) ListSharedPromptsWithUser(_ context.Context, _, _ string) ([]portal.SharedPromptRef, error) {
	return s.promptRefs, nil
}

func (s *stubShareStore) GetByToken(_ context.Context, _ string) (*portal.Share, error) {
	return s.getByTokenRes, s.getByTokenErr
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
		portalStore:     newTestPortalHandle(&noopS3Client{}),
		toolkitRegistry: newTestRegistry(tk),
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

func (*noopS3Client) PutObjectStream(_ context.Context, _, _ string, _ io.Reader, _ string) (int64, error) {
	return 0, nil
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
		portalStore:     newTestPortalHandle(&noopS3Client{}),
		toolkitRegistry: newTestRegistry(multiTk),
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
		portalStore:     newTestPortalHandle(&noopS3Client{}),
		toolkitRegistry: newTestRegistry(tk),
	}

	p.wireTrinoExport()
	assert.NotContains(t, tk.Tools(), "trino_export")
}

func TestWireTrinoExport_SkipsWhenNoPortalS3(t *testing.T) {
	tk, err := trinokit.New("test", trinokit.Config{Host: "localhost", User: "test"})
	require.NoError(t, err)
	defer tk.Close() //nolint:errcheck // test cleanup

	p := &Platform{
		config: &Config{},
		// S3 client is nil — no S3 configured; the asset store is still present.
		portalStore:     newTestPortalHandle(nil),
		toolkitRegistry: newTestRegistry(tk),
	}

	p.wireTrinoExport()

	// trino_export should NOT appear because S3 is missing
	assert.NotContains(t, tk.Tools(), "trino_export")
}

func TestWireTrinoExport_SkipsWhenNoTrino(_ *testing.T) {
	p := &Platform{
		config:          &Config{Portal: PortalConfig{S3Bucket: "b"}},
		portalStore:     newTestPortalHandle(&noopS3Client{}),
		toolkitRegistry: registry.NewRegistry(), // empty — no trino
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
