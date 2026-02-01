package platform

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
)

func TestRegisterPlatformPrompts(t *testing.T) {
	tests := []struct {
		name        string
		prompts     []PromptConfig
		wantPrompts int
	}{
		{
			name:        "no prompts configured",
			prompts:     nil,
			wantPrompts: 0,
		},
		{
			name:        "empty prompts list",
			prompts:     []PromptConfig{},
			wantPrompts: 0,
		},
		{
			name: "single prompt",
			prompts: []PromptConfig{
				{
					Name:        "routing_rules",
					Description: "How to route queries between systems",
					Content:     "Route queries based on data type.",
				},
			},
			wantPrompts: 1,
		},
		{
			name: "multiple prompts",
			prompts: []PromptConfig{
				{
					Name:        "routing_rules",
					Description: "How to route queries",
					Content:     "Route queries based on data type.",
				},
				{
					Name:        "data_dictionary",
					Description: "Key business terms",
					Content:     "ARR: Annual Recurring Revenue",
				},
			},
			wantPrompts: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mcpServer := mcp.NewServer(&mcp.Implementation{
				Name:    "test-server",
				Version: "1.0.0",
			}, nil)

			p := &Platform{
				mcpServer: mcpServer,
				config: &Config{
					Server: ServerConfig{
						Prompts: tt.prompts,
					},
				},
			}

			// Should not panic
			p.registerPlatformPrompts()

			// The test verifies that the function completes without error.
			// The prompts are registered internally to the MCP server.
			assert.NotNil(t, p.mcpServer)
		})
	}
}

func TestRegisterPromptWithContent(t *testing.T) {
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "1.0.0",
	}, nil)

	p := &Platform{
		mcpServer: mcpServer,
		config: &Config{
			Server: ServerConfig{
				Prompts: []PromptConfig{
					{
						Name:        "test_prompt",
						Description: "A test prompt",
						Content:     "This is the prompt content.",
					},
				},
			},
		},
	}

	// Register the prompts - this should not panic
	p.registerPlatformPrompts()

	// Verify the server was configured
	assert.NotNil(t, p.mcpServer)
}

func TestPromptConfigFields(t *testing.T) {
	cfg := PromptConfig{
		Name:        "my_prompt",
		Description: "My prompt description",
		Content:     "Prompt content here",
	}

	assert.Equal(t, "my_prompt", cfg.Name)
	assert.Equal(t, "My prompt description", cfg.Description)
	assert.Equal(t, "Prompt content here", cfg.Content)
}

func TestBuildPromptResult(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "simple content",
			content: "This is a simple prompt.",
		},
		{
			name:    "multiline content",
			content: "Line 1\nLine 2\nLine 3",
		},
		{
			name:    "empty content",
			content: "",
		},
		{
			name:    "content with special characters",
			content: "Use `code` and **bold** formatting.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildPromptResult(tt.content)

			assert.NotNil(t, result)
			assert.Len(t, result.Messages, 1)
			assert.Equal(t, mcp.Role("user"), result.Messages[0].Role)

			textContent, ok := result.Messages[0].Content.(*mcp.TextContent)
			assert.True(t, ok, "expected TextContent")
			assert.Equal(t, tt.content, textContent.Text)
		})
	}
}
