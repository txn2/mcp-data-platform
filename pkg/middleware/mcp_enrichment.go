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
			if method != "tools/call" {
				return next(ctx, method, req)
			}

			result, err := next(ctx, method, req)
			if err != nil {
				return result, err
			}

			return enrichToolResult(ctx, enricher, req, result)
		}
	}
}

// enrichToolResult checks if a tool call result is eligible for enrichment and applies it.
func enrichToolResult(ctx context.Context, enricher *semanticEnricher, req mcp.Request, result mcp.Result) (mcp.Result, error) {
	callResult, ok := result.(*mcp.CallToolResult)
	if !ok || callResult == nil || callResult.IsError {
		return result, nil
	}

	toolName, extractErr := extractToolName(req)
	if extractErr != nil {
		return result, nil //nolint:nilerr // enrichment is best-effort; skip if tool name extraction fails
	}

	toolkitKind := inferToolkitKind(toolName)
	if toolkitKind == "" {
		return result, nil
	}

	pc := GetPlatformContext(ctx)
	if pc == nil {
		pc = NewPlatformContext("")
	}
	pc.ToolkitKind = toolkitKind
	pc.ToolName = toolName

	return applyEnrichment(ctx, enricher, req, callResult, pc)
}

// discoveryNoteMessage is the soft note appended to enriched results when the
// session has not yet performed DataHub discovery.
const discoveryNoteMessage = "Note: No DataHub discovery has been performed in this session yet. " +
	"Call datahub_search to understand the business context, ownership, and data quality " +
	"of the tables you are querying."

// applyEnrichment enriches the result and tracks whether enrichment was applied on the PlatformContext.
func applyEnrichment(
	ctx context.Context,
	enricher *semanticEnricher,
	req mcp.Request,
	callResult *mcp.CallToolResult,
	pc *PlatformContext,
) (mcp.Result, error) {
	callReq := buildCallToolRequest(req)

	beforeLen := len(callResult.Content)
	enrichedResult, _ := enricher.enrich(ctx, callResult, callReq, pc)
	if len(enrichedResult.Content) > beforeLen {
		pc.EnrichmentApplied = true
		if pc.EnrichmentMode == "" {
			pc.EnrichmentMode = EnrichmentModeFull
		}
	}

	appendDiscoveryNoteIfNeeded(enrichedResult, pc, enricher.cfg.WorkflowTracker)

	return enrichedResult, nil
}

// appendDiscoveryNoteIfNeeded appends a soft discovery note to enriched results
// when the session has not yet performed DataHub discovery.
func appendDiscoveryNoteIfNeeded(result *mcp.CallToolResult, pc *PlatformContext, tracker *SessionWorkflowTracker) {
	if tracker == nil || !pc.EnrichmentApplied {
		return
	}
	if tracker.HasPerformedDiscovery(pc.SessionID) {
		return
	}

	noteJSON, err := json.Marshal(map[string]string{
		"discovery_note": discoveryNoteMessage,
	})
	if err != nil {
		return
	}
	result.Content = append(result.Content, &mcp.TextContent{Text: string(noteJSON)})
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
