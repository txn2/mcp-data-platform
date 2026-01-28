package platform

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandlePlatformInfo(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		wantName string
		wantVer  string
		wantDesc string
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Platform{config: &tt.config}

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

	p := &Platform{config: &config}
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

	p := &Platform{config: &config}
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
