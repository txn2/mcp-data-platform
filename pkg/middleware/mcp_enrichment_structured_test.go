package middleware

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// TestEnrichment_MergesSemanticContextIntoStructured verifies #571: the Trino
// semantic context the middleware appends as a text block is also folded into
// the structured result (merged alongside the tool's own structured fields),
// while the text block is kept for content-rendering clients.
func TestEnrichment_MergesSemanticContextIntoStructured(t *testing.T) {
	mockProvider := &mockSemanticProvider{
		getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
			return &semantic.TableContext{Description: "User accounts table", Tags: []string{"pii"}}, nil
		},
	}
	mw := MCPSemanticEnrichmentMiddleware(mockProvider, nil, nil,
		EnrichmentConfig{EnrichTrinoResults: true}, nil)

	handler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: "id | bigint"}},
			StructuredContent: map[string]any{"columns": []string{"id", "name"}},
		}, nil
	}
	req := createServerRequest(t, enrichTestDescribeTable, map[string]any{
		"catalog": "t", "schema": "p", "table": "u",
	})

	result, err := mw(handler)(context.Background(), enrichTestMethodToolsCall, req)
	require.NoError(t, err)
	cr, ok := result.(*mcp.CallToolResult)
	require.True(t, ok)

	// Text enrichment block still present (additive).
	require.GreaterOrEqual(t, len(cr.Content), 2)

	// Structured content now carries the semantic context alongside the original.
	sc, ok := cr.StructuredContent.(map[string]any)
	require.True(t, ok, "structured content should be a map, got %T", cr.StructuredContent)
	assert.Contains(t, sc, "columns", "original structured fields preserved")
	require.Contains(t, sc, "semantic_context", "semantic context folded into structured")
	scJSON, _ := json.Marshal(sc["semantic_context"])
	assert.Contains(t, string(scJSON), "User accounts table")
}

func TestStructuredAsMap(t *testing.T) {
	assert.Empty(t, structuredAsMap(nil), "nil yields an empty map")

	m := map[string]any{"a": float64(1)}
	assert.Equal(t, m, structuredAsMap(m), "an existing map is returned as-is")

	type out struct {
		Table string `json:"table"`
	}
	got := structuredAsMap(out{Table: "x"})
	assert.Equal(t, "x", got["table"], "a typed struct round-trips to a map")

	assert.Empty(t, structuredAsMap([]int{1, 2}), "a non-object value yields an empty map")
}

func TestMirrorEnrichmentToStructured_EdgeCases(t *testing.T) {
	// nil result and an out-of-range fromIndex are no-ops (no panic).
	mirrorEnrichmentToStructured(nil, 0)
	r := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "x"}}}
	mirrorEnrichmentToStructured(r, 5)
	assert.Nil(t, r.StructuredContent)

	// Resource links and non-JSON text blocks are skipped; nothing is folded.
	r2 := &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.ResourceLink{URI: "schema://x"},
		&mcp.TextContent{Text: "not json"},
	}}
	mirrorEnrichmentToStructured(r2, 0)
	assert.Nil(t, r2.StructuredContent, "no JSON enrichment blocks means structured content is untouched")
}

// TestEnrichment_StructuredViaAssembledServer is the #571 acceptance: boot a
// real mcp.Server with the enrichment middleware via AddReceivingMiddleware,
// call trino_describe_table over an in-memory transport, and assert the result
// the CLIENT receives carries the semantic context in StructuredContent (and the
// text block is still present). The in-process test cannot prove the assembled
// SDK wiring delivers the merged structured content over the wire.
func TestEnrichment_StructuredViaAssembledServer(t *testing.T) {
	mockProvider := &mockSemanticProvider{
		getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
			return &semantic.TableContext{Description: "Sales fact table", Tags: []string{"finance"}}, nil
		},
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "enrich-test", Version: "v0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "trino_describe_table",
		Description: "describe",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: "id | bigint"}},
			StructuredContent: map[string]any{"table": "sales", "columns": []string{"id"}},
		}, nil
	})
	server.AddReceivingMiddleware(MCPSemanticEnrichmentMiddleware(
		mockProvider, nil, nil, EnrichmentConfig{EnrichTrinoResults: true}, nil))

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	_, err := server.Connect(ctx, t1, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil)
	sess, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)
	defer func() { _ = sess.Close() }()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trino_describe_table",
		Arguments: map[string]any{"catalog": "warehouse", "schema": "public", "table": "sales"},
	})
	require.NoError(t, err)

	// Structured content reaches the client with the folded semantic context.
	scJSON, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	assert.Contains(t, string(scJSON), "semantic_context", "structured result carries semantic context")
	assert.Contains(t, string(scJSON), "Sales fact table")
	assert.Contains(t, string(scJSON), "table", "original structured fields preserved")

	// The text enrichment block is still present for content-rendering clients.
	var foundText bool
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && strings.Contains(tc.Text, "semantic_context") {
			foundText = true
		}
	}
	assert.True(t, foundText, "text enrichment block still present")
}
