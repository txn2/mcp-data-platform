package platform

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

func TestHandlePlatformInfo(t *testing.T) {
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
				Injection: InjectionConfig{
					TrinoSemanticEnrichment: true,
					DataHubQueryEnrichment:  true,
				},
				Audit: AuditConfig{
					Enabled: true,
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
					Version: "1.0.0",
				},
			},
			wantName: "minimal-platform",
			wantVer:  "1.0.0",
			wantDesc: "",
		},
		{
			name: "returns tags and agent instructions",
			config: Config{
				Server: ServerConfig{
					Name:              "tagged-platform",
					Version:           "1.0.0",
					Tags:              []string{"ACME Corp", "XWidget", "analytics"},
					AgentInstructions: "Prices are in cents - divide by 100.",
				},
			},
			wantName:              "tagged-platform",
			wantVer:               "1.0.0",
			wantTags:              []string{"ACME Corp", "XWidget", "analytics"},
			wantAgentInstructions: "Prices are in cents - divide by 100.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Platform{
				config:          &tt.config,
				personaRegistry: persona.NewRegistry(),
			}

			result, extra, err := p.handlePlatformInfo(context.Background(), &mcp.CallToolRequest{})

			require.NoError(t, err)
			assert.Nil(t, extra)
			require.NotNil(t, result)
			require.Len(t, result.Content, 1)

			textContent, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok, "expected TextContent")

			var info PlatformInfo
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

func TestPlatformInfoFeatures(t *testing.T) {
	config := Config{
		Server: ServerConfig{
			Name:    "feature-test",
			Version: "1.0.0",
		},
		Injection: InjectionConfig{
			TrinoSemanticEnrichment:  true,
			DataHubQueryEnrichment:   true,
			S3SemanticEnrichment:     false,
			DataHubStorageEnrichment: true,
		},
		Audit: AuditConfig{
			Enabled: true,
		},
	}

	p := &Platform{
		config:          &config,
		personaRegistry: persona.NewRegistry(),
	}
	result, _, err := p.handlePlatformInfo(context.Background(), &mcp.CallToolRequest{})

	require.NoError(t, err)
	textContent := result.Content[0].(*mcp.TextContent)

	var info PlatformInfo
	err = json.Unmarshal([]byte(textContent.Text), &info)
	require.NoError(t, err)

	assert.True(t, info.Features.SemanticEnrichment, "semantic enrichment should be enabled")
	assert.True(t, info.Features.QueryEnrichment, "query enrichment should be enabled")
	assert.True(t, info.Features.StorageEnrichment, "storage enrichment should be enabled")
	assert.True(t, info.Features.AuditLogging, "audit logging should be enabled")
}

func TestBuildInfoToolDescription(t *testing.T) {
	tests := []struct {
		name         string
		serverConfig ServerConfig
		wantContains []string
	}{
		{
			name: "default name uses generic description",
			serverConfig: ServerConfig{
				Name: "mcp-data-platform",
			},
			wantContains: []string{
				"Get information about this MCP data platform",
				"including its purpose",
			},
		},
		{
			name: "custom name appears in description",
			serverConfig: ServerConfig{
				Name: "ACME Data Platform",
			},
			wantContains: []string{
				"Get information about ACME Data Platform",
			},
		},
		{
			name: "tags appear in parentheses",
			serverConfig: ServerConfig{
				Name: "ACME Data Platform",
				Tags: []string{"analytics", "sales"},
			},
			wantContains: []string{
				"Get information about ACME Data Platform",
				"(analytics, sales)",
			},
		},
		{
			name: "empty tags omits parentheses",
			serverConfig: ServerConfig{
				Name: "ACME Data Platform",
				Tags: []string{},
			},
			wantContains: []string{
				"Get information about ACME Data Platform",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Platform{
				config: &Config{
					Server: tt.serverConfig,
				},
			}

			desc := p.buildInfoToolDescription()

			for _, want := range tt.wantContains {
				assert.Contains(t, desc, want)
			}
		})
	}
}

func TestPlatformInfoToolkits(t *testing.T) {
	config := Config{
		Server: ServerConfig{
			Name:    "toolkit-test",
			Version: "1.0.0",
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
	}
	result, _, err := p.handlePlatformInfo(context.Background(), &mcp.CallToolRequest{})

	require.NoError(t, err)
	textContent := result.Content[0].(*mcp.TextContent)

	var info PlatformInfo
	err = json.Unmarshal([]byte(textContent.Text), &info)
	require.NoError(t, err)

	assert.Len(t, info.Toolkits, 3)
	assert.Contains(t, info.Toolkits, "trino")
	assert.Contains(t, info.Toolkits, "datahub")
	assert.Contains(t, info.Toolkits, "s3")
}

func TestPlatformInfoPersonas(t *testing.T) {
	config := Config{
		Server: ServerConfig{
			Name:    "persona-test",
			Version: "1.0.0",
		},
	}

	registry := persona.NewRegistry()
	_ = registry.Register(&persona.Persona{
		Name:        "analyst",
		DisplayName: "Data Analyst",
		Description: "Analyze data and run queries",
	})
	_ = registry.Register(&persona.Persona{
		Name:        "admin",
		DisplayName: "Administrator",
		Description: "Full access to all features",
	})

	p := &Platform{
		config:          &config,
		personaRegistry: registry,
	}
	result, _, err := p.handlePlatformInfo(context.Background(), &mcp.CallToolRequest{})

	require.NoError(t, err)
	textContent := result.Content[0].(*mcp.TextContent)

	var info PlatformInfo
	err = json.Unmarshal([]byte(textContent.Text), &info)
	require.NoError(t, err)

	assert.Len(t, info.Personas, 2)

	// Find analyst persona
	var foundAnalyst bool
	for _, p := range info.Personas {
		if p.Name == "analyst" {
			foundAnalyst = true
			assert.Equal(t, "Data Analyst", p.DisplayName)
			assert.Equal(t, "Analyze data and run queries", p.Description)
		}
	}
	assert.True(t, foundAnalyst, "expected analyst persona in output")
}
