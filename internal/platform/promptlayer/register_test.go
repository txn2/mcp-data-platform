package promptlayer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// newRegisterHandle builds a Handle for the static/workflow registration path:
// no store, the given toolkit registry, server name/description, and operator
// prompt specs.
func newRegisterHandle(serverName, serverDesc string, operator []PromptSpec, reg ToolkitRegistry) *Handle {
	if reg == nil {
		reg = registry.NewRegistry()
	}
	return &Handle{
		registry:          reg,
		serverName:        serverName,
		serverDescription: serverDesc,
		operatorPrompts:   operator,
	}
}

// connectTestClient connects an in-memory MCP client to a server and returns the
// session. The caller must call cleanup() when done.
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

func TestSubstituteArgs(t *testing.T) {
	tests := []struct {
		name    string
		content string
		args    map[string]string
		want    string
	}{
		{
			name:    "no args",
			content: "Hello world",
			args:    nil,
			want:    "Hello world",
		},
		{
			name:    "single substitution",
			content: "Explore {topic}.",
			args:    map[string]string{"topic": "sales data"},
			want:    "Explore sales data.",
		},
		{
			name:    "multiple substitutions",
			content: "Report about {topic} for {dataset}.",
			args:    map[string]string{"topic": "revenue", "dataset": "orders"},
			want:    "Report about revenue for orders.",
		},
		{
			name:    "repeated placeholder",
			content: "{name} and {name}",
			args:    map[string]string{"name": "foo"},
			want:    "foo and foo",
		},
		{
			name:    "unresolved placeholder left as-is",
			content: "Hello {name}, welcome to {place}.",
			args:    map[string]string{"name": "Alice"},
			want:    "Hello Alice, welcome to {place}.",
		},
		{
			name:    "empty args map",
			content: "{topic}",
			args:    map[string]string{},
			want:    "{topic}",
		},
		{
			name:    "deterministic when value contains other placeholder",
			content: "{a} and {b}",
			args:    map[string]string{"a": "{b}", "b": "hello"},
			want:    "hello and hello",
		},
		{
			name:    "double-brace placeholder",
			content: "Hello {{name}}.",
			args:    map[string]string{"name": "Alice"},
			want:    "Hello Alice.",
		},
		{
			name:    "mixed single and double brace",
			content: "{{name}} ({role}) — {{name}} again",
			args:    map[string]string{"name": "Alice", "role": "admin"},
			want:    "Alice (admin) — Alice again",
		},
		{
			name:    "double-brace processed before single-brace to avoid {name}-eating-{{name}}",
			content: "{{name}}",
			args:    map[string]string{"name": "Alice"},
			want:    "Alice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := substituteArgs(tt.content, tt.args)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRegisterPlatformPrompts(t *testing.T) {
	tests := []struct {
		name         string
		prompts      []PromptSpec
		wantMetadata int
	}{
		{name: "no prompts configured", prompts: nil, wantMetadata: 0},
		{name: "empty prompts list", prompts: []PromptSpec{}, wantMetadata: 0},
		{
			name: "single prompt",
			prompts: []PromptSpec{
				{Name: "routing_rules", Description: "How to route queries between systems", Content: "Route queries based on data type."},
			},
			wantMetadata: 1,
		},
		{
			name: "multiple prompts",
			prompts: []PromptSpec{
				{Name: "routing_rules", Description: "How to route queries", Content: "Route queries based on data type."},
				{Name: "data_dictionary", Description: "Key business terms", Content: "ARR: Annual Recurring Revenue"},
			},
			wantMetadata: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
			h := newRegisterHandle("", "", tt.prompts, nil)

			h.RegisterPlatformPrompts(mcpServer)

			// With no toolkits registered, workflow prompts are gated out and
			// database prompts need a store, so only the operator prompts land in
			// the name-keyed metadata.
			assert.Len(t, h.AllPromptInfos(), tt.wantMetadata)
		})
	}
}

func TestRegisterPromptWithArguments(t *testing.T) {
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)

	h := newRegisterHandle("", "", []PromptSpec{
		{
			Name:        "explore-data",
			Description: "Explore data about a topic",
			Content:     "Explore {topic} and find insights.",
			Arguments: []PromptArgSpec{
				{Name: "topic", Description: "The topic to explore", Required: true},
			},
		},
	}, nil)

	h.RegisterPlatformPrompts(mcpServer)

	session, cleanup := connectTestClient(t, mcpServer)
	defer cleanup()

	// List prompts - should have the argument
	listResp, err := session.ListPrompts(context.Background(), &mcp.ListPromptsParams{})
	require.NoError(t, err)
	require.Len(t, listResp.Prompts, 1)
	assert.Equal(t, "explore-data", listResp.Prompts[0].Name)
	require.Len(t, listResp.Prompts[0].Arguments, 1)
	assert.Equal(t, "topic", listResp.Prompts[0].Arguments[0].Name)
	assert.True(t, listResp.Prompts[0].Arguments[0].Required)

	// Get prompt with arguments - should substitute
	resp, err := session.GetPrompt(context.Background(), &mcp.GetPromptParams{
		Name:      "explore-data",
		Arguments: map[string]string{"topic": "revenue trends"},
	})
	require.NoError(t, err)
	require.Len(t, resp.Messages, 1)
	textContent, ok := resp.Messages[0].Content.(*mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "Explore revenue trends and find insights.", textContent.Text)
}

func TestRegisterAutoPrompt(t *testing.T) {
	tests := []struct {
		name            string
		serverName      string
		serverDesc      string
		operatorPrompts []PromptSpec
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
			operatorPrompts: []PromptSpec{
				{Name: autoPromptName, Description: "custom", Content: "custom content"},
			},
			wantRegistered: false,
		},
		{
			name:       "registers alongside other operator prompts",
			serverName: "My Platform",
			serverDesc: "Covers analytics.",
			operatorPrompts: []PromptSpec{
				{Name: "routing-guide", Description: "routing", Content: "route here"},
			},
			wantRegistered: true,
			wantTitle:      "My Platform",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
			h := newRegisterHandle(tt.serverName, tt.serverDesc, tt.operatorPrompts, nil)

			h.registerAutoPrompt(mcpServer)

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
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)

	h := newRegisterHandle("ACME Data Platform", desc, nil, nil)

	h.registerAutoPrompt(mcpServer)

	session, cleanup := connectTestClient(t, mcpServer)
	defer cleanup()

	resp, err := session.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: autoPromptName})
	require.NoError(t, err)
	require.Len(t, resp.Messages, 1)

	textContent, ok := resp.Messages[0].Content.(*mcp.TextContent)
	require.True(t, ok, "expected TextContent")
	assert.True(t, strings.Contains(textContent.Text, desc), "content should include description")
	assert.True(t, strings.Contains(textContent.Text, "platform_info"), "content should mention platform_info")
}

func TestDynamicOverviewContentWithToolkits(t *testing.T) {
	reg := registry.NewRegistry()
	// Register mock toolkits for datahub and trino
	_ = reg.Register(&mockToolkit{kind: "datahub", name: "primary"})
	_ = reg.Register(&mockToolkit{kind: "trino", name: "default"})

	h := newRegisterHandle("Test Platform", "Test platform for analytics.", nil, reg)

	content := h.buildDynamicOverviewContent()

	assert.Contains(t, content, "Test platform for analytics.")
	assert.Contains(t, content, "Explore available data")
	assert.Contains(t, content, "Query data using SQL")
	assert.Contains(t, content, "Generate reports")
	assert.NotContains(t, content, "Save generated content")   // no portal
	assert.NotContains(t, content, "Capture domain knowledge") // no knowledge
	assert.Contains(t, content, "platform_info")
}

func TestWorkflowPromptsConditionalRegistration(t *testing.T) {
	t.Run("registers when required toolkits are present", func(t *testing.T) {
		reg := registry.NewRegistry()
		_ = reg.Register(&mockToolkit{kind: "datahub", name: "primary"})

		mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
		h := newRegisterHandle("", "Test platform.", nil, reg)

		h.RegisterPlatformPrompts(mcpServer)

		session, cleanup := connectTestClient(t, mcpServer)
		defer cleanup()

		resp, err := session.ListPrompts(context.Background(), &mcp.ListPromptsParams{})
		require.NoError(t, err)

		names := promptNames(resp.Prompts)
		assert.Contains(t, names, "explore-available-data")
		assert.Contains(t, names, "trace-data-lineage")
		// create-interactive-dashboard requires portal + trino + datahub
		assert.NotContains(t, names, "create-interactive-dashboard")
		// create-a-report requires trino + datahub
		assert.NotContains(t, names, "create-a-report")
	})

	t.Run("skips when required toolkits are missing", func(t *testing.T) {
		reg := registry.NewRegistry()
		// Only trino, no datahub

		mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
		h := newRegisterHandle("", "", nil, reg)

		h.registerWorkflowPrompts(mcpServer)

		session, cleanup := connectTestClient(t, mcpServer)
		defer cleanup()

		resp, err := session.ListPrompts(context.Background(), &mcp.ListPromptsParams{})
		require.NoError(t, err)

		assert.Empty(t, resp.Prompts, "no workflow prompts should be registered")
	})

	t.Run("operator override skips auto-registration", func(t *testing.T) {
		reg := registry.NewRegistry()
		_ = reg.Register(&mockToolkit{kind: "datahub", name: "primary"})

		mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
		h := newRegisterHandle("", "", []PromptSpec{
			{Name: "explore-available-data", Description: "Custom", Content: "Custom content."},
		}, reg)

		h.registerWorkflowPrompts(mcpServer)

		session, cleanup := connectTestClient(t, mcpServer)
		defer cleanup()

		resp, err := session.ListPrompts(context.Background(), &mcp.ListPromptsParams{})
		require.NoError(t, err)

		// Should not have explore-available-data from workflow (operator defined it)
		// But trace-data-lineage should still be there
		names := promptNames(resp.Prompts)
		assert.NotContains(t, names, "explore-available-data")
		assert.Contains(t, names, "trace-data-lineage")
	})

	t.Run("all toolkits registers all prompts", func(t *testing.T) {
		reg := registry.NewRegistry()
		_ = reg.Register(&mockToolkit{kind: "datahub", name: "primary"})
		_ = reg.Register(&mockToolkit{kind: "trino", name: "default"})
		_ = reg.Register(&mockToolkit{kind: "portal", name: "default"})

		mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
		h := newRegisterHandle("", "", nil, reg)

		h.registerWorkflowPrompts(mcpServer)

		session, cleanup := connectTestClient(t, mcpServer)
		defer cleanup()

		resp, err := session.ListPrompts(context.Background(), &mcp.ListPromptsParams{})
		require.NoError(t, err)

		names := promptNames(resp.Prompts)
		assert.Contains(t, names, "explore-available-data")
		assert.Contains(t, names, "create-interactive-dashboard")
		assert.Contains(t, names, "create-a-report")
		assert.Contains(t, names, "trace-data-lineage")
	})
}

func TestPromptMetadataCollection(t *testing.T) {
	reg := registry.NewRegistry()
	_ = reg.Register(&mockToolkit{kind: "datahub", name: "primary"})

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	h := newRegisterHandle("", "Test platform.", []PromptSpec{
		{
			Name:        "custom-prompt",
			Description: "A custom prompt",
			Content:     "Do {thing}.",
			Arguments: []PromptArgSpec{
				{Name: "thing", Description: "What to do", Required: true},
			},
		},
	}, reg)

	h.RegisterPlatformPrompts(mcpServer)

	infos := h.AllPromptInfos()
	assert.True(t, len(infos) > 0, "should have collected prompt infos")

	// Find custom-prompt
	var customFound bool
	for _, info := range infos {
		if info.Name != "custom-prompt" {
			continue
		}
		customFound = true
		assert.Equal(t, "A custom prompt", info.Description)
		assert.Equal(t, "Do {thing}.", info.Content)
		require.Len(t, info.Arguments, 1)
		assert.Equal(t, "thing", info.Arguments[0].Name)
		assert.True(t, info.Arguments[0].Required)
	}
	assert.True(t, customFound, "custom-prompt should be in collected infos")

	// platform-overview should NOT be in collected infos (excluded for copy UX)
	for _, info := range infos {
		assert.NotEqual(t, autoPromptName, info.Name, "platform-overview should not be in collected infos")
	}

	// Verify categories and content are set
	for _, info := range infos {
		assert.NotEmpty(t, info.Category, "prompt %q should have a category", info.Name)
		assert.NotEmpty(t, info.Content, "prompt %q should have content", info.Name)
	}
}

func TestPromptContentInJSON(t *testing.T) {
	reg := registry.NewRegistry()
	_ = reg.Register(&mockToolkit{kind: "datahub", name: "primary"})

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	h := newRegisterHandle("", "Test.", []PromptSpec{
		{
			Name:        "my-prompt",
			Description: "My prompt",
			Content:     "Do the thing about {topic}.",
			Arguments: []PromptArgSpec{
				{Name: "topic", Description: "The topic", Required: true},
			},
		},
	}, reg)

	h.RegisterPlatformPrompts(mcpServer)

	infos := h.AllPromptInfos()

	data, err := json.Marshal(infos)
	require.NoError(t, err)

	jsonStr := string(data)
	assert.Contains(t, jsonStr, `"content"`, "JSON output must include content field")
	assert.Contains(t, jsonStr, "Do the thing about {topic}.", "JSON output must include prompt template text")

	// Workflow prompts should also have content in the JSON
	for _, info := range infos {
		if info.Category == "workflow" {
			assert.NotEmpty(t, info.Content, "workflow prompt %q must have content", info.Name)
		}
	}
}

func TestCollectToolkitPromptInfos(t *testing.T) {
	reg := registry.NewRegistry()
	_ = reg.Register(&mockToolkitWithPrompts{
		mockToolkit: mockToolkit{kind: "portal", name: "default"},
		prompts: []registry.PromptInfo{
			{Name: "save-this-as-an-asset", Description: "Save asset"},
		},
	})

	h := &Handle{registry: reg}

	infos := h.collectToolkitPromptInfos()
	require.Len(t, infos, 1)
	assert.Equal(t, "save-this-as-an-asset", infos[0].Name)
}

func TestIsOperatorPrompt(t *testing.T) {
	h := &Handle{operatorPrompts: []PromptSpec{{Name: "my-prompt"}}}

	assert.True(t, h.isOperatorPrompt("my-prompt"))
	assert.False(t, h.isOperatorPrompt("nonexistent"))
}

func TestHasAllToolkitKinds(t *testing.T) {
	reg := registry.NewRegistry()
	_ = reg.Register(&mockToolkit{kind: "datahub", name: "primary"})
	_ = reg.Register(&mockToolkit{kind: "trino", name: "default"})

	h := &Handle{registry: reg}

	assert.True(t, h.hasAllToolkitKinds([]string{"datahub"}))
	assert.True(t, h.hasAllToolkitKinds([]string{"datahub", "trino"}))
	assert.False(t, h.hasAllToolkitKinds([]string{"datahub", "portal"}))
	assert.True(t, h.hasAllToolkitKinds([]string{}))
}

func TestBuildPromptResult(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "simple content", content: "This is a simple prompt."},
		{name: "multiline content", content: "Line 1\nLine 2\nLine 3"},
		{name: "empty content", content: ""},
		{name: "content with special characters", content: "Use `code` and **bold** formatting."},
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

func TestWorkflowPromptArguments(t *testing.T) {
	// Verify each workflow prompt has the expected argument
	prompts := workflowPrompts()
	for _, wp := range prompts {
		t.Run(wp.spec.Name, func(t *testing.T) {
			require.NotEmpty(t, wp.spec.Arguments, "workflow prompt should have arguments")
			require.NotEmpty(t, wp.requiredKinds, "workflow prompt should require toolkits")
			assert.NotEmpty(t, wp.spec.Description)
			assert.NotEmpty(t, wp.spec.Content)
			// Verify the argument placeholder exists in the content
			for _, arg := range wp.spec.Arguments {
				assert.Contains(t, wp.spec.Content, "{"+arg.Name+"}",
					"content should contain placeholder for argument %q", arg.Name)
			}
		})
	}
}

// --- Test helpers ---

func promptNames(prompts []*mcp.Prompt) []string {
	names := make([]string, 0, len(prompts))
	for _, p := range prompts {
		names = append(names, p.Name)
	}
	return names
}
