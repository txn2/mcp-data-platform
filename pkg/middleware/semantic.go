package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/sqltables"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// tokenDivisor is the approximate characters-per-token ratio for estimation.
const tokenDivisor = 4

// criticalTagPrefixes lists tag substrings that indicate safety-relevant metadata.
var criticalTagPrefixes = []string{sensitivityPII, "sensitive", "quality", "restricted", sensitivityConfidential}

// toolPrefixTrino is the toolkit kind identifier for Trino tools.
const toolPrefixTrino = "trino"

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
	// fieldURN is the canonical (uppercase) JSON key for entity references as
	// emitted by some upstream payloads. Used alongside keyURN for case-insensitive matching.
	fieldURN = "URN"
	// fieldDomain is the JSON/map key for the catalog domain attached to a table.
	fieldDomain = "domain"
	// fieldGlossaryTerms is the JSON/map key for glossary term lists.
	fieldGlossaryTerms = "glossary_terms"

	// sensitivityPII marks tags/columns classified as personally identifiable information.
	sensitivityPII = "pii"

	// sensitivityConfidential marks tags/columns classified as confidential.
	sensitivityConfidential = "confidential"

	// assertionStatusFailing is the data-contract status indicating a failing
	// assertion.
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

	// WorkflowTracker enables a soft discovery note appended to enriched
	// results when the session has not yet called any discovery tool.
	// If nil, no discovery note is appended.
	WorkflowTracker *SessionWorkflowTracker

	// ConnectionsForURN returns the names of the connections that can access the
	// dataset a catalog reference names.
	ConnectionsForURN func(urn string) []string

	// DatasetURN names the catalog dataset a table belongs to, the key a
	// recorded call's targets are stored under (#1919). Nil leaves a described
	// table without its proven queries.
	DatasetURN URNBuilder

	// ProvenQueries returns the caller's recorded queries that already
	// answered something on a table, appended when a table is described
	// (#1321). Nil leaves a describe carrying only what the catalog knows.
	ProvenQueries ProvenQueryLister

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

	// MemoryLimit caps how many memory records are recalled and rendered
	// into the memory_context enrichment block per tool call. Zero falls
	// back to defaultMemoryEnrichmentLimit (issue #761).
	MemoryLimit int

	// MemoryContextBudgetBytes bounds the byte size of the rendered memory
	// summaries appended per tool call. Records beyond the budget are listed
	// as compact id+reference stubs in memory_context_omitted (still
	// fetchable, not dropped); at least one record is always rendered so a
	// tiny budget does not silently suppress all memory enrichment. Zero
	// disables the budget (every recalled record is rendered). Issue #761.
	MemoryContextBudgetBytes int

	// VerifiableInsights marks a pushed insight as checkable: when the entity its
	// claim is about resolves through the enrichment's own query provider to an
	// available table, the rendered record names that table and connection, so
	// the claim arrives with the one query that would settle it rather than as
	// something to take on trust (#1220). False (the operator opted out) leaves
	// every rendered record exactly as it was, as does a query provider that
	// cannot resolve anything.
	VerifiableInsights bool

	// InsightVerifier is the resolver behind VerifiableInsights. The enrichment
	// constructor fills it from the query provider it is given; a caller sets it
	// directly only to supply its own resolver (tests).
	InsightVerifier EntityVerifier

	// MemorySummaryBytes caps each rendered memory record to a summary-first
	// excerpt (first paragraph, or the first N bytes on a rune boundary), so
	// the agent fetches the full record via its reference only when it
	// matters. Zero disables truncation (full content is rendered). Issue #761.
	MemorySummaryBytes int
}

// semanticEnricher holds the enrichment dependencies.
type semanticEnricher struct {
	semanticProvider semantic.Provider
	queryProvider    query.Provider
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
		slog.DebugContext(ctx, "dedup: cache is nil, full enrichment")
		pc.EnrichmentMode = enrichmentModeFull
		return e.enrichTrinoResult(ctx, result, request, pc)
	}

	// Identify tables from the request
	tableKeys := extractTableKeysFromRequest(request)
	if len(tableKeys) == 0 {
		slog.DebugContext(ctx, "dedup: no table keys extracted, full enrichment",
			"tool", pc.ToolName,
			keySessionID, pc.SessionID,
			"has_params", request.Params != nil,
		)
		return e.enrichTrinoResult(ctx, result, request, pc)
	}

	slog.DebugContext(ctx, "dedup: checking cache",
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

	enrichedResult, err := e.enrichTrinoResult(ctx, result, request, pc)
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
	pc.EnrichmentMode = enrichmentModeFull
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
	slog.DebugContext(ctx, "dedup: all tables cached, applying dedup mode",
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
		tables := sqltables.Extract(sql)
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

	refJSON, err := wirejson.Marshal(ref)
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

		enrichmentJSON, marshalErr := wirejson.Marshal(enrichment)
		if marshalErr != nil {
			continue
		}

		result.Content = append(result.Content, &mcp.TextContent{
			Text: string(enrichmentJSON),
		})
	}

	return result, nil
}

// enrichTrinoResult adds semantic context to Trino tool results.
// pc may be nil in test paths; when non-nil, EnrichmentMatchKind is set to
// enrichmentMatchURN on exact resolution or enrichmentMatchSemantic when the
// issue #444 similarity fallback fires.
func (e *semanticEnricher) enrichTrinoResult(
	ctx context.Context,
	result *mcp.CallToolResult,
	request mcp.CallToolRequest,
	pc *PlatformContext,
) (*mcp.CallToolResult, error) {
	// Check for SQL query first (multi-table support)
	if sql := extractSQLFromRequest(request); sql != "" {
		tables := sqltables.Extract(sql)
		if len(tables) > 0 {
			slog.DebugContext(ctx, "extracted tables from SQL for enrichment",
				"sql_length", len(sql),
				"table_count", len(tables),
				"tables", formatTableRefs(tables),
			)
			filterSQL := ""
			if e.cfg.ColumnContextFiltering {
				filterSQL = sql
			}
			return e.enrichTrinoQueryResult(ctx, result, tables, filterSQL)
		}
	}

	// Fall back to explicit table parameters (single table tools like trino_describe_table)
	tableName := extractTableFromRequest(request)
	if tableName == "" {
		return result, nil
	}

	table := parseTableIdentifier(tableName)

	enriched, err := e.enrichTrinoTable(ctx, result, table, tableName, pc)
	if err != nil {
		return enriched, err
	}
	// The queries that already answered something on this table are appended
	// whether or not the catalog knows the table: a table with no catalog entry
	// is exactly the one where someone else's working query is worth the most
	// (#1321).
	return e.appendProvenQueries(ctx, enriched, table, pc), nil
}

// enrichTrinoTable adds the catalog's context for one named table, which is
// what a describe returns against.
func (e *semanticEnricher) enrichTrinoTable(
	ctx context.Context,
	result *mcp.CallToolResult,
	table semantic.TableIdentifier,
	tableName string,
	pc *PlatformContext,
) (*mcp.CallToolResult, error) {
	// Get semantic context for the table
	semanticCtx, err := e.semanticProvider.GetTableContext(ctx, table)
	if err != nil {
		slog.DebugContext(ctx, "semantic enrichment failed for Trino result",
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
				pc.EnrichmentMatchKind = enrichmentMatchSemantic
			}
			return appendSemanticFallbackSuggestions(result, table, suggestions)
		}
		return result, nil
	}

	// Get column-level semantic context
	columnsCtx, columnsErr := e.semanticProvider.GetColumnsContext(ctx, table)
	if columnsErr != nil {
		slog.DebugContext(ctx, "column semantic enrichment failed for Trino result",
			keyTable, tableName,
			keyError, columnsErr,
		)
	}

	if pc != nil {
		pc.EnrichmentMatchKind = enrichmentMatchURN
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
		slog.DebugContext(ctx, "semantic fallback search failed",
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
			"match_kind":        enrichmentMatchSemantic,
			fieldNote:           "Exact URN lookup for the queried table missed. The following are SUGGESTED matches from a similarity search; verify before treating as authoritative.",
			"suggested_matches": items,
		},
	}
	payloadJSON, err := wirejson.Marshal(payload)
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
func formatTableRefs(refs []sqltables.Ref) []string {
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
	tables []sqltables.Ref,
	filterSQL string,
) (*mcp.CallToolResult, error) {
	if len(tables) == 0 {
		return result, nil
	}

	// Primary table gets full context
	primary := tables[0]
	tableID := refToTableIdentifier(primary)

	tableCtx, err := e.semanticProvider.GetTableContext(ctx, tableID)
	if err != nil {
		slog.DebugContext(ctx, "semantic enrichment failed for primary table",
			keyTable, primary.FullPath,
			keyError, err,
		)
		return result, nil
	}

	columnsCtx, columnsErr := e.semanticProvider.GetColumnsContext(ctx, tableID)
	if columnsErr != nil {
		slog.DebugContext(ctx, "column enrichment failed for primary table",
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
		tID := refToTableIdentifier(t)
		tCtx, tErr := e.semanticProvider.GetTableContext(ctx, tID)
		if tErr != nil {
			slog.DebugContext(ctx, "semantic enrichment failed for additional table",
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
	sqlIDs := extractIdentifiers(sql)

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

// refToTableIdentifier converts tableRef to semantic.TableIdentifier.
func refToTableIdentifier(ref sqltables.Ref) semantic.TableIdentifier {
	return semantic.TableIdentifier{
		Catalog: ref.Catalog,
		Schema:  ref.Schema,
		Table:   ref.Table,
	}
}

// buildAdditionalTableContext creates summary context for additional tables.
func buildAdditionalTableContext(ref sqltables.Ref, ctx *semantic.TableContext) map[string]any {
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

	enrichmentJSON, err := wirejson.Marshal(enrichment)
	if err != nil {
		return result, fmt.Errorf("marshal semantic context with additional tables: %w", err)
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
	tables := sqltables.Extract(sql)
	if len(tables) > 0 {
		return tables[0].FullPath
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

	enrichmentJSON, err := wirejson.Marshal(enrichment)
	if err != nil {
		return result, fmt.Errorf("marshal semantic context with columns: %w", err)
	}

	result.Content = append(result.Content, &mcp.TextContent{
		Text: string(enrichmentJSON),
	})

	return result, nil
}
