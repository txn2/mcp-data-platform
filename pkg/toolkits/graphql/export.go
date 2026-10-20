package graphql //nolint:revive // the export DTOs are adapter types for cross-package wiring: each kind declares its own so no toolkit imports another, which is what puts more than five in this file (see internal/platform/exportadapters)

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/exportasync"
	"github.com/txn2/mcp-data-platform/internal/exporttrunc"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/timeoutcap"
	"github.com/txn2/mcp-data-platform/internal/upstreamretry"
	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
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
	// DeleteObject removes an uploaded export no row came to name (#1903).
	DeleteObject(ctx context.Context, bucket, key string) error
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
	// Metadata records whether the export that wrote the asset was cut
	// (#2057).
	Metadata map[string]any
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
	// Metadata records a cut export on the version, and through it on the
	// asset (#2057). Nil for a complete one.
	Metadata map[string]any
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
	// RunOutputKey, set when a managed-script run made the call, turns the
	// export's name into the script's output identity, so a named export
	// inside a run writes the next version of one asset (#1854). Nil
	// otherwise.
	RunOutputKey func(name string) string
}

// ExportDeps holds the platform-side dependencies graphql_export needs.
// A nil AssetStore is export disabled: the tool is not registered, so
// the model never sees one it could not successfully call.
type ExportDeps struct {
	AssetStore   ExportAssetStore
	VersionStore ExportVersionStore
	S3Client     ExportS3Client
	ShareCreator ExportShareCreator
	// ResourceLander lands a result in a managed resource by path instead of in
	// a new asset (#1663). nil leaves the asset destination the only one, which
	// is what a deployment with no managed-resource library has.
	ResourceLander toolkit.ResourceLander
	S3Bucket       string
	S3Prefix       string
	BaseURL        string
	Config         ExportConfig
	GetUserContext func(ctx context.Context) *ExportUserContext
	// Background is the runner a long export runs in the background through
	// (#2212), read when a call asks for it. Nil, or a nil answer, runs every
	// export inline.
	Background func() exportasync.Runner
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
	// Async runs the export in the background (#2212). Nil runs a page walk
	// from a model client in the background and anything else inline.
	Async *bool `json:"async,omitempty"`
	// Resource, when set, lands the result in the managed resource at that path
	// instead of in a new portal asset (#1663).
	Resource *toolkit.ResourceDestination `json:"resource,omitempty"`
	// Options decide what a walk stopped at its page bound, or an unexpected
	// item count, does (#2057).
	exporttrunc.Options
}

// exportOutput is the asset metadata the model gets back. The data
// itself is not in it: that is the whole point of the tool.
type exportOutput struct {
	AssetID string `json:"asset_id,omitempty"`
	// AssetVersion is the version this export wrote: 1 for a new asset, the
	// next one for a named export a script run repeats (#1854).
	AssetVersion int      `json:"asset_version,omitempty"`
	PortalURL    string   `json:"portal_url,omitempty"`
	ShareURL     string   `json:"share_url,omitempty"`
	ContentType  string   `json:"content_type,omitempty"`
	SizeBytes    int64    `json:"size_bytes"`
	Operations   []string `json:"operations,omitempty"`
	// Status is the HTTP status the endpoint answered with, and
	// UpstreamError whether that answer was a failure: a non-2xx, or a
	// 200 carrying errors. They are the same facts graphql_query reports,
	// and what the call is audited on (#1678); the errors are written
	// into the asset as sent, so whoever opens it sees them too. A
	// replayed export (idempotency_key) made no call and carries no
	// status.
	Status        int               `json:"status,omitempty"`
	UpstreamError bool              `json:"upstream_error"`
	Errors        []Error           `json:"errors,omitempty"`
	Pagination    *PaginationReport `json:"pagination,omitempty"`
	// UpstreamBody, the retry advice and the transport failure are what
	// graphql_query reports for the same answer (#2211).
	UpstreamBody   string     `json:"upstream_body,omitempty"`
	TransportError string     `json:"transport_error,omitempty"`
	Error          *callError `json:"error,omitempty"`
	upstreamretry.Advice
	// TimeoutCap reports a timeout_seconds longer than the deployment's
	// export max_timeout, and the timeout applied in its place (#2210).
	timeoutcap.Cap
	// Resource is where a resource destination landed the result (#1663): the
	// reference and uri to hand to the next call, the version written, and what
	// the write did to the tables registered over the file. Set instead of
	// asset_id, never beside it.
	Resource *toolkit.ResourceLanding `json:"resource,omitempty"`
	Message  string                   `json:"message"`
	// Report says whether the walk's page bound cut it and whose bound it was
	// (#2057). Set on a page walk, and on an idempotency hit for an asset a
	// cut walk wrote; nil otherwise, since one response has no bound to cut.
	*exporttrunc.Report
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
	toolkit.AddTool(s, &mcp.Tool{
		Name:  ToolExport,
		Title: "Export a GraphQL Result",
		Description: "Run a GraphQL document and write its result into a portal asset INSTEAD of returning it " +
			"through the model context. Use this when graphql_query reports data_truncated, when you expect a " +
			"result too large to be useful through the model, or when you want to hand the data to another tool " +
			"or share it with a person. The document is parsed, validated and authorized exactly as graphql_query " +
			"validates it, and paginate walks pages the same way. " +
			"Pass `resource` to land the result in a MANAGED RESOURCE at a path instead of a new asset: the same " +
			"path next time is the NEXT VERSION of that one file, keeping its id, its mcp:// URI, the assets that " +
			"reference it and the tables registered over it. That is the destination for a document run on a " +
			"schedule. " +
			toolkit.ResourceLandingResultSentence + " NAMING: keep `name` short and portable, ASCII letters, digits, spaces, " +
			"hyphens and dots only; it doubles as the download filename, and is the display name when a resource " +
			"destination names the filename itself.",
		InputSchema: exportSchema,
		// An export lands a new asset, or the next version of a managed
		// resource with the earlier versions kept, so it only adds.
		Annotations: toolkit.WriteAnnotations(false),
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
	if err := in.ValidateWalk(in.Paginate != nil); err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	uc := resolveExportUser(ctx, deps)
	if uc == nil {
		return toolkit.ErrorResult("authentication required for graphql_export"), nil, nil
	}
	runner := exportasync.RunnerOf(deps.Background)
	read, prior, handled := exportasync.Resume(ctx, runner, ToolExport, in.IdempotencyKey, exportasync.HowToReadAgain(ToolExport, in.IdempotencyKey))
	if handled {
		return read, nil, nil
	}
	if in.Resource != nil {
		if denial := checkResourceDestination(ctx, deps, in); denial != nil {
			return denial, nil, nil
		}
	}
	prepared, errMsg := t.prepare(ctx, in.query())
	if errMsg != "" {
		return toolkit.ErrorResult(errMsg), nil, nil
	}
	if existing := checkExportIdempotency(ctx, deps, uc, in); existing != nil {
		return toolkit.JSONResult(existing), existing, nil
	}
	job := exportJob{deps: deps, uc: uc, prepared: prepared, in: in}
	if exportasync.Wanted(runner, in.Async, in.Paginate != nil, mcpcontext.ResultBudget(ctx) > 0) {
		return t.exportInBackground(ctx, runner, job, prior), nil, nil
	}
	result, out := t.exportNow(ctx, job)
	return result, out, nil
}

// exportJob is one prepared export: what exportNow runs, inline or in the
// background.
type exportJob struct {
	deps     *ExportDeps
	uc       *ExportUserContext
	prepared prepared
	in       exportInput
}

// exportNow runs one export to its end and answers with it.
func (t *Toolkit) exportNow(ctx context.Context, job exportJob) (result *mcp.CallToolResult, out any) {
	deps, uc, prepared, in := job.deps, job.uc, job.prepared, job.in
	timeout, capped := resolveExportTimeout(in.TimeoutSeconds, deps.Config)
	exportCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	written, err := t.runExport(exportCtx, deps, uc, prepared, in)
	if transport := (*TransportError)(nil); errors.As(err, &transport) {
		return exportTransportResult(prepared, transport, timeout, capped)
	}
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil
	}
	written.Cap = capped
	result = toolkit.JSONResult(written)
	stampAuditOutcome(result, classifyUpstream(written.Status, written.Errors, written.UpstreamError))
	return result, written
}

// exportInBackground starts the export as a background export (#2212),
// answering at once with the run and the key it is read again by. Nothing
// the caller is told to fix is left to the background: the document was
// prepared and the destination checked before this.
func (t *Toolkit) exportInBackground(ctx context.Context, runner exportasync.Runner, job exportJob, prior *exportasync.Run) *mcp.CallToolResult {
	launch := exportasync.Launch{
		Runner: runner, Tool: ToolExport, Prior: prior, Key: &job.in.IdempotencyKey,
		Timeout: &job.in.TimeoutSeconds, MaxTimeout: job.deps.Config.MaxTimeout,
	}
	if job.in.Resource != nil {
		// A resource destination keeps no key on what it writes: its repeat
		// call is the file's next version, and the key reads the run alone.
		key := job.in.IdempotencyKey
		launch.Key, job.in.IdempotencyKey = &key, ""
	}
	return exportasync.Start(ctx, launch, func(ctx context.Context) (*mcp.CallToolResult, error) {
		res, _ := t.exportNow(ctx, job)
		return res, nil
	})
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
	// Judged before anything is written: a cut the caller did not accept
	// writes nothing (#2057).
	cut := judgeWalk(result, in)
	if cut != nil && cut.Refusal != "" {
		return nil, errors.New("graphql_export: " + cut.Refusal)
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
	if in.Resource != nil {
		return landExport(ctx, deps, in, landedResult{payload: payload, result: result, cut: cut})
	}
	st, err := t.persist(ctx, deps, uc, in, persisted{payload: payload, cut: reportOf(cut)})
	if err != nil {
		return nil, err
	}
	assetID, version, size := st.assetID, st.version, st.size
	return &exportOutput{
		AssetID:       assetID,
		AssetVersion:  version,
		PortalURL:     buildExportPortalURL(deps.BaseURL, assetID),
		ShareURL:      maybeCreateExportShare(ctx, deps, in, assetID, uc.UserEmail),
		ContentType:   exportContentType,
		SizeBytes:     size,
		Operations:    result.Operations,
		Status:        result.Status,
		UpstreamError: result.UpstreamError,
		Errors:        result.Errors,
		Pagination:    result.Pagination,
		UpstreamBody:  result.UpstreamBody,
		Advice:        result.Advice,
		Message:       exporttrunc.Sentences(fmt.Sprintf("Exported %d bytes from connection %s.", size, in.Connection), noteOf(cut)),
		Report:        reportOf(cut),
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
func (*Toolkit) persist(ctx context.Context, deps *ExportDeps, uc *ExportUserContext, in exportInput, p persisted) (stored, error) {
	payload := p.payload
	assetID, err := generateExportAssetID()
	if err != nil {
		return stored{}, fmt.Errorf("graphql: generating asset id: %w", err)
	}
	s3Key := buildExportS3Key(deps.S3Prefix, uc.UserID, assetID)
	size, err := deps.S3Client.PutObjectStream(ctx, deps.S3Bucket, s3Key, bytes.NewReader(payload), exportContentType)
	if err != nil {
		return stored{}, fmt.Errorf("graphql: writing the export to storage failed: %w", err)
	}
	asset := ExportAsset{
		ID: assetID, OwnerID: uc.UserID, OwnerEmail: uc.UserEmail,
		Name: in.Name, Description: in.Description, ContentType: exportContentType,
		S3Bucket: deps.S3Bucket, S3Key: s3Key, SizeBytes: size, Tags: in.Tags,
		Provenance: buildExportProvenance(uc, in), SessionID: uc.SessionID,
		IdempotencyKey: in.IdempotencyKey,
	}
	var cutMeta map[string]any
	if p.cut != nil {
		asset.Tags, cutMeta = exporttrunc.WithTag(asset.Tags, *p.cut), p.cut.Metadata()
	}
	if key := runOutputKey(uc, in); key != "" && deps.VersionStore != nil {
		id, version, err := recordRunVersion(ctx, deps, asset, key, runVersion{createdBy: uc.UserID, metadata: cutMeta})
		if errors.Is(err, toolkit.ErrObjectUnreferenced) {
			discardExport(ctx, deps, s3Key)
		}
		return stored{assetID: id, version: version, size: size}, err
	}
	if err := deps.AssetStore.InsertExportAsset(ctx, asset); err != nil {
		discardExport(ctx, deps, s3Key)
		return stored{}, fmt.Errorf("graphql: recording the asset failed: %w", err)
	}
	recordExportVersion(ctx, deps, asset, runVersion{createdBy: uc.UserID, metadata: cutMeta})
	return stored{assetID: assetID, version: 1, size: size}, nil
}

// discardExport deletes an uploaded export no asset or version row names,
// which is otherwise an object nothing can find (#1903). A failure is logged:
// the call's own outcome has already been decided.
func discardExport(ctx context.Context, deps *ExportDeps, key string) {
	if deps.S3Client == nil {
		return
	}
	if err := deps.S3Client.DeleteObject(ctx, deps.S3Bucket, key); err != nil {
		slog.Warn("graphql_export: removing an upload no asset names failed",
			"key", logsan.SanitizeForLog(key), "error", logsan.SanitizeForLog(err.Error()))
	}
}

// stored is what persist wrote: the asset, the version, and the bytes.
type stored struct {
	assetID string
	version int
	size    int64
}

// recordRunVersion records a named export a script run made under the
// script's output identity: the next version of the asset the key names, or
// the new asset carrying it (#1854).
func recordRunVersion(ctx context.Context, deps *ExportDeps, asset ExportAsset, key string, rv runVersion) (assetID string, version int, err error) {
	assetID, version, err = toolkit.PersistRunAsset(ctx, key, asset.ID, toolkit.RunAssetWrite{
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
		Version: func(ctx context.Context, assetID string) (int, error) {
			versionID, err := generateExportAssetID()
			if err != nil {
				return 0, err
			}
			return deps.VersionStore.CreateExportVersion(ctx, ExportVersion{
				ID: versionID, AssetID: assetID, S3Key: asset.S3Key, S3Bucket: asset.S3Bucket,
				ContentType: asset.ContentType, SizeBytes: asset.SizeBytes,
				CreatedBy: rv.createdBy, ChangeSummary: "Exported from GraphQL", Metadata: rv.metadata,
			})
		},
	})
	if err != nil {
		return "", 0, fmt.Errorf("graphql: %w", err)
	}
	return assetID, version, nil
}

// runOutputKey is the script output identity a named export made inside a run
// writes under, or "" when the call is not a run's, names nothing, or carries
// its own idempotency key, which keeps its own meaning.
func runOutputKey(uc *ExportUserContext, in exportInput) string {
	if uc.RunOutputKey == nil || in.Name == "" || in.IdempotencyKey != "" {
		return ""
	}
	return uc.RunOutputKey(in.Name)
}

// recordExportVersion inserts the asset's first version row.
func recordExportVersion(ctx context.Context, deps *ExportDeps, asset ExportAsset, rv runVersion) {
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
		CreatedBy: rv.createdBy, ChangeSummary: "Initial export", Metadata: rv.metadata,
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
	out := &exportOutput{
		AssetID:   existing.ID,
		PortalURL: buildExportPortalURL(deps.BaseURL, existing.ID),
		SizeBytes: existing.SizeBytes,
		Message:   "Asset already exists (idempotency key matched).",
		Report:    exporttrunc.FromMetadata(existing.Metadata),
	}
	if out.Report != nil {
		out.Message += fmt.Sprintf(" That export was truncated at %d %s; the file is incomplete.", out.LimitApplied, out.LimitUnit)
	}
	return out
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
// over a larger request, and is reported back as the cap (#2210).
func resolveExportTimeout(seconds int, cfg ExportConfig) (time.Duration, timeoutcap.Cap) {
	if seconds <= 0 {
		return cfg.DefaultTimeout, timeoutcap.Cap{}
	}
	if cfg.MaxTimeout <= 0 {
		return time.Duration(seconds) * time.Second, timeoutcap.Cap{}
	}
	return timeoutcap.Apply(cfg.MaxTimeout, seconds, timeoutcap.CappedByExportMaxTimeout)
}

// exportTransportResult is graphql_export's answer to a send the endpoint
// never answered: nothing was written, and the result says why as
// graphql_query's does (#2211).
func exportTransportResult(p prepared, failure *TransportError, timeout time.Duration, capped timeoutcap.Cap) (result *mcp.CallToolResult, out any) {
	msg, outcome := transportMessage(failure, timeout, capped, "the export's timeout")
	output := &exportOutput{
		Operations:     p.invoked,
		UpstreamError:  true,
		TransportError: msg,
		Error:          &callError{Code: upstreamretry.CodeUnavailable, Category: upstreamretry.CategoryUnavailable, Message: msg},
		Cap:            capped,
		Message:        "Nothing was exported: " + msg,
	}
	result = toolkit.JSONResult(output)
	result.IsError = true
	stampAuditOutcome(result, auditVerdict{outcome: outcome, message: msg})
	return result, output
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
// <prefix>/graphql_export/<user>/<asset>/content.json.
//
// The object is alone in a directory of its own, the portal's convention for
// a version (#1851): a table registered over an asset points at the directory
// its content sits in and reads every file there, so an export written beside
// the same user's other exports could not be registered, and a version a
// script run writes onto an existing asset would land beside the one a pinned
// table reads.
func buildExportS3Key(prefix, userID, assetID string) string {
	parts := []string{}
	if prefix != "" {
		parts = append(parts, strings.Trim(prefix, "/"))
	}
	parts = append(parts, "graphql_export", userID, assetID, "content.json")
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
