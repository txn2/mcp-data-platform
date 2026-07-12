package platform

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPlatformToolsAdvertiseAndHonorOutputSchemas assembles the real platform
// (with the full middleware chain wired by finalizeSetup), connects an in-memory
// client, and proves the #925 contract for the stable platform-owned tools:
// tools/list advertises an OutputSchema for platform_info, list_connections, and
// platform_find_tools, and a real call's structuredContent validates against the
// advertised schema. It also proves the error envelope (what the error contract
// substitutes on failure) validates against each schema.
func TestPlatformToolsAdvertiseAndHonorOutputSchemas(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test-platform"},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		// Without a persona that can reach the tools, the deny-all default persona
		// hides them from tools/list and refuses tools/call; grant an allow-all
		// persona so the client exercises the real advertised surface.
		Personas: PersonasConfig{
			Definitions: map[string]PersonaDef{
				"default": {DisplayName: "Default", Tools: ToolRulesDef{Allow: []string{"*"}}},
			},
			DefaultPersona: "default",
		},
	}
	p, err := New(WithConfig(cfg))
	require.NoError(t, err)

	ctx := context.Background()
	// Start registers the platform-level tools (platform_info, list_connections,
	// platform_find_tools) on the MCP server.
	require.NoError(t, p.Start(ctx))
	defer func() { _ = p.Stop(ctx) }()

	t1, t2 := mcp.NewInMemoryTransports()
	ss, err := p.MCPServer().Connect(ctx, t1, nil)
	require.NoError(t, err)
	defer func() { _ = ss.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)
	defer func() { _ = cs.Close() }()

	schemas := listedPlatformSchemas(ctx, t, cs)

	// platform_info mints the session handle every other tool must thread; call it
	// first, validate its structuredContent, then reuse the minted session_id.
	infoSchema, ok := schemas["platform_info"]
	require.True(t, ok, "platform_info advertises an outputSchema in tools/list")
	infoRes, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "platform_info", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, infoRes.IsError, "platform_info succeeded: %v", infoRes.Content)
	require.NotNil(t, infoRes.StructuredContent)
	validatePlatformSC(t, infoSchema, infoRes.StructuredContent)
	sessionID := sessionIDFromSC(t, infoRes.StructuredContent)
	require.NotEmpty(t, sessionID, "platform_info mints a session_id")

	// The session-threaded tools.
	calls := []struct {
		tool string
		args map[string]any
	}{
		{"list_connections", map[string]any{"session_id": sessionID}},
		{"platform_find_tools", map[string]any{"query": "search", "session_id": sessionID}},
	}
	for _, c := range calls {
		t.Run(c.tool, func(t *testing.T) {
			resolved, ok := schemas[c.tool]
			require.True(t, ok, "%s advertises an outputSchema in tools/list", c.tool)

			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: c.tool, Arguments: c.args})
			require.NoError(t, err, "transport error")
			require.False(t, res.IsError, "call succeeded: %v", res.Content)
			require.NotNil(t, res.StructuredContent, "call emits structuredContent")
			validatePlatformSC(t, resolved, res.StructuredContent)
		})
	}

	// The error contract replaces the body with the shared {error} envelope on
	// failure; it must validate against every declared schema.
	envelope := map[string]any{
		"error": map[string]any{
			"code":     "internal_error",
			"category": "internal",
			"message":  "boom",
		},
	}
	for tool, resolved := range schemas {
		t.Run("error envelope validates: "+tool, func(t *testing.T) {
			validatePlatformSC(t, resolved, envelope)
		})
	}
}

// sessionIDFromSC extracts the minted session_id from a platform_info result's
// structured content.
func sessionIDFromSC(t *testing.T, sc any) string {
	t.Helper()
	b, err := json.Marshal(sc)
	require.NoError(t, err)
	var m struct {
		SessionID string `json:"session_id"`
	}
	require.NoError(t, json.Unmarshal(b, &m))
	return m.SessionID
}

func listedPlatformSchemas(ctx context.Context, t *testing.T, cs *mcp.ClientSession) map[string]*jsonschema.Resolved {
	t.Helper()
	lt, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	require.NoError(t, err)
	out := map[string]*jsonschema.Resolved{}
	for _, tool := range lt.Tools {
		if tool.OutputSchema == nil {
			continue
		}
		raw, err := json.Marshal(tool.OutputSchema)
		require.NoError(t, err)
		var s jsonschema.Schema
		require.NoError(t, json.Unmarshal(raw, &s))
		resolved, err := s.Resolve(nil)
		require.NoError(t, err)
		out[tool.Name] = resolved
	}
	return out
}

func validatePlatformSC(t *testing.T, resolved *jsonschema.Resolved, sc any) {
	t.Helper()
	b, err := json.Marshal(sc)
	require.NoError(t, err)
	var v any
	require.NoError(t, json.Unmarshal(b, &v))
	assert.NoError(t, resolved.Validate(v), "structuredContent must validate: %s", string(b))
}

// TestPlatformOutputSchemasAreOpen guards the design decision: every declared
// platform schema is open (additionalProperties allowed, nothing required) so
// the error envelope and any future enrichment key never invalidate a result.
func TestPlatformOutputSchemasAreOpen(t *testing.T) {
	for name, s := range map[string]*jsonschema.Schema{
		"platform_info":       infoOutputSchema,
		"list_connections":    connectionsOutputSchema,
		"platform_find_tools": findToolsOutputSchema,
	} {
		require.NotNil(t, s.AdditionalProperties, "%s: additionalProperties set", name)
		assert.Empty(t, s.AdditionalProperties.Type, "%s: additionalProperties is the open {} schema", name)
		assert.Nil(t, s.Required, "%s: no top-level required fields", name)
		_, ok := s.Properties["error"]
		assert.True(t, ok, "%s: declares the shared error property", name)
	}
}
