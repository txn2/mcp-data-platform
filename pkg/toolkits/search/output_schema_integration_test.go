package search

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// TestOutputSchema_AdvertisedAndValidated wires the real search toolkit onto an
// mcp.Server with the error-contract middleware, connects an in-memory client,
// and proves the #925 contract end to end: tools/list advertises an OutputSchema
// for both search and fetch, and a real call's structuredContent validates
// against the advertised schema across relevance mode, browse mode, the
// structured found=false answer, and a real tool error (error envelope).
func TestOutputSchema_AdvertisedAndValidated(t *testing.T) {
	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "search-test", Version: "v0"}, nil)
	assembledToolkit().RegisterTools(server)
	// The error-contract middleware is what replaces a failed body with the
	// shared {error} envelope; wire it so the error-path assertion exercises the
	// real normalization, not a hand-built result.
	server.AddReceivingMiddleware(middleware.MCPErrorContractMiddleware())

	t1, t2 := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, t1, nil)
	require.NoError(t, err)
	defer func() { _ = ss.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)
	defer func() { _ = cs.Close() }()

	// tools/list advertises an OutputSchema for search and fetch.
	schemas := listedOutputSchemas(ctx, t, cs)
	require.Contains(t, schemas, "search", "search advertises an outputSchema")
	require.Contains(t, schemas, "fetch", "fetch advertises an outputSchema")

	cases := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"search relevance mode", "search", map[string]any{"intent": "alice"}},
		{"search browse mode", "search", map[string]any{"sources": []any{"knowledge_pages"}}},
		{"fetch found", "fetch", map[string]any{"reference": "mcp:knowledge_page:kp-1"}},
		{"fetch structured not-found", "fetch", map[string]any{"reference": "mcp:knowledge_page:missing"}},
		// A blank reference is a tool error; the contract middleware turns it into
		// the {error} envelope, which must still validate against the schema.
		{"fetch tool error", "fetch", map[string]any{"reference": "   "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
			require.NoError(t, err, "transport error")
			require.NotNil(t, res.StructuredContent, "call emits structuredContent")
			validateAgainst(t, schemas[tc.tool], res.StructuredContent)
		})
	}
}

// listedOutputSchemas returns each tool's advertised OutputSchema, resolved and
// ready to validate, keyed by tool name.
func listedOutputSchemas(ctx context.Context, t *testing.T, cs *mcp.ClientSession) map[string]*jsonschema.Resolved {
	t.Helper()
	lt, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	require.NoError(t, err)
	out := map[string]*jsonschema.Resolved{}
	for _, tool := range lt.Tools {
		if tool.OutputSchema == nil {
			continue
		}
		// The advertised schema arrives as generic JSON over the wire; remarshal
		// it into a *jsonschema.Schema, exactly as a spec-faithful client would to
		// validate results.
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

// validateAgainst validates a wire-round-tripped structuredContent value against
// a resolved schema, the same check a spec-faithful client performs.
func validateAgainst(t *testing.T, resolved *jsonschema.Resolved, sc any) {
	t.Helper()
	require.NotNil(t, resolved)
	b, err := json.Marshal(sc)
	require.NoError(t, err)
	var v any
	require.NoError(t, json.Unmarshal(b, &v))
	assert.NoError(t, resolved.Validate(v), "structuredContent must validate against the advertised schema: %s", string(b))
}
