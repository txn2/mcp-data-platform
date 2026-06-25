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
var criticalTagPrefixes = []string{sensitivityPII, "sensitive", "quality", "restricted", sensitivityConfidential}

// toolPrefixTrino is the toolkit kind identifier for Trino tools.
const toolPrefixTrino = "trino"

// toolPrefixDatahub is the toolkit kind identifier for DataHub tools.
const toolPrefixDatahub = "datahub"

// toolPrefixS3 is the toolkit kind identifier for S3 tools.
const toolPrefixS3 = "s3"

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

	// fieldName is the JSON/map key for entity names.
	fieldName = "name"
	// fieldNote is the JSON/map key for note text appended to enrichment payloads.
	fieldNote = "note"
	// fieldURN is the canonical (uppercase) JSON key for DataHub entity URNs as
	// emitted by some upstream payloads. Used alongside keyURN for case-insensitive matching.
	fieldURN = "URN"
	// fieldDomain is the JSON/map key for the DataHub domain attached to a table.
	fieldDomain = "domain"
	// fieldGlossaryTerms is the JSON/map key for glossary term lists.
	fieldGlossaryTerms = "glossary_terms"

	// sensitivityPII marks tags/columns classified as personally identifiable information.
	sensitivityPII = "pii"

	// sensitivityConfidential marks tags/columns classified as confidential.
	sensitivityConfidential = "confidential"

	// assertionStatusFailing is the DataHub data-contract status indicating a
	// failing assertion.
	assertionStatusFailing = "FAILING"

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

	// ColumnContextFiltering limits column-level enrichment to only columns
	// whose names appear in the SQL query. This saves tokens when a query
	// references 3 of 70 columns but the table has metadata for all 70.
	// Only applies to the SQL path (trino_query); trino_describe_table
	// always shows all columns. Defaults to true.
	ColumnContextFiltering bool

	// SearchSchemaPreview adds a bounded column-name+type preview to
	// datahub_search query_context for available tables, so agents can
	// write SQL without an intermediate datahub_get_schema call.
	SearchSchemaPreview bool

	// SchemaPreviewMaxColumns caps how many columns appear per entity
	// in the schema preview. Zero disables the preview.
	SchemaPreviewMaxColumns int

	// WorkflowTracker enables a soft discovery note appended to enriched
	// results when the session has not yet called any discovery tool.
	// If nil, no discovery note is appended.
	WorkflowTracker *SessionWorkflowTracker

	// ForConnection returns the DataHub source name and catalog mapping for
	// a named connection. Returns ("", nil) if the connection has no mapping.
	// This avoids an import cycle between middleware and platform packages.
	ForConnection func(connectionName string) (datahubSourceName string, catalogMapping map[string]string)

	// ConnectionsForURN returns connection names that can access the dataset
	// identified by a DataHub URN, based on the URN's platform component.
	ConnectionsForURN func(urn string) []string

	// SemanticFallbackEnabled turns on the issue #444 fallback: when
	// a URN-equality lookup misses on the semantic provider, the
	// enricher calls SearchTables with Mode="semantic" and surfaces
	// the top-K hits as SUGGESTED matches (annotated with
	// match_kind=semantic so the model knows they are similarity-
	// inferred rather than URN-resolved). Default off; operators opt
	// in explicitly because similarity is heuristic.
	SemanticFallbackEnabled bool

	// SemanticFallbackTopK caps how many similarity-search results
	// the fallback returns per miss. Caller is expected to clamp to
	// a sane range (the platform-level config helper does this).
	SemanticFallbackTopK int
}

// schemaPreviewColumn is a minimal column entry for search result schema previews.
type schemaPreviewColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// queryContextEntry extends TableAvailability with an optional schema preview.
type queryContextEntry struct {
	Available            bool                  `json:"available"`
	QueryTable           string                `json:"query_table,omitempty"`
	Connection           string                `json:"connection,omitempty"`
	AvailableConnections []string              `json:"available_connections,omitempty"`
	EstimatedRows        *int64                `json:"estimated_rows,omitempty"`
	Error                string                `json:"error,omitempty"`
	SchemaPreview        []schemaPreviewColumn `json:"schema_preview,omitempty"`
	TotalColumns         int                   `json:"total_columns,omitempty"`
}

// semanticEnricher holds the enrichment dependencies.
type semanticEnricher struct {
	semanticProvider semantic.Provider
	queryProvider    query.Provider
	storageProvider  storage.Provider
	memoryProvider   MemoryProvider
	pageProvider     KnowledgePageProvider
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
	case toolPrefixDatahub:
		return e.enrichDataHubResultWithAll(ctx, result, request)
	case toolPrefixS3:
		if e.cfg.EnrichS3Results && e.semanticProvider != nil {
			var catalogMapping map[string]string
			if e.cfg.ForConnection != nil && pc.Connection != "" {
				_, catalogMapping = e.cfg.ForConnection(pc.Connection)
			}
			return enrichS3Result(ctx, result, request, e.semanticProvider, catalogMapping)
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
	// Resolve catalog mapping for the current connection.
	var catalogMapping map[string]string
	if e.cfg.ForConnection != nil && pc.Connection != "" {
		_, catalogMapping = e.cfg.ForConnection(pc.Connection)
	}

	cache := e.cfg.SessionCache
	if cache == nil {
		slog.Debug("dedup: cache is nil, full enrichment")
		pc.EnrichmentMode = EnrichmentModeFull
		return e.enrichTrinoResult(ctx, result, request, catalogMapping, pc)
	}

	// Identify tables from the request
	tableKeys := extractTableKeysFromRequest(request)
	if len(tableKeys) == 0 {
		slog.Debug("dedup: no table keys extracted, full enrichment",
			"tool", pc.ToolName,
			keySessionID, pc.SessionID,
			"has_params", request.Params != nil,
		)
		return e.enrichTrinoResult(ctx, result, request, catalogMapping, pc)
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

	// Resolve catalog mapping for the current connection.
	var catalogMapping map[string]string
	if e.cfg.ForConnection != nil && pc.Connection != "" {
		_, catalogMapping = e.cfg.ForConnection(pc.Connection)
	}

	enrichedResult, err := e.enrichTrinoResult(ctx, result, request, catalogMapping, pc)
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
	pc.EnrichmentMode = EnrichmentModeFull
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
	pc.EnrichmentMode = string(e.cfg.DedupMode)
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
			"tables":  tableKeys,
			fieldNote: "Full semantic metadata was provided earlier in this session. Refer to previous responses for column descriptions, tags, owners, and glossary terms.",
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
			fieldNote:         "Compact view. Full metadata was provided earlier in this session. Only critical warnings, quality scores, and sensitivity flags are shown.",
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

// enrichDataHubResultWithAll enriches DataHub results with query, storage, and curated query context.
func (e *semanticEnricher) enrichDataHubResultWithAll(
	ctx context.Context,
	result *mcp.CallToolResult,
	request mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	enrichedResult, err := e.enrichDataHubQueryAndStorage(ctx, result, request)
	if err != nil {
		return enrichedResult, err
	}

	// Enrich with curated query availability
	if e.cfg.EnrichDataHubResults && e.semanticProvider != nil {
		enrichedResult, err = enrichDataHubResultWithCuratedQueries(ctx, enrichedResult, e.semanticProvider)
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

// enrichDataHubQueryAndStorage enriches DataHub results with query context (Trino) and storage context (S3).
func (e *semanticEnricher) enrichDataHubQueryAndStorage(
	ctx context.Context,
	result *mcp.CallToolResult,
	request mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	enrichedResult := result

	// Enrich with query context (Trino) and optional schema preview
	if e.cfg.EnrichDataHubResults && e.queryProvider != nil {
		var err error
		enrichedResult, err = e.enrichDataHubResult(ctx, enrichedResult, request)
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

// applyCatalogMapping replaces the table's catalog using the given mapping.
// If mapping is nil or the catalog has no entry, the table is returned unchanged.
func applyCatalogMapping(table semantic.TableIdentifier, mapping map[string]string) semantic.TableIdentifier {
	if mapping == nil {
		return table
	}
	if mapped, ok := mapping[table.Catalog]; ok {
		table.Catalog = mapped
	}
	return table
}

// enrichTrinoResult adds semantic context to Trino tool results.
// catalogMapping optionally remaps connection catalog names to DataHub catalog names.
// pc may be nil in test paths; when non-nil, EnrichmentMatchKind is set to
// EnrichmentMatchURN on exact resolution or EnrichmentMatchSemantic when the
// issue #444 similarity fallback fires.
func (e *semanticEnricher) enrichTrinoResult(
	ctx context.Context,
	result *mcp.CallToolResult,
	request mcp.CallToolRequest,
	catalogMapping map[string]string,
	pc *PlatformContext,
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
			filterSQL := ""
			if e.cfg.ColumnContextFiltering {
				filterSQL = sql
			}
			return e.enrichTrinoQueryResult(ctx, result, tables, filterSQL, catalogMapping)
		}
	}

	// Fall back to explicit table parameters (single table tools like trino_describe_table)
	tableName := extractTableFromRequest(request)
	if tableName == "" {
		return result, nil
	}

	// Parse table identifier and apply catalog mapping
	table := applyCatalogMapping(parseTableIdentifier(tableName), catalogMapping)

	// Get semantic context for the table
	semanticCtx, err := e.semanticProvider.GetTableContext(ctx, table)
	if err != nil {
		slog.Debug("semantic enrichment failed for Trino result",
			keyTable, tableName,
			"parsed_catalog", table.Catalog,
			"parsed_schema", table.Schema,
			"parsed_table", table.Table,
			keyError, err,
		)
		// Issue #444: when URN-equality lookup misses and the
		// operator opted into semantic_fallback, surface the top-K
		// similarity hits as SUGGESTED matches so the model can
		// still get a hint about likely-related entities. The
		// match_kind tag on both the payload and the audit row
		// makes it visible that this is heuristic, not asserted.
		if suggestions := e.trySemanticFallback(ctx, table); len(suggestions) > 0 {
			if pc != nil {
				pc.EnrichmentMatchKind = EnrichmentMatchSemantic
			}
			return appendSemanticFallbackSuggestions(result, table, suggestions)
		}
		return result, nil
	}

	// Get column-level semantic context
	columnsCtx, columnsErr := e.semanticProvider.GetColumnsContext(ctx, table)
	if columnsErr != nil {
		slog.Debug("column semantic enrichment failed for Trino result",
			keyTable, tableName,
			keyError, columnsErr,
		)
	}

	if pc != nil {
		pc.EnrichmentMatchKind = EnrichmentMatchURN
	}
	return appendSemanticContextWithColumns(result, semanticCtx, columnsCtx)
}

// fallbackSearchMode is the SearchFilter.Mode value the issue #444
// fallback requests on the semantic provider. Named so the same
// literal does not float between the caller and any future test
// that needs to assert on the value the provider sees.
const fallbackSearchMode = "semantic"

// trySemanticFallback runs the issue #444 similarity-search path
// when a URN-equality lookup for table misses. Returns nil when the
// operator did not enable the fallback, when the provider is
// unavailable, when the constructed query is empty, or when the
// underlying search errors. A non-nil empty slice is treated as
// "ran but no hits" and the caller suppresses the suggested-matches
// payload.
func (e *semanticEnricher) trySemanticFallback(ctx context.Context, table semantic.TableIdentifier) []semantic.TableSearchResult {
	if !e.cfg.SemanticFallbackEnabled || e.semanticProvider == nil {
		return nil
	}
	searchQuery := buildSemanticFallbackQuery(table)
	if searchQuery == "" {
		return nil
	}
	topK := e.cfg.SemanticFallbackTopK
	if topK <= 0 {
		topK = 1
	}
	results, err := e.semanticProvider.SearchTables(ctx, semantic.SearchFilter{
		Query: searchQuery,
		Mode:  fallbackSearchMode,
		Limit: topK,
	})
	if err != nil {
		slog.Debug("semantic fallback search failed",
			keyTable, table.String(),
			keyError, err,
		)
		return nil
	}
	return results
}

// buildSemanticFallbackQuery turns a TableIdentifier into the query
// text the similarity search receives. Table name carries the most
// information so it leads; schema follows to disambiguate
// similarly-named tables across schemas. Catalog is intentionally
// omitted because catalog names are often deployment-internal
// identifiers ("hive", "rdbms") that add noise without recall.
// Returns "" when no usable component is present.
func buildSemanticFallbackQuery(table semantic.TableIdentifier) string {
	parts := make([]string, 0, 2)
	if table.Table != "" {
		parts = append(parts, table.Table)
	}
	if table.Schema != "" {
		parts = append(parts, table.Schema)
	}
	return strings.Join(parts, " ")
}

// appendSemanticFallbackSuggestions appends the suggested-matches
// payload produced by trySemanticFallback to the result. The
// payload is wrapped under "semantic_fallback" with a match_kind
// tag and a human-readable note so the model can distinguish
// these heuristic suggestions from URN-resolved enrichment.
// Returns the result unchanged when suggestions is empty.
func appendSemanticFallbackSuggestions(
	result *mcp.CallToolResult,
	table semantic.TableIdentifier,
	suggestions []semantic.TableSearchResult,
) (*mcp.CallToolResult, error) {
	if len(suggestions) == 0 {
		return result, nil
	}
	items := make([]map[string]any, 0, len(suggestions))
	for _, s := range suggestions {
		item := map[string]any{
			keyURN:    s.URN,
			fieldName: s.Name,
		}
		if s.Platform != "" {
			item["platform"] = s.Platform
		}
		if s.Description != "" {
			item[keyDescription] = s.Description
		}
		if len(s.Tags) > 0 {
			item[keyTags] = s.Tags
		}
		if s.Domain != "" {
			item[fieldDomain] = s.Domain
		}
		items = append(items, item)
	}
	payload := map[string]any{
		"semantic_fallback": map[string]any{
			"queried_table":     table.String(),
			"match_kind":        EnrichmentMatchSemantic,
			fieldNote:           "Exact URN lookup for the queried table missed. The following are SUGGESTED matches from a similarity search; verify before treating as authoritative.",
			"suggested_matches": items,
		},
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return result, fmt.Errorf("marshal semantic fallback: %w", err)
	}
	result.Content = append(result.Content, &mcp.TextContent{Text: string(payloadJSON)})
	return result, nil
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
// When filterSQL is non-empty, column context is filtered to only columns
// whose names appear as identifiers in the SQL query (plus PII/sensitive columns).
func (e *semanticEnricher) enrichTrinoQueryResult(
	ctx context.Context,
	result *mcp.CallToolResult,
	tables []TableRef,
	filterSQL string,
	catalogMapping map[string]string,
) (*mcp.CallToolResult, error) {
	if len(tables) == 0 {
		return result, nil
	}

	// Primary table gets full context
	primary := tables[0]
	tableID := applyCatalogMapping(refToTableIdentifier(primary), catalogMapping)

	tableCtx, err := e.semanticProvider.GetTableContext(ctx, tableID)
	if err != nil {
		slog.Debug("semantic enrichment failed for primary table",
			keyTable, primary.FullPath,
			keyError, err,
		)
		return result, nil
	}

	columnsCtx, columnsErr := e.semanticProvider.GetColumnsContext(ctx, tableID)
	if columnsErr != nil {
		slog.Debug("column enrichment failed for primary table",
			keyTable, primary.FullPath,
			keyError, columnsErr,
		)
	}

	// Filter columns to only those referenced in the SQL query
	if filterSQL != "" && len(columnsCtx) > 0 {
		columnsCtx = filterColumnsBySQL(columnsCtx, filterSQL)
	}

	// Additional tables get summary context
	additionalTables := make([]map[string]any, 0, len(tables)-1)
	for _, t := range tables[1:] {
		tID := applyCatalogMapping(refToTableIdentifier(t), catalogMapping)
		tCtx, tErr := e.semanticProvider.GetTableContext(ctx, tID)
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

// filterColumnsBySQL returns only columns whose names appear as identifiers
// in the SQL query. PII/sensitive columns and columns with critical tags are
// always included for safety. If no column names match (e.g., SELECT *), all
// columns are returned as a graceful fallback.
func filterColumnsBySQL(columnsCtx map[string]*semantic.ColumnContext, sql string) map[string]*semantic.ColumnContext {
	sqlIDs := ExtractIdentifiers(sql)

	filtered := make(map[string]*semantic.ColumnContext, len(columnsCtx))
	for name, col := range columnsCtx {
		if sqlIDs[strings.ToLower(name)] || isSafetyRelevant(col) {
			filtered[name] = col
		}
	}

	// Graceful degradation: if nothing matched, return all columns
	if len(filtered) == 0 {
		return columnsCtx
	}

	return filtered
}

// isSafetyRelevant returns true if a column has PII, sensitivity, or critical
// tag flags that should always be included regardless of SQL references.
func isSafetyRelevant(col *semantic.ColumnContext) bool {
	if col.IsPII || col.IsSensitive {
		return true
	}
	for _, tag := range col.Tags {
		lower := strings.ToLower(tag)
		for _, prefix := range criticalTagPrefixes {
			if strings.Contains(lower, prefix) {
				return true
			}
		}
	}
	return false
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
	if ctx.ActiveIncidents > 0 {
		summary["active_incidents"] = ctx.ActiveIncidents
	}
	if ctx.DataContract != nil {
		summary["data_contract"] = ctx.DataContract
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

// enrichDataHubResult adds query context (and optional schema preview) to DataHub tool results.
func (e *semanticEnricher) enrichDataHubResult(
	ctx context.Context,
	result *mcp.CallToolResult,
	request mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	provider := e.queryProvider

	// Extract URNs from result content, keeping only dataset URNs.
	// Non-dataset URNs (domains, tags, queries, owners) cannot be
	// resolved to queryable tables and would produce spurious errors.
	urns := filterDatasetURNs(extractURNsFromResult(result))

	// Also extract URN from request (for tools like datahub_get_schema that take urn param)
	if reqURN := extractURNFromRequest(request); reqURN != "" {
		if isDatasetURN(reqURN) && !slices.Contains(urns, reqURN) {
			urns = append(urns, reqURN)
		}
	}

	if len(urns) == 0 {
		return result, nil
	}

	// Get query context for each URN
	queryContexts := make(map[string]*queryContextEntry, len(urns))
	for _, urn := range urns {
		entry := e.buildQueryContextEntry(ctx, provider, urn)
		if entry != nil {
			queryContexts[urn] = entry
		}
	}

	// Append query context to result
	return appendQueryContext(result, queryContexts)
}

// buildQueryContextEntry builds a single query context entry for a URN,
// including availability, connections, and optional schema preview.
func (e *semanticEnricher) buildQueryContextEntry(
	ctx context.Context,
	provider query.Provider,
	urn string,
) *queryContextEntry {
	availability, err := provider.GetTableAvailability(ctx, urn)
	if err != nil {
		return nil
	}
	entry := &queryContextEntry{
		Available:     availability.Available,
		QueryTable:    availability.QueryTable,
		Connection:    availability.Connection,
		EstimatedRows: availability.EstimatedRows,
		Error:         availability.Error,
	}
	// Add all connections that can access this URN via source map lookup.
	if e.cfg.ConnectionsForURN != nil {
		entry.AvailableConnections = e.cfg.ConnectionsForURN(urn)
	}
	// Best-effort schema preview for available tables.
	if availability.Available && e.cfg.SearchSchemaPreview && e.cfg.SchemaPreviewMaxColumns > 0 {
		preview, total := fetchSchemaPreview(ctx, provider, urn, e.cfg.SchemaPreviewMaxColumns)
		if len(preview) > 0 {
			entry.SchemaPreview = preview
			entry.TotalColumns = total
		}
	}
	return entry
}

// fetchSchemaPreview resolves a URN to a table and returns a bounded column
// preview. Primary key columns are listed first. Returns nil on any error.
func fetchSchemaPreview(
	ctx context.Context,
	provider query.Provider,
	urn string,
	maxCols int,
) (preview []schemaPreviewColumn, totalColumns int) {
	tableID, err := provider.ResolveTable(ctx, urn)
	if err != nil || tableID == nil {
		return nil, 0
	}
	schema, err := provider.GetTableSchema(ctx, *tableID)
	if err != nil || schema == nil || len(schema.Columns) == 0 {
		return nil, 0
	}
	return buildSchemaPreview(schema, maxCols), len(schema.Columns)
}

// buildSchemaPreview selects up to maxCols columns with primary key columns first.
func buildSchemaPreview(schema *query.TableSchema, maxCols int) []schemaPreviewColumn {
	pkSet := make(map[string]bool, len(schema.PrimaryKey))
	for _, pk := range schema.PrimaryKey {
		pkSet[pk] = true
	}
	preview := make([]schemaPreviewColumn, 0, min(maxCols, len(schema.Columns)))
	// Primary key columns first
	for _, col := range schema.Columns {
		if len(preview) >= maxCols {
			break
		}
		if pkSet[col.Name] {
			preview = append(preview, schemaPreviewColumn{Name: col.Name, Type: col.Type})
		}
	}
	// Then remaining columns
	for _, col := range schema.Columns {
		if len(preview) >= maxCols {
			break
		}
		if !pkSet[col.Name] {
			preview = append(preview, schemaPreviewColumn{Name: col.Name, Type: col.Type})
		}
	}
	return preview
}

// enrichDataHubResultWithCuratedQueries adds curated query availability
// to DataHub tool results so agents know curated queries exist without
// requiring the 3-call discovery chain.
func enrichDataHubResultWithCuratedQueries(
	ctx context.Context,
	result *mcp.CallToolResult,
	provider semantic.Provider,
) (*mcp.CallToolResult, error) {
	urns := extractURNsFromResult(result)
	if len(urns) == 0 {
		return result, nil
	}

	counts := make(map[string]any)
	for _, urn := range urns {
		count, err := provider.GetCuratedQueryCount(ctx, urn)
		if err != nil || count == 0 {
			continue
		}
		counts[urn] = map[string]any{
			"has_curated_queries": true,
			"curated_query_count": count,
		}
	}

	if len(counts) == 0 {
		return result, nil
	}

	return appendCuratedQueryContext(result, counts)
}

// appendCuratedQueryContext appends curated query context to the result.
func appendCuratedQueryContext(result *mcp.CallToolResult, contexts map[string]any) (*mcp.CallToolResult, error) {
	enrichment := map[string]any{
		"curated_query_context": contexts,
	}

	enrichmentJSON, err := json.Marshal(enrichment)
	if err != nil {
		return result, fmt.Errorf("marshal curated query context: %w", err)
	}

	result.Content = append(result.Content, &mcp.TextContent{
		Text: string(enrichmentJSON),
	})

	return result, nil
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

// datasetURNPrefix identifies dataset URNs in DataHub's URN scheme.
const datasetURNPrefix = "urn:li:dataset:"

// isDatasetURN returns true if the URN represents a dataset entity.
func isDatasetURN(urn string) bool {
	return strings.HasPrefix(urn, datasetURNPrefix)
}

// filterDatasetURNs returns only dataset URNs from the input slice.
func filterDatasetURNs(urns []string) []string {
	filtered := make([]string, 0, len(urns))
	for _, urn := range urns {
		if isDatasetURN(urn) {
			filtered = append(filtered, urn)
		}
	}
	return filtered
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
		if k == keyURN || k == fieldURN {
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
		fieldDomain:     ctx.Domain,
		"quality_score": ctx.QualityScore,
		keyDeprecation:  ctx.Deprecation,
	}

	if ctx.URN != "" {
		semanticCtx[keyURN] = ctx.URN
	}
	if len(ctx.GlossaryTerms) > 0 {
		semanticCtx[fieldGlossaryTerms] = ctx.GlossaryTerms
	}
	if len(ctx.CustomProperties) > 0 {
		semanticCtx["custom_properties"] = ctx.CustomProperties
	}
	if ctx.LastModified != nil {
		semanticCtx["last_modified"] = ctx.LastModified
	}
	if len(ctx.StructuredProperties) > 0 {
		semanticCtx["structured_properties"] = ctx.StructuredProperties
	}
	if ctx.ActiveIncidents > 0 {
		semanticCtx["active_incidents"] = ctx.ActiveIncidents
		semanticCtx["incidents"] = ctx.Incidents
	}
	if ctx.DataContract != nil {
		semanticCtx["data_contract"] = ctx.DataContract
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
		compact[fieldDomain] = ctx.Domain
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
	if ctx.ActiveIncidents > 0 {
		compact["active_incidents"] = ctx.ActiveIncidents
	}
	if ctx.DataContract != nil && ctx.DataContract.Status == assertionStatusFailing {
		compact["data_contract"] = ctx.DataContract
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
		keyDescription:     col.Description,
		fieldGlossaryTerms: col.GlossaryTerms,
		keyTags:            col.Tags,
		"is_pii":           col.IsPII,
		"is_sensitive":     col.IsSensitive,
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
func appendQueryContext(result *mcp.CallToolResult, contexts map[string]*queryContextEntry) (*mcp.CallToolResult, error) {
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
// catalogMapping optionally remaps connection catalog names to DataHub catalog names.
func enrichS3Result(
	ctx context.Context,
	result *mcp.CallToolResult,
	request mcp.CallToolRequest,
	provider semantic.Provider,
	catalogMapping map[string]string,
) (*mcp.CallToolResult, error) {
	bucket, prefix := extractS3PathFromRequest(request)
	if bucket == "" {
		return result, nil
	}

	// Apply catalog mapping to the S3 search platform if applicable.
	platform := toolPrefixS3
	if mapped, ok := catalogMapping[toolPrefixS3]; ok {
		platform = mapped
	}

	searchResults := searchS3Datasets(ctx, provider, bucket, prefix, platform)
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
	bucket, prefix, platform string,
) []semantic.TableSearchResult {
	searchQuery := bucket
	if prefix != "" {
		searchQuery = bucket + "/" + prefix
	}

	searchResults, err := provider.SearchTables(ctx, semantic.SearchFilter{
		Query:    searchQuery,
		Platform: platform,
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
		fieldName:      sr.Name,
		keyDescription: tableCtx.Description,
	}
	if len(tableCtx.Owners) > 0 {
		semanticCtx["owners"] = tableCtx.Owners
	}
	if len(tableCtx.Tags) > 0 {
		semanticCtx[keyTags] = tableCtx.Tags
	}
	if tableCtx.Domain != nil {
		semanticCtx[fieldDomain] = tableCtx.Domain.Name
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
			fieldNote:           "Semantic metadata from DataHub for S3 location",
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
		if k == keyURN || k == fieldURN {
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
			MIMEType:    mimeTypeJSON,
		})

		availURI := fmt.Sprintf("availability://%s.%s/%s", catalog, schema, table)
		result.Content = append(result.Content, &mcp.ResourceLink{
			URI:         availURI,
			Name:        fmt.Sprintf("Availability: %s.%s.%s", catalog, schema, table),
			Description: "Data availability status and row count",
			MIMEType:    mimeTypeJSON,
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
