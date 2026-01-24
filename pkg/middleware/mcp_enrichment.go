package middleware

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/storage"
)

// MCPSemanticEnrichmentMiddleware creates MCP protocol-level middleware that
// enriches tool call responses with semantic context.
//
// This middleware intercepts tools/call responses and adds cross-service context:
//   - Trino results get DataHub metadata (descriptions, owners, tags, etc.)
//   - DataHub results get Trino query context (can query? sample SQL?)
//   - S3 results get semantic metadata for matching datasets
func MCPSemanticEnrichmentMiddleware(
	semanticProvider semantic.Provider,
	queryProvider query.Provider,
	storageProvider storage.Provider,
	cfg EnrichmentConfig,
) mcp.Middleware {
	enricher := &semanticEnricher{
		semanticProvider: semanticProvider,
		queryProvider:    queryProvider,
		storageProvider:  storageProvider,
		cfg:              cfg,
	}

	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			// Only intercept tools/call requests
			if method != "tools/call" {
				return next(ctx, method, req)
			}

			// Execute tool handler first
			result, err := next(ctx, method, req)
			if err != nil {
				return result, err
			}

			// Only enrich successful CallToolResults
			callResult, ok := result.(*mcp.CallToolResult)
			if !ok || callResult == nil || callResult.IsError {
				return result, nil
			}

			// Get tool name from request
			toolName, extractErr := extractToolName(req)
			if extractErr != nil {
				return result, nil
			}

			// Determine toolkit kind from tool name prefix
			toolkitKind := inferToolkitKind(toolName)
			if toolkitKind == "" {
				return result, nil
			}

			// Build a CallToolRequest for the enrichment functions
			callReq := buildCallToolRequest(req)

			// Get or create platform context for the enricher
			pc := GetPlatformContext(ctx)
			if pc == nil {
				pc = NewPlatformContext("")
			}
			pc.ToolkitKind = toolkitKind
			pc.ToolName = toolName

			// Enrich the result
			enrichedResult, _ := enricher.enrich(ctx, callResult, callReq, pc)
			return enrichedResult, nil
		}
	}
}

// inferToolkitKind determines the toolkit kind from a tool name prefix.
func inferToolkitKind(toolName string) string {
	switch {
	case strings.HasPrefix(toolName, "trino_"):
		return "trino"
	case strings.HasPrefix(toolName, "datahub_"):
		return "datahub"
	case strings.HasPrefix(toolName, "s3_"):
		return "s3"
	default:
		return ""
	}
}

// buildCallToolRequest builds a CallToolRequest from an MCP Request.
func buildCallToolRequest(req mcp.Request) mcp.CallToolRequest {
	if req == nil {
		return mcp.CallToolRequest{}
	}
	params := req.GetParams()
	if params == nil {
		return mcp.CallToolRequest{}
	}

	callParams, ok := params.(*mcp.CallToolParamsRaw)
	if !ok || callParams == nil {
		return mcp.CallToolRequest{}
	}

	return mcp.CallToolRequest{
		Params: callParams,
	}
}

// extractArgumentsMap extracts arguments as a map from CallToolParamsRaw.
func extractArgumentsMap(params *mcp.CallToolParamsRaw) map[string]any {
	if params == nil || len(params.Arguments) == 0 {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal(params.Arguments, &args); err != nil {
		return nil
	}
	return args
}
