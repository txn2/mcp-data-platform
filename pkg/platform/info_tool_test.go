package platform

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/storage"
)

const (
	testInfoVersion      = "1.0.0"
	testInfoToolkitCount = 4 // 3 configured + 1 prepended "platform"
	testInfoVersionV1    = "v1"
)

// requireInfoFromResult extracts an Info struct from a tool call result.
func requireInfoFromResult(t *testing.T, result *mcp.CallToolResult) Info {
	t.Helper()
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
	textContent, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected TextContent")
	var info Info
	err := json.Unmarshal([]byte(textContent.Text), &info)
	require.NoError(t, err)
	return info
}

func TestHandleInfo(t *testing.T) {
	tests := []struct {
		name                  string
		config                Config
		wantName              string
		wantVer               string
		wantDesc              string
		wantTags              []string
		wantAgentInstructions string
	}{
		{
			name: "returns configured values",
			config: Config{
				Server: ServerConfig{
					Name:        "test-platform",
					Version:     "2.0.0",
					Description: "Test platform description",
				},
				Toolkits: map[string]any{
					"trino":   map[string]any{},
					"datahub": map[string]any{},
				},
				Enrichment: EnrichmentConfig{
					TrinoSemanticEnrichment: new(true),
					DataHubQueryEnrichment:  new(true),
				},
				Audit: AuditConfig{
					Enabled: new(true),
				},
			},
			wantName: "test-platform",
			wantVer:  "2.0.0",
			wantDesc: "Test platform description",
		},
		{
			name: "handles empty description",
			config: Config{
				Server: ServerConfig{
					Name:    "minimal-platform",
					Version: testInfoVersion,
				},
			},
			wantName: "minimal-platform",
			wantVer:  testInfoVersion,
			wantDesc: "",
		},
		{
			name: "returns tags and agent instructions",
			config: Config{
				Server: ServerConfig{
					Name:              "tagged-platform",
					Version:           testInfoVersion,
					Tags:              []string{"ACME Corp", "XWidget", "analytics"},
					AgentInstructions: "Prices are in cents - divide by 100.",
				},
			},
			wantName:              "tagged-platform",
			wantVer:               testInfoVersion,
			wantTags:              []string{"ACME Corp", "XWidget", "analytics"},
			wantAgentInstructions: "Prices are in cents - divide by 100.",
		},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			p := &Platform{
				config:          &tt.config,
				personaRegistry: persona.NewRegistry(),
				toolkitRegistry: registry.NewRegistry(),
			}

			result, extra, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

			require.NoError(t, err)
			assert.Nil(t, extra)
			require.NotNil(t, result)
			require.Len(t, result.Content, 1)

			textContent, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok, "expected TextContent")

			var info Info
			err = json.Unmarshal([]byte(textContent.Text), &info)
			require.NoError(t, err)

			assert.Equal(t, tt.wantName, info.Name)
			assert.Equal(t, tt.wantVer, info.Version)
			assert.Equal(t, tt.wantDesc, info.Description)
			assert.Equal(t, tt.wantTags, info.Tags)
			assert.Equal(t, tt.wantAgentInstructions, info.AgentInstructions)
		})
	}
}

// regWithTools returns a toolkit registry exposing the given tool names.
func regWithTools(t *testing.T, tools ...string) *registry.Registry {
	t.Helper()
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(&mockToolkit{kind: "search", name: "default", tools: tools}))
	return reg
}

// TestHandleInfo_ComposesBaselineBeneathAdmin proves the platform baseline (#646)
// is composed above the admin's agent_instructions and surfaced separately.
func TestHandleInfo_ComposesBaselineBeneathAdmin(t *testing.T) {
	config := Config{Server: ServerConfig{
		Name:              "baseline-test",
		Version:           testInfoVersion,
		AgentInstructions: "ACME stores transactions in Cassandra.",
	}}
	p := &Platform{
		config:          &config,
		personaRegistry: persona.NewRegistry(),
		toolkitRegistry: regWithTools(t, "search", "memory_capture"),
	}
	result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})
	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	// The composed instructions lead with the baseline (naming both tools) and
	// carry the admin text below it.
	assert.True(t, strings.HasPrefix(info.AgentInstructions, "How to operate this platform:"),
		"composed instructions should lead with the baseline, got: %q", info.AgentInstructions)
	assert.Contains(t, info.AgentInstructions, "`search`")
	assert.Contains(t, info.AgentInstructions, "`memory_capture`")
	assert.Contains(t, info.AgentInstructions, "ACME stores transactions in Cassandra.")
}

// TestHandleInfo_BaselineGatedByPersona proves the baseline names only tools the
// caller's persona can reach: a persona allowed search but not memory_capture
// gets a baseline that mentions search and never memory_capture.
func TestHandleInfo_BaselineGatedByPersona(t *testing.T) {
	pr := persona.NewRegistry()
	require.NoError(t, pr.Register(&persona.Persona{
		Name:  "reader",
		Tools: persona.ToolRules{Allow: []string{"search"}},
	}))
	pr.SetDefault("reader")

	p := &Platform{
		config:          &Config{Server: ServerConfig{Name: "g", Version: testInfoVersion}},
		personaRegistry: pr,
		toolkitRegistry: regWithTools(t, "search", "memory_capture"),
	}
	result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})
	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	assert.Contains(t, info.AgentInstructions, "`search`")
	assert.NotContains(t, info.AgentInstructions, "`memory_capture`",
		"baseline must not name memory_capture for a persona that cannot call it")
}

// TestHandleInfo_NoBaselineWithoutBaselineTools proves a deployment exposing
// none of the baseline's tools gets no baseline (nothing to say without a tool).
func TestHandleInfo_NoBaselineWithoutBaselineTools(t *testing.T) {
	p := &Platform{
		config:          &Config{Server: ServerConfig{Name: "g", Version: testInfoVersion}},
		personaRegistry: persona.NewRegistry(),
		toolkitRegistry: regWithTools(t, "trino_query"),
	}
	result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})
	require.NoError(t, err)
	info := requireInfoFromResult(t, result)
	assert.NotContains(t, info.AgentInstructions, "How to operate this platform:",
		"no baseline tools registered should yield no baseline")
}

func TestInfoFeatures(t *testing.T) {
	config := Config{
		Server: ServerConfig{
			Name:    "feature-test",
			Version: testInfoVersion,
		},
		Enrichment: EnrichmentConfig{
			TrinoSemanticEnrichment:  new(true),
			DataHubQueryEnrichment:   new(true),
			S3SemanticEnrichment:     new(false),
			DataHubStorageEnrichment: new(true),
		},
		Audit: AuditConfig{
			Enabled: new(true),
		},
	}

	p := &Platform{
		config:           &config,
		personaRegistry:  persona.NewRegistry(),
		toolkitRegistry:  registry.NewRegistry(),
		semanticProvider: semantic.NewNoopProvider(),
		queryProvider:    query.NewNoopProvider(),
		storageProvider:  storage.NewNoopProvider(),
	}
	result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	assert.True(t, info.Features.SemanticEnrichment, "semantic enrichment should be enabled")
	assert.True(t, info.Features.QueryEnrichment, "query enrichment should be enabled")
	assert.True(t, info.Features.StorageEnrichment, "storage enrichment should be enabled")
	assert.True(t, info.Features.AuditLogging, "audit logging should be enabled")
}

// TestInfoFeatures_EnrichmentReportedOffWithoutProvider verifies the honesty
// gate: with enrichment flags default-on (nil) but no providers configured,
// platform_info must NOT report enrichment as enabled, since nothing would be
// produced. The agent should not be told it has context it cannot receive.
func TestInfoFeatures_EnrichmentReportedOffWithoutProvider(t *testing.T) {
	config := Config{
		Server: ServerConfig{Name: "feature-test", Version: testInfoVersion},
		// Injection left zero-value: every enrichment flag is nil => default-on.
	}

	p := &Platform{
		config:          &config,
		personaRegistry: persona.NewRegistry(),
		toolkitRegistry: registry.NewRegistry(),
		// No providers configured.
	}
	result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	assert.False(t, info.Features.SemanticEnrichment, "no semantic provider => not reported")
	assert.False(t, info.Features.QueryEnrichment, "no query provider => not reported")
	assert.False(t, info.Features.StorageEnrichment, "no storage provider => not reported")
}

func TestInfoConfigVersion(t *testing.T) {
	config := Config{
		APIVersion: testInfoVersionV1,
		Server: ServerConfig{
			Name:    "version-test",
			Version: testInfoVersion,
		},
	}

	p := &Platform{
		config:          &config,
		personaRegistry: persona.NewRegistry(),
		toolkitRegistry: registry.NewRegistry(),
	}
	result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	assert.Equal(t, testInfoVersionV1, info.ConfigVersion.APIVersion)
	assert.Equal(t, testInfoVersionV1, info.ConfigVersion.LatestVersion)
	assert.Contains(t, info.ConfigVersion.SupportedVersions, testInfoVersionV1)
}

func TestInfoToolkitDescriptions(t *testing.T) {
	config := Config{
		Server: ServerConfig{Name: "desc-test", Version: testInfoVersion},
		Toolkits: map[string]any{
			"trino":   map[string]any{"description": "Run SQL queries against rdbms and opensearch catalogs"},
			"datahub": map[string]any{"description": "Browse the ACME data catalog"},
			"s3":      map[string]any{}, // no description — should be omitted
		},
	}

	p := &Platform{config: &config, personaRegistry: persona.NewRegistry(), toolkitRegistry: registry.NewRegistry()}
	result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	require.NotNil(t, info.ToolkitDescriptions)
	assert.Equal(t, "Run SQL queries against rdbms and opensearch catalogs", info.ToolkitDescriptions["trino"])
	assert.Equal(t, "Browse the ACME data catalog", info.ToolkitDescriptions["datahub"])
	assert.NotContains(t, info.ToolkitDescriptions, "s3", "empty description should be omitted")
}

func TestInfoToolkitDescriptionsNilWhenNone(t *testing.T) {
	config := Config{
		Server: ServerConfig{Name: "no-desc-test", Version: testInfoVersion},
		Toolkits: map[string]any{
			"trino": map[string]any{},
		},
	}

	p := &Platform{config: &config, personaRegistry: persona.NewRegistry(), toolkitRegistry: registry.NewRegistry()}
	result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	// Only the auto-injected platform description should be present
	require.NotNil(t, info.ToolkitDescriptions)
	assert.Len(t, info.ToolkitDescriptions, 1, "only platform description should be present")
	assert.NotEmpty(t, info.ToolkitDescriptions["platform"])
}

func TestInfoToolkits(t *testing.T) {
	config := Config{
		Server: ServerConfig{
			Name:    "toolkit-test",
			Version: testInfoVersion,
		},
		Toolkits: map[string]any{
			"trino":   map[string]any{"host": "localhost"},
			"datahub": map[string]any{"url": "http://localhost"},
			"s3":      map[string]any{"region": "us-east-1"},
		},
	}

	p := &Platform{
		config:          &config,
		personaRegistry: persona.NewRegistry(),
		toolkitRegistry: registry.NewRegistry(),
	}
	result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	assert.Len(t, info.Toolkits, testInfoToolkitCount)
	assert.Equal(t, "platform", info.Toolkits[0], "platform should be prepended first")
	assert.Contains(t, info.Toolkits, "trino")
	assert.Contains(t, info.Toolkits, "datahub")
	assert.Contains(t, info.Toolkits, "s3")
	assert.NotEmpty(t, info.ToolkitDescriptions["platform"], "platform toolkit should have a description")
}

func newPersonaRegistry(t *testing.T) *persona.Registry {
	t.Helper()
	reg := persona.NewRegistry()
	_ = reg.Register(&persona.Persona{
		Name:        "analyst",
		DisplayName: "Data Analyst",
		Description: "Analyze data and run queries",
	})
	_ = reg.Register(&persona.Persona{
		Name:        "admin",
		DisplayName: "Administrator",
		Description: "Full access to all features",
	})
	return reg
}

func TestInfoPersona(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{Name: "persona-test", Version: testInfoVersion},
	}

	t.Run("shows caller's persona from context", func(t *testing.T) {
		reg := newPersonaRegistry(t)
		p := &Platform{config: &cfg, personaRegistry: reg, toolkitRegistry: registry.NewRegistry()}

		ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
			PersonaName: "analyst",
		})
		result, _, err := p.handleInfo(ctx, &mcp.CallToolRequest{})

		require.NoError(t, err)
		info := requireInfoFromResult(t, result)

		require.NotNil(t, info.Persona)
		assert.Equal(t, "analyst", info.Persona.Name)
		assert.Equal(t, "Data Analyst", info.Persona.DisplayName)
		assert.Equal(t, "Analyze data and run queries", info.Persona.Description)
	})

	t.Run("falls back to default persona when no context", func(t *testing.T) {
		reg := newPersonaRegistry(t)
		reg.SetDefault("admin")
		p := &Platform{config: &cfg, personaRegistry: reg, toolkitRegistry: registry.NewRegistry()}

		result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

		require.NoError(t, err)
		info := requireInfoFromResult(t, result)

		require.NotNil(t, info.Persona)
		assert.Equal(t, "admin", info.Persona.Name)
	})

	t.Run("no persona when no context and no default", func(t *testing.T) {
		reg := newPersonaRegistry(t)
		p := &Platform{config: &cfg, personaRegistry: reg, toolkitRegistry: registry.NewRegistry()}

		result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

		require.NoError(t, err)
		info := requireInfoFromResult(t, result)

		assert.Nil(t, info.Persona)
	})
}

func TestInfoPortalURL(t *testing.T) {
	t.Run("includes portal_url when configured", func(t *testing.T) {
		cfg := Config{
			Server: ServerConfig{Name: "portal-test", Version: testInfoVersion},
			Portal: PortalConfig{PublicBaseURL: "https://portal.example.com"},
		}
		p := &Platform{config: &cfg, personaRegistry: persona.NewRegistry(), toolkitRegistry: registry.NewRegistry()}
		result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

		require.NoError(t, err)
		info := requireInfoFromResult(t, result)
		assert.Equal(t, "https://portal.example.com", info.PortalURL)
	})

	t.Run("omits portal_url when not configured", func(t *testing.T) {
		cfg := Config{
			Server: ServerConfig{Name: "no-portal-test", Version: testInfoVersion},
		}
		p := &Platform{config: &cfg, personaRegistry: persona.NewRegistry(), toolkitRegistry: registry.NewRegistry()}
		result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

		require.NoError(t, err)
		info := requireInfoFromResult(t, result)
		assert.Empty(t, info.PortalURL)
	})
}

func TestInfoPlatformToolkitPrepended(t *testing.T) {
	t.Run("platform is always first even with no configured toolkits", func(t *testing.T) {
		cfg := Config{
			Server: ServerConfig{Name: "empty-tk-test", Version: testInfoVersion},
		}
		p := &Platform{config: &cfg, personaRegistry: persona.NewRegistry(), toolkitRegistry: registry.NewRegistry()}
		result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

		require.NoError(t, err)
		info := requireInfoFromResult(t, result)
		require.NotEmpty(t, info.Toolkits)
		assert.Equal(t, "platform", info.Toolkits[0])
		assert.NotEmpty(t, info.ToolkitDescriptions["platform"])
	})

	t.Run("does not override operator-provided platform description", func(t *testing.T) {
		cfg := Config{
			Server: ServerConfig{Name: "custom-desc-test", Version: testInfoVersion},
			Toolkits: map[string]any{
				"platform": map[string]any{
					"description": "Our custom platform description",
				},
				"trino": map[string]any{},
			},
		}
		p := &Platform{config: &cfg, personaRegistry: persona.NewRegistry(), toolkitRegistry: registry.NewRegistry()}
		result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})

		require.NoError(t, err)
		info := requireInfoFromResult(t, result)
		assert.Equal(t, "platform", info.Toolkits[0])
		assert.Equal(t, "Our custom platform description", info.ToolkitDescriptions["platform"])
		assert.Contains(t, info.Toolkits, "trino")
	})
}

func TestResolveCallerPersona(t *testing.T) {
	cfg := Config{Server: ServerConfig{Name: "test"}}

	t.Run("returns nil when registry is empty and no context", func(t *testing.T) {
		p := &Platform{config: &cfg, personaRegistry: persona.NewRegistry(), toolkitRegistry: registry.NewRegistry()}
		result := p.resolveCallerPersona(context.Background())
		assert.Nil(t, result)
	})

	t.Run("returns nil when persona name not found in registry", func(t *testing.T) {
		p := &Platform{config: &cfg, personaRegistry: persona.NewRegistry(), toolkitRegistry: registry.NewRegistry()}
		ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
			PersonaName: "nonexistent",
		})
		result := p.resolveCallerPersona(ctx)
		assert.Nil(t, result)
	})
}
