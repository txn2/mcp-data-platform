package graphql //nolint:revive // the export DTOs are adapter types for cross-package wiring: each kind declares its own so no toolkit imports another, which is what puts more than five in this file (see internal/platform/exportadapters)

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// Export defaults, applied when the platform wires no explicit limits.
const (
	defaultExportMaxBytes   = int64(100 * 1024 * 1024)
	defaultExportTimeout    = 5 * time.Minute
	defaultMaxExportTimeout = 10 * time.Minute
	// exportContentType is what a GraphQL result is written as. A
	// GraphQL response is JSON by definition, so unlike an HTTP
	// response there is nothing to detect.
	exportContentType = "application/json"
)

// The export dependency types are declared here rather than shared with
// the API gateway's for the reason stated in
// internal/platform/exportadapters: a toolkit kind must not import
// another kind, so each declares the slice of the portal it needs and
// the platform adapts its stores onto all of them.

// ExportAssetStore is the subset of the portal asset store graphql_export
// writes through.
type ExportAssetStore interface {
	InsertExportAsset(ctx context.Context, asset ExportAsset) error
	GetByIdempotencyKey(ctx context.Context, ownerID, key string) (*ExportAssetRef, error)
}

// ExportVersionStore is the subset of the portal version store
// graphql_export writes through.
type ExportVersionStore interface {
	CreateExportVersion(ctx context.Context, version ExportVersion) (int, error)
}

// ExportS3Client is the object storage graphql_export writes to.
type ExportS3Client interface {
	// PutObjectStream uploads body to bucket/key, returning the bytes
	// written.
	PutObjectStream(ctx context.Context, bucket, key string, body io.Reader, contentType string) (size int64, err error)
}

// ExportShareCreator creates a public share link for an exported asset.
// nil disables public-link creation.
type ExportShareCreator interface {
	CreatePublicShare(ctx context.Context, assetID, createdBy string) (shareURL string, err error)
}

// ExportAsset is the row inserted into the portal's assets when a
// graphql_export call succeeds.
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

// ExportProvenance records the call that produced an asset, so a portal
// viewer can render where its content came from.
type ExportProvenance struct {
	ToolCalls []ExportProvenanceCall
	SessionID string
	UserID    string
}

// ExportProvenanceCall is one step in the provenance chain.
type ExportProvenanceCall struct {
	ToolName   string
	Timestamp  string
	Parameters map[string]any
}

// ExportAssetRef is what an idempotency-key lookup returns.
type ExportAssetRef struct {
	ID        string
	SizeBytes int64
}

// ExportVersion is the row inserted into the portal's asset versions.
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

// ExportConfig holds the platform-level limits for graphql_export.
type ExportConfig struct {
	MaxBytes       int64
	DefaultTimeout time.Duration
	MaxTimeout     time.Duration
}

// ExportUserContext is the caller's identity, supplied by the platform
// through a callback so the toolkit does not import the middleware.
type ExportUserContext struct {
	UserID    string
	UserEmail string
	SessionID string
}

// ExportDeps holds the platform-side dependencies graphql_export needs.
// A nil AssetStore is export disabled: the tool is not registered, so
// the model never sees one it could not successfully call.
type ExportDeps struct {
	AssetStore     ExportAssetStore
	VersionStore   ExportVersionStore
	S3Client       ExportS3Client
	ShareCreator   ExportShareCreator
	S3Bucket       string
	S3Prefix       string
	BaseURL        string
	Config         ExportConfig
	GetUserContext func(ctx context.Context) *ExportUserContext
}

// SetExportDeps wires the platform-side dependencies for
// graphql_export.
func (t *Toolkit) SetExportDeps(deps ExportDeps) {
	deps.Config = applyExportDefaults(deps.Config)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.exportDeps = &deps
}

// applyExportDefaults fills in the limits the platform left unset.
func applyExportDefaults(cfg ExportConfig) ExportConfig {
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = defaultExportMaxBytes
	}
	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = defaultExportTimeout
	}
	if cfg.MaxTimeout <= 0 {
		cfg.MaxTimeout = defaultMaxExportTimeout
	}
	return cfg
}

// exportInput is the parsed argument shape for graphql_export: the
// executing arguments graphql_query takes, plus the asset metadata.
type exportInput struct {
	Connection       string          `json:"connection"`
	Query            string          `json:"query"`
	Variables        json.RawMessage `json:"variables,omitempty"`
	OperationName    string          `json:"operation_name,omitempty"`
	TimeoutSeconds   int             `json:"timeout_seconds,omitempty"`
	Paginate         *PaginateInput  `json:"paginate,omitempty"`
	Name             string          `json:"name"`
	Description      string          `json:"description,omitempty"`
	Tags             []string        `json:"tags,omitempty"`
	IdempotencyKey   string          `json:"idempotency_key,omitempty"`
	CreatePublicLink bool            `json:"create_public_link,omitempty"`
}

// exportOutput is the asset metadata the model gets back. The data
// itself is not in it: that is the whole point of the tool.
type exportOutput struct {
	AssetID     string            `json:"asset_id"`
	PortalURL   string            `json:"portal_url,omitempty"`
	ShareURL    string            `json:"share_url,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
	SizeBytes   int64             `json:"size_bytes"`
	Operations  []string          `json:"operations,omitempty"`
	Errors      []Error           `json:"errors,omitempty"`
	Pagination  *PaginationReport `json:"pagination,omitempty"`
	Message     string            `json:"message"`
}

// registerExportTool registers graphql_export, but only when the
// platform wired its dependencies.
func (t *Toolkit) registerExportTool(s *mcp.Server) {
	t.mu.RLock()
	deps := t.exportDeps
	t.mu.RUnlock()
	if deps == nil {
		return
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:  ToolExport,
		Title: "Export a GraphQL Result",
		Description: "Run a GraphQL document and write its result into a portal asset INSTEAD of returning it " +
			"through the model context. Use this when graphql_query reports data_truncated, when you expect a " +
			"result too large to be useful through the model, or when you want to hand the data to another tool " +
			"or share it with a person. The document is parsed, validated and authorized exactly as graphql_query " +
			"validates it, and paginate walks pages the same way. Returns asset metadata (id, URL, size) — the " +
			"data is NOT in this response. NAMING: keep `name` short and portable, ASCII letters, digits, spaces, " +
			"hyphens and dots only; it doubles as the download filename.",
		InputSchema: exportSchema,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false},
	}, t.handleExport)
}

// handleExport serves graphql_export: the same gates graphql_query
// applies, then the result written to storage rather than returned.
func (t *Toolkit) handleExport(ctx context.Context, _ *mcp.CallToolRequest, in exportInput) (*mcp.CallToolResult, any, error) {
	t.mu.RLock()
	deps := t.exportDeps
	t.mu.RUnlock()
	if deps == nil {
		return toolkit.ErrorResult("graphql_export is not configured on this deployment (no portal asset store)"), nil, nil
	}
	if strings.TrimSpace(in.Name) == "" {
		return toolkit.ErrorResult("name is required (it becomes the asset's download filename)"), nil, nil
	}
	uc := resolveExportUser(ctx, deps)
	if uc == nil {
		return toolkit.ErrorResult("authentication required for graphql_export"), nil, nil
	}
	prepared, errMsg := t.prepare(ctx, in.query())
	if errMsg != "" {
		return toolkit.ErrorResult(errMsg), nil, nil
	}
	if existing := checkExportIdempotency(ctx, deps, uc, in); existing != nil {
		return toolkit.JSONResult(existing), existing, nil
	}
	exportCtx, cancel := context.WithTimeout(ctx, resolveExportTimeout(in.TimeoutSeconds, deps.Config))
	defer cancel()

	out, err := t.runExport(exportCtx, deps, uc, prepared, in)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	return toolkit.JSONResult(out), out, nil
}

// query projects an export's executing arguments onto the query input,
// so both tools go through one prepare step and cannot diverge on what
// they validate or authorize.
func (in exportInput) query() QueryInput {
	return QueryInput{
		Connection:    in.Connection,
		Query:         in.Query,
		Variables:     in.Variables,
		OperationName: in.OperationName,
		Paginate:      in.Paginate,
	}
}

// runExport executes the document and persists its result.
func (t *Toolkit) runExport(ctx context.Context, deps *ExportDeps, uc *ExportUserContext, p prepared, in exportInput) (*exportOutput, error) {
	result, err := t.runPrepared(ctx, p, in.Paginate)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(exportPayload{Data: result.Data, Errors: result.Errors, Extensions: result.Extensions})
	if err != nil {
		return nil, fmt.Errorf("graphql: encoding the result: %w", err)
	}
	// All or nothing: a partial asset would read as a complete answer
	// to whoever opens it later.
	if int64(len(payload)) > deps.Config.MaxBytes {
		return nil, fmt.Errorf("the result (%d bytes) exceeds the graphql_export cap of %d bytes — narrow the selection or the page size, or ask an administrator to raise platform.export.max_bytes",
			len(payload), deps.Config.MaxBytes)
	}
	assetID, size, err := t.persist(ctx, deps, uc, in, payload)
	if err != nil {
		return nil, err
	}
	return &exportOutput{
		AssetID:     assetID,
		PortalURL:   buildExportPortalURL(deps.BaseURL, assetID),
		ShareURL:    maybeCreateExportShare(ctx, deps, in, assetID, uc.UserEmail),
		ContentType: exportContentType,
		SizeBytes:   size,
		Operations:  result.Operations,
		Errors:      result.Errors,
		Pagination:  result.Pagination,
		Message:     fmt.Sprintf("Exported %d bytes from connection %s.", size, in.Connection),
	}, nil
}

// exportPayload is what an exported asset holds: the GraphQL response
// itself, so whoever opens the asset reads the shape the endpoint
// answered with rather than a platform envelope around it.
type exportPayload struct {
	Data       json.RawMessage `json:"data,omitempty"`
	Errors     []Error         `json:"errors,omitempty"`
	Extensions map[string]any  `json:"extensions,omitempty"`
}

// persist writes the payload to storage and records the asset and its
// first version. A version-row failure is not fatal: the asset row is
// already in place and the caller has an id, and failing the call would
// orphan the stored object.
func (*Toolkit) persist(ctx context.Context, deps *ExportDeps, uc *ExportUserContext, in exportInput, payload []byte) (assetID string, size int64, err error) {
	assetID, err = generateExportAssetID()
	if err != nil {
		return "", 0, fmt.Errorf("graphql: generating asset id: %w", err)
	}
	s3Key := buildExportS3Key(deps.S3Prefix, uc.UserID, assetID)
	size, err = deps.S3Client.PutObjectStream(ctx, deps.S3Bucket, s3Key, bytes.NewReader(payload), exportContentType)
	if err != nil {
		return "", 0, fmt.Errorf("graphql: writing the export to storage failed: %w", err)
	}
	asset := ExportAsset{
		ID: assetID, OwnerID: uc.UserID, OwnerEmail: uc.UserEmail,
		Name: in.Name, Description: in.Description, ContentType: exportContentType,
		S3Bucket: deps.S3Bucket, S3Key: s3Key, SizeBytes: size, Tags: in.Tags,
		Provenance: buildExportProvenance(uc, in), SessionID: uc.SessionID,
		IdempotencyKey: in.IdempotencyKey,
	}
	if err := deps.AssetStore.InsertExportAsset(ctx, asset); err != nil {
		return "", 0, fmt.Errorf("graphql: recording the asset failed: %w", err)
	}
	recordExportVersion(ctx, deps, asset, uc)
	return assetID, size, nil
}

// recordExportVersion inserts the asset's first version row.
func recordExportVersion(ctx context.Context, deps *ExportDeps, asset ExportAsset, uc *ExportUserContext) {
	if deps.VersionStore == nil {
		return
	}
	versionID, err := generateExportAssetID()
	if err != nil {
		slog.Warn("graphql_export: generating version id failed", "asset_id", asset.ID, logKeyError, err)
		return
	}
	_, err = deps.VersionStore.CreateExportVersion(ctx, ExportVersion{
		ID: versionID, AssetID: asset.ID, S3Key: asset.S3Key, S3Bucket: asset.S3Bucket,
		ContentType: asset.ContentType, SizeBytes: asset.SizeBytes,
		CreatedBy: uc.UserID, ChangeSummary: "Initial export",
	})
	if err != nil {
		slog.Warn("graphql_export: recording the asset version failed", "asset_id", asset.ID, logKeyError, err)
	}
}

// resolveExportUser reads the platform-injected caller identity.
// Without it the asset row would have no owner and be unreachable from
// the portal.
func resolveExportUser(ctx context.Context, deps *ExportDeps) *ExportUserContext {
	if deps.GetUserContext == nil {
		return nil
	}
	return deps.GetUserContext(ctx)
}

// checkExportIdempotency returns an existing asset when the key matches
// one this caller already produced. A failed lookup falls through to a
// fresh run: exporting twice is better than failing closed while the
// database is degraded.
func checkExportIdempotency(ctx context.Context, deps *ExportDeps, uc *ExportUserContext, in exportInput) *exportOutput {
	if in.IdempotencyKey == "" {
		return nil
	}
	existing, err := deps.AssetStore.GetByIdempotencyKey(ctx, uc.UserID, in.IdempotencyKey)
	if err != nil || existing == nil {
		return nil
	}
	return &exportOutput{
		AssetID:   existing.ID,
		PortalURL: buildExportPortalURL(deps.BaseURL, existing.ID),
		SizeBytes: existing.SizeBytes,
		Message:   "Asset already exists (idempotency key matched).",
	}
}

// maybeCreateExportShare creates a public link when the caller asked
// for one. A share failure is not fatal: the asset exists and the
// caller has its id.
func maybeCreateExportShare(ctx context.Context, deps *ExportDeps, in exportInput, assetID, createdBy string) string {
	if !in.CreatePublicLink || deps.ShareCreator == nil {
		return ""
	}
	url, err := deps.ShareCreator.CreatePublicShare(ctx, assetID, createdBy)
	if err != nil {
		slog.Warn("graphql_export: creating the public share failed", "asset_id", assetID, logKeyError, err)
		return ""
	}
	return url
}

// resolveExportTimeout bounds one export. The platform's maximum wins
// over a larger request.
func resolveExportTimeout(seconds int, cfg ExportConfig) time.Duration {
	if seconds <= 0 {
		return cfg.DefaultTimeout
	}
	requested := time.Duration(seconds) * time.Second
	if requested > cfg.MaxTimeout {
		return cfg.MaxTimeout
	}
	return requested
}

// generateExportAssetID returns a 16-byte hex id, the same format every
// other export uses so the portal's view of assets needs no per-source
// case.
func generateExportAssetID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// buildExportS3Key composes the object key:
// <prefix>/graphql_export/<user>/<asset>.json.
func buildExportS3Key(prefix, userID, assetID string) string {
	parts := []string{}
	if prefix != "" {
		parts = append(parts, strings.Trim(prefix, "/"))
	}
	parts = append(parts, "graphql_export", userID, assetID+".json")
	return path.Join(parts...)
}

// buildExportPortalURL composes the portal asset URL. An unset base URL
// yields "" and the caller gets the asset id alone.
func buildExportPortalURL(baseURL, assetID string) string {
	if baseURL == "" {
		return ""
	}
	return strings.TrimRight(baseURL, "/") + "/portal/assets/" + assetID
}

// buildExportProvenance records the export call so a portal viewer can
// render where the asset came from. The document is in it: it is the
// whole of what produced the data, and it is what someone re-running
// the export needs.
func buildExportProvenance(uc *ExportUserContext, in exportInput) ExportProvenance {
	return ExportProvenance{
		UserID:    uc.UserID,
		SessionID: uc.SessionID,
		ToolCalls: []ExportProvenanceCall{{
			ToolName:  ToolExport,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Parameters: map[string]any{
				"connection":     in.Connection,
				"query":          in.Query,
				"operation_name": in.OperationName,
			},
		}},
	}
}
