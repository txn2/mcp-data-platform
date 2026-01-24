package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/storage"
)

// EnrichmentConfig configures semantic enrichment.
type EnrichmentConfig struct {
	// EnrichTrinoResults adds semantic context to Trino tool results.
	EnrichTrinoResults bool

	// EnrichDataHubResults adds query context to DataHub tool results.
	EnrichDataHubResults bool

	// EnrichS3Results adds semantic context to S3 tool results.
	EnrichS3Results bool

	// EnrichDataHubStorageResults adds storage context to DataHub tool results.
	EnrichDataHubStorageResults bool
}

// semanticEnricher holds the enrichment dependencies.
type semanticEnricher struct {
	semanticProvider semantic.Provider
	queryProvider    query.Provider
	storageProvider  storage.Provider
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
		return e.enrichDataHubResultWithAll(ctx, result, request)
	case "s3":
		if e.cfg.EnrichS3Results && e.semanticProvider != nil {
			return enrichS3Result(ctx, result, request, e.semanticProvider)
		}
	}
	return result, nil
}

// enrichDataHubResultWithAll enriches DataHub results with query and storage context.
func (e *semanticEnricher) enrichDataHubResultWithAll(
	ctx context.Context,
	result *mcp.CallToolResult,
	request mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	var enrichedResult = result

	// Enrich with query context (Trino)
	if e.cfg.EnrichDataHubResults && e.queryProvider != nil {
		var err error
		enrichedResult, err = enrichDataHubResult(ctx, enrichedResult, request, e.queryProvider)
		if err != nil {
			return enrichedResult, err
		}
	}

	// Enrich with storage context (S3)
	if e.cfg.EnrichDataHubStorageResults && e.storageProvider != nil {
		var err error
		enrichedResult, err = enrichDataHubStorageResult(ctx, enrichedResult, e.storageProvider)
		if err != nil {
			return enrichedResult, err
		}
	}

	return enrichedResult, nil
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
		// Log the failure for debugging - this often indicates URN mapping issues
		slog.Debug("semantic enrichment failed for Trino result",
			"table", tableName,
			"parsed_catalog", table.Catalog,
			"parsed_schema", table.Schema,
			"parsed_table", table.Table,
			"error", err,
		)
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
	request mcp.CallToolRequest,
	provider query.Provider,
) (*mcp.CallToolResult, error) {
	// Extract URNs from result content
	urns := extractURNsFromResult(result)

	// Also extract URN from request (for tools like datahub_get_schema that take urn param)
	if reqURN := extractURNFromRequest(request); reqURN != "" {
		found := false
		for _, u := range urns {
			if u == reqURN {
				found = true
				break
			}
		}
		if !found {
			urns = append(urns, reqURN)
		}
	}

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
// Handles both combined format (table="catalog.schema.table") and
// separate parameters (catalog="x", schema="y", table="z").
func extractTableFromRequest(request mcp.CallToolRequest) string {
	if len(request.Params.Arguments) == 0 {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal(request.Params.Arguments, &args); err != nil {
		return ""
	}

	// Check for separate catalog/schema/table parameters first
	catalog, _ := args["catalog"].(string)
	schema, _ := args["schema"].(string)
	table, _ := args["table"].(string)

	// If we have separate parameters, combine them
	if table != "" && (catalog != "" || schema != "") {
		var parts []string
		if catalog != "" {
			parts = append(parts, catalog)
		}
		if schema != "" {
			parts = append(parts, schema)
		}
		parts = append(parts, table)
		return strings.Join(parts, ".")
	}

	// Fall back to combined table name
	if table != "" {
		return table
	}
	if tableName, ok := args["table_name"].(string); ok {
		return tableName
	}
	return ""
}

// extractURNFromRequest extracts URN from request arguments.
func extractURNFromRequest(request mcp.CallToolRequest) string {
	if request.Params == nil || len(request.Params.Arguments) == 0 {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal(request.Params.Arguments, &args); err != nil {
		return ""
	}
	if urn, ok := args["urn"].(string); ok {
		return urn
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
		if textContent, ok := content.(*mcp.TextContent); ok {
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

	// Create enrichment content with all available semantic metadata
	semanticCtx := map[string]any{
		"description":   ctx.Description,
		"owners":        ctx.Owners,
		"tags":          ctx.Tags,
		"domain":        ctx.Domain,
		"quality_score": ctx.QualityScore,
		"deprecation":   ctx.Deprecation,
	}

	// Add URN if available (useful for cross-referencing)
	if ctx.URN != "" {
		semanticCtx["urn"] = ctx.URN
	}

	// Add glossary terms if available
	if len(ctx.GlossaryTerms) > 0 {
		semanticCtx["glossary_terms"] = ctx.GlossaryTerms
	}

	// Add custom properties if available
	if len(ctx.CustomProperties) > 0 {
		semanticCtx["custom_properties"] = ctx.CustomProperties
	}

	// Add last modified timestamp if available
	if ctx.LastModified != nil {
		semanticCtx["last_modified"] = ctx.LastModified
	}

	enrichment := map[string]any{
		"semantic_context": semanticCtx,
	}

	enrichmentJSON, err := json.Marshal(enrichment)
	if err != nil {
		return result, nil
	}

	// Append to result
	result.Content = append(result.Content, &mcp.TextContent{
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
	result.Content = append(result.Content, &mcp.TextContent{
		Text: string(enrichmentJSON),
	})

	return result, nil
}

// enrichS3Result adds semantic context to S3 tool results.
func enrichS3Result(
	ctx context.Context,
	result *mcp.CallToolResult,
	request mcp.CallToolRequest,
	provider semantic.Provider,
) (*mcp.CallToolResult, error) {
	bucket, prefix := extractS3PathFromRequest(request)
	if bucket == "" {
		return result, nil
	}

	searchResults := searchS3Datasets(ctx, provider, bucket, prefix)
	if len(searchResults) == 0 {
		return result, nil
	}

	semanticContexts := buildS3SemanticContexts(ctx, provider, bucket, searchResults)
	if len(semanticContexts) == 0 {
		return result, nil
	}

	return appendS3SemanticContext(result, semanticContexts)
}

// searchS3Datasets searches for datasets matching an S3 location.
func searchS3Datasets(
	ctx context.Context,
	provider semantic.Provider,
	bucket, prefix string,
) []semantic.TableSearchResult {
	searchQuery := bucket
	if prefix != "" {
		searchQuery = bucket + "/" + prefix
	}

	searchResults, err := provider.SearchTables(ctx, semantic.SearchFilter{
		Query:    searchQuery,
		Platform: "s3",
		Limit:    5,
	})
	if err != nil {
		return nil
	}
	return searchResults
}

// buildS3SemanticContexts builds semantic context maps for S3 search results.
func buildS3SemanticContexts(
	ctx context.Context,
	provider semantic.Provider,
	bucket string,
	searchResults []semantic.TableSearchResult,
) []map[string]any {
	semanticContexts := make([]map[string]any, 0, len(searchResults))
	for _, sr := range searchResults {
		tableCtx, err := provider.GetTableContext(ctx, semantic.TableIdentifier{
			Catalog: "s3",
			Schema:  bucket,
			Table:   sr.Name,
		})
		if err != nil || tableCtx == nil {
			continue
		}

		semanticCtx := buildTableSemanticContext(sr, tableCtx)
		semanticContexts = append(semanticContexts, semanticCtx)
	}
	return semanticContexts
}

// buildTableSemanticContext builds a semantic context map from search result and table context.
func buildTableSemanticContext(sr semantic.TableSearchResult, tableCtx *semantic.TableContext) map[string]any {
	semanticCtx := map[string]any{
		"urn":         sr.URN,
		"name":        sr.Name,
		"description": tableCtx.Description,
	}
	if len(tableCtx.Owners) > 0 {
		semanticCtx["owners"] = tableCtx.Owners
	}
	if len(tableCtx.Tags) > 0 {
		semanticCtx["tags"] = tableCtx.Tags
	}
	if tableCtx.Domain != nil {
		semanticCtx["domain"] = tableCtx.Domain.Name
	}
	if tableCtx.Deprecation != nil && tableCtx.Deprecation.Deprecated {
		semanticCtx["deprecation"] = tableCtx.Deprecation
	}
	if tableCtx.QualityScore != nil {
		semanticCtx["quality_score"] = *tableCtx.QualityScore
	}
	return semanticCtx
}

// extractS3PathFromRequest extracts bucket and prefix from S3 request arguments.
func extractS3PathFromRequest(request mcp.CallToolRequest) (bucket, prefix string) {
	if len(request.Params.Arguments) == 0 {
		return "", ""
	}
	var args map[string]any
	if err := json.Unmarshal(request.Params.Arguments, &args); err != nil {
		return "", ""
	}

	if b, ok := args["bucket"].(string); ok {
		bucket = b
	}
	if p, ok := args["prefix"].(string); ok {
		prefix = p
	}
	if p, ok := args["key"].(string); ok && prefix == "" {
		// For get_object, use key's directory as prefix
		if idx := strings.LastIndex(p, "/"); idx > 0 {
			prefix = p[:idx]
		}
	}
	return bucket, prefix
}

// appendS3SemanticContext appends S3 semantic context to the result.
func appendS3SemanticContext(result *mcp.CallToolResult, contexts []map[string]any) (*mcp.CallToolResult, error) {
	if len(contexts) == 0 {
		return result, nil
	}

	// Create enrichment content
	enrichment := map[string]any{
		"semantic_context": map[string]any{
			"matching_datasets": contexts,
			"note":              "Semantic metadata from DataHub for S3 location",
		},
	}

	enrichmentJSON, err := json.Marshal(enrichment)
	if err != nil {
		return result, nil
	}

	// Append to result
	result.Content = append(result.Content, &mcp.TextContent{
		Text: string(enrichmentJSON),
	})

	return result, nil
}

// enrichDataHubStorageResult adds storage context to DataHub tool results.
func enrichDataHubStorageResult(
	ctx context.Context,
	result *mcp.CallToolResult,
	provider storage.Provider,
) (*mcp.CallToolResult, error) {
	// Extract URNs from result content and filter for S3 datasets
	urns := extractS3URNsFromResult(result)
	if len(urns) == 0 {
		return result, nil
	}

	// Get storage context for each URN
	storageContexts := make(map[string]*storage.DatasetAvailability)
	for _, urn := range urns {
		availability, err := provider.GetDatasetAvailability(ctx, urn)
		if err != nil {
			continue
		}
		storageContexts[urn] = availability
	}

	// Append storage context to result
	return appendStorageContext(result, storageContexts)
}

// extractS3URNsFromResult extracts S3 dataset URNs from result content.
func extractS3URNsFromResult(result *mcp.CallToolResult) []string {
	var urns []string
	for _, content := range result.Content {
		if textContent, ok := content.(*mcp.TextContent); ok {
			var data map[string]any
			if err := json.Unmarshal([]byte(textContent.Text), &data); err == nil {
				urns = append(urns, extractS3URNsFromMap(data)...)
			}
		}
	}
	return urns
}

// extractS3URNsFromMap extracts S3 URNs from a map recursively.
func extractS3URNsFromMap(data map[string]any) []string {
	var urns []string
	for k, v := range data {
		if k == "urn" || k == "URN" {
			if urn, ok := v.(string); ok {
				// Check if this is an S3 dataset URN
				if strings.Contains(urn, "dataPlatform:s3") {
					urns = append(urns, urn)
				}
			}
		}
		if m, ok := v.(map[string]any); ok {
			urns = append(urns, extractS3URNsFromMap(m)...)
		}
		if arr, ok := v.([]any); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]any); ok {
					urns = append(urns, extractS3URNsFromMap(m)...)
				}
			}
		}
	}
	return urns
}

// appendStorageContext appends storage context to the result.
func appendStorageContext(result *mcp.CallToolResult, contexts map[string]*storage.DatasetAvailability) (*mcp.CallToolResult, error) {
	if len(contexts) == 0 {
		return result, nil
	}

	// Create enrichment content
	enrichment := map[string]any{
		"storage_context": contexts,
	}

	enrichmentJSON, err := json.Marshal(enrichment)
	if err != nil {
		return result, nil
	}

	// Append to result
	result.Content = append(result.Content, &mcp.TextContent{
		Text: string(enrichmentJSON),
	})

	return result, nil
}
