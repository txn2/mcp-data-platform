//go:build integration

package helpers

import (
	"context"
	"fmt"
	"time"

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
			TrinoSemanticEnrichment:  true,
			DataHubQueryEnrichment:   true,
			S3SemanticEnrichment:     true,
			DataHubStorageEnrichment: true,
		},
		Toolkits: map[string]any{
			"trino": map[string]any{
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
				"instances": map[string]any{
					"e2e": map[string]any{
						"url":   e2eCfg.DataHubURL,
						"token": e2eCfg.DataHubToken,
					},
				},
			},
			"s3": map[string]any{
				"instances": map[string]any{
					"e2e": map[string]any{
						"endpoint":          e2eCfg.MinIOEndpoint,
						"access_key_id":     e2eCfg.MinIOAccessKey,
						"secret_access_key": e2eCfg.MinIOSecretKey,
						"region":            e2eCfg.MinIORegion,
						"connection_name":   "e2e-s3",
					},
				},
			},
		},
		Database: platform.DatabaseConfig{
			DSN: e2eCfg.PostgresDSN,
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

// StorageProvider returns the storage provider for direct testing.
func (tp *TestPlatform) StorageProvider() storage.Provider {
	return tp.Platform.StorageProvider()
}

// MiddlewareChain returns the middleware chain for direct testing.
func (tp *TestPlatform) MiddlewareChain() *middleware.Chain {
	if tp.Platform == nil {
		return nil
	}
	return tp.Platform.MiddlewareChain()
}

// TestContext creates a test context with timeout.
func TestContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeout)
}

// SkipIfDataHubUnavailable skips the test if DataHub is not available.
func SkipIfDataHubUnavailable(cfg *E2EConfig) bool {
	return !cfg.IsDataHubAvailable()
}
