package middleware

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// exportResponsePayload mirrors the trino_export / api_export handler output:
// a single JSON text block carrying asset metadata, with no StructuredContent
// set by the handler (see pkg/toolkits/trino/export.go exportSuccess).
func exportResponsePayload(t *testing.T) *mcp.CallToolResult {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"asset_id":   "exp_abc123",
		"portal_url": "https://portal.example.com/assets/exp_abc123",
		"format":     "csv",
		"row_count":  10,
		"size_bytes": 2048,
		"message":    "Exported 10 rows.",
	})
	require.NoError(t, err)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(body)}},
	}
}

// assertExportContractSurvives asserts that the export asset metadata is present
// and was not replaced by source-table semantic_context (#822). It checks both
// the text content block and the structured content the enrichment layer may
// synthesize.
func assertExportContractSurvives(t *testing.T, cr *mcp.CallToolResult) {
	t.Helper()

	// The handler's asset-metadata text block must still be present verbatim.
	require.NotEmpty(t, cr.Content)
	tc, ok := cr.Content[0].(*mcp.TextContent)
	require.True(t, ok, "first content block should be the export text block, got %T", cr.Content[0])
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &payload))
	assert.Equal(t, "exp_abc123", payload["asset_id"])
	assert.Equal(t, "csv", payload["format"])
	assert.EqualValues(t, 10, payload["row_count"])
	assert.EqualValues(t, 2048, payload["size_bytes"])
	assert.NotEmpty(t, payload["portal_url"])
	assert.NotEmpty(t, payload["message"])

	// Enrichment must not have leaked source-table semantic context onto the
	// export response, nor synthesized structured content that omits the payload.
	assert.NotContains(t, tc.Text, "semantic_context")
	if cr.StructuredContent != nil {
		scJSON, err := json.Marshal(cr.StructuredContent)
		require.NoError(t, err)
		assert.Contains(t, string(scJSON), "asset_id",
			"structured content, if present, must retain the asset metadata")
		assert.NotContains(t, string(scJSON), "semantic_context",
			"structured content must not be clobbered by source-table enrichment")
	}
}

// exportEnrichmentProvider is a semantic provider that would enrich any table it
// is asked about — used to prove the export path never routes into it.
func exportEnrichmentProvider() *mockSemanticProvider {
	return &mockSemanticProvider{
		getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
			return &semantic.TableContext{Description: "Geographic sales regions", Tags: []string{"reference"}}, nil
		},
	}
}

// TestIsExportTool is the classification guard for the #822 bypass. Unlike the
// middleware tests below, this directly pins which tool names bypass enrichment,
// so it catches a regression for api_export (whose prefix inferToolkitKind does
// not classify, meaning the assembled-server test alone cannot prove the guard
// is what protects it).
func TestIsExportTool(t *testing.T) {
	for _, name := range []string{"trino_export", "api_export", "s3_export"} {
		assert.Truef(t, isExportTool(name), "%q must be treated as an export tool (bypass enrichment)", name)
	}
	for _, name := range []string{"trino_query", "trino_describe_table", "datahub_browse", "s3_get_object", "export_config"} {
		assert.Falsef(t, isExportTool(name), "%q must not be treated as an export tool", name)
	}
}

// TestEnrichment_ExportToolsBypassEnrichment is the #822 middleware-level
// regression: an export tool carrying a source-SQL argument that resolves to an
// enrichable table must pass through with its asset-metadata payload intact and
// no semantic_context appended. Only trino_export is exercised here because it
// is the tool whose trino_ prefix inferToolkitKind classifies as enrichable —
// i.e. the case that was actually clobbered on the running deployment and that
// the isExportTool bypass has to catch. (api_export is covered by
// TestIsExportTool for classification and by the assembled-server test below for
// the response contract.)
func TestEnrichment_ExportToolsBypassEnrichment(t *testing.T) {
	mw := MCPSemanticEnrichmentMiddleware(exportEnrichmentProvider(), nil, nil,
		EnrichmentConfig{EnrichTrinoResults: true}, nil)

	handler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return exportResponsePayload(t), nil
	}
	// A source SQL that names an enrichable table — the exact shape that
	// triggered the clobber on the running deployment.
	req := createServerRequest(t, "trino_export", map[string]any{
		"sql":    "SELECT * FROM warehouse.public.regions ORDER BY 1 LIMIT 10",
		"format": "csv",
	})

	result, err := mw(handler)(context.Background(), enrichTestMethodToolsCall, req)
	require.NoError(t, err)
	cr, ok := result.(*mcp.CallToolResult)
	require.True(t, ok)

	// No enrichment block appended.
	assert.Len(t, cr.Content, 1, "export response must not gain enrichment blocks")
	assertExportContractSurvives(t, cr)
}

// TestEnrichment_ExportContractViaAssembledServer is the #822 acceptance: boot a
// real mcp.Server with the enrichment middleware via AddReceivingMiddleware,
// call the export tool over an in-memory transport, and assert the result the
// CLIENT receives still carries the export asset metadata (asset_id, format,
// row_count, size_bytes, message) and was not overwritten by source-table
// semantic_context. Both export tool names are exercised end-to-end.
func TestEnrichment_ExportContractViaAssembledServer(t *testing.T) {
	for _, toolName := range []string{"trino_export", "api_export"} {
		t.Run(toolName, func(t *testing.T) {
			server := mcp.NewServer(&mcp.Implementation{Name: "export-enrich-test", Version: "v0"}, nil)
			server.AddTool(&mcp.Tool{
				Name:        toolName,
				Description: "export",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return exportResponsePayload(t), nil
			})
			server.AddReceivingMiddleware(MCPSemanticEnrichmentMiddleware(
				exportEnrichmentProvider(), nil, nil, EnrichmentConfig{EnrichTrinoResults: true}, nil))

			ctx := context.Background()
			t1, t2 := mcp.NewInMemoryTransports()
			_, err := server.Connect(ctx, t1, nil)
			require.NoError(t, err)
			client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil)
			sess, err := client.Connect(ctx, t2, nil)
			require.NoError(t, err)
			defer func() { _ = sess.Close() }()

			res, err := sess.CallTool(ctx, &mcp.CallToolParams{
				Name: toolName,
				Arguments: map[string]any{
					"sql":    "SELECT * FROM warehouse.public.regions ORDER BY 1 LIMIT 10",
					"format": "csv",
				},
			})
			require.NoError(t, err)
			assertExportContractSurvives(t, res)
		})
	}
}
