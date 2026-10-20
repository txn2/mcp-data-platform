package platform

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pastOutputSchema is one tool's output schema as one release advertised it.
type pastOutputSchema struct {
	Version string
	Tool    string
	Schema  *jsonschema.Resolved
}

// pastOutputSchemas reads testdata/past_output_schemas.json.
func pastOutputSchemas(t *testing.T) []pastOutputSchema {
	t.Helper()
	raw, err := os.ReadFile("testdata/past_output_schemas.json")
	require.NoError(t, err)
	var record struct {
		Releases []struct {
			Version string                        `json:"version"`
			Schemas map[string]*jsonschema.Schema `json:"schemas"`
		} `json:"releases"`
	}
	require.NoError(t, json.Unmarshal(raw, &record))
	var out []pastOutputSchema
	for _, rel := range record.Releases {
		for tool, s := range rel.Schemas {
			resolved, err := s.Resolve(nil)
			require.NoError(t, err, "%s %s", rel.Version, tool)
			out = append(out, pastOutputSchema{Version: rel.Version, Tool: tool, Schema: resolved})
		}
	}
	require.NotEmpty(t, out)
	return out
}

// TestEveryPastOutputSchemaAcceptsTheResult is #2216's gate. A client caches
// a tool list across an upgrade and validates every later result against the
// output schema it holds, so a key an earlier release marked required cannot
// leave the result: v1.146.0 removed features.query_enrichment and
// features.storage_enrichment, and platform_info, the first call of every
// session, failed for a client holding any tool list up to v1.145.0. Each
// recorded tool is called and its result validated against every release's
// schema for it.
func TestEveryPastOutputSchemaAcceptsTheResult(t *testing.T) {
	ctx := context.Background()
	cs := pastReleaseClient(ctx, t)

	info := structuredResult(ctx, t, cs, "platform_info", "")
	session, _ := info.(map[string]any)["session_id"].(string)
	require.NotEmpty(t, session, "platform_info names no session_id")
	results := map[string]any{"platform_info": info}
	for _, past := range pastOutputSchemas(t) {
		result, ok := results[past.Tool]
		if !ok {
			result = structuredResult(ctx, t, cs, past.Tool, session)
			results[past.Tool] = result
		}
		assert.NoError(t, past.Schema.Validate(result), "%s's result against the schema %s advertised", past.Tool, past.Version)
	}
	assert.Contains(t, results, "platform_info")
}

// structuredResult calls a tool in the session platform_info opened and
// returns its structured content as plain JSON values.
func structuredResult(ctx context.Context, t *testing.T, cs *mcp.ClientSession, tool, session string) any {
	t.Helper()
	args := map[string]any{}
	if session != "" {
		args["session_id"] = session
	}
	if tool == "platform_find_tools" {
		args["query"] = "query a table"
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	require.NoError(t, err)
	require.False(t, res.IsError, "%s: %v", tool, res.Content)
	raw, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var v any
	require.NoError(t, json.Unmarshal(raw, &v))
	return v
}
