package apigateway //nolint:revive // adapter types for cross-package wiring

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/pagewalk"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// exportToolName is the MCP tool name. The trino_export pattern
// established the *_export naming for tools whose purpose is "run
// something that produces a potentially-huge result, write it to a
// portal asset, return asset metadata not data". api_export does
// the same for upstream HTTP API responses.
const exportToolName = "api_export"

// defaultExportMaxBytes caps how much of an upstream response will
// be written to a portal asset when the operator has not configured
// a per-platform limit. 100 MiB matches trino_export's default and
// is generous enough for typical CRM / analytics pulls without
// inviting accidental DoS via a misconfigured upstream.
const defaultExportMaxBytes = int64(100 * 1024 * 1024)

// defaultExportTimeout / defaultMaxExportTimeout cap how long a
// single api_export call can run. The default is generous for
// large paginated pulls; the max prevents an operator-supplied
// timeout from holding the request handler indefinitely.
const (
	defaultExportTimeout    = 5 * time.Minute
	defaultMaxExportTimeout = 30 * time.Minute
)

// ExportAssetStore is the subset of portal.AssetStore needed by
// api_export. Defined locally to avoid an import cycle (portal →
// registry → apigateway). Mirrors trinokit.ExportAssetStore.
type ExportAssetStore interface {
	InsertExportAsset(ctx context.Context, asset ExportAsset) error
	GetByIdempotencyKey(ctx context.Context, ownerID, key string) (*ExportAssetRef, error)
}

// ExportVersionStore is the subset of portal.VersionStore needed
// by api_export.
type ExportVersionStore interface {
	CreateExportVersion(ctx context.Context, version ExportVersion) (int, error)
}

// ExportS3Client is the subset of portal.S3Client needed by
// api_export. It streams the upstream response straight to S3 without
// buffering the full body in heap (issue #537), so a large export no
// longer holds the whole payload in memory or competes for the global
// in-flight budget. The platform adapter implements it via the mcp-s3
// client's PutObjectStream.
type ExportS3Client interface {
	// PutObjectStream uploads body to bucket/key without buffering the
	// whole payload, returning the bytes written. The caller bounds the
	// size by wrapping body in a reader that errors past the cap; the
	// transfer manager aborts the incomplete multipart upload on that
	// read error, so no partial object or orphaned parts remain.
	PutObjectStream(ctx context.Context, bucket, key string, body io.Reader, contentType string) (size int64, err error)
}

// ExportShareCreator creates public share links for exported
// assets. nil disables public-link creation.
type ExportShareCreator interface {
	CreatePublicShare(ctx context.Context, assetID, createdBy string) (shareURL string, err error)
}

// ExportAsset is the row inserted into portal_assets when an
// api_export call succeeds. Field shape mirrors trinokit.ExportAsset
// so the platform-side adapter can reuse its conversion logic.
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

// ExportProvenance captures the chain of tool calls that produced
// an asset so portal viewers can render "exported via api_export
// from <connection> <method> <path>".
type ExportProvenance struct {
	ToolCalls []ExportProvenanceCall
	SessionID string
	UserID    string
	// DeclaredContentType is the upstream's Content-Type header, carried only
	// when detection stored the asset under a different type.
	DeclaredContentType string
}

// ExportProvenanceCall is one step in the provenance chain.
type ExportProvenanceCall struct {
	ToolName   string
	Timestamp  string
	Parameters map[string]any
}

// ExportAssetRef is returned by idempotency-key lookup. We only
// need the id + size for the response — the model doesn't see the
// existing asset's full row.
type ExportAssetRef struct {
	ID        string
	SizeBytes int64
}

// ExportVersion is the row inserted into portal_asset_versions.
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

// ExportConfig holds platform-level limits for api_export. MaxBytes
// caps any single export's size (above which the call returns an
// error rather than a truncated asset — partial-data assets would
// be misleading). DefaultTimeout / MaxTimeout bound how long a
// single call may run.
type ExportConfig struct {
	MaxBytes       int64
	DefaultTimeout time.Duration
	MaxTimeout     time.Duration
}

// applyExportDefaults fills zero values with defaults.
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

// ExportUserContext holds user identity extracted from the request
// context. Populated by the GetUserContext callback provided in
// ExportDeps so the toolkit doesn't import middleware directly.
type ExportUserContext struct {
	UserID    string
	UserEmail string
	SessionID string
}

// ExportDeps holds platform-side dependencies injected into the
// api gateway toolkit. All types are defined locally to avoid
// import cycles. Mirrors trinokit.ExportDeps so the platform-side
// wiring can stay symmetric.
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

// SetExportDeps wires platform-side dependencies for api_export.
// Calling with nil-AssetStore deps is treated as "export disabled":
// registerExportTool checks t.exportDeps and skips registration so
// the model doesn't see a tool that would always fail.
func (t *Toolkit) SetExportDeps(deps ExportDeps) {
	deps.Config = applyExportDefaults(deps.Config)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.exportDeps = &deps
}

// exportInput is the parsed input for api_export. Fields parallel
// the api_invoke_endpoint input plus the portal-asset metadata
// fields trino_export takes (name, description, tags,
// idempotency_key, create_public_link).
type exportInput struct {
	Connection       string            `json:"connection"`
	Method           string            `json:"method,omitempty"`
	Path             string            `json:"path,omitempty"`
	OperationID      string            `json:"operation_id,omitempty"`
	Spec             string            `json:"spec,omitempty"`
	PathParams       map[string]string `json:"path_params,omitempty"`
	Query            map[string]any    `json:"query_params"`
	Headers          map[string]string `json:"headers"`
	Body             any               `json:"body"`
	TimeoutSeconds   int               `json:"timeout_seconds"`
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	Tags             []string          `json:"tags"`
	IdempotencyKey   string            `json:"idempotency_key"`
	CreatePublicLink bool              `json:"create_public_link"`
	// Paginate, when set, makes the export a page walk (issue #1535): the
	// merged array is streamed into the one asset as pages arrive.
	Paginate *PaginateInput `json:"paginate,omitempty"`
}

// exportOutput is the response returned to the model. Mirrors
// trino_export's output: asset metadata, no body bytes.
type exportOutput struct {
	AssetID     string `json:"asset_id"`
	PortalURL   string `json:"portal_url,omitempty"`
	ShareURL    string `json:"share_url,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Status      int    `json:"upstream_status"`
	SizeBytes   int64  `json:"size_bytes"`
	Message     string `json:"message"`
	// WalkStats is set on a page walk; nil on a single-page export.
	*WalkStats
}

// registerExportTool registers api_export on the MCP server. No-op
// when ExportDeps were never wired (admin or single-replica
// deployment without portal asset store).
func (t *Toolkit) registerExportTool(s *mcp.Server) {
	t.mu.RLock()
	deps := t.exportDeps
	t.mu.RUnlock()
	if deps == nil {
		return
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:  exportToolName,
		Title: "Export API Endpoint Response",
		Description: "Invoke an upstream API endpoint and stream the response into a portal asset INSTEAD of returning it through the model context. " +
			"Use this when api_invoke_endpoint reports body_truncated, when you expect a response too large to be useful through the model, or when you want to hand off the data to trino_query / s3_get_object / a portal share. " +
			"Address the operation either by operation_id (with any path template values in path_params) or by method+path directly, exactly like api_invoke_endpoint; supply one form, not both. " +
			"Pass `paginate` to walk every page of a paginated collection in this one call: the merged array is streamed into the asset as pages arrive, and the result reports pages_fetched, items_merged, and stopped_by. " +
			"Returns asset metadata (id, URL, size, content type) — the data is NOT returned through this response. " +
			"NAMING: keep `name` short and portable, ASCII letters / digits / spaces / hyphens / dots only. " +
			"The name doubles as the download filename.",
		InputSchema: apiExportInputSchema,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: false,
		},
	}, t.handleExport)
}

// handleExport is the MCP handler for api_export. The flow:
//  1. Validate input + resolve connection (auth, route policy,
//     same gates as api_invoke_endpoint).
//  2. Resolve user context — required for OwnerID/OwnerEmail on
//     the asset row.
//  3. Idempotency check — if (user, key) matches an existing
//     asset, return its metadata without re-running the upstream.
//  4. Build + send the upstream request, capped at deps.Config.MaxBytes.
//  5. Reject the call (no partial asset) if the response exceeds
//     the cap — partial data in a portal asset would be misleading.
//  6. PutObject to S3, insert asset row, insert version row,
//     optionally create a public share.
//  7. Return asset metadata.
func (t *Toolkit) handleExport(ctx context.Context, _ *mcp.CallToolRequest, in exportInput) (*mcp.CallToolResult, any, error) {
	t.mu.RLock()
	deps := t.exportDeps
	c, connOK := t.connections[in.Connection]
	policy := t.routePolicy
	t.mu.RUnlock()
	if deps == nil {
		return toolkit.ErrorResult("api_export is not configured (portal asset store unavailable)"), nil, nil
	}
	if in.Connection == "" {
		return toolkit.ErrorResult("connection is required"), nil, nil
	}
	if !connOK {
		return toolkit.ErrorResult(fmt.Sprintf("connection %q not found", in.Connection)), nil, nil
	}
	if in.Name == "" {
		return toolkit.ErrorResult("name is required (becomes the asset's download filename)"), nil, nil
	}
	uc := resolveExportUser(ctx, deps)
	if uc == nil {
		return toolkit.ErrorResult("authentication required for api_export"), nil, nil
	}

	// Resolve operation_id addressing into concrete method+path before
	// route-policy and upstream work, so api_export speaks the same
	// operation_id shortcut as api_invoke_endpoint (issue #1046).
	// endpointPath (not "path") avoids shadowing the "path" import.
	method, endpointPath, addrErr := operationAddressing{
		Method: in.Method, Path: in.Path, OperationID: in.OperationID,
		Spec: in.Spec, PathParams: in.PathParams,
	}.resolve(c)
	if addrErr != nil {
		return toolkit.ErrorResult(addrErr.Error()), nil, nil
	}
	in.Method, in.Path = method, endpointPath

	// Honor the same route policy that api_invoke_endpoint does so
	// a persona scoped to GET /v1/users cannot export from
	// DELETE /v1/users/{id}.
	if _, mErr := validateMethod(in.Method); mErr != nil {
		return toolkit.ErrorResult(mErr.Error()), nil, nil
	}
	if denial := checkRoutePolicy(ctx, policy, InvokeInput{
		Connection: in.Connection, Method: in.Method, Path: in.Path,
	}, routeTemplateFor(policy, c, in.Method, in.Path)); denial != nil {
		return denial, nil, nil
	}

	if existing := checkExportIdempotency(ctx, deps, uc, in); existing != nil {
		return toolkit.JSONResult(existing), existing, nil
	}

	args := runExportArgs{
		deps: deps, cfg: c.cfg, auth: c.auth, client: c.client, specs: c.specs,
		webdavRoutes: c.webdavRoutes(), uc: uc, in: in,
	}
	if in.Paginate != nil {
		t.mu.RLock()
		args.budget = t.memBudget
		t.mu.RUnlock()
		args.authorize = pageAuthorizer(ctx, policy, c)
		out, runErr := t.runExportWalk(ctx, args)
		if runErr != nil {
			return budgetOrErrorResult(runErr), nil, nil
		}
		res := toolkit.JSONResult(out)
		stampWalkMeta(res, out.WalkStats)
		return res, out, nil
	}
	out, runErr := t.runExport(ctx, args)
	if runErr != nil {
		return toolkit.ErrorResult(runErr.Error()), nil, nil
	}
	return toolkit.JSONResult(out), out, nil
}

// resolveExportUser fetches the platform-injected user context. nil
// (no GetUserContext callback) or a nil result both yield nil — the
// caller surfaces "authentication required". Without owner identity
// the asset row would have no owner and be unreachable from the
// portal "my exports" view.
func resolveExportUser(ctx context.Context, deps *ExportDeps) *ExportUserContext {
	if deps.GetUserContext == nil {
		return nil
	}
	return deps.GetUserContext(ctx)
}

// checkExportIdempotency returns an existing asset's metadata when
// the idempotency key is set and matches. Returns nil when no key
// was supplied, the lookup found nothing, or the lookup errored
// (lookup errors fall through to a fresh run — better to
// accidentally double-export than to fail closed when the DB is
// degraded).
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

// runExportArgs bundles the inputs runExport needs. Splitting into
// a struct keeps the function under revive's argument-limit ceiling
// and makes the call site self-documenting.
type runExportArgs struct {
	deps         *ExportDeps
	cfg          Config
	auth         Authenticator
	client       *http.Client
	specs        map[string]*specState
	webdavRoutes []webdavRoute
	uc           *ExportUserContext
	in           exportInput
	// budget and authorize are the page walk's: the in-flight memory
	// budget each page is read against, and the route policy check run
	// on every page's address (nil when no policy is installed).
	budget    *MemBudget
	authorize func(InvokeInput) error
}

// runExport executes the upstream call, uploads the response to
// S3, and inserts the asset + version rows.
func (*Toolkit) runExport(ctx context.Context, a runExportArgs) (*exportOutput, error) {
	deps, cfg, auth, client, specs, uc, in := a.deps, a.cfg, a.auth, a.client, a.specs, a.uc, a.in
	timeout := resolveExportTimeout(in.TimeoutSeconds, deps.Config)
	exportCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := buildExportRequest(exportCtx, exportRequestParams{cfg: cfg, auth: auth, specs: specs, webdavRoutes: a.webdavRoutes, in: in})
	if err != nil {
		return nil, err
	}

	// #nosec G107 G704 -- req.URL is constructed via buildURL which
	// parses the operator-configured base_url independently and
	// asserts the joined URL's scheme + host equal the base's;
	// validatePath rejects path shapes (//, @, CR/LF/NUL) that
	// would let url.Parse be tricked into changing the host. Same
	// SSRF guards as api_invoke_endpoint, same #nosec rationale.
	resp, err := client.Do(req) //nolint:bodyclose // closed below
	if err != nil {
		return nil, fmt.Errorf("upstream request: %s", scrubTransportError(err))
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup

	// Reject before any S3 write when the upstream declares a length over
	// the cap — the common case, and the same all-or-nothing contract the
	// buffered path had (a partial asset would mislead the operator).
	// Chunked/undeclared-length bodies are bounded during the stream by
	// MaxBytes in persistExportAsset, which aborts and cleans up the
	// incomplete multipart upload past the cap (issue #537).
	if resp.ContentLength > 0 && resp.ContentLength > deps.Config.MaxBytes {
		return nil, fmt.Errorf("upstream response (%d bytes) exceeds api_export cap of %d bytes — narrow the request (smaller page, fewer fields) or raise platform.export.max_bytes", resp.ContentLength, deps.Config.MaxBytes)
	}

	declaredType := resp.Header.Get("Content-Type")

	// An upstream that omits Content-Type, or answers a JSON endpoint with
	// text/plain, would otherwise produce an asset the viewer can only show as
	// raw text. Detection reads a bounded prefix and hands back a reader that
	// replays it, so the body still streams to storage unbuffered.
	contentType, body, err := contenttype.DetectStream(declaredType, resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading upstream response: %s", scrubTransportError(err))
	}

	// Stream the upstream body straight to S3 (no full-body buffer). Bound
	// the read by the export timeout via resp.Body, which is tied to
	// exportCtx.
	assetID, size, err := persistExportAsset(ctx, persistExportArgs{
		deps: deps, uc: uc, in: in, body: body, maxBytes: deps.Config.MaxBytes,
		contentType: contentType, declaredType: declaredType, status: resp.StatusCode,
	})
	if err != nil {
		return nil, err
	}

	shareURL := maybeCreateExportShare(ctx, deps, in, assetID, uc.UserEmail)

	method, _ := validateMethod(in.Method)
	return &exportOutput{
		AssetID:     assetID,
		PortalURL:   buildExportPortalURL(deps.BaseURL, assetID),
		ShareURL:    shareURL,
		ContentType: contentType,
		Status:      resp.StatusCode,
		SizeBytes:   size,
		Message:     fmt.Sprintf("Exported %d bytes from %s %s.", size, method, in.Path),
	}, nil
}

// exportRequestParams bundles the inputs buildExportRequest needs so
// the call site stays under revive's argument-limit ceiling now that
// the connection's OpenAPI catalog (specs) participates in the
// Content-Type decision (issue #453).
type exportRequestParams struct {
	cfg          Config
	auth         Authenticator
	specs        map[string]*specState
	webdavRoutes []webdavRoute
	in           exportInput
}

// buildExportRequest assembles the *http.Request for the upstream
// call. It delegates to buildUpstreamRequest so the SSRF guards,
// reserved-header checks, Content-Type negotiation, and credential
// injection are byte-for-byte the same ones api_invoke_endpoint and the
// raw passthrough use — there is exactly one place those rules live.
func buildExportRequest(ctx context.Context, p exportRequestParams) (*http.Request, error) {
	return buildUpstreamRequest(ctx, p.cfg, p.auth, catalogView{specs: p.specs, webdavRoutes: p.webdavRoutes}, exportInvokeInput(p.in))
}

// persistExportArgs bundles the inputs persistExportAsset needs.
type persistExportArgs struct {
	deps     *ExportDeps
	uc       *ExportUserContext
	in       exportInput
	body     io.Reader
	maxBytes int64
	// contentType is the type the asset is stored under, after detection.
	contentType string
	// declaredType is what the upstream's Content-Type header said, recorded
	// in provenance when detection replaced it.
	declaredType string
	status       int
}

// persistExportAsset streams the response body to S3 and inserts the
// asset row + version row. Returns the asset id and the number of bytes
// written on success. Version-row failure is non-fatal — the asset row
// is already in place and the model has an id; failing the whole call
// would orphan the S3 object. An over-cap stream aborts the multipart
// upload (no orphaned parts, no asset row) and returns the all-or-nothing
// rejection error.
func persistExportAsset(ctx context.Context, p persistExportArgs) (assetID string, size int64, err error) {
	obj, err := putExportObject(ctx, p)
	if err != nil {
		return "", 0, err
	}
	prov := buildExportProvenance(p.uc, p.in, p.status, replacedDeclaration(p.declaredType, p.contentType))
	return obj.assetID, obj.size, recordExportAsset(ctx, p, obj, prov)
}

// exportObject is what putExportObject stored: the asset id it minted,
// the key the bytes live under, and how many were written.
type exportObject struct {
	assetID     string
	s3Key       string
	size        int64
	contentType string
}

// putExportObject streams p.body to storage under a fresh asset id. Bound
// the stream at the cap with a reader that errors past it; the transfer
// manager aborts the incomplete multipart upload on that read error, so
// no partial object or orphaned parts remain. The exceeded flag (not the
// SDK's wrapped error) is what distinguishes an over-cap body from a
// transient storage error.
func putExportObject(ctx context.Context, p persistExportArgs) (exportObject, error) {
	deps, contentType := p.deps, p.contentType
	assetID, err := generateExportAssetID()
	if err != nil {
		return exportObject{}, fmt.Errorf("generating asset id: %w", err)
	}
	s3Key := buildExportS3Key(deps.S3Prefix, p.uc.UserID, assetID, contentType)
	capped := &cappedReader{r: p.body, max: p.maxBytes}
	size, err := deps.S3Client.PutObjectStream(ctx, deps.S3Bucket, s3Key, capped, contentType)
	if err != nil {
		if capped.exceeded {
			return exportObject{}, fmt.Errorf("upstream response exceeded api_export cap of %d bytes — narrow the request (smaller page, fewer fields) or raise platform.export.max_bytes", p.maxBytes)
		}
		return exportObject{}, fmt.Errorf("streaming export to storage failed: %w", err)
	}
	return exportObject{assetID: assetID, s3Key: s3Key, size: size, contentType: contentType}, nil
}

// recordExportAsset inserts the asset row and the version row for a
// stored object.
func recordExportAsset(ctx context.Context, p persistExportArgs, obj exportObject, prov ExportProvenance) error {
	deps, uc, in := p.deps, p.uc, p.in
	assetID, s3Key, size, contentType := obj.assetID, obj.s3Key, obj.size, obj.contentType
	asset := ExportAsset{
		ID:             assetID,
		OwnerID:        uc.UserID,
		OwnerEmail:     uc.UserEmail,
		Name:           in.Name,
		Description:    in.Description,
		ContentType:    contentType,
		S3Bucket:       deps.S3Bucket,
		S3Key:          s3Key,
		SizeBytes:      size,
		Tags:           in.Tags,
		Provenance:     prov,
		SessionID:      uc.SessionID,
		IdempotencyKey: in.IdempotencyKey,
	}
	if err := deps.AssetStore.InsertExportAsset(ctx, asset); err != nil {
		return fmt.Errorf("insert asset row: %w", err)
	}
	versionID, vidErr := generateExportAssetID()
	if vidErr != nil {
		// Generating an id should never fail (crypto/rand). If it
		// somehow does, the asset row is already in place — log
		// and return the asset id so the model has a usable handle.
		slog.Warn("api_export: generating version id failed",
			"asset_id", assetID, "error", vidErr)
		return nil
	}
	if _, vErr := deps.VersionStore.CreateExportVersion(ctx, ExportVersion{
		ID:            versionID,
		AssetID:       assetID,
		S3Key:         s3Key,
		S3Bucket:      deps.S3Bucket,
		ContentType:   contentType,
		SizeBytes:     size,
		CreatedBy:     uc.UserEmail,
		ChangeSummary: "Exported from API endpoint",
	}); vErr != nil {
		// Version-row failure is non-fatal: the asset row is
		// already in place and the model has the id. Surface via
		// slog so operators can spot DB issues — silently
		// dropping the error would let portal_asset_versions
		// drift out of sync with portal_assets indefinitely.
		slog.Warn("api_export: failed to create version record",
			"asset_id", assetID, "error", vErr)
	}
	return nil
}

// runExportWalk is api_export as a page walk (issue #1535). The walk
// writes each page's items into one end of a pipe as they arrive and
// storage reads the other, so memory holds one page at a time whatever
// the page count. The asset is one JSON document whose content is the
// merged array. A page that fails, fails the call: the pipe closes with
// the page's error, the upload aborts, and no asset row is written,
// which is api_export's all-or-nothing contract.
func (*Toolkit) runExportWalk(ctx context.Context, a runExportArgs) (*exportOutput, error) {
	deps, uc, in := a.deps, a.uc, a.in
	timeout := resolveExportTimeout(in.TimeoutSeconds, deps.Config)
	exportCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	inv := invocation{cfg: a.cfg, auth: a.auth, client: a.client, specs: a.specs, webdavRoutes: a.webdavRoutes, budget: a.budget}
	pr, pw := io.Pipe()
	arr := &jsonArrayWriter{w: pw}
	walk, err := newPageWalk(inv, exportInvokeInput(in), a.authorize, arr.write)
	if err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() {
		walkErr := walk.Run(exportCtx)
		if walkErr == nil {
			walkErr = arr.close()
		}
		_ = pw.CloseWithError(walkErr) // a pipe close never fails
		done <- walkErr
	}()

	persist := persistExportArgs{deps: deps, uc: uc, in: in, body: pr, maxBytes: deps.Config.MaxBytes, contentType: applicationJSON}
	obj, putErr := putExportObject(ctx, persist)
	// Unblock the walk if storage stopped reading first, then take its
	// verdict: a failed page is the cause the caller should see; a walk
	// that only stopped because its reader went away defers to the
	// reader's error.
	_ = pr.Close() // a pipe close never fails
	if walkErr := <-done; walkErr != nil && !errors.Is(walkErr, errWalkConsumerStopped) {
		return nil, walkErr
	}
	if putErr != nil {
		return nil, putErr
	}

	prov := buildExportProvenance(uc, in, walk.Last.Status, "")
	prov.ToolCalls[0].Parameters["paginate"] = in.Paginate
	prov.ToolCalls[0].Parameters["pages_fetched"] = walk.Stats.PagesFetched
	prov.ToolCalls[0].Parameters["items_merged"] = walk.Stats.ItemsMerged
	prov.ToolCalls[0].Parameters["stopped_by"] = walk.Stats.StoppedBy
	prov.ToolCalls[0].Parameters["final_cursor"] = pagewalk.FinalCursor(walk.Lead)
	if err := recordExportAsset(ctx, persist, obj, prov); err != nil {
		return nil, err
	}
	shareURL := maybeCreateExportShare(ctx, deps, in, obj.assetID, uc.UserEmail)
	method, _ := validateMethod(in.Method)
	return &exportOutput{
		AssetID:     obj.assetID,
		PortalURL:   buildExportPortalURL(deps.BaseURL, obj.assetID),
		ShareURL:    shareURL,
		ContentType: applicationJSON,
		Status:      walk.Last.Status,
		SizeBytes:   obj.size,
		Message:     fmt.Sprintf("Exported %d items from %d pages of %s %s (%d bytes).", walk.Stats.ItemsMerged, walk.Stats.PagesFetched, method, in.Path, obj.size),
		WalkStats:   &walk.Stats,
	}, nil
}

// exportInvokeInput is the request template an export walk runs on: the
// same projection buildExportRequest makes, plus the paginate block.
func exportInvokeInput(in exportInput) InvokeInput {
	return InvokeInput{
		Connection:     in.Connection,
		Method:         in.Method,
		Path:           in.Path,
		Query:          in.Query,
		Headers:        in.Headers,
		Body:           in.Body,
		TimeoutSeconds: in.TimeoutSeconds,
		Paginate:       in.Paginate,
	}
}

// errWalkConsumerStopped marks a walk that ended because the reader of
// its output went away (the storage stream failed or was capped). The
// consumer's own error is the one to report; this one says the walk is
// not the cause.
var errWalkConsumerStopped = errors.New("walk output consumer stopped")

// jsonArrayWriter is api_export's sink: it streams the merged array as
// one JSON document, opening it on the first page, separating items
// with commas, and closing it when the walk ends, so memory holds one
// page at a time however many pages there are.
type jsonArrayWriter struct {
	w      io.Writer
	opened bool
	count  int
}

// write is the walk's sink.
func (a *jsonArrayWriter) write(items []json.RawMessage) error {
	if err := a.open(); err != nil {
		return err
	}
	for _, it := range items {
		if a.count > 0 {
			if _, err := io.WriteString(a.w, ","); err != nil {
				return consumerError(err)
			}
		}
		if _, err := a.w.Write(it); err != nil {
			return consumerError(err)
		}
		a.count++
	}
	return nil
}

func (a *jsonArrayWriter) open() error {
	if a.opened {
		return nil
	}
	a.opened = true
	if _, err := io.WriteString(a.w, "["); err != nil {
		return consumerError(err)
	}
	return nil
}

// close finishes the document. A walk that merged nothing still writes
// an empty array.
func (a *jsonArrayWriter) close() error {
	if err := a.open(); err != nil {
		return err
	}
	if _, err := io.WriteString(a.w, "]"); err != nil {
		return consumerError(err)
	}
	return nil
}

// consumerError classifies a write failure on the walk's output. A
// closed pipe means the reader stopped first, and its error is the one
// to report.
func consumerError(err error) error {
	if errors.Is(err, io.ErrClosedPipe) {
		return errWalkConsumerStopped
	}
	return fmt.Errorf("writing merged page: %w", err)
}

// cappedReader bounds a stream at max bytes. Once more than max have
// been read it returns an error (so the S3 transfer manager aborts the
// incomplete multipart upload) and sets exceeded, which the caller
// checks to distinguish an over-cap body from a transient storage
// error. A max <= 0 disables the cap. The over-cap error text is
// internal — the caller substitutes the operator-facing message.
type cappedReader struct {
	r        io.Reader
	max      int64
	n        int64
	exceeded bool
}

func (c *cappedReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.max > 0 && c.n > c.max {
		c.exceeded = true
		return n, fmt.Errorf("export body exceeded cap of %d bytes", c.max)
	}
	return n, err //nolint:wrapcheck // transparent pass-through of the wrapped reader's error
}

// resolveExportTimeout picks the timeout for a single api_export
// call. The model's timeout_seconds wins when supplied, capped at
// MaxTimeout; absent input falls back to DefaultTimeout.
func resolveExportTimeout(timeoutSeconds int, cfg ExportConfig) time.Duration {
	if timeoutSeconds <= 0 {
		return cfg.DefaultTimeout
	}
	requested := time.Duration(timeoutSeconds) * time.Second
	if requested > cfg.MaxTimeout {
		return cfg.MaxTimeout
	}
	return requested
}

// generateExportAssetID returns a 16-byte hex id. Same format as
// trino_export's ID so the portal "exports" view doesn't need to
// distinguish source.
func generateExportAssetID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// buildExportS3Key composes the S3 key for a given asset:
// <prefix>/<user>/<asset>.<ext>. Falls back to "bin" when the
// content type doesn't yield a known extension.
func buildExportS3Key(prefix, userID, assetID, contentType string) string {
	ext := extensionForContentType(contentType)
	parts := []string{}
	if prefix != "" {
		parts = append(parts, strings.Trim(prefix, "/"))
	}
	parts = append(parts, "api_export", userID, assetID+"."+ext)
	return path.Join(parts...)
}

// extensionForContentType picks the file extension (without its leading dot)
// for an object key. It delegates to the shared contenttype table so an export
// key names the same family the viewer resolves the asset into.
func extensionForContentType(contentType string) string {
	return strings.TrimPrefix(contenttype.Extension(contentType), ".")
}

// buildExportPortalURL composes the portal asset URL. Empty BaseURL
// (operator did not configure portal.public_base_url) yields ""
// and the model just gets the asset id.
func buildExportPortalURL(baseURL, assetID string) string {
	if baseURL == "" {
		return ""
	}
	return strings.TrimRight(baseURL, "/") + "/portal/assets/" + assetID
}

// replacedDeclaration returns the upstream's declared type when detection
// stored the asset under a different one, and "" when the declaration stood.
// Recording only the disagreement keeps provenance free of noise on the common
// path where the upstream labeled its response correctly.
func replacedDeclaration(declared, stored string) string {
	if declared == "" || contenttype.Normalize(declared) == stored {
		return ""
	}
	return declared
}

// buildExportProvenance records the api_export call so portal
// viewers can render where the asset came from.
func buildExportProvenance(uc *ExportUserContext, in exportInput, status int, declaredType string) ExportProvenance {
	return ExportProvenance{
		UserID:              uc.UserID,
		SessionID:           uc.SessionID,
		DeclaredContentType: declaredType,
		ToolCalls: []ExportProvenanceCall{
			{
				ToolName:  exportToolName,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Parameters: map[string]any{
					"connection":      in.Connection,
					"method":          in.Method,
					"path":            in.Path,
					"query_params":    in.Query,
					"body":            in.Body,
					"upstream_status": status,
				},
			},
		},
	}
}

// maybeCreateExportShare creates a public share link when the
// model asked for one AND the platform wired a ShareCreator.
// Failures are non-fatal — the asset is already created; a missing
// share is a degraded but usable result.
func maybeCreateExportShare(ctx context.Context, deps *ExportDeps, in exportInput, assetID, createdBy string) string {
	if !in.CreatePublicLink || deps.ShareCreator == nil {
		return ""
	}
	url, err := deps.ShareCreator.CreatePublicShare(ctx, assetID, createdBy)
	if err != nil {
		return ""
	}
	return url
}
