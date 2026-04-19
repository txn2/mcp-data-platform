package trino //nolint:revive // adapter types for cross-package wiring

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

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
)

// exportTagPattern validates lowercase kebab-case tags.
var exportTagPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

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
	SQL            string   `json:"sql"`
	Connection     string   `json:"connection"`
	Format         string   `json:"format"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Tags           []string `json:"tags"`
	Limit          int      `json:"limit"`
	IdempotencyKey string   `json:"idempotency_key"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

// exportOutput is the response returned to the agent.
type exportOutput struct {
	AssetID   string `json:"asset_id"`
	PortalURL string `json:"portal_url,omitempty"`
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
			"Returns asset metadata (ID, URL, row count, size) — the data is NOT returned through this response.",
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
func (t *Toolkit) validateAndPrepare(ctx context.Context, req *mcp.CallToolRequest, deps *ExportDeps) (exportInput, *ExportUserContext, *mcp.CallToolResult) {
	input, err := parseExportInput(*req)
	if err != nil {
		return exportInput{}, nil, exportError(err.Error())
	}
	if err := validateExportInput(input, deps.Config); err != nil {
		return exportInput{}, nil, exportError(err.Error())
	}
	if t.config.ReadOnly {
		interceptor := NewReadOnlyInterceptor()
		if _, interceptErr := interceptor.Intercept(ctx, input.SQL, ""); interceptErr != nil {
			return exportInput{}, nil, exportError(interceptErr.Error())
		}
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
	timeout, limit, errResult := resolveExportLimits(input, deps.Config)
	if errResult != nil {
		return errResult, nil
	}

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

	s3Key := fmt.Sprintf("%s/%s/%s/content%s", deps.S3Prefix, uc.UserID, assetID, formatter.FileExtension())

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

	if err := deps.AssetStore.InsertExportAsset(ctx, asset); err != nil {
		return exportError(fmt.Sprintf("saving asset record: %v", err)), nil
	}

	t.createExportVersion(ctx, deps, ExportVersion{
		AssetID:     assetID,
		S3Key:       s3Key,
		ContentType: formatter.ContentType(),
		SizeBytes:   int64(len(formatted)),
		CreatedBy:   uc.UserEmail,
	})

	return exportSuccess(exportOutput{
		AssetID:   assetID,
		PortalURL: buildPortalURL(deps.BaseURL, assetID),
		Format:    input.Format,
		RowCount:  len(rows),
		SizeBytes: int64(len(formatted)),
		Message:   fmt.Sprintf("Exported %d rows as %s.", len(rows), input.Format),
	}), nil
}

// resolveExportLimits resolves timeout and row limit from input and config.
func resolveExportLimits(input exportInput, cfg ExportConfig) (time.Duration, int, *mcp.CallToolResult) {
	timeout := cfg.DefaultTimeout
	if input.TimeoutSeconds > 0 {
		requested := time.Duration(input.TimeoutSeconds) * time.Second
		if requested > cfg.MaxTimeout {
			return 0, 0, exportError(fmt.Sprintf("timeout_seconds exceeds maximum of %d", int(cfg.MaxTimeout.Seconds())))
		}
		timeout = requested
	}
	limit := cfg.MaxRows
	if input.Limit > 0 {
		if input.Limit > cfg.MaxRows {
			return 0, 0, exportError(fmt.Sprintf("limit exceeds deployment maximum of %d rows", cfg.MaxRows))
		}
		limit = input.Limit
	}
	return timeout, limit, nil
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
		slog.Warn("trino_export: failed to generate version ID", "error", err, "asset_id", ver.AssetID)
		return
	}
	ver.ID = versionID
	ver.S3Bucket = deps.S3Bucket
	ver.ChangeSummary = "Exported from Trino query"
	if _, err := deps.VersionStore.CreateExportVersion(ctx, ver); err != nil {
		slog.Warn("trino_export: failed to create version record", "error", err, "asset_id", ver.AssetID)
	}
}

// executeExportQuery runs the SQL against the Trino client.
func (t *Toolkit) executeExportQuery(ctx context.Context, sql, connection string, limit int) (*trinoclient.QueryResult, error) {
	opts := trinoclient.QueryOptions{
		Limit: limit,
	}

	// In multi-connection mode, resolve the correct client
	if t.manager != nil && connection != "" {
		client, err := t.manager.Client(connection)
		if err != nil {
			return nil, fmt.Errorf("resolving connection %q: %w", connection, err)
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
				"table", table.String(), "error", err)
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
				schemaKeyDesc: "Display name for the exported asset.",
				"maxLength":   maxExportNameLength,
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
		},
		"required": []string{"sql", "format", "name"},
	}
}
