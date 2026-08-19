package platform

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/auth"
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
		// A caller matching no persona reaches the deny-all default, which hides
		// the tools from tools/list and refuses tools/call. Grant a persona whose
		// role the anonymous test identity carries so the client exercises the
		// real advertised surface.
		Personas: PersonasConfig{
			Definitions: map[string]PersonaDef{
				"default": {
					DisplayName: "Default",
					Roles:       []string{auth.RoleAnonymous},
					Tools:       ToolRulesDef{Allow: []string{"*"}},
				},
			},
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

// TestThirdPartyToolResultsValidateAgainstTheirAdvertisedSchemas is the gate
// #1381 asked for: it assembles the real Trino and S3 toolkits (against
// endpoints that refuse every request, so nearly every call fails the way a
// deployment's does when its backend is down; s3_presign_url signs locally and
// succeeds), lists their tools through the assembled server, calls each tool
// through the full receiving chain, and validates the returned
// structuredContent against the output schema the same server advertised for
// it. mcp-trino before v1.4.0 registered typed handlers with no explicit
// schema, so the SDK inferred one and jsonschema-go closed it; both toolkits
// now declare open schemas, and the platform opens every advertised one
// regardless. Every advertised schema must admit what the chain hands back: the error envelope the
// contract substitutes on failure, and a success body. The success path that
// carries a call_reference is proved on a real database by
// TestRealDB_TrinoQueryResultWithCallReferenceValidatesAgainstAdvertisedSchema.
func TestThirdPartyToolResultsValidateAgainstTheirAdvertisedSchemas(t *testing.T) {
	s3Instance := map[string]any{"region": "us-east-1", "access_key_id": "a", "secret_access_key": "b"}
	cfg := &Config{
		Server:   ServerConfig{Name: "test-platform"},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Personas: PersonasConfig{Definitions: map[string]PersonaDef{"default": {
			DisplayName: "Default",
			Roles:       []string{auth.RoleAnonymous},
			Tools:       ToolRulesDef{Allow: []string{"*"}},
			Connections: ConnectionRulesDef{Allow: []string{"*"}},
		}}},
		// The search-first gate would refuse trino_query before the toolkit
		// runs; this gate is about what the toolkit's own result looks like.
		Workflow: WorkflowConfig{RequireSearch: new(false)},
		Toolkits: map[string]any{
			"trino": map[string]any{"enabled": true, "instances": map[string]any{
				"acme": map[string]any{"host": "127.0.0.1", "port": 1, "user": "t"},
			}},
			"s3": map[string]any{"enabled": true, "instances": map[string]any{"acme": s3Instance}},
		},
	}
	// An S3 endpoint that refuses every request at once, so the S3 client's
	// retry schedule does not set the pace of the test.
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `<Error><Code>AccessDenied</Code><Message>refused</Message></Error>`, http.StatusForbidden)
	}))
	t.Cleanup(refusing.Close)
	s3Instance["endpoint"] = refusing.URL
	s3Instance["use_path_style"] = true

	p, err := New(WithConfig(cfg))
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, p.Start(ctx))
	defer func() { _ = p.Stop(ctx) }()

	t1, t2 := mcp.NewInMemoryTransports()
	ss, err := p.MCPServer().Connect(ctx, t1, nil)
	require.NoError(t, err)
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, t2, nil)
	require.NoError(t, err)
	defer func() { _ = cs.Close() }()

	schemas := listedPlatformSchemas(ctx, t, cs)
	for _, name := range []string{"trino_query", "trino_describe_table", "trino_explain", "trino_browse", "s3_list_objects", "s3_list_buckets"} {
		require.Contains(t, schemas, name, "%s advertises an output schema", name)
	}
	// A real client states a purpose exactly where the server advertised the
	// argument; the gate refuses a data call without one before the toolkit
	// runs, and a tool that does not advertise it rejects the unknown argument.
	takesPurpose := map[string]bool{}
	lt, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	for _, tool := range lt.Tools {
		raw, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		var in struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		require.NoError(t, json.Unmarshal(raw, &in))
		_, takesPurpose[tool.Name] = in.Properties["purpose"]
	}

	info, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "platform_info", Arguments: map[string]any{}})
	require.NoError(t, err)
	sid := sessionIDFromSC(t, info.StructuredContent)
	require.NotEmpty(t, sid)

	// The arguments a real caller would send; anything the tool needs beyond
	// these is still answered with a result whose structuredContent must
	// validate, so an unexpected refusal is covered as well.
	args := map[string]map[string]any{
		"trino_query":            {"sql": "SELECT 1"},
		"trino_execute":          {"sql": "SELECT 1"},
		"trino_explain":          {"sql": "SELECT 1"},
		"trino_describe_table":   {"table": "memory.default.t"},
		"s3_list_objects":        {"bucket": "b"},
		"s3_get_object":          {"bucket": "b", "key": "k"},
		"s3_get_object_metadata": {"bucket": "b", "key": "k"},
		"s3_presign_url":         {"bucket": "b", "key": "k"},
		"s3_put_object":          {"bucket": "b", "key": "k", "content": "x"},
		"s3_copy_object":         {"source_bucket": "b", "source_key": "k", "dest_bucket": "b", "dest_key": "k2"},
		"s3_delete_object":       {"bucket": "b", "key": "k"},
	}
	checked := 0
	for name, resolved := range schemas {
		if !strings.HasPrefix(name, "trino_") && !strings.HasPrefix(name, "s3_") {
			continue
		}
		checked++
		t.Run(name, func(t *testing.T) {
			callArgs := map[string]any{"session_id": sid, "connection": "acme"}
			if takesPurpose[name] {
				callArgs["purpose"] = "Proving every tool result validates against its advertised schema."
			}
			maps.Copy(callArgs, args[name])
			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: callArgs})
			require.NoError(t, err, "the call is answered with a result, not a protocol error")
			require.NotNil(t, res.StructuredContent, "the chain emits structuredContent: %v", res.Content)
			validatePlatformSC(t, resolved, res.StructuredContent)
		})
	}
	require.GreaterOrEqual(t, checked, 10, "the real Trino and S3 toolkits were assembled and their tools called")
}
