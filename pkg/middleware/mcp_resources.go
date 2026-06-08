package middleware

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// errResourceBlobMissing marks a read failure where the resource row exists but
// its backing object is gone (an orphaned resource). handleManagedRead checks
// for it to self-heal by pruning the dead row.
var errResourceBlobMissing = errors.New("backing object missing")

// resourcePruner is the optional capability to delete a resource row. The
// configured Store (resource.Store) implements it; ResourceListProvider does
// not require it, so the read path type-asserts and prunes only when available.
type resourcePruner interface {
	Delete(ctx context.Context, id string) error
}

const (
	methodListResources = "resources/list"
	methodReadResource  = "resources/read"
	logKeyURI           = "uri"
	logKeyUserID        = "user_id"
	logKeyError         = "error"

	// mimeTypeJSON is the MIME type for JSON content.
	mimeTypeJSON = "application/json"

	// userIDAnonymous is the stable label used in log lines when no
	// authenticated PlatformContext is available (auth failed or wasn't
	// attempted). Keeps log queries groupable on user_id.
	userIDAnonymous = "anonymous"
)

// ResourceListProvider loads managed resources for a given set of scope filters.
type ResourceListProvider interface {
	List(ctx context.Context, filter resource.Filter) ([]resource.Resource, int, error)
	GetByURI(ctx context.Context, uri string) (*resource.Resource, error)
}

// ResourceBlobReader fetches resource content from blob storage.
type ResourceBlobReader interface {
	GetObject(ctx context.Context, bucket, key string) (body []byte, contentType string, err error)
}

// PersonasForRoles resolves all persona names a user belongs to from their roles.
type PersonasForRoles func(roles []string) []string

// ManagedResourceConfig configures the managed resources middleware.
type ManagedResourceConfig struct {
	Store            ResourceListProvider
	S3Client         ResourceBlobReader
	S3Bucket         string
	URIScheme        string
	PersonasForRoles PersonasForRoles // resolves roles → persona names
	Authenticator    Authenticator    // authenticates users for resources/list and resources/read
	AdminPersona     string           // persona name that grants platform admin
}

// MCPManagedResourceMiddleware intercepts resources/list and resources/read
// to inject managed (database-backed) resources alongside the SDK's static
// resources. It filters the list by the caller's visible scopes derived from
// PlatformContext.
func MCPManagedResourceMiddleware(cfg ManagedResourceConfig) mcp.Middleware {
	slog.Debug("MCPManagedResourceMiddleware: registered",
		"store_nil", cfg.Store == nil,
		"s3_nil", cfg.S3Client == nil,
		"auth_nil", cfg.Authenticator == nil,
		"scheme", cfg.URIScheme,
		"admin_persona", cfg.AdminPersona,
	)
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case methodListResources:
				return handleManagedList(ctx, next, method, req, cfg)
			case methodReadResource:
				return handleManagedRead(ctx, next, method, req, cfg)
			default:
				return next(ctx, method, req)
			}
		}
	}
}

// handleManagedList filters the SDK's resource list by the caller's visible
// scopes. Managed resources are registered with the SDK via AddResource for
// discoverability, but the SDK returns ALL resources to every client. This
// middleware removes resources the caller shouldn't see based on their auth.
//
// Logging philosophy: at DEBUG we emit one line per resources/list call when
// filtering actually happens (with the user/persona that authorized the
// filter and the in/out counts). Per-resource enumeration and no-op early
// returns are intentionally silent: the resource list itself is the audit
// surface for "what was visible", not infra logs.
func handleManagedList(ctx context.Context, next mcp.MethodHandler, method string, req mcp.Request, cfg ManagedResourceConfig) (mcp.Result, error) {
	result, err := next(ctx, method, req)
	if err != nil {
		return result, err
	}

	listResult, ok := result.(*mcp.ListResourcesResult)
	if !ok || cfg.Store == nil {
		return result, nil
	}

	prefix := managedURIPrefix(cfg)
	if !containsManagedResources(listResult.Resources, prefix) {
		return result, nil
	}

	pc, visibleURIs := resolveVisibleManagedURIsWithPC(ctx, req, cfg)
	before := len(listResult.Resources)
	listResult.Resources = filterResources(listResult.Resources, prefix, visibleURIs)
	slog.Debug("managed resources list: filtered",
		"in", before,
		"out", len(listResult.Resources),
		logKeyUserID, userIDForLog(pc),
		"persona", personaForLog(pc),
	)
	return listResult, nil
}

// userIDForLog returns a stable user attribution for the filter log line.
// userIDAnonymous when no PlatformContext is present (auth failed or
// skipped); concrete UserID otherwise. Keeps the log line shape stable
// across the authenticated and anonymous paths so log queries can group
// on user_id.
func userIDForLog(pc *PlatformContext) string {
	if pc == nil || pc.UserID == "" {
		return userIDAnonymous
	}
	return pc.UserID
}

// personaForLog mirrors userIDForLog for the persona attribution column.
func personaForLog(pc *PlatformContext) string {
	if pc == nil || pc.PersonaName == "" {
		return ""
	}
	return pc.PersonaName
}

// managedURIPrefix returns the URI prefix for managed resources.
func managedURIPrefix(cfg ManagedResourceConfig) string {
	scheme := cfg.URIScheme
	if scheme == "" {
		scheme = resource.DefaultURIScheme
	}
	return scheme + "://"
}

// containsManagedResources checks if any resource in the list has the managed prefix.
func containsManagedResources(resources []*mcp.Resource, prefix string) bool {
	for _, r := range resources {
		if strings.HasPrefix(r.URI, prefix) {
			return true
		}
	}
	return false
}

// resolveVisibleManagedURIsWithPC returns the set of managed resource URIs
// visible to the authenticated caller, along with the PlatformContext that
// authorized the filter so callers can include user/persona attribution in
// their log lines. Returns (nil, nil) when auth fails.
func resolveVisibleManagedURIsWithPC(ctx context.Context, req mcp.Request, cfg ManagedResourceConfig) (pc *PlatformContext, visible map[string]bool) {
	pc = getOrAuthenticatePC(ctx, req, cfg.Authenticator, cfg.PersonasForRoles, cfg.AdminPersona)
	if pc == nil {
		return nil, nil
	}
	scopes := scopesFromPlatformContext(pc, cfg)
	managed, _, err := cfg.Store.List(ctx, resource.Filter{Scopes: scopes, Limit: 1000})
	if err != nil {
		slog.Warn("managed resources: scope filter failed, removing all managed",
			logKeyError, err,
			logKeyUserID, pc.UserID,
			"persona", pc.PersonaName,
		)
		return pc, nil
	}
	visible = make(map[string]bool, len(managed))
	for i := range managed {
		visible[managed[i].URI] = true
	}
	return pc, visible
}

// filterResources keeps static resources and only visible managed resources.
// If visibleURIs is nil, all managed resources are removed.
func filterResources(resources []*mcp.Resource, prefix string, visibleURIs map[string]bool) []*mcp.Resource {
	filtered := make([]*mcp.Resource, 0, len(resources))
	for _, r := range resources {
		if !strings.HasPrefix(r.URI, prefix) {
			filtered = append(filtered, r) // static — always keep
		} else if visibleURIs != nil && visibleURIs[r.URI] {
			filtered = append(filtered, r) // managed — keep if visible
		}
	}
	return filtered
}

// handleManagedRead tries the managed resource store first, then falls through
// to the SDK's handler for static/template resources.
func handleManagedRead(ctx context.Context, next mcp.MethodHandler, method string, req mcp.Request, cfg ManagedResourceConfig) (mcp.Result, error) {
	uri, err := extractResourceURI(req)
	if err != nil || uri == "" {
		slog.Debug("managed resources read: URI extraction failed, falling through", logKeyError, err, logKeyURI, uri)
		return next(ctx, method, req)
	}

	scheme := cfg.URIScheme
	if scheme == "" {
		scheme = resource.DefaultURIScheme
	}
	prefix := scheme + "://"

	if !strings.HasPrefix(uri, prefix) {
		slog.Debug("managed resources read: URI doesn't match scheme, falling through", logKeyURI, uri, "prefix", prefix)
		return next(ctx, method, req)
	}

	res, getErr := cfg.Store.GetByURI(ctx, uri)
	if getErr != nil {
		slog.Debug("managed resources read: not in store, falling through to SDK", logKeyURI, uri, logKeyError, getErr)
		return next(ctx, method, req)
	}
	slog.Debug("managed resources read: found in store", logKeyURI, uri, "scope", res.Scope, "id", res.ID)

	pc := getOrAuthenticatePC(ctx, req, cfg.Authenticator, cfg.PersonasForRoles, cfg.AdminPersona)
	if pc == nil {
		slog.Warn("managed resources read: auth failed, falling through to SDK", logKeyURI, uri)
		return next(ctx, method, req)
	}
	slog.Debug("managed resources read: authenticated", logKeyURI, uri, logKeyUserID, pc.UserID, "persona", pc.PersonaName)

	claims := claimsFromPC(pc, cfg)
	if !resource.CanReadResource(claims, res) {
		slog.Warn("managed resources read: permission denied", logKeyURI, uri, logKeyUserID, pc.UserID, "scope", res.Scope)
		return nil, fmt.Errorf("resource not found: %s", uri)
	}

	slog.Debug("managed resources read: serving content", logKeyURI, uri, "mime_type", res.MIMEType, "s3_key", res.S3Key)
	result, err := fetchResourceContent(ctx, cfg, res)
	if err != nil && errors.Is(err, errResourceBlobMissing) {
		// Self-heal: the backing object is confirmed gone, so the row is a
		// permanent orphan that would keep appearing in resources/list and
		// failing every read. Best-effort prune it so the list/read divergence
		// closes the moment an orphan is actually accessed.
		pruneOrphanedResource(ctx, cfg, res)
	}
	return result, err
}

// pruneOrphanedResource best-effort deletes a resource row whose backing object
// is missing, so it stops appearing in resources/list. It only runs on a
// confirmed not-found (errResourceBlobMissing), never on a transient failure,
// and a delete error is logged but not surfaced (the read already returned the
// actionable error to the caller).
func pruneOrphanedResource(ctx context.Context, cfg ManagedResourceConfig, res *resource.Resource) {
	pruner, ok := cfg.Store.(resourcePruner)
	if !ok || res.ID == "" {
		return
	}
	if delErr := pruner.Delete(ctx, res.ID); delErr != nil {
		slog.Warn("managed resource: failed to prune orphaned row", logKeyURI, res.URI, "id", res.ID, logKeyError, delErr)
		return
	}
	slog.Info("managed resource: pruned orphaned row (backing object missing)", logKeyURI, res.URI, "id", res.ID)
}

// isObjectNotFound reports whether a blob-store GetObject error indicates the
// object does not exist (an orphaned resource), as opposed to a transient or
// permission failure that a retry might resolve. The mcp-s3 client wraps the
// underlying AWS/SeaweedFS error without a typed not-found, so detection is by
// the standard S3 not-found signatures present in the wrapped message.
func isObjectNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "nosuchkey") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "notfound") ||
		strings.Contains(msg, "status code: 404") ||
		strings.Contains(msg, "404 not found")
}

// fetchResourceContent fetches resource content from S3 and builds the read result.
func fetchResourceContent(ctx context.Context, cfg ManagedResourceConfig, res *resource.Resource) (*mcp.ReadResourceResult, error) {
	if cfg.S3Client == nil {
		slog.Warn("managed resource read: S3 client nil, returning placeholder", logKeyURI, res.URI)
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      res.URI,
				MIMEType: res.MIMEType,
				Text:     "(blob storage not configured)",
			}},
		}, nil
	}

	body, _, s3Err := cfg.S3Client.GetObject(ctx, cfg.S3Bucket, res.S3Key)
	if s3Err != nil {
		if isObjectNotFound(s3Err) {
			// The resource row exists but its backing object is gone: an
			// orphaned managed resource (it still appears in resources/list).
			// Return a distinct, actionable message instead of an opaque
			// failure, so the caller learns the content is permanently missing
			// (and an operator can prune or re-upload it) rather than concluding
			// resource reads are broken.
			slog.Warn("managed resource read: backing object missing (orphaned resource)",
				logKeyURI, res.URI, "s3_key", res.S3Key)
			return nil, fmt.Errorf("resource content unavailable for %q: %w (orphaned resource; remove or re-upload it)", res.URI, errResourceBlobMissing)
		}
		slog.Error("managed resource read: s3 get failed", logKeyError, s3Err, logKeyURI, res.URI)
		return nil, fmt.Errorf("error reading resource content for %q", res.URI)
	}

	// For text types under 1 MB, return inline. Otherwise, return as blob.
	const maxInlineSize = 1 << 20
	if isTextMIME(res.MIMEType) && int64(len(body)) <= maxInlineSize {
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      res.URI,
				MIMEType: res.MIMEType,
				Text:     string(body),
			}},
		}, nil
	}

	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{
			URI:      res.URI,
			MIMEType: res.MIMEType,
			Blob:     body,
		}},
	}, nil
}

// scopesFromPlatformContext derives resource visibility scopes from a known PlatformContext.
func scopesFromPlatformContext(pc *PlatformContext, cfg ManagedResourceConfig) []resource.ScopeFilter {
	claims := claimsFromPC(pc, cfg)
	return resource.VisibleScopes(claims)
}

// claimsFromPC builds resource Claims from PlatformContext.
func claimsFromPC(pc *PlatformContext, cfg ManagedResourceConfig) resource.Claims {
	claims := resource.Claims{
		Sub:   pc.UserID,
		Email: pc.UserEmail,
		Roles: pc.Roles,
	}
	if cfg.PersonasForRoles != nil {
		claims.Personas = cfg.PersonasForRoles(pc.Roles)
	} else if pc.PersonaName != "" {
		claims.Personas = []string{pc.PersonaName}
	}
	claims.IsAdmin = pc.IsAdmin
	claims.AdminOfPersonas = extractPersonaAdminRoles(pc.Roles)
	return claims
}

// getOrAuthenticatePC returns the PlatformContext from the context if available
// (set by MCPToolCallMiddleware for tools/call), or authenticates the user
// directly for resources/list and resources/read methods. Returns nil if
// authentication fails or no authenticator is configured.
func getOrAuthenticatePC(ctx context.Context, req mcp.Request, auth Authenticator, personasForRoles PersonasForRoles, adminPersona string) *PlatformContext {
	if pc := GetPlatformContext(ctx); pc != nil {
		slog.Debug("getOrAuthenticatePC: using existing PlatformContext", logKeyUserID, pc.UserID)
		return pc
	}
	if auth == nil {
		slog.Debug("getOrAuthenticatePC: no authenticator configured")
		return nil
	}
	// Bridge auth token from per-request headers (Streamable HTTP).
	if req != nil {
		ctx = bridgeAuthToken(ctx, req)
	}
	tokenPresent := GetToken(ctx) != ""
	slog.Debug("getOrAuthenticatePC: attempting direct auth", "token_present", tokenPresent)
	userInfo, err := auth.Authenticate(ctx)
	if err != nil || userInfo == nil {
		slog.Debug("getOrAuthenticatePC: auth failed", logKeyError, err, "user_nil", userInfo == nil)
		return nil
	}
	slog.Debug("getOrAuthenticatePC: auth succeeded", logKeyUserID, userInfo.UserID, "email", userInfo.Email)
	pc := &PlatformContext{
		UserID:    userInfo.UserID,
		UserEmail: userInfo.Email,
		Roles:     userInfo.Roles,
	}
	// Resolve persona for admin status.
	if personasForRoles != nil {
		personas := personasForRoles(userInfo.Roles)
		if len(personas) > 0 {
			pc.PersonaName = personas[0]
		}
		pc.IsAdmin = adminPersona != "" && slices.Contains(personas, adminPersona)
	}
	return pc
}

// personaAdminInfix is the role substring that marks a persona-admin grant.
const personaAdminInfix = "persona-admin:"

// extractPersonaAdminRoles extracts persona names from roles containing
// the "persona-admin:" pattern, tolerating any prefix (e.g., "dp_persona-admin:finance").
func extractPersonaAdminRoles(roles []string) []string {
	var out []string
	for _, r := range roles {
		if _, name, ok := strings.Cut(r, personaAdminInfix); ok && name != "" {
			out = append(out, name)
		}
	}
	return out
}

// extractResourceURI extracts the URI from a resources/read request.
func extractResourceURI(req mcp.Request) (string, error) {
	if req == nil {
		return "", fmt.Errorf("nil request")
	}
	params, ok := req.GetParams().(*mcp.ReadResourceParams)
	if !ok || params == nil {
		return "", fmt.Errorf("unexpected params type: %T", req.GetParams())
	}
	return params.URI, nil
}

// isTextMIME returns true for MIME types that should be returned as inline text.
func isTextMIME(mime string) bool {
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	textTypes := []string{
		mimeTypeJSON,
		"application/xml",
		"application/yaml",
		"application/x-yaml",
		"application/javascript",
		"application/typescript",
		"application/sql",
		"application/csv",
	}
	return slices.Contains(textTypes, mime)
}
