package apigateway //nolint:revive // adapter types for cross-package wiring

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
// api_export. Note: this is the same shape as trinokit's; the
// platform adapter implementing it can serve both toolkits.
type ExportS3Client interface {
	PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error
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
	Method           string            `json:"method"`
	Path             string            `json:"path"`
	Query            map[string]any    `json:"query_params"`
	Headers          map[string]string `json:"headers"`
	Body             any               `json:"body"`
	TimeoutSeconds   int               `json:"timeout_seconds"`
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	Tags             []string          `json:"tags"`
	IdempotencyKey   string            `json:"idempotency_key"`
	CreatePublicLink bool              `json:"create_public_link"`
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
		return errorResult("api_export is not configured (portal asset store unavailable)"), nil, nil
	}
	if in.Connection == "" {
		return errorResult("connection is required"), nil, nil
	}
	if !connOK {
		return errorResult(fmt.Sprintf("connection %q not found", in.Connection)), nil, nil
	}
	if in.Name == "" {
		return errorResult("name is required (becomes the asset's download filename)"), nil, nil
	}
	uc := resolveExportUser(ctx, deps)
	if uc == nil {
		return errorResult("authentication required for api_export"), nil, nil
	}

	// Honor the same route policy that api_invoke_endpoint does so
	// a persona scoped to GET /v1/users cannot export from
	// DELETE /v1/users/{id}.
	if _, mErr := validateMethod(in.Method); mErr != nil {
		return errorResult(mErr.Error()), nil, nil
	}
	if denial := checkRoutePolicy(ctx, policy, InvokeInput{
		Connection: in.Connection, Method: in.Method, Path: in.Path,
	}); denial != nil {
		return denial, nil, nil
	}

	if existing := checkExportIdempotency(ctx, deps, uc, in); existing != nil {
		return jsonResult(existing), existing, nil
	}

	out, runErr := t.runExport(ctx, runExportArgs{
		deps: deps, cfg: c.cfg, auth: c.auth, client: c.client, specs: c.specs, uc: uc, in: in,
	})
	if runErr != nil {
		return errorResult(runErr.Error()), nil, nil
	}
	return jsonResult(out), out, nil
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
	deps   *ExportDeps
	cfg    Config
	auth   Authenticator
	client *http.Client
	specs  map[string]*specState
	uc     *ExportUserContext
	in     exportInput
}

// runExport executes the upstream call, uploads the response to
// S3, and inserts the asset + version rows.
func (*Toolkit) runExport(ctx context.Context, a runExportArgs) (*exportOutput, error) {
	deps, cfg, auth, client, specs, uc, in := a.deps, a.cfg, a.auth, a.client, a.specs, a.uc, a.in
	timeout := resolveExportTimeout(in.TimeoutSeconds, deps.Config)
	exportCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := buildExportRequest(exportCtx, exportRequestParams{cfg: cfg, auth: auth, specs: specs, in: in})
	if err != nil {
		return nil, err
	}

	// #nosec G107 G704 -- req.URL is constructed via buildURL which
	// parses the operator-configured base_url independently and
	// asserts the joined URL's scheme + host equal the base's;
	// validatePath rejects path shapes (//, @, CR/LF/NUL) that
	// would let url.Parse be tricked into changing the host. Same
	// SSRF guards as api_invoke_endpoint, same #nosec rationale.
	resp, err := client.Do(req) //nolint:bodyclose // closed via readCappedExportBody
	if err != nil {
		return nil, fmt.Errorf("upstream request: %s", scrubTransportError(err))
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup

	body, err := readCappedExportBody(resp.Body, deps.Config.MaxBytes)
	if err != nil {
		return nil, err
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	assetID, err := persistExportAsset(ctx, persistExportArgs{
		deps: deps, uc: uc, in: in, body: body, contentType: contentType, status: resp.StatusCode,
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
		SizeBytes:   int64(len(body)),
		Message:     fmt.Sprintf("Exported %d bytes from %s %s.", len(body), method, in.Path),
	}, nil
}

// exportRequestParams bundles the inputs buildExportRequest needs so
// the call site stays under revive's argument-limit ceiling now that
// the connection's OpenAPI catalog (specs) participates in the
// Content-Type decision (issue #453).
type exportRequestParams struct {
	cfg   Config
	auth  Authenticator
	specs map[string]*specState
	in    exportInput
}

// buildExportRequest assembles the *http.Request for the upstream
// call using the same machinery api_invoke_endpoint uses (so SSRF
// guards apply identically). Split out of runExport so the
// build-and-go logic doesn't dominate one function's complexity.
func buildExportRequest(ctx context.Context, p exportRequestParams) (*http.Request, error) {
	cfg, auth, specs, in := p.cfg, p.auth, p.specs, p.in
	method, _ := validateMethod(in.Method) // already validated by caller
	if err := validatePath(in.Path); err != nil {
		return nil, err
	}
	authHeader := authHeaderForConfig(cfg)
	if err := validateCustomHeaders(in.Headers, authHeader, cfg.StaticHeaders); err != nil {
		return nil, err
	}
	declaredContentTypes := resolveDeclaredContentTypes(specs, method, in.Path)
	bodyBytes, contentTypeFromBody, err := encodeBody(method, in.Body, declaredContentTypes, in.Headers)
	if err != nil {
		return nil, err
	}
	urlStr, err := buildURL(cfg.BaseURL, in.Path, in.Query)
	if err != nil {
		return nil, err
	}
	req, err := buildRequest(ctx, requestSpec{
		method:        method,
		url:           urlStr,
		body:          bodyBytes,
		contentType:   contentTypeFromBody,
		headers:       in.Headers,
		staticHeaders: cfg.StaticHeaders,
	})
	if err != nil {
		return nil, err
	}
	if applyErr := auth.Apply(req); applyErr != nil {
		return nil, fmt.Errorf("auth: %w", applyErr)
	}
	return req, nil
}

// persistExportArgs bundles the inputs persistExportAsset needs.
type persistExportArgs struct {
	deps        *ExportDeps
	uc          *ExportUserContext
	in          exportInput
	body        []byte
	contentType string
	status      int
}

// persistExportAsset uploads the response bytes to S3 and inserts
// the asset row + version row. Returns the asset id on success.
// Version-row failure is non-fatal — the asset row is already in
// place and the model has an id; failing the whole call would
// orphan the S3 object.
func persistExportAsset(ctx context.Context, p persistExportArgs) (string, error) {
	deps, uc, in, body, contentType, status := p.deps, p.uc, p.in, p.body, p.contentType, p.status
	assetID, err := generateExportAssetID()
	if err != nil {
		return "", fmt.Errorf("generating asset id: %w", err)
	}
	s3Key := buildExportS3Key(deps.S3Prefix, uc.UserID, assetID, contentType)
	if err := deps.S3Client.PutObject(ctx, deps.S3Bucket, s3Key, body, contentType); err != nil {
		return "", fmt.Errorf("s3 upload: %w", err)
	}
	asset := ExportAsset{
		ID:             assetID,
		OwnerID:        uc.UserID,
		OwnerEmail:     uc.UserEmail,
		Name:           in.Name,
		Description:    in.Description,
		ContentType:    contentType,
		S3Bucket:       deps.S3Bucket,
		S3Key:          s3Key,
		SizeBytes:      int64(len(body)),
		Tags:           in.Tags,
		Provenance:     buildExportProvenance(uc, in, status),
		SessionID:      uc.SessionID,
		IdempotencyKey: in.IdempotencyKey,
	}
	if err := deps.AssetStore.InsertExportAsset(ctx, asset); err != nil {
		return "", fmt.Errorf("insert asset row: %w", err)
	}
	versionID, vidErr := generateExportAssetID()
	if vidErr != nil {
		// Generating an id should never fail (crypto/rand). If it
		// somehow does, the asset row is already in place — log
		// and return the asset id so the model has a usable handle.
		slog.Warn("api_export: generating version id failed",
			"asset_id", assetID, "error", vidErr)
		return assetID, nil
	}
	if _, vErr := deps.VersionStore.CreateExportVersion(ctx, ExportVersion{
		ID:            versionID,
		AssetID:       assetID,
		S3Key:         s3Key,
		S3Bucket:      deps.S3Bucket,
		ContentType:   contentType,
		SizeBytes:     int64(len(body)),
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
	return assetID, nil
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

// readCappedExportBody reads up to maxBytes+1 bytes; returning an
// error (NOT a truncated asset) when the body exceeded the cap. A
// truncated asset would be misleading — the operator clicking the
// portal asset would have no way to know the file is incomplete.
// Refusing the export and pointing the model at "raise the cap or
// narrow the request" is the correct contract.
func readCappedExportBody(r io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = defaultExportMaxBytes
	}
	read, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading upstream response: %w", err)
	}
	if int64(len(read)) > maxBytes {
		return nil, fmt.Errorf("upstream response exceeded api_export cap of %d bytes — narrow the request (smaller page, fewer fields) or raise platform.export.max_bytes", maxBytes)
	}
	return read, nil
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

// extensionForContentType picks a file extension for a content-type
// string. The Go std library's `mime` package has ExtensionsByType,
// which returns a list (we take the first entry stripped of its
// leading dot). Falls back to "bin" — a downloaded "blob.bin" is
// always more useful than no extension.
func extensionForContentType(contentType string) string {
	if contentType == "" {
		return "bin"
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "bin"
	}
	exts, _ := mime.ExtensionsByType(mediaType)
	if len(exts) == 0 {
		// Hand-roll the most common cases the std mime db
		// often misses on Linux containers.
		switch mediaType {
		case "application/json":
			return "json"
		case "application/xml", "text/xml":
			return "xml"
		case "text/csv":
			return "csv"
		case "text/plain":
			return "txt"
		}
		return "bin"
	}
	return strings.TrimPrefix(exts[0], ".")
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

// buildExportProvenance records the api_export call so portal
// viewers can render where the asset came from.
func buildExportProvenance(uc *ExportUserContext, in exportInput, status int) ExportProvenance {
	return ExportProvenance{
		UserID:    uc.UserID,
		SessionID: uc.SessionID,
		ToolCalls: []ExportProvenanceCall{
			{
				ToolName:  exportToolName,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Parameters: map[string]any{
					"connection":      in.Connection,
					"method":          in.Method,
					"path":            in.Path,
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
