package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/storage"
)

// tokenDivisor is the approximate characters-per-token ratio for estimation.
const tokenDivisor = 4

// criticalTagPrefixes lists tag substrings that indicate safety-relevant metadata.
var criticalTagPrefixes = []string{"pii", "sensitive", "quality", "restricted", "confidential"}

// toolPrefixTrino is the toolkit kind identifier for Trino tools.
const toolPrefixTrino = "trino"

// String constants for repeated map keys and comparisons.
const (
	keyTable           = "table"
	keyError           = "error"
	keyURN             = "urn"
	keyDescription     = "description"
	keyTags            = "tags"
	keyDeprecation     = "deprecation"
	keySemanticContext = "semantic_context"
	keySessionID       = "session_id"

	// noteNoColumnMetadata is the note appended when columns exist but none have
	// meaningful metadata (description, tags, glossary terms, PII flags, etc.).
	noteNoColumnMetadata = "No column-level metadata available"
)

// tablePartsWithCatalog is the expected number of parts in a fully-qualified table name (catalog.schema.table).
const tablePartsWithCatalog = 3

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

	// ResourceLinksEnabled adds resource links to DataHub search results,
	// pointing to schema:// and availability:// resource templates.
	ResourceLinksEnabled bool

	// SessionCache enables session-level metadata deduplication.
	// If nil, dedup is disabled and full enrichment is always sent.
	SessionCache *SessionEnrichmentCache

	// DedupMode controls what content is sent for previously-enriched tables.
	// Only used when SessionCache is non-nil.
	DedupMode DedupMode
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
	case toolPrefixTrino:
		if e.cfg.EnrichTrinoResults && e.semanticProvider != nil {
			return e.enrichTrinoResultWithDedup(ctx, result, request, pc)
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

// enrichTrinoResultWithDedup wraps Trino enrichment with session deduplication.
// If the session cache is nil or the tables haven't been seen, full enrichment is sent.
// Otherwise, the dedup mode determines what (if any) reduced content is sent.
func (e *semanticEnricher) enrichTrinoResultWithDedup(
	ctx context.Context,
	result *mcp.CallToolResult,
	request mcp.CallToolRequest,
	pc *PlatformContext,
) (*mcp.CallToolResult, error) {
	cache := e.cfg.SessionCache
	if cache == nil {
		slog.Debug("dedup: cache is nil, full enrichment")
		return enrichTrinoResult(ctx, result, request, e.semanticProvider)
	}

	// Identify tables from the request
	tableKeys := extractTableKeysFromRequest(request)
	if len(tableKeys) == 0 {
		slog.Debug("dedup: no table keys extracted, full enrichment",
			"tool", pc.ToolName,
			keySessionID, pc.SessionID,
			"has_params", request.Params != nil,
		)
		return enrichTrinoResult(ctx, result, request, e.semanticProvider)
	}

	slog.Debug("dedup: checking cache",
		keySessionID, pc.SessionID,
		"table_keys", tableKeys,
		"entry_ttl", e.cfg.SessionCache.EntryTTL(),
		"session_count", cache.SessionCount(),
	)

	if allSentRecently(cache, pc.SessionID, tableKeys) {
		return e.handleDedupEnrichment(ctx, result, request, pc, tableKeys)
	}

	return e.handleFullEnrichment(ctx, result, request, pc, tableKeys)
}

// allSentRecently returns true if every table key was recently enriched.
func allSentRecently(cache *SessionEnrichmentCache, sessionID string, tableKeys []string) bool {
	for _, key := range tableKeys {
		if !cache.WasSentRecently(sessionID, key) {
			return false
		}
	}
	return true
}

// handleFullEnrichment performs full enrichment, measures tokens, and marks tables as sent.
func (e *semanticEnricher) handleFullEnrichment(
	ctx context.Context,
	result *mcp.CallToolResult,
	request mcp.CallToolRequest,
	pc *PlatformContext,
	tableKeys []string,
) (*mcp.CallToolResult, error) {
	cache := e.cfg.SessionCache
	beforeChars := contentChars(result)

	enrichedResult, err := enrichTrinoResult(ctx, result, request, e.semanticProvider)
	if err != nil {
		return enrichedResult, err
	}

	afterChars := contentChars(enrichedResult)
	tokens := measureEnrichmentTokens(beforeChars, afterChars)

	// Mark all tables as sent with token count
	for _, key := range tableKeys {
		cache.MarkSent(pc.SessionID, key, tokens)
	}
	cache.AddTokensFull(int64(tokens))

	pc.EnrichmentTokensFull = tokens
	return enrichedResult, nil
}

// handleDedupEnrichment applies dedup mode and records token savings.
func (e *semanticEnricher) handleDedupEnrichment(
	ctx context.Context,
	result *mcp.CallToolResult,
	request mcp.CallToolRequest,
	pc *PlatformContext,
	tableKeys []string,
) (*mcp.CallToolResult, error) {
	cache := e.cfg.SessionCache
	slog.Debug("dedup: all tables cached, applying dedup mode",
		keySessionID, pc.SessionID,
		"mode", e.cfg.DedupMode,
		"table_keys", tableKeys,
	)

	storedTokens := sumStoredTokenCounts(cache, pc.SessionID, tableKeys)
	beforeChars := contentChars(result)

	dedupResult, err := e.applyDedupMode(ctx, result, request, tableKeys)
	if err != nil {
		return dedupResult, err
	}

	afterChars := contentChars(dedupResult)
	dedupTokens := measureEnrichmentTokens(beforeChars, afterChars)
	cache.AddTokensDeduped(int64(dedupTokens))

	pc.EnrichmentTokensFull = storedTokens
	pc.EnrichmentTokensDedup = dedupTokens
	return dedupResult, nil
}

// extractTableKeysFromRequest extracts table keys from a tool call request.
func extractTableKeysFromRequest(request mcp.CallToolRequest) []string {
	// Check for SQL query first (multi-table support)
	if sql := extractSQLFromRequest(request); sql != "" {
		tables := ExtractTablesFromSQL(sql)
		if len(tables) > 0 {
			keys := make([]string, len(tables))
			for i, t := range tables {
				keys[i] = t.FullPath
			}
			return keys
		}
	}

	// Fall back to explicit table parameter
	tableName := extractTableFromRequest(request)
	if tableName == "" {
		return nil
	}
	return []string{tableName}
}

// applyDedupMode sends reduced enrichment based on the configured dedup mode.
func (e *semanticEnricher) applyDedupMode(
	ctx context.Context,
	result *mcp.CallToolResult,
	_ mcp.CallToolRequest,
	tableKeys []string,
) (*mcp.CallToolResult, error) {
	switch e.cfg.DedupMode {
	case DedupModeNone:
		return result, nil

	case DedupModeSummary:
		return e.appendSemanticSummary(ctx, result, tableKeys)

	case DedupModeReference:
		return appendMetadataReference(result, tableKeys), nil

	default:
		// Unknown mode, treat as reference
		return appendMetadataReference(result, tableKeys), nil
	}
}

// appendMetadataReference appends a minimal reference noting that full metadata
// was already provided earlier in the session.
func appendMetadataReference(result *mcp.CallToolResult, tableKeys []string) *mcp.CallToolResult {
	ref := map[string]any{
		"metadata_reference": map[string]any{
			"tables": tableKeys,
			"note":   "Full semantic metadata was provided earlier in this session. Refer to previous responses for column descriptions, tags, owners, and glossary terms.",
		},
	}

	refJSON, err := json.Marshal(ref)
	if err != nil {
		return result
	}

	result.Content = append(result.Content, &mcp.TextContent{
		Text: string(refJSON),
	})

	return result
}

// appendSemanticSummary appends compact semantic context with only critical fields.
func (e *semanticEnricher) appendSemanticSummary(
	ctx context.Context,
	result *mcp.CallToolResult,
	tableKeys []string,
) (*mcp.CallToolResult, error) {
	for _, key := range tableKeys {
		table := parseTableIdentifier(key)
		tableCtx, err := e.semanticProvider.GetTableContext(ctx, table)
		if err != nil {
			continue
		}

		enrichment := map[string]any{
			"compact_context": buildCompactSemanticContext(tableCtx),
			"tables":          []string{key},
			"note":            "Compact view. Full metadata was provided earlier in this session. Only critical warnings, quality scores, and sensitivity flags are shown.",
		}

		enrichmentJSON, marshalErr := json.Marshal(enrichment)
		if marshalErr != nil {
			continue
		}

		result.Content = append(result.Content, &mcp.TextContent{
			Text: string(enrichmentJSON),
		})
	}

	return result, nil
}

// enrichDataHubResultWithAll enriches DataHub results with query and storage context.
func (e *semanticEnricher) enrichDataHubResultWithAll(
	ctx context.Context,
	result *mcp.CallToolResult,
	request mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	enrichedResult := result

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

	// Add resource links to DataHub search results
	if e.cfg.ResourceLinksEnabled {
		enrichedResult = appendResourceLinks(enrichedResult, extractURNsFromResult(enrichedResult))
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
	// Check for SQL query first (multi-table support)
	if sql := extractSQLFromRequest(request); sql != "" {
		tables := ExtractTablesFromSQL(sql)
		if len(tables) > 0 {
			slog.Debug("extracted tables from SQL for enrichment",
				"sql_length", len(sql),
				"table_count", len(tables),
				"tables", formatTableRefs(tables),
			)
			return enrichTrinoQueryResult(ctx, result, tables, provider)
		}
	}

	// Fall back to explicit table parameters (single table tools like trino_describe_table)
	tableName := extractTableFromRequest(request)
	if tableName == "" {
		return result, nil
	}

	// Parse table identifier
	table := parseTableIdentifier(tableName)

	// Get semantic context for the table
	semanticCtx, err := provider.GetTableContext(ctx, table)
	if err != nil {
		slog.Debug("semantic enrichment failed for Trino result",
			keyTable, tableName,
			"parsed_catalog", table.Catalog,
			"parsed_schema", table.Schema,
			"parsed_table", table.Table,
			keyError, err,
		)
		return result, nil
	}

	// Get column-level semantic context
	columnsCtx, columnsErr := provider.GetColumnsContext(ctx, table)
	if columnsErr != nil {
		slog.Debug("column semantic enrichment failed for Trino result",
			keyTable, tableName,
			keyError, columnsErr,
		)
	}

	return appendSemanticContextWithColumns(result, semanticCtx, columnsCtx)
}

// extractSQLFromRequest extracts SQL from request arguments.
func extractSQLFromRequest(request mcp.CallToolRequest) string {
	if request.Params == nil || len(request.Params.Arguments) == 0 {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal(request.Params.Arguments, &args); err != nil {
		return ""
	}
	if sql, ok := args["sql"].(string); ok {
		return sql
	}
	return ""
}

// formatTableRefs formats table refs for logging.
func formatTableRefs(refs []TableRef) []string {
	result := make([]string, len(refs))
	for i, r := range refs {
		result[i] = r.FullPath
	}
	return result
}

// enrichTrinoQueryResult enriches results for SQL queries with multiple tables.
func enrichTrinoQueryResult(
	ctx context.Context,
	result *mcp.CallToolResult,
	tables []TableRef,
	provider semantic.Provider,
) (*mcp.CallToolResult, error) {
	if len(tables) == 0 {
		return result, nil
	}

	// Primary table gets full context
	primary := tables[0]
	tableID := refToTableIdentifier(primary)

	tableCtx, err := provider.GetTableContext(ctx, tableID)
	if err != nil {
		slog.Debug("semantic enrichment failed for primary table",
			keyTable, primary.FullPath,
			keyError, err,
		)
		return result, nil
	}

	columnsCtx, columnsErr := provider.GetColumnsContext(ctx, tableID)
	if columnsErr != nil {
		slog.Debug("column enrichment failed for primary table",
			keyTable, primary.FullPath,
			keyError, columnsErr,
		)
	}

	// Additional tables get summary context
	additionalTables := make([]map[string]any, 0, len(tables)-1)
	for _, t := range tables[1:] {
		tID := refToTableIdentifier(t)
		tCtx, tErr := provider.GetTableContext(ctx, tID)
		if tErr != nil {
			slog.Debug("semantic enrichment failed for additional table",
				keyTable, t.FullPath,
				keyError, tErr,
			)
			continue
		}
		additionalTables = append(additionalTables, buildAdditionalTableContext(t, tCtx))
	}

	return appendSemanticContextWithAdditional(result, tableCtx, columnsCtx, additionalTables)
}

// refToTableIdentifier converts TableRef to semantic.TableIdentifier.
func refToTableIdentifier(ref TableRef) semantic.TableIdentifier {
	return semantic.TableIdentifier{
		Catalog: ref.Catalog,
		Schema:  ref.Schema,
		Table:   ref.Table,
	}
}

// buildAdditionalTableContext creates summary context for additional tables.
func buildAdditionalTableContext(ref TableRef, ctx *semantic.TableContext) map[string]any {
	summary := map[string]any{
		keyTable:       ref.FullPath,
		keyDescription: ctx.Description,
	}
	if ctx.URN != "" {
		summary[keyURN] = ctx.URN
	}
	if ctx.Deprecation != nil && ctx.Deprecation.Deprecated {
		summary[keyDeprecation] = ctx.Deprecation
	}
	if len(ctx.Tags) > 0 {
		summary[keyTags] = ctx.Tags
	}
	if len(ctx.Owners) > 0 {
		summary["owners"] = ctx.Owners
	}
	return summary
}

// appendSemanticContextWithAdditional appends context with additional tables to result.
func appendSemanticContextWithAdditional(
	result *mcp.CallToolResult,
	ctx *semantic.TableContext,
	columnsCtx map[string]*semantic.ColumnContext,
	additionalTables []map[string]any,
) (*mcp.CallToolResult, error) {
	if ctx == nil {
		return result, nil
	}

	enrichment := map[string]any{
		keySemanticContext: buildTrinoSemanticContext(ctx),
	}

	if len(columnsCtx) > 0 {
		columnContext, sources := buildColumnContexts(columnsCtx)
		if len(columnContext) > 0 {
			enrichment["column_context"] = columnContext
		} else {
			enrichment["column_context_note"] = noteNoColumnMetadata
		}
		if len(sources) > 0 {
			enrichment["inheritance_sources"] = sources
		}
	}

	if len(additionalTables) > 0 {
		enrichment["additional_tables"] = additionalTables
	}

	enrichmentJSON, err := json.Marshal(enrichment)
	if err != nil {
		return result, fmt.Errorf("marshal semantic context with additional tables: %w", err)
	}

	result.Content = append(result.Content, &mcp.TextContent{
		Text: string(enrichmentJSON),
	})

	return result, nil
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
		found := slices.Contains(urns, reqURN)
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
// Also handles SQL queries via the "sql" parameter.
func extractTableFromRequest(request mcp.CallToolRequest) string {
	args := parseRequestArgs(request)
	if args == nil {
		return ""
	}

	// Try explicit table parameters first
	if table := extractExplicitTable(args); table != "" {
		return table
	}

	// Handle SQL query parameter (for trino_query tool)
	return extractTableFromSQL(args)
}

// parseRequestArgs parses the request arguments into a map.
func parseRequestArgs(request mcp.CallToolRequest) map[string]any {
	if len(request.Params.Arguments) == 0 {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal(request.Params.Arguments, &args); err != nil {
		return nil
	}
	return args
}

// extractExplicitTable extracts table from explicit catalog/schema/table params.
func extractExplicitTable(args map[string]any) string {
	catalog, _ := args["catalog"].(string)
	schema, _ := args["schema"].(string)
	table, _ := args[keyTable].(string)

	// If we have separate parameters, combine them
	if table != "" && (catalog != "" || schema != "") {
		return combineTableParts(catalog, schema, table)
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

// combineTableParts joins non-empty catalog, schema, and table parts.
func combineTableParts(catalog, schema, table string) string {
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

// extractTableFromSQL extracts the first table from a SQL query.
func extractTableFromSQL(args map[string]any) string {
	sql, ok := args["sql"].(string)
	if !ok || sql == "" {
		return ""
	}
	tables := ExtractTablesFromSQL(sql)
	if len(tables) > 0 {
		return tables[0].FullPath
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
	if urn, ok := args[keyURN].(string); ok {
		return urn
	}
	return ""
}

// parseTableIdentifier parses a dot-separated table name.
func parseTableIdentifier(name string) semantic.TableIdentifier {
	parts := splitTableName(name)
	switch len(parts) {
	case tablePartsWithCatalog:
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

// splitTableName splits a table name by dots, filtering empty parts.
func splitTableName(name string) []string {
	parts := strings.Split(name, ".")
	// Filter empty parts (handles leading/trailing/consecutive dots)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			result = append(result, p)
		}
	}
	return result
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
		if k == keyURN || k == "URN" {
			if urn, ok := v.(string); ok {
				urns = append(urns, urn)
			}
		}
		urns = append(urns, extractURNsFromValue(v)...)
	}
	return urns
}

// extractURNsFromValue extracts URNs from a nested value (map or slice).
func extractURNsFromValue(v any) []string {
	if m, ok := v.(map[string]any); ok {
		return extractURNsFromMap(m)
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var urns []string
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			urns = append(urns, extractURNsFromMap(m)...)
		}
	}
	return urns
}

// buildTrinoSemanticContext creates the semantic context map from table context for Trino enrichment.
func buildTrinoSemanticContext(ctx *semantic.TableContext) map[string]any {
	semanticCtx := map[string]any{
		keyDescription:  ctx.Description,
		"owners":        ctx.Owners,
		keyTags:         ctx.Tags,
		"domain":        ctx.Domain,
		"quality_score": ctx.QualityScore,
		keyDeprecation:  ctx.Deprecation,
	}

	if ctx.URN != "" {
		semanticCtx[keyURN] = ctx.URN
	}
	if len(ctx.GlossaryTerms) > 0 {
		semanticCtx["glossary_terms"] = ctx.GlossaryTerms
	}
	if len(ctx.CustomProperties) > 0 {
		semanticCtx["custom_properties"] = ctx.CustomProperties
	}
	if ctx.LastModified != nil {
		semanticCtx["last_modified"] = ctx.LastModified
	}

	return semanticCtx
}

// buildCompactSemanticContext extracts only critical safety-relevant fields
// from a table context for deduped responses.
func buildCompactSemanticContext(ctx *semantic.TableContext) map[string]any {
	compact := make(map[string]any)
	if ctx.URN != "" {
		compact[keyURN] = ctx.URN
	}
	if ctx.Domain != nil {
		compact["domain"] = ctx.Domain
	}
	if ctx.Deprecation != nil && ctx.Deprecation.Deprecated {
		compact[keyDeprecation] = ctx.Deprecation
	}
	if ctx.QualityScore != nil {
		compact["quality_score"] = *ctx.QualityScore
	}
	if critical := filterCriticalTags(ctx.Tags); len(critical) > 0 {
		compact["critical_tags"] = critical
	}
	return compact
}

// filterCriticalTags returns tags that match known critical prefixes.
func filterCriticalTags(tags []string) []string {
	var critical []string
	for _, tag := range tags {
		lower := strings.ToLower(tag)
		for _, prefix := range criticalTagPrefixes {
			if strings.Contains(lower, prefix) {
				critical = append(critical, tag)
				break
			}
		}
	}
	return critical
}

// estimateTokens estimates the token count from raw byte data.
func estimateTokens(data []byte) int {
	return len(data) / tokenDivisor
}

// contentChars returns the total character count of text content in a result.
func contentChars(result *mcp.CallToolResult) int {
	total := 0
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			total += len(tc.Text)
		}
	}
	return total
}

// measureEnrichmentTokens estimates tokens added by enrichment.
func measureEnrichmentTokens(beforeChars, afterChars int) int {
	added := afterChars - beforeChars
	if added <= 0 {
		return 0
	}
	return added / tokenDivisor
}

// sumStoredTokenCounts sums the stored full enrichment token counts for multiple tables.
func sumStoredTokenCounts(cache *SessionEnrichmentCache, sessionID string, tableKeys []string) int {
	total := 0
	for _, key := range tableKeys {
		total += cache.GetTokenCount(sessionID, key)
	}
	return total
}

// buildColumnInfo creates a column info map from column context.
func buildColumnInfo(col *semantic.ColumnContext) map[string]any {
	colInfo := map[string]any{
		keyDescription:   col.Description,
		"glossary_terms": col.GlossaryTerms,
		keyTags:          col.Tags,
		"is_pii":         col.IsPII,
		"is_sensitive":   col.IsSensitive,
	}

	if col.InheritedFrom != nil {
		colInfo["inherited_from"] = map[string]any{
			"source_dataset": col.InheritedFrom.SourceURN,
			"source_column":  col.InheritedFrom.SourceColumn,
			"hops":           col.InheritedFrom.Hops,
			"match_method":   col.InheritedFrom.MatchMethod,
		}
	}

	return colInfo
}

// buildColumnContexts creates column context and collects inheritance sources.
func buildColumnContexts(columnsCtx map[string]*semantic.ColumnContext) (columnContext map[string]any, sources []string) {
	columnContext = make(map[string]any)
	inheritanceSources := make(map[string]bool)

	for name, col := range columnsCtx {
		if !col.HasContent() {
			continue // Skip columns with no meaningful metadata
		}
		columnContext[name] = buildColumnInfo(col)
		if col.InheritedFrom != nil {
			inheritanceSources[col.InheritedFrom.SourceURN] = true
		}
	}

	sources = make([]string, 0, len(inheritanceSources))
	for src := range inheritanceSources {
		sources = append(sources, src)
	}

	return columnContext, sources
}

// appendSemanticContextWithColumns appends semantic context including column metadata to the result.
func appendSemanticContextWithColumns(
	result *mcp.CallToolResult,
	ctx *semantic.TableContext,
	columnsCtx map[string]*semantic.ColumnContext,
) (*mcp.CallToolResult, error) {
	if ctx == nil {
		return result, nil
	}

	enrichment := map[string]any{
		keySemanticContext: buildTrinoSemanticContext(ctx),
	}

	if len(columnsCtx) > 0 {
		columnContext, sources := buildColumnContexts(columnsCtx)
		if len(columnContext) > 0 {
			enrichment["column_context"] = columnContext
		} else {
			enrichment["column_context_note"] = noteNoColumnMetadata
		}
		if len(sources) > 0 {
			enrichment["inheritance_sources"] = sources
		}
	}

	enrichmentJSON, err := json.Marshal(enrichment)
	if err != nil {
		return result, fmt.Errorf("marshal semantic context with columns: %w", err)
	}

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
		return result, fmt.Errorf("marshal query context: %w", err)
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
		keyURN:         sr.URN,
		"name":         sr.Name,
		keyDescription: tableCtx.Description,
	}
	if len(tableCtx.Owners) > 0 {
		semanticCtx["owners"] = tableCtx.Owners
	}
	if len(tableCtx.Tags) > 0 {
		semanticCtx[keyTags] = tableCtx.Tags
	}
	if tableCtx.Domain != nil {
		semanticCtx["domain"] = tableCtx.Domain.Name
	}
	if tableCtx.Deprecation != nil && tableCtx.Deprecation.Deprecated {
		semanticCtx[keyDeprecation] = tableCtx.Deprecation
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
		keySemanticContext: map[string]any{
			"matching_datasets": contexts,
			"note":              "Semantic metadata from DataHub for S3 location",
		},
	}

	enrichmentJSON, err := json.Marshal(enrichment)
	if err != nil {
		return result, fmt.Errorf("marshal S3 semantic context: %w", err)
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
		if k == keyURN || k == "URN" {
			urns = appendIfS3URN(urns, v)
		}
		switch val := v.(type) {
		case map[string]any:
			urns = append(urns, extractS3URNsFromMap(val)...)
		case []any:
			urns = extractS3URNsFromSlice(urns, val)
		}
	}
	return urns
}

// appendIfS3URN appends the value to urns if it is an S3 dataset URN string.
func appendIfS3URN(urns []string, v any) []string {
	urn, ok := v.(string)
	if ok && strings.Contains(urn, "dataPlatform:s3") {
		return append(urns, urn)
	}
	return urns
}

// extractS3URNsFromSlice extracts S3 URNs from slice items that are maps.
func extractS3URNsFromSlice(urns []string, items []any) []string {
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			urns = append(urns, extractS3URNsFromMap(m)...)
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
		return result, fmt.Errorf("marshal storage context: %w", err)
	}

	// Append to result
	result.Content = append(result.Content, &mcp.TextContent{
		Text: string(enrichmentJSON),
	})

	return result, nil
}

// appendResourceLinks adds schema:// and availability:// resource links to a
// tool result for each DataHub URN found. This lets agents follow up with
// resource template reads for detailed schema or availability info.
func appendResourceLinks(result *mcp.CallToolResult, urns []string) *mcp.CallToolResult {
	if len(urns) == 0 {
		return result
	}

	for _, urn := range urns {
		catalog, schema, table := parseDataHubURNComponents(urn)
		if table == "" {
			continue
		}

		schemaURI := fmt.Sprintf("schema://%s.%s/%s", catalog, schema, table)
		result.Content = append(result.Content, &mcp.ResourceLink{
			URI:         schemaURI,
			Name:        fmt.Sprintf("Schema: %s.%s.%s", catalog, schema, table),
			Description: "Table schema with semantic context",
			MIMEType:    "application/json",
		})

		availURI := fmt.Sprintf("availability://%s.%s/%s", catalog, schema, table)
		result.Content = append(result.Content, &mcp.ResourceLink{
			URI:         availURI,
			Name:        fmt.Sprintf("Availability: %s.%s.%s", catalog, schema, table),
			Description: "Data availability status and row count",
			MIMEType:    "application/json",
		})
	}

	return result
}

// parseDataHubURNComponents extracts catalog, schema, and table from a DataHub
// dataset URN. The expected format is:
//
//	urn:li:dataset:(urn:li:dataPlatform:<platform>,<catalog>.<schema>.<table>,PROD)
//
// Returns empty strings if the URN doesn't match the expected format.
func parseDataHubURNComponents(urn string) (catalog, schema, table string) {
	const prefix = "urn:li:dataset:(urn:li:dataPlatform:"
	if !strings.HasPrefix(urn, prefix) {
		return "", "", ""
	}

	// Strip prefix to get: "trino,catalog.schema.table,PROD)"
	rest := urn[len(prefix):]

	// Find first comma after platform name
	firstComma := strings.Index(rest, ",")
	if firstComma < 0 {
		return "", "", ""
	}

	// Get the qualified name portion: "catalog.schema.table,PROD)"
	rest = rest[firstComma+1:]

	// Find the next comma (before ",PROD)")
	qualifiedName, _, found := strings.Cut(rest, ",")
	if !found {
		return "", "", ""
	}

	// Split by dots: catalog.schema.table
	parts := strings.SplitN(qualifiedName, ".", 3) //nolint:mnd,revive // 3 parts: catalog.schema.table
	if len(parts) != 3 {                           //nolint:mnd,revive // 3 parts: catalog.schema.table
		return "", "", ""
	}

	return parts[0], parts[1], parts[2]
}
