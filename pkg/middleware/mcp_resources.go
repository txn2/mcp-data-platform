package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

const (
	methodListResources = "resources/list"
	methodReadResource  = "resources/read"
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
}

// MCPManagedResourceMiddleware intercepts resources/list and resources/read
// to inject managed (database-backed) resources alongside the SDK's static
// resources. It filters the list by the caller's visible scopes derived from
// PlatformContext.
func MCPManagedResourceMiddleware(cfg ManagedResourceConfig) mcp.Middleware {
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

// handleManagedList appends managed resources to the SDK's static list.
func handleManagedList(ctx context.Context, next mcp.MethodHandler, method string, req mcp.Request, cfg ManagedResourceConfig) (mcp.Result, error) {
	// First get the static resources from the SDK.
	result, err := next(ctx, method, req)
	if err != nil {
		return result, err
	}

	// Derive visible scopes from PlatformContext.
	scopes := scopesFromContext(ctx, cfg)

	// Query managed resources.
	managed, _, listErr := cfg.Store.List(ctx, resource.Filter{
		Scopes: scopes,
		Limit:  500, // reasonable upper bound for resources/list
	})
	if listErr != nil {
		slog.Warn("managed resources: list failed, returning static only", "error", listErr)
		return result, nil
	}
	if len(managed) == 0 {
		return result, nil
	}

	// Merge managed resources into the result.
	listResult, ok := result.(*mcp.ListResourcesResult)
	if !ok {
		return result, nil
	}

	for i := range managed {
		r := &managed[i]
		listResult.Resources = append(listResult.Resources, &mcp.Resource{
			URI:         r.URI,
			Name:        r.DisplayName,
			Description: r.Description,
			MIMEType:    r.MIMEType,
		})
	}

	return listResult, nil
}

// handleManagedRead tries the managed resource store first, then falls through
// to the SDK's handler for static/template resources.
func handleManagedRead(ctx context.Context, next mcp.MethodHandler, method string, req mcp.Request, cfg ManagedResourceConfig) (mcp.Result, error) {
	// Extract URI from the request.
	uri, err := extractResourceURI(req)
	if err != nil || uri == "" {
		return next(ctx, method, req)
	}

	scheme := cfg.URIScheme
	if scheme == "" {
		scheme = resource.DefaultURIScheme
	}
	prefix := scheme + "://"

	// Only handle URIs matching our scheme.
	if !strings.HasPrefix(uri, prefix) {
		return next(ctx, method, req)
	}

	// Look up in managed resources store.
	res, getErr := cfg.Store.GetByURI(ctx, uri)
	if getErr != nil {
		// Not found in managed resources — fall through to SDK.
		return next(ctx, method, req)
	}

	// Permission check.
	pc := GetPlatformContext(ctx)
	claims := resource.Claims{}
	if pc != nil {
		claims = claimsFromPC(pc, cfg)
	}
	if !resource.CanReadResource(claims, res) {
		return nil, fmt.Errorf("resource not found: %s", uri)
	}

	return fetchResourceContent(ctx, cfg, res)
}

// fetchResourceContent fetches resource content from S3 and builds the read result.
func fetchResourceContent(ctx context.Context, cfg ManagedResourceConfig, res *resource.Resource) (*mcp.ReadResourceResult, error) {
	if cfg.S3Client == nil {
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
		slog.Error("managed resource read: s3 get failed", "error", s3Err, "uri", res.URI)
		return nil, fmt.Errorf("error reading resource content")
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

// scopesFromContext derives resource visibility scopes from PlatformContext.
func scopesFromContext(ctx context.Context, cfg ManagedResourceConfig) []resource.ScopeFilter {
	pc := GetPlatformContext(ctx)
	if pc == nil {
		return []resource.ScopeFilter{{Scope: resource.ScopeGlobal}}
	}
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
	return claims
}

// extractResourceURI extracts the URI from a resources/read request.
func extractResourceURI(req mcp.Request) (string, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}
	var wrapper struct {
		Params struct {
			URI string `json:"uri"`
		} `json:"params"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return "", fmt.Errorf("unmarshaling request: %w", err)
	}
	return wrapper.Params.URI, nil
}

// isTextMIME returns true for MIME types that should be returned as inline text.
func isTextMIME(mime string) bool {
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	textTypes := []string{
		"application/json",
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
