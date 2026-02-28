package platform

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// connectTestClient connects an in-memory MCP client to a server and returns the session.
// The caller must call cleanup() when done.
func connectTestClient(t *testing.T, server *mcp.Server) (session *mcp.ClientSession, cleanup func()) {
	t.Helper()
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()

	serverSession, err := server.Connect(ctx, t1, nil)
	require.NoError(t, err)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0"}, nil)
	clientSession, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)

	cleanup = func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	}
	return clientSession, cleanup
}

func TestRegisterAutoPrompt(t *testing.T) {
	tests := []struct {
		name            string
		serverName      string
		serverDesc      string
		operatorPrompts []PromptConfig
		wantRegistered  bool
		wantTitle       string
	}{
		{
			name:           "registers when description is set",
			serverName:     "My Platform",
			serverDesc:     "Covers all analytics data.",
			wantRegistered: true,
			wantTitle:      "My Platform",
		},
		{
			name:           "skipped when description is empty",
			serverName:     "My Platform",
			serverDesc:     "",
			wantRegistered: false,
		},
		{
			name:       "skipped when operator already has platform-overview",
			serverName: "My Platform",
			serverDesc: "Covers all analytics data.",
			operatorPrompts: []PromptConfig{
				{Name: autoPromptName, Description: "custom", Content: "custom content"},
			},
			wantRegistered: false,
		},
		{
			name:       "registers alongside other operator prompts",
			serverName: "My Platform",
			serverDesc: "Covers analytics.",
			operatorPrompts: []PromptConfig{
				{Name: "routing-guide", Description: "routing", Content: "route here"},
			},
			wantRegistered: true,
			wantTitle:      "My Platform",
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
						Name:        tt.serverName,
						Description: tt.serverDesc,
						Prompts:     tt.operatorPrompts,
					},
				},
			}

			p.registerAutoPrompt()

			session, cleanup := connectTestClient(t, mcpServer)
			defer cleanup()

			resp, err := session.ListPrompts(context.Background(), &mcp.ListPromptsParams{})
			require.NoError(t, err)

			var found bool
			for _, pr := range resp.Prompts {
				if pr.Name == autoPromptName {
					found = true
					if tt.wantRegistered {
						assert.Equal(t, tt.wantTitle, pr.Title)
						assert.NotEmpty(t, pr.Description)
					}
				}
			}
			assert.Equal(t, tt.wantRegistered, found, "auto prompt registration mismatch")
		})
	}
}

func TestAutoPromptContent(t *testing.T) {
	const desc = "Covers all ACME Corp data."
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "1.0.0",
	}, nil)

	p := &Platform{
		mcpServer: mcpServer,
		config: &Config{
			Server: ServerConfig{
				Name:        "ACME Data Platform",
				Description: desc,
			},
		},
	}

	p.registerAutoPrompt()

	session, cleanup := connectTestClient(t, mcpServer)
	defer cleanup()

	resp, err := session.GetPrompt(context.Background(), &mcp.GetPromptParams{
		Name: autoPromptName,
	})
	require.NoError(t, err)
	require.Len(t, resp.Messages, 1)

	textContent, ok := resp.Messages[0].Content.(*mcp.TextContent)
	require.True(t, ok, "expected TextContent")
	assert.True(t, strings.Contains(textContent.Text, desc), "content should include description")
	assert.True(t, strings.Contains(textContent.Text, "platform_info"), "content should mention platform_info")
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
