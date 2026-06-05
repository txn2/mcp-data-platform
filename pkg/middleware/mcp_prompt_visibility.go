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
	filtered := make([]*mcp.Prompt, 0, before)
	for _, pr := range listResult.Prompts {
		if cfg.IsVisible(email, personas, isAdmin, pr.Name) {
			filtered = append(filtered, pr)
		}
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
	if req == nil {
		return ""
	}
	params, ok := req.GetParams().(*mcp.GetPromptParams)
	if !ok || params == nil {
		return ""
	}
	return params.Name
}
