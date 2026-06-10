//go:build integration

package helpers

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/storage"
)

// TestPlatform wraps a platform instance for E2E testing.
type TestPlatform struct {
	Platform *platform.Platform
	Config   *E2EConfig
}

// NewTestPlatform creates a new test platform with E2E configuration.
func NewTestPlatform(ctx context.Context, e2eCfg *E2EConfig) (*TestPlatform, error) {
	cfg := buildPlatformConfig(e2eCfg)

	p, err := platform.New(
		platform.WithConfig(cfg),
	)
	if err != nil {
		return nil, fmt.Errorf("creating platform: %w", err)
	}

	return &TestPlatform{
		Platform: p,
		Config:   e2eCfg,
	}, nil
}

// buildPlatformConfig creates platform configuration from E2E config.
func buildPlatformConfig(e2eCfg *E2EConfig) *platform.Config {
	return &platform.Config{
		Server: platform.ServerConfig{
			Name:      "e2e-test-platform",
			Transport: "stdio",
		},
		Semantic: platform.SemanticConfig{
			Provider: "datahub",
			Instance: "e2e",
			Cache: platform.CacheConfig{
				Enabled: false,
			},
		},
		Query: platform.QueryConfig{
			Provider: "trino",
			Instance: "e2e",
		},
		Storage: platform.StorageConfig{
			Provider: "s3",
			Instance: "e2e",
		},
		Injection: platform.InjectionConfig{
			TrinoSemanticEnrichment:  new(true),
			DataHubQueryEnrichment:   new(true),
			S3SemanticEnrichment:     new(true),
			DataHubStorageEnrichment: new(true),
		},
		Toolkits: map[string]any{
			// enabled:true is required per kind: the registry loader skips any
			// toolkit kind that does not set it (loader.LoadFromMap). Without
			// this the trino/datahub/s3 toolkits load nothing and their tools
			// never register on the assembled server.
			"trino": map[string]any{
				"enabled": true,
				"instances": map[string]any{
					"e2e": map[string]any{
						"host":            e2eCfg.TrinoHost,
						"port":            e2eCfg.TrinoPort,
						"user":            "e2e-test",
						"catalog":         "memory",
						"schema":          "e2e_test",
						"connection_name": "e2e-trino",
					},
				},
			},
			"datahub": map[string]any{
				"enabled": true,
				"instances": map[string]any{
					"e2e": map[string]any{
						"url": e2eCfg.DataHubURL,
						// The datahub provider requires a non-empty token at
						// construction. Default a placeholder so the assembled
						// platform still builds when DataHub is not configured,
						// letting Trino/S3 assertions run; enrichment assertions
						// gate on real reachability and skip in that case. A real
						// E2E_DATAHUB_TOKEN takes precedence for full runs.
						"token": orDefault(e2eCfg.DataHubToken, "e2e-placeholder-token"),
					},
				},
			},
			"s3": map[string]any{
				"enabled": true,
				"instances": map[string]any{
					"e2e": map[string]any{
						"endpoint":          e2eCfg.S3Endpoint,
						"access_key_id":     e2eCfg.S3AccessKey,
						"secret_access_key": e2eCfg.S3SecretKey,
						"region":            e2eCfg.S3Region,
						"connection_name":   "e2e-s3",
					},
				},
			},
		},
		Database: platform.DatabaseConfig{
			DSN: e2eCfg.PostgresDSN,
		},
		// Allow an anonymous caller mapped to an allow-all admin persona so a
		// test driving the assembled server over an in-process session is
		// authorized. Personas are deny-by-default (DefaultPersona denies "*"),
		// so without this every tool call through the real middleware chain
		// would be rejected before reaching the handler. Tests that bypass the
		// authorizer (calling the enrichment middleware directly) are unaffected.
		Auth: platform.AuthConfig{
			AllowAnonymous: true,
		},
		Personas: platform.PersonasConfig{
			Definitions: map[string]platform.PersonaDef{
				"admin": {
					DisplayName: "E2E Admin",
					Roles:       []string{"admin"},
					Tools:       platform.ToolRulesDef{Allow: []string{"*"}},
					Connections: platform.ConnectionRulesDef{Allow: []string{"*"}},
				},
			},
			DefaultPersona: "admin",
		},
	}
}

// orDefault returns v when non-empty, otherwise def.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// Close closes the test platform.
func (tp *TestPlatform) Close() error {
	if tp.Platform != nil {
		return tp.Platform.Close()
	}
	return nil
}

// SemanticProvider returns the semantic provider for direct testing.
func (tp *TestPlatform) SemanticProvider() semantic.Provider {
	return tp.Platform.SemanticProvider()
}

// QueryProvider returns the query provider for direct testing.
func (tp *TestPlatform) QueryProvider() query.Provider {
	return tp.Platform.QueryProvider()
}

// StorageProvider returns the storage provider for direct testing.
func (tp *TestPlatform) StorageProvider() storage.Provider {
	return tp.Platform.StorageProvider()
}

// MCPServer returns the MCP server for protocol-level testing.
func (tp *TestPlatform) MCPServer() *mcp.Server {
	if tp.Platform == nil {
		return nil
	}
	return tp.Platform.MCPServer()
}

// TestContext creates a test context with timeout.
func TestContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeout)
}

// SkipIfDataHubUnavailable reports whether DataHub is not actually reachable, so
// enrichment tests skip cleanly instead of failing when it is down. It probes
// the real endpoint rather than only checking that a URL is configured
// (IsDataHubAvailable is always true because the URL has a default).
func SkipIfDataHubUnavailable(cfg *E2EConfig) bool {
	return !DataHubReachable(cfg)
}

// MockMCPRequest implements mcp.Request for E2E testing.
type MockMCPRequest struct {
	Params *mcp.CallToolParamsRaw
}

// GetSession returns nil session (not used in tests).
func (m *MockMCPRequest) GetSession() mcp.Session {
	return nil
}

// GetParams returns the request parameters.
func (m *MockMCPRequest) GetParams() mcp.Params {
	if m == nil || m.Params == nil {
		return nil
	}
	return m.Params
}

// GetExtra returns nil (no extra in tests).
func (m *MockMCPRequest) GetExtra() *mcp.RequestExtra {
	return nil
}

// CreateEnrichmentMiddleware creates the MCP semantic enrichment middleware for testing.
func CreateEnrichmentMiddleware(
	semanticProvider semantic.Provider,
	queryProvider query.Provider,
	storageProvider storage.Provider,
) mcp.Middleware {
	return middleware.MCPSemanticEnrichmentMiddleware(
		semanticProvider,
		queryProvider,
		storageProvider,
		middleware.EnrichmentConfig{
			EnrichTrinoResults:          true,
			EnrichDataHubResults:        true,
			EnrichS3Results:             true,
			EnrichDataHubStorageResults: true,
		},
		nil, // no memory provider in test helpers
	)
}
