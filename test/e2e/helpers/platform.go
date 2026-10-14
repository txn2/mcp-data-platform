//go:build integration

package helpers

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// TestPlatform wraps a platform instance for E2E testing.
type TestPlatform struct {
	Platform *platform.Platform
	Config   *E2EConfig
}

// NewTestPlatform creates a new test platform with E2E configuration.
func NewTestPlatform(ctx context.Context, e2eCfg *E2EConfig) (*TestPlatform, error) {
	cfg := buildPlatformConfig(e2eCfg)

	// Run the config this helper builds through the same check the server
	// applies at startup. The helper assembles the struct directly rather than
	// loading YAML, so without this a key a release retires stays in the helper
	// and surfaces as an unexplained tool-authorization failure in the nightly
	// run instead of a named config error here (#1380).
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("e2e platform config: %w", err)
	}

	p, err := platform.New(
		platform.WithConfig(cfg),
	)
	if err != nil {
		return nil, fmt.Errorf("creating platform: %w", err)
	}

	// platform.New migrated the database, so the catalog the semantic
	// provider reads is there to seed.
	if err := SeedCatalog(ctx, e2eCfg.PostgresDSN); err != nil {
		_ = p.Close()
		return nil, fmt.Errorf("seeding the e2e catalog: %w", err)
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
		// The platform catalog is the semantic provider wherever there is a
		// database; SeedCatalog writes the fixture it is read from.
		Semantic: platform.SemanticConfig{
			Cache: platform.CacheConfig{
				Enabled: false,
			},
		},
		Query: platform.QueryConfig{
			Provider: "trino",
			Instance: "e2e",
		},
		Enrichment: platform.EnrichmentConfig{
			TrinoSemanticEnrichment: new(true),
		},
		Toolkits: map[string]any{
			// enabled:true is required per kind: the registry loader skips any
			// toolkit kind that does not set it (loader.LoadFromMap). Without
			// this the trino and s3 toolkits load nothing and their tools
			// never register on the assembled server.
			"trino": map[string]any{
				"enabled": true,
				"instances": map[string]any{
					E2ETrinoConnection: map[string]any{
						"host":    e2eCfg.TrinoHost,
						"port":    e2eCfg.TrinoPort,
						"user":    "e2e-test",
						"catalog": "memory",
						"schema":  "e2e_test",
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
		// authorized. The allowed-anonymous identity carries exactly one role,
		// auth.RoleAnonymous, and a caller whose roles match no persona
		// resolves to the deny-all persona (#1109), so the admin persona has to
		// list that role itself: there is no fallback that would hand it a
		// persona it was not granted. Without it every tool call through the
		// real middleware chain is refused before reaching the handler. Tests
		// that bypass the authorizer (calling the enrichment middleware
		// directly) are unaffected.
		Auth: platform.AuthConfig{
			AllowAnonymous: true,
		},
		Personas: platform.PersonasConfig{
			Definitions: map[string]platform.PersonaDef{
				"admin": {
					DisplayName: "E2E Admin",
					Roles:       []string{"admin", auth.RoleAnonymous},
					Tools:       platform.ToolRulesDef{Allow: []string{"*"}},
					Connections: platform.ConnectionRulesDef{Allow: []string{"*"}},
				},
			},
		},
	}
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
) mcp.Middleware {
	return middleware.MCPSemanticEnrichmentMiddleware(
		semanticProvider,
		queryProvider,
		middleware.EnrichmentConfig{
			EnrichTrinoResults: true,
		},
		nil, // no memory provider in test helpers
	)
}
