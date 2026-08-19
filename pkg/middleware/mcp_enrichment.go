package middleware

import (
	"context"
	"encoding/json"
	"maps"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/internal/tableavail"
	"github.com/txn2/mcp-data-platform/pkg/observability"
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
	memoryProvider MemoryProvider,
	pageProvider ...KnowledgePageProvider,
) mcp.Middleware {
	// The checkable-insight marker resolves through the same query provider this
	// middleware already holds, so it is built here rather than handed in. The
	// nil check is on the concrete type: a nil *Cache stored in the interface
	// would not read as absent, leaving every enriched call asking a resolver
	// that can only answer nothing (#1220).
	if c := tableavail.NewWithOptions(queryProvider,
		tableavail.Options{Timeout: tableavail.DeliveryTimeout, SkipRowEstimate: true}); cfg.VerifiableInsights && c != nil {
		cfg.InsightVerifier = c
	}

	enricher := &semanticEnricher{
		semanticProvider: semanticProvider,
		queryProvider:    queryProvider,
		storageProvider:  storageProvider,
		memoryProvider:   memoryProvider,
		cfg:              cfg,
	}
	if len(pageProvider) > 0 {
		enricher.pageProvider = pageProvider[0]
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

	// A managed script's tool calls are never enriched (#1283). Enrichment
	// appends cross-service context that varies with catalog state — owners,
	// tags, deprecation notices, memories — which is exactly the kind of
	// variation the determinism contract promises a script will not see: the
	// same script version on the same data must produce the same output, and a
	// glossary edit is not a data change. It is also pure cost here, because a
	// script consumes structured content and would have to parse around the
	// appended blocks. Enrichment exists to give a MODEL context it lacks; a
	// script has no model in it.
	if pc := GetPlatformContext(ctx); pc != nil && pc.Source == SourceScript {
		return result, nil
	}

	toolName, extractErr := extractToolName(req)
	if extractErr != nil {
		return result, nil //nolint:nilerr // enrichment is best-effort; skip if tool name extraction fails
	}

	// Export tools (trino_export, api_export, ...) return asset metadata
	// (asset_id, portal_url, row_count, size_bytes), not query rows. Routing
	// them through query enrichment parses the source SQL and appends a
	// source-table semantic_context block. The export handler sets no
	// StructuredContent, so mirrorEnrichmentToStructured then synthesizes a
	// structured result from that appended block alone: clients that render
	// structured output receive only semantic_context and never see the
	// asset_id/portal_url the handler returned (the original text block is
	// retained, but structured-output clients don't read it) (#822). Skip the
	// whole enrichment pipeline so the export response reaches the client
	// unchanged.
	if isExportTool(toolName) {
		return result, nil
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

	// Trace the cross-service enrichment fan-out — the original
	// motivation for tracing (issue #428). ChildSpan is a no-op unless
	// this call is already within a sampled trace, so it costs a single
	// span-context check when tracing is off.
	ctx, span := observability.ChildSpan(ctx, "enrichment",
		trace.WithAttributes(
			attribute.String(spanAttrToolkitKind, toolkitKind),
			attribute.String(spanAttrTool, toolName),
		))
	defer span.End()

	res, err := applyEnrichment(ctx, enricher, req, callResult, pc)
	span.SetAttributes(
		attribute.Bool(spanAttrEnrichApplied, pc.EnrichmentApplied),
		attribute.String(spanAttrEnrichMode, pc.EnrichmentMode),
		attribute.String("enrichment.match_kind", pc.EnrichmentMatchKind),
	)
	return res, err
}

// discoveryNoteMessage is the soft note appended to enriched results when the
// session has not yet performed DataHub discovery.
const discoveryNoteMessage = "Note: No discovery has been performed in this session yet. " +
	"Call search to understand the business context, ownership, and data quality " +
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

	// Capture the entities the tool itself returned before any enrichment appends
	// blocks, so the knowledge-page lookup keys off the tool's own entities rather
	// than enriched neighbors or memory free-text.
	entityURNs := extractEntityURNsFromResult(callResult)

	beforeLen := len(callResult.Content)
	enrichedResult, _ := enricher.enrich(ctx, callResult, callReq, pc)
	if len(enrichedResult.Content) > beforeLen {
		pc.EnrichmentApplied = true
		if pc.EnrichmentMode == "" {
			pc.EnrichmentMode = enrichmentModeFull
		}
	}

	// Attach relevant memories from the memory layer.
	enrichedResult = enrichWithMemories(ctx, enricher.memoryProvider, enrichedResult, pc, enricher.cfg)

	// Attach the canonical knowledge pages that document the named entities (#634).
	enrichedResult = enrichWithKnowledgePages(ctx, enricher.pageProvider, enrichedResult, entityURNs)

	appendDiscoveryNoteIfNeeded(ctx, enrichedResult, pc, enricher.cfg.WorkflowTracker)

	// Record how many bytes of enrichment this call appended so the metrics
	// middleware can surface the per-response overhead (issue #761).
	pc.EnrichmentBytes = enrichmentContentBytes(enrichedResult, beforeLen)

	// Mirror every platform-added enrichment block into the structured result so
	// MCP clients that render only structured output still receive the semantic
	// context, memories, and discovery note (#571). The text blocks are kept for
	// content-rendering clients.
	mirrorEnrichmentToStructured(enrichedResult, beforeLen)

	return enrichedResult, nil
}

// mirrorEnrichmentToStructured copies the JSON enrichment blocks the middleware
// appended to result.Content (at indices >= fromIndex) into
// result.StructuredContent, so a client that surfaces only structured output
// still receives them. Each platform block is a JSON object with named top-level
// keys (semantic_context, column_context, related_memories, discovery_note,
// metadata_reference, ...), which merge cleanly. Resource links and non-JSON
// blocks are skipped. Best-effort: any failure leaves the result unchanged.
func mirrorEnrichmentToStructured(result *mcp.CallToolResult, fromIndex int) {
	if result == nil || fromIndex < 0 || fromIndex >= len(result.Content) {
		return
	}
	added := map[string]any{}
	for _, c := range result.Content[fromIndex:] {
		tc, ok := c.(*mcp.TextContent)
		if !ok {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(tc.Text), &obj); err != nil {
			continue
		}
		maps.Copy(added, obj)
	}
	if len(added) == 0 {
		return
	}
	base := structuredAsMap(result.StructuredContent)
	maps.Copy(base, added)
	result.StructuredContent = base
}

// structuredAsMap returns sc as a map[string]any: the value itself if already a
// map, a JSON round-trip of a typed struct, or a fresh map when nil or not
// convertible. The original typed structured value is replaced by this map so
// the enrichment keys can be merged in.
//
// Interaction with declared OutputSchemas (#925, #1381): the tools this
// middleware enriches (trino_/datahub_/s3_, gated by inferToolkitKind) do
// advertise output schemas, and a typed handler registered without one (as
// mcp-trino's were before v1.4.0) advertises the closed schema the SDK infers. Mirroring open-ended keys into structuredContent is
// schema-valid only because MCPOutputSchemaMiddleware opens the top level of
// every advertised schema in tools/list, the same way the platform-owned
// schemas are built open by middleware.OpenToolOutputSchema.
func structuredAsMap(sc any) map[string]any {
	if sc == nil {
		return map[string]any{}
	}
	if m, ok := sc.(map[string]any); ok {
		return m
	}
	data, err := json.Marshal(sc)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// appendDiscoveryNoteIfNeeded appends a soft discovery note to enriched results
// when the session has not yet performed DataHub discovery.
func appendDiscoveryNoteIfNeeded(ctx context.Context, result *mcp.CallToolResult, pc *PlatformContext, tracker *SessionWorkflowTracker) {
	if tracker == nil || !pc.EnrichmentApplied {
		return
	}
	// Stateless shim runs (portal/admin, gateway/rest) are exempt from the
	// search-first gate (checkWorkflowGate), so the "call search first" nudge is
	// misleading for them: their isolated per-run scope has performed no
	// discovery by construction, but the gate never blocks them. Suppress the
	// note so a portal query replay's result is not littered with an
	// inapplicable prompt (issue #859).
	if isStatelessShimSource(pc.Source) {
		return
	}
	if tracker.HasPerformedDiscovery(ctx, pc.DiscoveryScopeKey()) {
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

// enrichmentContentBytes sums the byte length of the text content blocks the
// enrichment pipeline appended (indices >= fromIndex), giving the per-response
// enrichment overhead the metrics layer reports (issue #761). Non-text blocks
// (resource links, images) carry no enrichment prose and are not counted.
func enrichmentContentBytes(result *mcp.CallToolResult, fromIndex int) int {
	if result == nil || fromIndex < 0 || fromIndex >= len(result.Content) {
		return 0
	}
	total := 0
	for _, c := range result.Content[fromIndex:] {
		if tc, ok := c.(*mcp.TextContent); ok {
			total += len(tc.Text)
		}
	}
	return total
}

// isExportTool reports whether toolName is a stream-to-asset export tool
// (trino_export, api_export, ...). Export tools return asset metadata rather
// than query rows and must bypass semantic enrichment so their response
// contract survives (#822).
func isExportTool(toolName string) bool {
	return strings.HasSuffix(toolName, "_export")
}

// inferToolkitKind determines the toolkit kind from a tool name prefix.
func inferToolkitKind(toolName string) string {
	switch {
	case strings.HasPrefix(toolName, "trino_"):
		return toolPrefixTrino
	case strings.HasPrefix(toolName, "datahub_"):
		return toolPrefixDatahub
	case strings.HasPrefix(toolName, "s3_"):
		return toolPrefixS3
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
