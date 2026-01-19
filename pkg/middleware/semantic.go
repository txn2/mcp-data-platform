package middleware

import (
	"context"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// EnrichmentConfig configures semantic enrichment.
type EnrichmentConfig struct {
	// EnrichTrinoResults adds semantic context to Trino tool results.
	EnrichTrinoResults bool

	// EnrichDataHubResults adds query context to DataHub tool results.
	EnrichDataHubResults bool
}

// semanticEnricher holds the enrichment dependencies.
type semanticEnricher struct {
	semanticProvider semantic.Provider
	queryProvider    query.Provider
	cfg              EnrichmentConfig
}

func (e *semanticEnricher) enrich(
	ctx context.Context,
	result *mcp.CallToolResult,
	request mcp.CallToolRequest,
	pc *PlatformContext,
) (*mcp.CallToolResult, error) {
	switch pc.ToolkitKind {
	case "trino":
		if e.cfg.EnrichTrinoResults && e.semanticProvider != nil {
			return enrichTrinoResult(ctx, result, request, e.semanticProvider)
		}
	case "datahub":
		if e.cfg.EnrichDataHubResults && e.queryProvider != nil {
			return enrichDataHubResult(ctx, result, request, e.queryProvider)
		}
	}
	return result, nil
}

// SemanticEnrichmentMiddleware creates middleware that enriches results with semantic context.
func SemanticEnrichmentMiddleware(
	semanticProvider semantic.Provider,
	queryProvider query.Provider,
	cfg EnrichmentConfig,
) Middleware {
	enricher := &semanticEnricher{
		semanticProvider: semanticProvider,
		queryProvider:    queryProvider,
		cfg:              cfg,
	}

	return func(next Handler) Handler {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			result, err := next(ctx, request)
			if err != nil || result == nil || result.IsError {
				return result, err
			}

			pc := GetPlatformContext(ctx)
			if pc == nil {
				return result, nil
			}

			return enricher.enrich(ctx, result, request, pc)
		}
	}
}

// enrichTrinoResult adds semantic context to Trino tool results.
func enrichTrinoResult(
	ctx context.Context,
	result *mcp.CallToolResult,
	request mcp.CallToolRequest,
	provider semantic.Provider,
) (*mcp.CallToolResult, error) {
	// Extract table identifier from request arguments
	tableName := extractTableFromRequest(request)
	if tableName == "" {
		return result, nil
	}

	// Parse table identifier
	table := parseTableIdentifier(tableName)

	// Get semantic context
	semanticCtx, err := provider.GetTableContext(ctx, table)
	if err != nil {
		// Don't fail the request if enrichment fails
		return result, nil
	}

	// Append semantic context to result
	return appendSemanticContext(result, semanticCtx)
}

// enrichDataHubResult adds query context to DataHub tool results.
func enrichDataHubResult(
	ctx context.Context,
	result *mcp.CallToolResult,
	_ mcp.CallToolRequest,
	provider query.Provider,
) (*mcp.CallToolResult, error) {
	// Extract URNs from result content
	urns := extractURNsFromResult(result)
	if len(urns) == 0 {
		return result, nil
	}

	// Get query context for each URN
	queryContexts := make(map[string]*query.TableAvailability)
	for _, urn := range urns {
		availability, err := provider.GetTableAvailability(ctx, urn)
		if err != nil {
			continue
		}
		queryContexts[urn] = availability
	}

	// Append query context to result
	return appendQueryContext(result, queryContexts)
}

// extractTableFromRequest extracts table name from request arguments.
func extractTableFromRequest(request mcp.CallToolRequest) string {
	args, ok := request.Params.Arguments.(map[string]any)
	if !ok {
		return ""
	}
	if table, ok := args["table"].(string); ok {
		return table
	}
	if table, ok := args["table_name"].(string); ok {
		return table
	}
	return ""
}

// parseTableIdentifier parses a dot-separated table name.
func parseTableIdentifier(name string) semantic.TableIdentifier {
	parts := splitTableName(name)
	switch len(parts) {
	case 3:
		return semantic.TableIdentifier{
			Catalog: parts[0],
			Schema:  parts[1],
			Table:   parts[2],
		}
	case 2:
		return semantic.TableIdentifier{
			Schema: parts[0],
			Table:  parts[1],
		}
	default:
		return semantic.TableIdentifier{
			Table: name,
		}
	}
}

// splitTableName splits a table name by dots.
func splitTableName(name string) []string {
	var parts []string
	var current string
	for _, c := range name {
		if c == '.' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

// extractURNsFromResult extracts URNs from result content.
func extractURNsFromResult(result *mcp.CallToolResult) []string {
	var urns []string
	for _, content := range result.Content {
		if textContent, ok := content.(mcp.TextContent); ok {
			// Try to parse as JSON and extract URNs
			var data map[string]any
			if err := json.Unmarshal([]byte(textContent.Text), &data); err == nil {
				urns = append(urns, extractURNsFromMap(data)...)
			}
		}
	}
	return urns
}

// extractURNsFromMap extracts URNs from a map recursively.
func extractURNsFromMap(data map[string]any) []string {
	var urns []string
	for k, v := range data {
		if k == "urn" || k == "URN" {
			if urn, ok := v.(string); ok {
				urns = append(urns, urn)
			}
		}
		if m, ok := v.(map[string]any); ok {
			urns = append(urns, extractURNsFromMap(m)...)
		}
		if arr, ok := v.([]any); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]any); ok {
					urns = append(urns, extractURNsFromMap(m)...)
				}
			}
		}
	}
	return urns
}

// appendSemanticContext appends semantic context to the result.
func appendSemanticContext(result *mcp.CallToolResult, ctx *semantic.TableContext) (*mcp.CallToolResult, error) {
	if ctx == nil {
		return result, nil
	}

	// Create enrichment content
	enrichment := map[string]any{
		"semantic_context": map[string]any{
			"description":   ctx.Description,
			"owners":        ctx.Owners,
			"tags":          ctx.Tags,
			"domain":        ctx.Domain,
			"quality_score": ctx.QualityScore,
			"deprecation":   ctx.Deprecation,
		},
	}

	enrichmentJSON, err := json.Marshal(enrichment)
	if err != nil {
		return result, nil
	}

	// Append to result
	result.Content = append(result.Content, mcp.TextContent{
		Type: "text",
		Text: string(enrichmentJSON),
	})

	return result, nil
}

// appendQueryContext appends query context to the result.
func appendQueryContext(result *mcp.CallToolResult, contexts map[string]*query.TableAvailability) (*mcp.CallToolResult, error) {
	if len(contexts) == 0 {
		return result, nil
	}

	// Create enrichment content
	enrichment := map[string]any{
		"query_context": contexts,
	}

	enrichmentJSON, err := json.Marshal(enrichment)
	if err != nil {
		return result, nil
	}

	// Append to result
	result.Content = append(result.Content, mcp.TextContent{
		Type: "text",
		Text: string(enrichmentJSON),
	})

	return result, nil
}
