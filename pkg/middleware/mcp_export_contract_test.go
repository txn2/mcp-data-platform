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

// exportResponsePayload mirrors an export handler's own response: a single
// JSON text block carrying asset metadata, with no StructuredContent. Both
// export tools register through the generic mcp.AddTool with a typed output,
// so on the running server the SDK writes the same object as the structured
// result too — see exportStructuredPayload; a handler on the untyped
// Server.AddTool path, such as the gateway forwarder, leaves the response as
// this function returns it.
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
// and was not replaced by anything a middleware appended (#822, #1416). It
// checks the handler's text block, and any structured result the chain left on
// the response.
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
	// export response, and a structured result, if the chain produced one at
	// all, must carry the payload rather than stand in for it.
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
	for _, name := range []string{"trino_query", "trino_describe_table", "datahub_browse", "s3_object", "export_config"} {
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

// TestExportContractViaAssembledChain is the #1416 acceptance, and the
// regression test whose absence let #822 return by a second route: an export
// tool called through the middleware chain as the platform assembles it — the
// call reference outer to enrichment, both fed the PlatformContext the
// auth/authz layer writes — hands the client the asset metadata its own
// description promises.
//
// Testing each middleware alone proved neither one clobbers the response, which
// is not the property that matters: what reaches the client is the chain's
// output, and it was the second middleware appending through the same helper
// that emptied it.
func TestExportContractViaAssembledChain(t *testing.T) {
	for _, tc := range []struct {
		toolName    string
		toolkitKind string
		// structured is whether this tool's real handler leaves the SDK a
		// structured result to merge into. trino_export and api_export
		// register through the generic mcp.AddTool with a typed output and do
		// (#1589 moved trino_export there). A tool the gateway proxies from an
		// upstream server that answered in text registers through the untyped
		// Server.AddTool path and does not; its appended blocks stay in
		// content beside the response they belong to.
		structured bool
	}{
		{toolName: "trino_export", toolkitKind: "trino", structured: true},
		{toolName: "api_export", toolkitKind: "api", structured: true},
		{toolName: "mcp-test-fixture__echo", toolkitKind: "mcp", structured: false},
	} {
		t.Run(tc.toolName, func(t *testing.T) {
			server := mcp.NewServer(&mcp.Implementation{Name: "export-chain-test", Version: "v0"}, nil)
			server.AddTool(&mcp.Tool{
				Name:        tc.toolName,
				Description: "export",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				res := exportResponsePayload(t)
				if tc.structured {
					res.StructuredContent = exportStructuredPayload(t)
				}
				return res, nil
			})

			// Registered innermost-first, so the assembled order is the
			// platform's: PlatformContext outermost, then the call reference,
			// then enrichment (pkg/platform/middleware_chain.go).
			server.AddReceivingMiddleware(MCPSemanticEnrichmentMiddleware(
				exportEnrichmentProvider(), nil, nil, EnrichmentConfig{EnrichTrinoResults: true}, nil))
			server.AddReceivingMiddleware(MCPCallReferenceMiddleware([]string{"trino", "api", "mcp"}))
			server.AddReceivingMiddleware(exportPlatformContextMiddleware(tc.toolkitKind))

			ctx := context.Background()
			t1, t2 := mcp.NewInMemoryTransports()
			_, err := server.Connect(ctx, t1, nil)
			require.NoError(t, err)
			client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil)
			sess, err := client.Connect(ctx, t2, nil)
			require.NoError(t, err)
			defer func() { _ = sess.Close() }()

			res, err := sess.CallTool(ctx, &mcp.CallToolParams{
				Name: tc.toolName,
				Arguments: map[string]any{
					"sql":    "SELECT * FROM warehouse.public.regions ORDER BY 1 LIMIT 10",
					"format": "csv",
				},
			})
			require.NoError(t, err)

			assertExportContractSurvives(t, res)
			if tc.structured {
				// The typed handler's own keys are still there, with the
				// reference merged in beside them rather than over them.
				structured, ok := res.StructuredContent.(map[string]any)
				require.True(t, ok, "structured result = %T", res.StructuredContent)
				assert.Equal(t, "exp_abc123", structured["asset_id"])
				assert.Contains(t, structured, CallReferenceKey)
			} else {
				assert.Nil(t, res.StructuredContent,
					"a handler that set no structured result must not be given one built from the chain's own blocks")
			}

			// The call is still citable: the reference the export gained is
			// what an agent names when it says which call built the asset.
			ref, ok := readCallReference(t, res)
			require.True(t, ok, "an export is a data call and keeps its reference")
			assert.Equal(t, exportEventID, ref.CallID)
		})
	}
}

// exportStructuredPayload is the structured result the SDK writes for an export
// tool registered with a typed output, which both are
// (pkg/toolkits/apigateway/export.go and pkg/toolkits/trino/export.go return
// their output value alongside the result, and the SDK marshals it into
// StructuredContent).
func exportStructuredPayload(t *testing.T) map[string]any {
	t.Helper()
	tc, ok := exportResponsePayload(t).Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	return out
}

// exportEventID is the recorded call id the assembled-chain test stamps with.
const exportEventID = "evt-export-1"

// exportPlatformContextMiddleware supplies the PlatformContext the auth/authz
// middleware writes in the running server, which is what the call-reference
// middleware reads to decide a call is a citable data call.
func exportPlatformContextMiddleware(toolkitKind string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			pc := NewPlatformContext("req-export")
			pc.EventID = exportEventID
			pc.ToolkitKind = toolkitKind
			return next(WithPlatformContext(ctx, pc), method, req)
		}
	}
}
