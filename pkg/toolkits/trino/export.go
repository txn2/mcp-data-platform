package trino //nolint:revive // adapter types for cross-package wiring

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"
)

// Schema key/type constants to satisfy revive add-constant.
const (
	schemaTypeString  = "string"
	schemaTypeObject  = "object"
	schemaTypeArray   = "array"
	schemaTypeInteger = "integer"
	schemaKeyType     = "type"
	schemaKeyDesc     = "description"
)

const (
	// exportToolName is the MCP tool name.
	exportToolName = "trino_export"

	// exportIDLength is the number of random bytes used for asset IDs.
	exportIDLength = 16

	// Default export limits.
	defaultMaxExportRows    = 100_000
	defaultMaxExportBytes   = 100 * 1024 * 1024 // 100 MB
	defaultExportTimeout    = 5 * time.Minute
	defaultMaxExportTimeout = 10 * time.Minute

	// Tag validation constants.
	maxExportTagLength = 50
	maxExportTags      = 20
	sysTagPrefix       = "_sys-"

	// Asset field limits (mirrors portal constants to avoid import).
	maxExportNameLength        = 255
	maxExportDescriptionLength = 2000

	// logKeyAssetID is the structured log key for asset IDs.
	logKeyAssetID = "asset_id"
)

// exportTagPattern validates lowercase kebab-case tags.
var exportTagPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// userIDPathSafe matches characters allowed in a path segment without escaping.
var userIDPathSafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// repeatedWhitespace collapses consecutive whitespace.
var repeatedWhitespace = regexp.MustCompile(`\s+`)

// nameCharReplacements maps Unicode punctuation that breaks downstream consumers
// (Content-Disposition headers, filename heuristics, some object stores) to
// portable ASCII equivalents.
var nameCharReplacements = map[rune]string{
	'—': "-",   // em dash
	'–': "-",   // en dash
	'‒': "-",   // figure dash
	'―': "-",   // horizontal bar
	'−': "-",   // minus sign
	'‘': "'",   // left single quote
	'’': "'",   // right single quote
	'‚': "'",   // single low-9 quote
	'‛': "'",   // single high-reversed-9 quote
	'“': `"`,   // left double quote
	'”': `"`,   // right double quote
	'„': `"`,   // double low-9 quote
	'‟': `"`,   // double high-reversed-9 quote
	'…': "...", // ellipsis
	' ': " ",   // non-breaking space
}

// ExportAssetStore is the subset of portal.AssetStore needed by trino_export.
// Defined here to avoid import cycles (portal → registry → trino).
type ExportAssetStore interface {
	InsertExportAsset(ctx context.Context, asset ExportAsset) error
	GetByIdempotencyKey(ctx context.Context, ownerID, key string) (*ExportAssetRef, error)
}

// ExportVersionStore is the subset of portal.VersionStore needed by trino_export.
type ExportVersionStore interface {
	CreateExportVersion(ctx context.Context, version ExportVersion) (int, error)
}

// ExportS3Client is the subset of portal.S3Client needed by trino_export.
type ExportS3Client interface {
	PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error
}

// ExportShareCreator creates public share links for exported assets.
type ExportShareCreator interface {
	CreatePublicShare(ctx context.Context, assetID, createdBy string) (shareURL string, err error)
}

// ExportAsset is the asset data needed for insert.
type ExportAsset struct {
	ID             string
	OwnerID        string
	OwnerEmail     string
	Name           string
	Description    string
	ContentType    string
	S3Bucket       string
	S3Key          string
	SizeBytes      int64
	Tags           []string
	Provenance     ExportProvenance
	SessionID      string
	IdempotencyKey string
}

// ExportProvenance records provenance for an exported asset.
type ExportProvenance struct {
	ToolCalls []ExportProvenanceCall
	SessionID string
	UserID    string
}

// ExportAssetRef is returned by idempotency key lookup.
type ExportAssetRef struct {
	ID        string
	SizeBytes int64
}

// ExportVersion is the version data for creating a new version.
type ExportVersion struct {
	ID            string
	AssetID       string
	S3Key         string
	S3Bucket      string
	ContentType   string
	SizeBytes     int64
	CreatedBy     string
	ChangeSummary string
}

// ExportConfig holds configuration for the trino_export tool.
type ExportConfig struct {
	MaxRows        int           `yaml:"max_rows"`
	MaxBytes       int64         `yaml:"max_bytes"`
	DefaultTimeout time.Duration `yaml:"default_timeout"`
	MaxTimeout     time.Duration `yaml:"max_timeout"`
}

// applyExportDefaults fills in zero values with defaults.
func applyExportDefaults(cfg ExportConfig) ExportConfig {
	if cfg.MaxRows <= 0 {
		cfg.MaxRows = defaultMaxExportRows
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = defaultMaxExportBytes
	}
	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = defaultExportTimeout
	}
	if cfg.MaxTimeout <= 0 {
		cfg.MaxTimeout = defaultMaxExportTimeout
	}
	return cfg
}

// ExportUserContext holds user identity extracted from the request context.
type ExportUserContext struct {
	UserID    string
	UserEmail string
	SessionID string
}

// ExportProvenanceCall represents a tool call in the provenance chain.
type ExportProvenanceCall struct {
	ToolName   string
	Timestamp  string
	Parameters map[string]any
}

// ExportDeps holds portal-side dependencies injected into the Trino toolkit.
// All types are defined locally to avoid import cycles (portal → registry → trino).
type ExportDeps struct {
	AssetStore   ExportAssetStore
	VersionStore ExportVersionStore
	S3Client     ExportS3Client
	ShareCreator ExportShareCreator // nil = public link creation disabled
	S3Bucket     string
	S3Prefix     string
	BaseURL      string
	Config       ExportConfig

	// GetUserContext extracts user identity from the request context.
	// Injected by the platform to avoid importing middleware.
	GetUserContext func(ctx context.Context) *ExportUserContext

	// GetProvenanceCalls extracts provenance tool calls from the request context.
	// Injected by the platform to avoid importing middleware.
	GetProvenanceCalls func(ctx context.Context) []ExportProvenanceCall
}

// exportInput is the parsed input for trino_export.
type exportInput struct {
	SQL              string   `json:"sql"`
	Connection       string   `json:"connection"`
	Format           string   `json:"format"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Tags             []string `json:"tags"`
	Limit            int      `json:"limit"`
	IdempotencyKey   string   `json:"idempotency_key"`
	TimeoutSeconds   int      `json:"timeout_seconds"`
	CreatePublicLink bool     `json:"create_public_link"`
}

// exportOutput is the response returned to the agent.
type exportOutput struct {
	AssetID   string `json:"asset_id"`
	PortalURL string `json:"portal_url,omitempty"`
	ShareURL  string `json:"share_url,omitempty"`
	Format    string `json:"format"`
	RowCount  int    `json:"row_count"`
	SizeBytes int64  `json:"size_bytes"`
	Message   string `json:"message"`
}

// SetExportDeps injects portal dependencies for trino_export.
func (t *Toolkit) SetExportDeps(deps ExportDeps) {
	deps.Config = applyExportDefaults(deps.Config)
	t.exportDeps = &deps
}

// registerExportTool registers trino_export on the MCP server.
func (t *Toolkit) registerExportTool(s *mcp.Server) {
	s.AddTool(&mcp.Tool{
		Name: exportToolName,
		Description: "Export query results directly to a portal asset file (CSV, JSON, Markdown, or text). " +
			"Use ONLY after you have validated the query shape with trino_query using a small LIMIT. " +
			"Do NOT use this for data exploration. " +
			"Returns asset metadata (ID, URL, row count, size); the data is NOT returned through this response. " +
			"NAMING: keep `name` short and portable, using only ASCII letters, digits, spaces, hyphens, and dots. " +
			"Avoid em/en dashes, smart quotes, ellipses, and other Unicode punctuation; they will be normalized to ASCII. " +
			"The name doubles as the download filename.",
		InputSchema: exportInputSchema(),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: false,
		},
	}, t.handleExport)
}

// handleExport is the MCP tool handler for trino_export.
func (t *Toolkit) handleExport(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	deps := t.exportDeps
	if deps == nil {
		return exportError("trino_export is not configured"), nil
	}

	input, uc, errResult := t.validateAndPrepare(ctx, req, deps)
	if errResult != nil {
		return errResult, nil
	}

	// Idempotency check
	if input.IdempotencyKey != "" {
		if hit := t.checkIdempotency(ctx, deps, uc, input); hit != nil {
			return hit, nil
		}
	}

	return t.executeAndPersist(ctx, deps, input, uc)
}

// validateAndPrepare parses input, validates it, enforces read-only, and extracts user context.
func (*Toolkit) validateAndPrepare(ctx context.Context, req *mcp.CallToolRequest, deps *ExportDeps) (exportInput, *ExportUserContext, *mcp.CallToolResult) {
	input, err := parseExportInput(*req)
	if err != nil {
		return exportInput{}, nil, exportError(err.Error())
	}
	input.Name = sanitizeExportName(input.Name)
	if err := validateExportInput(input, deps.Config); err != nil {
		return exportInput{}, nil, exportError(err.Error())
	}
	// trino_export always enforces read-only regardless of deployment config.
	// Even when read_only: false (allowing trino_execute writes), exports must be SELECT-only.
	interceptor := NewReadOnlyInterceptor()
	if _, interceptErr := interceptor.Intercept(ctx, input.SQL, ""); interceptErr != nil {
		return exportInput{}, nil, exportError(interceptErr.Error())
	}
	var uc *ExportUserContext
	if deps.GetUserContext != nil {
		uc = deps.GetUserContext(ctx)
	}
	if uc == nil {
		return exportInput{}, nil, exportError("authentication required")
	}
	return input, uc, nil
}

// checkIdempotency returns a success result if the idempotency key already exists.
func (*Toolkit) checkIdempotency(ctx context.Context, deps *ExportDeps, uc *ExportUserContext, input exportInput) *mcp.CallToolResult {
	existing, lookupErr := deps.AssetStore.GetByIdempotencyKey(ctx, uc.UserID, input.IdempotencyKey)
	if lookupErr == nil && existing != nil {
		return exportSuccess(exportOutput{
			AssetID:   existing.ID,
			PortalURL: buildPortalURL(deps.BaseURL, existing.ID),
			Format:    input.Format,
			RowCount:  0,
			SizeBytes: existing.SizeBytes,
			Message:   "Asset already exists (idempotency key matched).",
		})
	}
	return nil
}

// executeAndPersist runs the query, formats, uploads to S3, and saves the asset record.
func (t *Toolkit) executeAndPersist(ctx context.Context, deps *ExportDeps, input exportInput, uc *ExportUserContext) (*mcp.CallToolResult, error) {
	timeout, limit := resolveExportLimits(input, deps.Config)

	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := t.executeExportQuery(queryCtx, input.SQL, input.Connection, limit)
	if err != nil {
		return exportError(fmt.Sprintf("query execution failed: %v", err)), nil
	}

	columns, rows := convertQueryResult(result)

	formatted, formatter, errResult := formatExportResult(input.Format, columns, rows, deps.Config.MaxBytes)
	if errResult != nil {
		return errResult, nil
	}

	sysTags := t.inheritSensitivityTags(ctx, input.SQL)
	allTags := make([]string, 0, len(input.Tags)+len(sysTags))
	allTags = append(allTags, input.Tags...)
	allTags = append(allTags, sysTags...)

	assetID, err := generateExportID()
	if err != nil {
		return exportError(fmt.Sprintf("generating asset ID: %v", err)), nil
	}

	s3Key := buildExportS3Key(deps.S3Prefix, uc.UserID, assetID, formatter.FileExtension())

	if err := deps.S3Client.PutObject(ctx, deps.S3Bucket, s3Key, formatted, formatter.ContentType()); err != nil {
		return exportError(fmt.Sprintf("S3 upload failed: %v", err)), nil
	}

	sourceTables := extractSourceTableNames(input.SQL)
	prov := buildExportProvenance(ctx, deps, exportProvenanceParams{
		userID:       uc.UserID,
		sessionID:    uc.SessionID,
		sql:          input.SQL,
		sourceTables: sourceTables,
		format:       input.Format,
		rowCount:     len(rows),
	})

	asset := ExportAsset{
		ID:             assetID,
		OwnerID:        uc.UserID,
		OwnerEmail:     uc.UserEmail,
		Name:           input.Name,
		Description:    input.Description,
		ContentType:    formatter.ContentType(),
		S3Bucket:       deps.S3Bucket,
		S3Key:          s3Key,
		SizeBytes:      int64(len(formatted)),
		Tags:           allTags,
		Provenance:     prov,
		SessionID:      uc.SessionID,
		IdempotencyKey: input.IdempotencyKey,
	}

	if errResult := t.insertAssetWithRace(ctx, deps, asset, input, uc); errResult != nil {
		return errResult, nil
	}

	t.createExportVersion(ctx, deps, ExportVersion{
		AssetID:     assetID,
		S3Key:       s3Key,
		ContentType: formatter.ContentType(),
		SizeBytes:   int64(len(formatted)),
		CreatedBy:   uc.UserEmail,
	})

	shareURL := t.maybeCreateShare(ctx, deps, input, assetID, uc.UserEmail)

	return exportSuccess(exportOutput{
		AssetID:   assetID,
		PortalURL: buildPortalURL(deps.BaseURL, assetID),
		ShareURL:  shareURL,
		Format:    input.Format,
		RowCount:  len(rows),
		SizeBytes: int64(len(formatted)),
		Message:   fmt.Sprintf("Exported %d rows as %s.", len(rows), input.Format),
	}), nil
}

// insertAssetWithRace inserts the asset record, handling idempotency race conditions.
func (*Toolkit) insertAssetWithRace(ctx context.Context, deps *ExportDeps, asset ExportAsset, input exportInput, uc *ExportUserContext) *mcp.CallToolResult {
	if err := deps.AssetStore.InsertExportAsset(ctx, asset); err != nil {
		// Handle idempotency race: if a concurrent request inserted with the same key,
		// re-fetch and return the existing asset instead of failing.
		if input.IdempotencyKey != "" {
			if existing, lookupErr := deps.AssetStore.GetByIdempotencyKey(ctx, uc.UserID, input.IdempotencyKey); lookupErr == nil && existing != nil {
				return exportSuccess(exportOutput{
					AssetID:   existing.ID,
					PortalURL: buildPortalURL(deps.BaseURL, existing.ID),
					Format:    input.Format,
					SizeBytes: existing.SizeBytes,
					Message:   "Asset already exists (idempotency key matched).",
				})
			}
		}
		return exportError(fmt.Sprintf("saving asset record: %v", err))
	}
	return nil
}

// maybeCreateShare creates a public share link if requested and returns the URL.
func (*Toolkit) maybeCreateShare(ctx context.Context, deps *ExportDeps, input exportInput, assetID, email string) string {
	if !input.CreatePublicLink || deps.ShareCreator == nil {
		return ""
	}
	url, err := deps.ShareCreator.CreatePublicShare(ctx, assetID, email)
	if err != nil {
		slog.Warn("trino_export: failed to create public share link",
			logKeyError, err, logKeyAssetID, assetID)
		return ""
	}
	return url
}

// resolveExportLimits resolves timeout and row limit from input and config.
// Validation of max bounds is already done in validateExportInput; this
// applies the values or falls back to defaults.
func resolveExportLimits(input exportInput, cfg ExportConfig) (timeout time.Duration, limit int) { //nolint:gocritic // named returns for clarity
	timeout = cfg.DefaultTimeout
	if input.TimeoutSeconds > 0 {
		timeout = time.Duration(input.TimeoutSeconds) * time.Second
	}
	limit = cfg.MaxRows
	if input.Limit > 0 {
		limit = input.Limit
	}
	return timeout, limit
}

// convertQueryResult converts a trinoclient.QueryResult into columns and rows.
func convertQueryResult(result *trinoclient.QueryResult) (columns []string, rows [][]any) { //nolint:gocritic // named returns for clarity
	columns = make([]string, len(result.Columns))
	for i, col := range result.Columns {
		columns[i] = col.Name
	}
	rows = make([][]any, len(result.Rows))
	for i, row := range result.Rows {
		vals := make([]any, len(columns))
		for j, col := range columns {
			vals[j] = row[col]
		}
		rows[i] = vals
	}
	return columns, rows
}

// formatExportResult formats columns/rows and checks the byte cap.
func formatExportResult(format string, columns []string, rows [][]any, maxBytes int64) ([]byte, Formatter, *mcp.CallToolResult) {
	formatter, err := newFormatter(format)
	if err != nil {
		return nil, nil, exportError(err.Error())
	}
	formatted, err := formatter.Format(columns, rows)
	if err != nil {
		return nil, nil, exportError(fmt.Sprintf("formatting failed: %v", err))
	}
	if int64(len(formatted)) > maxBytes {
		return nil, nil, exportError(fmt.Sprintf(
			"formatted output (%d bytes) exceeds deployment maximum of %d bytes",
			len(formatted), maxBytes,
		))
	}
	return formatted, formatter, nil
}

// createExportVersion creates the v1 version record, logging on failure.
func (*Toolkit) createExportVersion(ctx context.Context, deps *ExportDeps, ver ExportVersion) {
	versionID, err := generateExportID()
	if err != nil {
		slog.Warn("trino_export: failed to generate version ID", logKeyError, err, logKeyAssetID, ver.AssetID)
		return
	}
	ver.ID = versionID
	ver.S3Bucket = deps.S3Bucket
	ver.ChangeSummary = "Exported from Trino query"
	if _, err := deps.VersionStore.CreateExportVersion(ctx, ver); err != nil {
		slog.Warn("trino_export: failed to create version record", logKeyError, err, logKeyAssetID, ver.AssetID)
	}
}

// executeExportQuery runs the SQL against the Trino client.
func (t *Toolkit) executeExportQuery(ctx context.Context, sql, connection string, limit int) (*trinoclient.QueryResult, error) {
	opts := trinoclient.QueryOptions{
		Limit: limit,
	}

	// In multi-connection mode, resolve the correct client
	if t.manager != nil {
		var client *trinoclient.Client
		var err error
		if connection != "" {
			client, err = t.manager.Client(connection)
		} else {
			client, err = t.manager.DefaultClient()
		}
		if err != nil {
			return nil, fmt.Errorf("resolving trino connection: %w", err)
		}
		result, err := client.Query(ctx, sql, opts)
		if err != nil {
			return nil, fmt.Errorf("executing export query: %w", err)
		}
		return result, nil
	}

	if t.client == nil {
		return nil, fmt.Errorf("no Trino client available")
	}
	result, err := t.client.Query(ctx, sql, opts)
	if err != nil {
		return nil, fmt.Errorf("executing export query: %w", err)
	}
	return result, nil
}

// inheritSensitivityTags reads DataHub tags on source tables and returns
// system tags for sensitive classifications. Degrades gracefully.
func (t *Toolkit) inheritSensitivityTags(ctx context.Context, sql string) []string {
	if t.semanticProvider == nil {
		return nil
	}

	tables := extractTablesFromSQL(sql)
	if len(tables) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var sysTags []string

	for _, table := range tables {
		tableCtx, err := t.semanticProvider.GetTableContext(ctx, table)
		if err != nil {
			slog.Debug("trino_export: failed to get table context for sensitivity check",
				"table", table.String(), logKeyError, err)
			continue
		}
		if tableCtx == nil {
			continue
		}
		for _, tag := range tableCtx.Tags {
			lower := strings.ToLower(tag)
			if isSensitivityTag(lower) && !seen[lower] {
				seen[lower] = true
				sysTags = append(sysTags, sysTagPrefix+"classification:"+lower)
			}
		}
	}

	return sysTags
}

// isSensitivityTag checks if a tag indicates sensitive data.
func isSensitivityTag(tag string) bool {
	sensitivePatterns := []string{"pii", "sensitive", "confidential", "restricted", "phi", "pci"}
	for _, pattern := range sensitivePatterns {
		if strings.Contains(tag, pattern) {
			return true
		}
	}
	return false
}

// extractSourceTableNames extracts table names as strings for provenance.
func extractSourceTableNames(sql string) []string {
	tables := extractTablesFromSQL(sql)
	names := make([]string, len(tables))
	for i, t := range tables {
		names[i] = t.String()
	}
	return names
}

// exportProvenanceParams holds parameters for provenance construction.
type exportProvenanceParams struct {
	userID, sessionID, sql, format string
	sourceTables                   []string
	rowCount                       int
}

// buildExportProvenance constructs provenance metadata for an exported asset.
func buildExportProvenance(ctx context.Context, deps *ExportDeps, p exportProvenanceParams) ExportProvenance {
	prov := ExportProvenance{
		UserID:    p.userID,
		SessionID: p.sessionID,
	}

	// Include session tool calls from the provenance middleware
	if deps.GetProvenanceCalls != nil {
		calls := deps.GetProvenanceCalls(ctx)
		if len(calls) > 0 {
			prov.ToolCalls = make([]ExportProvenanceCall, len(calls))
			copy(prov.ToolCalls, calls)
		}
	}

	// Record the export operation itself in provenance
	prov.ToolCalls = append(prov.ToolCalls, ExportProvenanceCall{
		ToolName:  exportToolName,
		Timestamp: time.Now().Format(time.RFC3339),
		Parameters: map[string]any{
			"export_query":  p.sql,
			"source_tables": p.sourceTables,
			"format":        p.format,
			"row_count":     p.rowCount,
		},
	})

	return prov
}

// parseExportInput parses the MCP request into an exportInput.
func parseExportInput(req mcp.CallToolRequest) (exportInput, error) {
	if req.Params == nil || len(req.Params.Arguments) == 0 {
		return exportInput{}, fmt.Errorf("missing arguments")
	}
	var input exportInput
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return exportInput{}, fmt.Errorf("parsing arguments: %w", err)
	}
	return input, nil
}

// validateExportInput validates all input fields.
func validateExportInput(input exportInput, cfg ExportConfig) error {
	if input.SQL == "" {
		return fmt.Errorf("sql is required")
	}
	if input.Format == "" {
		return fmt.Errorf("format is required")
	}
	if _, err := newFormatter(input.Format); err != nil {
		return err
	}
	if input.Name == "" {
		return fmt.Errorf("name is required")
	}
	if len(input.Name) > maxExportNameLength {
		return fmt.Errorf("name exceeds %d characters", maxExportNameLength)
	}
	if len(input.Description) > maxExportDescriptionLength {
		return fmt.Errorf("description exceeds %d characters", maxExportDescriptionLength)
	}
	if err := validateExportTags(input.Tags); err != nil {
		return err
	}
	if input.Limit > cfg.MaxRows {
		return fmt.Errorf("limit %d exceeds deployment maximum of %d rows", input.Limit, cfg.MaxRows)
	}
	if input.TimeoutSeconds > int(cfg.MaxTimeout.Seconds()) {
		return fmt.Errorf("timeout_seconds %d exceeds maximum of %d", input.TimeoutSeconds, int(cfg.MaxTimeout.Seconds()))
	}
	return nil
}

// validateExportTags validates tags with stricter rules than portal defaults.
func validateExportTags(tags []string) error {
	if len(tags) > maxExportTags {
		return fmt.Errorf("too many tags: %d (max %d)", len(tags), maxExportTags)
	}
	for _, tag := range tags {
		if len(tag) > maxExportTagLength {
			return fmt.Errorf("tag %q exceeds %d characters", tag, maxExportTagLength)
		}
		if strings.HasPrefix(tag, sysTagPrefix) {
			return fmt.Errorf("tag %q uses reserved prefix %q", tag, sysTagPrefix)
		}
		if !exportTagPattern.MatchString(tag) {
			return fmt.Errorf("tag %q must be lowercase kebab-case (a-z, 0-9, hyphens)", tag)
		}
	}
	return nil
}

// sanitizeExportName normalizes a user-supplied asset name into a portable
// display string. It replaces Unicode punctuation that breaks
// Content-Disposition headers and downstream filename heuristics with ASCII
// equivalents, strips control and zero-width characters, and collapses
// runs of whitespace. The result preserves readability — letters, digits,
// spaces, and ASCII punctuation are kept as-is.
func sanitizeExportName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if replacement, ok := nameCharReplacements[r]; ok {
			b.WriteString(replacement)
			continue
		}
		if unicode.IsControl(r) || isZeroWidth(r) {
			continue
		}
		b.WriteRune(r)
	}

	return strings.TrimSpace(repeatedWhitespace.ReplaceAllString(b.String(), " "))
}

// isZeroWidth reports whether r is a zero-width or invisible formatting rune
// that should be stripped from display names. Covers zero-width spacing
// chars, directional marks and embedding controls, invisible math operators,
// and Unicode variation selectors.
func isZeroWidth(r rune) bool {
	switch r {
	case '\u200B', // zero-width space
		'\u200C', // zero-width non-joiner
		'\u200D', // zero-width joiner
		'\u200E', // left-to-right mark
		'\u200F', // right-to-left mark
		'\u2060', // word joiner
		'\u2061', // function application
		'\u2062', // invisible times
		'\u2063', // invisible separator
		'\u2064', // invisible plus
		'\uFEFF': // BOM / zero-width no-break space
		return true
	}
	// Bidi embedding/override controls (U+202A..U+202E).
	if r >= '\u202A' && r <= '\u202E' {
		return true
	}
	// Variation selectors (U+FE00..U+FE0F).
	if r >= '\uFE00' && r <= '\uFE0F' {
		return true
	}
	return false
}

// sanitizeUserIDPath returns a path-safe representation of a user ID for use
// as an S3 object key segment. Subjects from OIDC or API keys may contain
// ':', '@', '/', or other characters that produce non-portable keys (rejected
// by stricter object stores like MinIO). All non-[A-Za-z0-9._-] characters
// are replaced with '_'. An all-dots result ('.', '..', '...', etc.) is
// replaced with '_' so path.Clean cannot interpret the segment as path
// navigation and escape the configured prefix.
func sanitizeUserIDPath(userID string) string {
	if userID == "" {
		return "_"
	}
	cleaned := userIDPathSafe.ReplaceAllString(userID, "_")
	if strings.Trim(cleaned, ".") == "" {
		return "_"
	}
	return cleaned
}

// buildExportS3Key composes the S3 object key for an exported asset, ensuring
// the result is portable across S3-compatible backends. path.Join collapses
// redundant slashes (e.g., when prefix has a trailing '/'), the user ID
// segment is sanitized to remove characters that some backends reject, and
// any leading '/' is trimmed because MinIO rejects keys that start with '/'.
func buildExportS3Key(prefix, userID, assetID, extension string) string {
	key := path.Join(prefix, sanitizeUserIDPath(userID), assetID, "content"+extension)
	return strings.TrimPrefix(key, "/")
}

// generateExportID generates a cryptographically random hex ID.
func generateExportID() (string, error) {
	b := make([]byte, exportIDLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// buildPortalURL constructs the portal URL for an asset.
func buildPortalURL(baseURL, assetID string) string {
	if baseURL == "" {
		return ""
	}
	return baseURL + "/portal/assets/" + assetID
}

// exportError returns an error result to the agent.
func exportError(msg string) *mcp.CallToolResult {
	errObj := struct {
		Error string `json:"error"`
	}{Error: msg}
	data, _ := json.Marshal(errObj) //nolint:errcheck // simple struct
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(data)},
		},
		IsError: true,
	}
}

// exportSuccess returns a success result to the agent.
func exportSuccess(out exportOutput) *mcp.CallToolResult {
	data, _ := json.Marshal(out) //nolint:errcheck // simple struct
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(data)},
		},
	}
}

// exportInputSchema returns the JSON Schema for trino_export input.
func exportInputSchema() map[string]any {
	return map[string]any{
		schemaKeyType: schemaTypeObject,
		"properties": map[string]any{
			"sql": map[string]any{
				schemaKeyType: schemaTypeString,
				schemaKeyDesc: "The SQL query to execute. Must be read-only (SELECT). Validate the query shape with trino_query first.",
			},
			"connection": map[string]any{
				schemaKeyType: schemaTypeString,
				schemaKeyDesc: "Trino connection name (optional, uses default if not specified).",
			},
			"format": map[string]any{
				schemaKeyType: schemaTypeString,
				"enum":        []string{"csv", "json", "markdown", "text"},
				schemaKeyDesc: "Output format for the exported data.",
			},
			"name": map[string]any{
				schemaKeyType: schemaTypeString,
				schemaKeyDesc: "Display name for the exported asset; also used as the download filename. " +
					"Use ASCII letters, digits, spaces, hyphens, and dots. " +
					"Em/en dashes, smart quotes, ellipses, and other Unicode punctuation are auto-normalized to ASCII.",
				"maxLength": maxExportNameLength,
			},
			"description": map[string]any{
				schemaKeyType: schemaTypeString,
				schemaKeyDesc: "Description of the exported asset.",
				"maxLength":   maxExportDescriptionLength,
			},
			"tags": map[string]any{
				schemaKeyType: schemaTypeArray,
				"items":       map[string]any{schemaKeyType: schemaTypeString},
				schemaKeyDesc: "Tags for categorization. Lowercase kebab-case, max 50 chars each, max 20 tags. Tags starting with _sys- are reserved.",
			},
			"limit": map[string]any{
				schemaKeyType: schemaTypeInteger,
				schemaKeyDesc: "Maximum number of rows to export. Subject to deployment cap.",
			},
			"idempotency_key": map[string]any{
				schemaKeyType: schemaTypeString,
				schemaKeyDesc: "Client-supplied key to prevent duplicate assets on retry.",
			},
			"timeout_seconds": map[string]any{
				schemaKeyType: schemaTypeInteger,
				schemaKeyDesc: "Query execution timeout in seconds.",
			},
			"create_public_link": map[string]any{
				schemaKeyType: "boolean",
				schemaKeyDesc: "Generate a public share link for the exported asset. Useful for automation pipelines that need a shareable URL.",
			},
		},
		"required": []string{"sql", "format", "name"},
	}
}
