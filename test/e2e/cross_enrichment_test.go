//go:build integration

// Package e2e provides end-to-end tests for the MCP Data Platform.
package e2e

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/test/e2e/helpers"
)

// TestTrinoToCatalogEnrichment tests that trino_describe_table results carry
// the semantic context the platform catalog holds for the table: its owners
// and tags as helpers.SeedCatalog wrote them.
func TestTrinoToCatalogEnrichment(t *testing.T) {
	cfg := helpers.DefaultE2EConfig()

	ctx, cancel := helpers.TestContext(cfg.Timeout)
	defer cancel()

	platform, err := helpers.NewTestPlatform(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create test platform: %v", err)
	}
	defer func() {
		if cerr := platform.Close(); cerr != nil {
			t.Errorf("failed to close platform: %v", cerr)
		}
	}()

	result := callTrinoDescribeTable(t, ctx, platform, helpers.TestOrdersPath)
	sc := helpers.AssertHasSemanticContext(t, result)
	helpers.AssertOwnerPresent(t, sc, helpers.TestOrdersOwnerA)
	helpers.AssertOwnerPresent(t, sc, helpers.TestOrdersOwnerB)
	for _, tag := range helpers.TestOrdersTags {
		helpers.AssertTagPresent(t, sc, tag)
	}
}

// callTrinoDescribeTable simulates a trino_describe_table tool call.
func callTrinoDescribeTable(t *testing.T, ctx context.Context, tp *helpers.TestPlatform, tableName string) *mcp.CallToolResult {
	t.Helper()

	if tp == nil {
		t.Fatal("TestPlatform is nil")
	}

	args, _ := json.Marshal(map[string]string{
		"table": tableName,
	})

	request := mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "trino_describe_table",
			Arguments: args,
		},
	}

	return executeWithMiddleware(t, ctx, tp, request)
}

// executeWithMiddleware executes a tool request through the semantic
// enrichment middleware. This tests the enrichment logic directly rather than
// through the full MCP protocol stack.
func executeWithMiddleware(t *testing.T, ctx context.Context, tp *helpers.TestPlatform, request mcp.CallToolRequest) *mcp.CallToolResult {
	t.Helper()

	// A stand-in handler answering what trino_describe_table answers.
	mockMCPHandler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		var args map[string]string
		_ = json.Unmarshal(request.Params.Arguments, &args)
		resultJSON, _ := json.Marshal(map[string]any{
			"table":   args["table"],
			"columns": []map[string]string{{"name": "order_id", "type": "bigint"}},
		})
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(resultJSON)}},
		}, nil
	}

	enrichmentMW := helpers.CreateEnrichmentMiddleware(tp.SemanticProvider(), tp.QueryProvider())
	wrappedHandler := enrichmentMW(mockMCPHandler)

	serverReq := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
		Params: request.Params,
	}

	result, err := wrappedHandler(ctx, "tools/call", serverReq)
	if err != nil {
		t.Fatalf("middleware execution failed: %v", err)
	}

	callResult, ok := result.(*mcp.CallToolResult)
	if !ok {
		t.Fatalf("expected *mcp.CallToolResult, got %T", result)
	}

	return callResult
}
