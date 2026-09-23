package trino //nolint:revive // adapter types for cross-package wiring

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"

	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// Schema key/type constants to satisfy revive add-constant.
const (
	schemaTypeString  = "string"
	schemaTypeObject  = "object"
	schemaTypeArray   = "array"
	schemaTypeInteger = "integer"
	schemaKeyType     = "type"
	schemaKeyDesc     = "description"

	// Export schema property names and provenance keys. Defined as
	// constants because each appears multiple times across the schema
	// definition, parameter parsing, and provenance output.
	propProperties = "properties"
	propSQL        = "sql"
	propConnection = "connection"
	propFormat     = "format"
	propName       = "name"
	propTags       = "tags"
	propResource   = "resource"
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
	// RunOutputKey, set when a managed-script run made the call, turns the
	// export's name into the script's output identity, so a named export
	// inside a run writes the next version of one asset (#1854). Nil
	// otherwise.
	RunOutputKey func(name string) string
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
	// ResourceLander lands a result in a managed resource by path instead of in
	// a new asset (#1663). nil leaves the asset destination the only one, which
	// is what a deployment with no managed-resource library has.
	ResourceLander toolkit.ResourceLander
	S3Bucket       string
	S3Prefix       string
	BaseURL        string
	Config         ExportConfig

	// GetUserContext extracts user identity from the request context.
	// Injected by the platform to avoid importing middleware.
	GetUserContext func(ctx context.Context) *ExportUserContext
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
	// Resource, when set, lands the formatted result in the managed resource at
	// that path instead of in a new portal asset (#1663).
	Resource *toolkit.ResourceDestination `json:"resource,omitempty"`
}

// exportOutput is the response returned to the agent.
type exportOutput struct {
	AssetID string `json:"asset_id,omitempty"`
	// AssetVersion is the version this export wrote: 1 for a new asset, the
	// next one for a named export a script run repeats (#1854).
	AssetVersion int    `json:"asset_version,omitempty"`
	PortalURL    string `json:"portal_url,omitempty"`
	ShareURL     string `json:"share_url,omitempty"`
	Format       string `json:"format"`
	RowCount     int    `json:"row_count"`
	SizeBytes    int64  `json:"size_bytes"`
	// Resource is where a resource destination landed the result (#1663): the
	// reference and uri to hand to the next call, the version written, and what
	// the write did to the tables registered over the file. Set instead of
	// asset_id, never beside it.
	Resource *toolkit.ResourceLanding `json:"resource,omitempty"`
	Message  string                   `json:"message"`
}

// SetExportDeps injects portal dependencies for trino_export.
func (t *Toolkit) SetExportDeps(deps ExportDeps) {
	deps.Config = applyExportDefaults(deps.Config)
	t.exportDeps = &deps
}

// registerExportTool registers trino_export on the MCP server through the
// generic mcp.AddTool, so the SDK validates the arguments against
// exportInputSchema and writes the handler's output value as the structured
// result. The blocks the platform appends (the call reference, #1416) merge
// into that result; a tool with no structured result keeps them in content as
// a second text block, which is how trino_export reached a running deployment
// before (#1589).
func (t *Toolkit) registerExportTool(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  exportToolName,
		Title: "Export Query Results",
		Description: "Export query results directly to a portal asset file (CSV, JSON, Markdown, or text). " +
			"Use ONLY after you have validated the query shape with trino_query using a small LIMIT. " +
			"Do NOT use this for data exploration. " +
			"Pass `resource` to land the result in a MANAGED RESOURCE at a path instead of a new asset: the same " +
			"path next time is the NEXT VERSION of that one file, keeping its id, its mcp:// URI, the assets that " +
			"reference it and the tables registered over it. That is the destination for a recurring export. " +
			toolkit.ResourceLandingResultSentence + " A row count is reported beside the asset metadata. " +
			"NAMING: keep `name` short and portable, using only ASCII letters, digits, spaces, hyphens, and dots. " +
			"Avoid em/en dashes, smart quotes, ellipses, and other Unicode punctuation; they will be normalized to ASCII. " +
			"The name doubles as the download filename.",
		InputSchema: exportInputSchema(),
		// An export lands a new asset, or the next version of a managed
		// resource with the earlier versions kept, so it only adds.
		Annotations: toolkit.WriteAnnotations(false),
	}, t.handleExport)
}

// handleExport is the MCP tool handler for trino_export. The SDK has already
// validated the arguments against exportInputSchema and decoded them into in.
// A success returns its exportOutput as the structured result; a refusal
// returns an in-band error result and no structured value.
func (t *Toolkit) handleExport(ctx context.Context, _ *mcp.CallToolRequest, in exportInput) (*mcp.CallToolResult, any, error) {
	deps := t.exportDeps
	if deps == nil {
		return exportError("trino_export is not configured"), nil, nil
	}

	input, uc, errResult := t.validateAndPrepare(ctx, in, deps)
	if errResult != nil {
		return errResult, nil, nil
	}

	if input.Resource != nil {
		if denial := checkResourceDestination(ctx, deps, input); denial != nil {
			return denial, nil, nil
		}
	}

	// Idempotency check
	if input.IdempotencyKey != "" {
		if hit := t.checkIdempotency(ctx, deps, uc, input); hit != nil {
			return exportSuccess(hit)
		}
	}

	out, errResult := t.executeAndPersist(ctx, deps, input, uc)
	if errResult != nil {
		return errResult, nil, nil
	}
	return exportSuccess(out)
}

// validateAndPrepare validates the decoded input, enforces read-only, and extracts user context.
func (*Toolkit) validateAndPrepare(ctx context.Context, input exportInput, deps *ExportDeps) (exportInput, *ExportUserContext, *mcp.CallToolResult) {
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

// checkIdempotency returns the existing asset's output if the idempotency key already exists.
func (*Toolkit) checkIdempotency(ctx context.Context, deps *ExportDeps, uc *ExportUserContext, input exportInput) *exportOutput {
	existing, lookupErr := deps.AssetStore.GetByIdempotencyKey(ctx, uc.UserID, input.IdempotencyKey)
	if lookupErr == nil && existing != nil {
		return existingExportOutput(deps, existing, input)
	}
	return nil
}

// existingExportOutput is the output for an asset an earlier call with the
// same idempotency key already wrote.
func existingExportOutput(deps *ExportDeps, existing *ExportAssetRef, input exportInput) *exportOutput {
	return &exportOutput{
		AssetID:   existing.ID,
		PortalURL: buildPortalURL(deps.BaseURL, existing.ID),
		Format:    input.Format,
		RowCount:  0,
		SizeBytes: existing.SizeBytes,
		Message:   "Asset already exists (idempotency key matched).",
	}
}

// executeAndPersist runs the query, formats, uploads to S3, and saves the asset
// record. It returns the output on success, or the error result to hand back.
func (t *Toolkit) executeAndPersist(ctx context.Context, deps *ExportDeps, input exportInput, uc *ExportUserContext) (*exportOutput, *mcp.CallToolResult) {
	timeout, limit := resolveExportLimits(input, deps.Config)

	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Parquet is written from the driver's own values, the ones every other
	// format is written from the JSON-friendly form of: a TIMESTAMP(6) keeps
	// its microseconds and a VARBINARY its bytes (#1833).
	typed := input.Format == formatParquet
	result, err := t.executeExportQuery(queryCtx, input.SQL, input.Connection, trinoclient.QueryOptions{
		Limit: limit, RawValues: typed,
	})
	if err != nil {
		return nil, exportError(fmt.Sprintf("query execution failed: %v", err))
	}

	rows := queryRows(result)

	out, errResult := formatQueryResult(input.Format, result, rows, deps.Config.MaxBytes)
	if errResult != nil {
		return nil, errResult
	}
	formatted, formatter, note := out.body, out.formatter, out.note

	sysTags := t.inheritSensitivityTags(ctx, input.SQL)
	allTags := make([]string, 0, len(input.Tags)+len(sysTags))
	allTags = append(allTags, input.Tags...)
	allTags = append(allTags, sysTags...)

	if input.Resource != nil {
		return t.landExport(ctx, deps, input, landedResult{
			body: formatted, contentType: formatter.ContentType(), tags: allTags, rowCount: len(rows), note: note,
		})
	}

	assetID, err := generateExportID()
	if err != nil {
		return nil, exportError(fmt.Sprintf("generating asset ID: %v", err))
	}

	s3Key := buildExportS3Key(deps.S3Prefix, uc.UserID, assetID, formatter.FileExtension())

	if err := deps.S3Client.PutObject(ctx, deps.S3Bucket, s3Key, formatted, formatter.ContentType()); err != nil {
		return nil, exportError(fmt.Sprintf("S3 upload failed: %v", err))
	}

	sourceTables := extractSourceTableNames(input.SQL)
	prov := buildExportProvenance(exportProvenanceParams{
		userID:       uc.UserID,
		sessionID:    uc.SessionID,
		sql:          input.SQL,
		sourceTables: sourceTables,
		connection:   input.Connection,
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

	recorded, hit, errResult := t.storeAsset(ctx, deps, asset, input, uc)
	if hit != nil || errResult != nil {
		return hit, errResult
	}
	assetID, version := recorded.assetID, recorded.version

	shareURL := t.maybeCreateShare(ctx, deps, input, assetID, uc.UserEmail)

	return &exportOutput{
		AssetID:      assetID,
		AssetVersion: version,
		PortalURL:    buildPortalURL(deps.BaseURL, assetID),
		ShareURL:     shareURL,
		Format:       input.Format,
		RowCount:     len(rows),
		SizeBytes:    int64(len(formatted)),
		Message:      strings.Join(nonEmpty(fmt.Sprintf("Exported %d rows as %s.", len(rows), input.Format), note), " "),
	}, nil
}

// storeAsset records the uploaded export: under the script's output identity
// when a run made a named export (the next version of one asset, #1854), and
// otherwise as a new asset and its first version, as trino_export always has.
func (t *Toolkit) storeAsset(ctx context.Context, deps *ExportDeps, asset ExportAsset, input exportInput, uc *ExportUserContext) (
	recorded storedAsset, hit *exportOutput, errResult *mcp.CallToolResult,
) {
	version0 := ExportVersion{
		S3Key: asset.S3Key, ContentType: asset.ContentType, SizeBytes: asset.SizeBytes, CreatedBy: uc.UserEmail,
	}
	if key := runOutputKey(uc, input); key != "" {
		id, version, err := toolkit.PersistRunAsset(ctx, key, asset.ID, toolkit.RunAssetWrite{
			Lookup: func(ctx context.Context, key string) (string, bool) {
				ref, err := deps.AssetStore.GetByIdempotencyKey(ctx, asset.OwnerID, key)
				if err != nil || ref == nil {
					return "", false
				}
				return ref.ID, true
			},
			Insert: func(ctx context.Context, key string) error {
				asset.IdempotencyKey = key
				return deps.AssetStore.InsertExportAsset(ctx, asset)
			},
			Version: func(ctx context.Context, id string) (int, error) {
				ver := version0
				ver.AssetID, ver.S3Bucket, ver.ChangeSummary = id, deps.S3Bucket, "Exported from Trino query"
				var err error
				if ver.ID, err = generateExportID(); err != nil {
					return 0, err
				}
				return deps.VersionStore.CreateExportVersion(ctx, ver)
			},
		})
		if err != nil {
			return storedAsset{}, nil, exportError(err.Error())
		}
		return storedAsset{assetID: id, version: version}, nil, nil
	}
	if hit, errResult := t.insertAssetWithRace(ctx, deps, asset, input, uc); hit != nil || errResult != nil {
		return storedAsset{}, hit, errResult
	}
	version0.AssetID = asset.ID
	t.createExportVersion(ctx, deps, version0)
	return storedAsset{assetID: asset.ID, version: 1}, nil, nil
}

// storedAsset is the asset and version an export recorded.
type storedAsset struct {
	assetID string
	version int
}

// runOutputKey is the script output identity a named export made inside a run
// writes under, or "" when the call is not a run's, names nothing, or carries
// its own idempotency key, which keeps its own meaning.
func runOutputKey(uc *ExportUserContext, input exportInput) string {
	if uc.RunOutputKey == nil || input.Name == "" || input.IdempotencyKey != "" {
		return ""
	}
	return uc.RunOutputKey(input.Name)
}

// insertAssetWithRace inserts the asset record. When the insert loses an
// idempotency race to a concurrent call with the same key, it returns that
// call's asset as the output; any other failure is returned as an error result.
func (*Toolkit) insertAssetWithRace(ctx context.Context, deps *ExportDeps, asset ExportAsset, input exportInput, uc *ExportUserContext) (*exportOutput, *mcp.CallToolResult) {
	err := deps.AssetStore.InsertExportAsset(ctx, asset)
	if err == nil {
		return nil, nil
	}
	if input.IdempotencyKey != "" {
		if existing, lookupErr := deps.AssetStore.GetByIdempotencyKey(ctx, uc.UserID, input.IdempotencyKey); lookupErr == nil && existing != nil {
			return existingExportOutput(deps, existing, input), nil
		}
	}
	return nil, exportError(fmt.Sprintf("saving asset record: %v", err))
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

// queryRows converts a trinoclient.QueryResult's rows into positional values,
// in the order of its columns.
func queryRows(result *trinoclient.QueryResult) [][]any {
	rows := make([][]any, len(result.Rows))
	for i, row := range result.Rows {
		vals := make([]any, len(result.Columns))
		for j, col := range result.Columns {
			vals[j] = row[col.Name]
		}
		rows[i] = vals
	}
	return rows
}

// formattedExport is a query's rows written in the requested format: the bytes,
// the formatter that wrote them, and what a typed format could not keep of the
// query's columns.
type formattedExport struct {
	body      []byte
	formatter Formatter
	note      string
}

// formatQueryResult formats a query's rows in the requested format and checks
// the byte cap. A typed format is handed the types Trino reported, and says
// what it could not keep of them in the note.
func formatQueryResult(
	format string, result *trinoclient.QueryResult, rows [][]any, maxBytes int64,
) (formattedExport, *mcp.CallToolResult) {
	formatter, err := newFormatter(format)
	if err != nil {
		return formattedExport{}, exportError(err.Error())
	}
	// Whether a format takes the column types is the format's own answer, not
	// a name this function knows: a formatter that implements TypedFormatter
	// is handed them.
	typedFormatter, ok := formatter.(TypedFormatter)
	if !ok {
		columns := make([]string, len(result.Columns))
		for i, c := range result.Columns {
			columns[i] = c.Name
		}
		body, f, errResult := formatExportResult(format, columns, rows, maxBytes)
		return formattedExport{body: body, formatter: f}, errResult
	}
	typed, err := columnTypes(result.Columns)
	if err != nil {
		return formattedExport{}, exportError(fmt.Sprintf("formatting failed: %v", err))
	}
	body, err := typedFormatter.FormatTyped(typed, rows)
	if err != nil {
		return formattedExport{}, exportError(fmt.Sprintf("formatting failed: %v", err))
	}
	if int64(len(body)) > maxBytes {
		return formattedExport{}, exportError(fmt.Sprintf(
			"formatted output (%d bytes) exceeds deployment maximum of %d bytes", len(body), maxBytes))
	}
	return formattedExport{body: body, formatter: typedFormatter, note: storageNote(typed)}, nil
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
func (t *Toolkit) executeExportQuery(
	ctx context.Context, sql, connection string, opts trinoclient.QueryOptions,
) (*trinoclient.QueryResult, error) {
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
		return nil, errors.New("no Trino client available")
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
	userID, sessionID, sql, format, connection string
	sourceTables                               []string
	rowCount                                   int
}

// buildExportProvenance records the export call itself.
//
// It states only what this call knows: the platform resolves the rest of the
// asset's sources from the audit log when the asset is written (#1320), and
// this call's own audit row does not exist yet — it is written after the tool
// returns — so the export states its own statement here.
func buildExportProvenance(p exportProvenanceParams) ExportProvenance {
	prov := ExportProvenance{
		UserID:    p.userID,
		SessionID: p.sessionID,
	}

	prov.ToolCalls = append(prov.ToolCalls, ExportProvenanceCall{
		ToolName:  exportToolName,
		Timestamp: time.Now().Format(time.RFC3339),
		Parameters: map[string]any{
			"export_query":  p.sql,
			"source_tables": p.sourceTables,
			propConnection:  p.connection,
			propFormat:      p.format,
			"row_count":     p.rowCount,
		},
	})

	return prov
}

// validateExportInput validates all input fields.
func validateExportInput(input exportInput, cfg ExportConfig) error {
	if input.SQL == "" {
		return errors.New("sql is required")
	}
	if input.Format == "" {
		return errors.New("format is required")
	}
	if _, err := newFormatter(input.Format); err != nil {
		return err
	}
	if input.Name == "" {
		return errors.New("name is required")
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

// exportSuccess returns a success result to the agent: the output as one JSON
// text block, and the same value for the SDK to write as the structured result.
func exportSuccess(out *exportOutput) (*mcp.CallToolResult, any, error) {
	data, _ := json.Marshal(out) //nolint:errcheck // simple struct
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(data)},
		},
	}, out, nil
}

// exportInputSchema returns the JSON Schema for trino_export input.
func exportInputSchema() map[string]any {
	return map[string]any{
		schemaKeyType: schemaTypeObject,
		// Closed to unknown arguments: a misnamed field is refused by name
		// rather than dropped (issue #1057). Enforced by the SDK, which
		// validates every call against this schema before the handler runs.
		// The platform-injected session_id argument never reaches that check:
		// the session resolver strips it in middleware.
		"additionalProperties": false,
		propProperties: map[string]any{
			propSQL: map[string]any{
				schemaKeyType: schemaTypeString,
				schemaKeyDesc: "The SQL query to execute. Must be read-only (SELECT). Validate the query shape with trino_query first.",
			},
			"connection": map[string]any{
				schemaKeyType: schemaTypeString,
				schemaKeyDesc: "Trino connection name (optional, uses default if not specified).",
			},
			propFormat: map[string]any{
				schemaKeyType: schemaTypeString,
				"enum":        []string{formatCSV, formatJSON, formatJSONL, formatMarkdown, formatParquet, formatText},
				schemaKeyDesc: "Output format for the exported data. parquet writes a typed, compressed, columnar file " +
					"with every column as its own Trino type -- DECIMAL, TIMESTAMP to the microsecond, ARRAY, MAP and " +
					"ROW included -- and registers with manage_table as the same types; a timestamp with a time zone " +
					"is kept as its instant in UTC, and a type Parquet has no form for (TIME, UUID, JSON, an " +
					"interval) as text. jsonl writes one JSON object per row: a line break, a backslash or a null " +
					"inside a value survives it, and its columns are typed from the values when registered, with a " +
					"nested value written as its JSON text. csv cannot carry a line break inside a cell and reads a " +
					"null back as an empty string.",
			},
			propName: map[string]any{
				schemaKeyType: schemaTypeString,
				schemaKeyDesc: "Display name for the exported asset, or for the managed resource a resource " +
					"destination lands in; also used as the download filename for an asset. " +
					"Use ASCII letters, digits, spaces, hyphens, and dots. " +
					"Em/en dashes, smart quotes, ellipses, and other Unicode punctuation are auto-normalized to ASCII.",
				"maxLength": maxExportNameLength,
			},
			schemaKeyDesc: map[string]any{
				schemaKeyType: schemaTypeString,
				schemaKeyDesc: "Description of the exported asset.",
				"maxLength":   maxExportDescriptionLength,
			},
			propTags: map[string]any{
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
			propResource: resourceDestinationSchema(),
		},
		"required": []string{propSQL, propFormat, propName},
	}
}
