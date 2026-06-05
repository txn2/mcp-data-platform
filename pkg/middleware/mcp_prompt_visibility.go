package middleware

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// PromptVisibility reports whether a prompt is visible to a caller identified
// by email, persona memberships, and admin status. Built-in (non-database)
// prompts MUST return true. The platform supplies this; it consults the
// per-prompt scope/owner recorded at registration time.
type PromptVisibility func(email string, personas []string, isAdmin bool, promptName string) bool

// PromptVisibilityConfig configures the prompts visibility filter.
type PromptVisibilityConfig struct {
	Authenticator    Authenticator
	PersonasForRoles PersonasForRoles
	AdminPersona     string
	IsVisible        PromptVisibility

	// ListVisible returns the caller's visible database prompts as MCP
	// descriptors, each under its scope-prefixed name (global-, <persona>-,
	// personal-) computed per-viewer. Database prompts are served from here
	// rather than the shared static registry. Optional; nil disables injection.
	ListVisible func(ctx context.Context, email string, personas []string) []*mcp.Prompt

	// GetByName resolves a prefixed prompt name to the caller's visible database
	// prompt and renders it with the request arguments. Returns ok=false when no
	// such visible prompt exists. Optional; nil disables database serving.
	GetByName func(ctx context.Context, email string, personas []string, name string, args map[string]string) (*mcp.GetPromptResult, bool)
}

// MCPPromptVisibilityMiddleware scopes the native MCP prompt surface to the
// caller. It filters prompts/list and denies prompts/get for prompts the
// caller may not see: global and built-in prompts to everyone, persona-scoped
// prompts to members of the prompt's personas, and personal prompts to their
// owner. It mirrors the tools/list and resources visibility filters.
//
// This matters because every database prompt — including every user's personal
// prompts — is registered on the single shared MCP server, so without this
// filter the SDK returns all of them to every client (and serves any of them by
// name). The REST API and the manage_prompt tool already scope by owner/persona;
// this closes the same gap on the native MCP prompts surface.
func MCPPromptVisibilityMiddleware(cfg PromptVisibilityConfig) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case methodPromptsList:
				result, err := next(ctx, method, req)
				if err != nil {
					return result, err
				}
				return filterPromptVisibility(ctx, cfg, req, result), nil
			case methodPromptsGet:
				// Database prompts are served from the database by their
				// scope-prefixed name, since they are not in the shared registry.
				if res, ok := serveDatabaseGet(ctx, cfg, req); ok {
					return res, nil
				}
				if err := denyHiddenPromptGet(ctx, cfg, req); err != nil {
					return nil, err
				}
				return next(ctx, method, req)
			default:
				return next(ctx, method, req)
			}
		}
	}
}

// resolvePromptCaller resolves the caller's email, persona memberships, and
// admin status for a prompt visibility decision. A nil PlatformContext (auth
// failed or absent) yields an empty identity, so only global and built-in
// prompts survive — fail closed.
func resolvePromptCaller(ctx context.Context, cfg PromptVisibilityConfig, req mcp.Request) (email string, personas []string, isAdmin bool) {
	pc := getOrAuthenticatePC(ctx, req, cfg.Authenticator, cfg.PersonasForRoles, cfg.AdminPersona)
	if pc == nil {
		return "", nil, false
	}
	email = pc.UserEmail
	isAdmin = pc.IsAdmin
	if cfg.PersonasForRoles != nil {
		personas = cfg.PersonasForRoles(pc.Roles)
	} else if pc.PersonaName != "" {
		personas = []string{pc.PersonaName}
	}
	return email, personas, isAdmin
}

// filterPromptVisibility removes prompts the caller may not see from a
// prompts/list result.
func filterPromptVisibility(ctx context.Context, cfg PromptVisibilityConfig, req mcp.Request, result mcp.Result) mcp.Result {
	listResult, ok := result.(*mcp.ListPromptsResult)
	if !ok || listResult == nil || cfg.IsVisible == nil {
		return result
	}

	email, personas, isAdmin := resolvePromptCaller(ctx, cfg, req)

	before := len(listResult.Prompts)

	// The static result holds only built-in prompts; database prompts are served
	// from the database with a per-viewer scope prefix. Filter the built-ins by
	// visibility, then append the caller's visible database prompts (each under
	// its prefixed name, so names never collide and need no dedup).
	filtered := make([]*mcp.Prompt, 0, before)
	for _, pr := range listResult.Prompts {
		if cfg.IsVisible(email, personas, isAdmin, pr.Name) {
			filtered = append(filtered, pr)
		}
	}
	if cfg.ListVisible != nil {
		filtered = append(filtered, cfg.ListVisible(ctx, email, personas)...)
	}
	listResult.Prompts = filtered

	if before != len(filtered) {
		slog.Debug("prompts list: filtered",
			"in", before,
			"out", len(filtered),
		)
	}
	return listResult
}

// denyHiddenPromptGet returns a not-found error when the caller requests a
// prompt they are not entitled to see, so a known name cannot be used to fetch
// another user's prompt content. It deliberately mirrors a not-found result
// rather than a permission error so callers cannot probe for a prompt's
// existence. Returns nil (allow) for visible prompts, built-in prompts, and
// when the name cannot be determined (the SDK handles those).
func denyHiddenPromptGet(ctx context.Context, cfg PromptVisibilityConfig, req mcp.Request) error {
	if cfg.IsVisible == nil {
		return nil
	}
	name := promptNameFromRequest(req)
	if name == "" {
		return nil
	}
	email, personas, isAdmin := resolvePromptCaller(ctx, cfg, req)
	if cfg.IsVisible(email, personas, isAdmin, name) {
		return nil
	}
	slog.Debug("prompts get: denied hidden prompt", "prompt", name)
	return fmt.Errorf("prompt %q not found", name)
}

// promptNameFromRequest extracts the requested prompt name from a prompts/get
// request, or "" when it cannot be determined.
func promptNameFromRequest(req mcp.Request) string {
	params := getPromptParams(req)
	if params == nil {
		return ""
	}
	return params.Name
}

// getPromptParams returns the typed params of a prompts/get request, or nil.
func getPromptParams(req mcp.Request) *mcp.GetPromptParams {
	if req == nil {
		return nil
	}
	params, ok := req.GetParams().(*mcp.GetPromptParams)
	if !ok {
		return nil
	}
	return params
}

// serveDatabaseGet resolves a prefixed prompt name to the caller's visible
// database prompt and serves it, returning (result, true) when it exists.
// Database prompts are not in the shared registry, so this is how a caller
// invokes them by their scope-prefixed name.
func serveDatabaseGet(ctx context.Context, cfg PromptVisibilityConfig, req mcp.Request) (mcp.Result, bool) {
	if cfg.GetByName == nil {
		return nil, false
	}
	params := getPromptParams(req)
	if params == nil || params.Name == "" {
		return nil, false
	}
	email, personas, _ := resolvePromptCaller(ctx, cfg, req)
	res, ok := cfg.GetByName(ctx, email, personas, params.Name, params.Arguments)
	if !ok {
		return nil, false
	}
	return res, true
}
