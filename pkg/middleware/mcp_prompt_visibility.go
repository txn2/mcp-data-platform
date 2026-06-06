package middleware

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// PromptVisibilityConfig configures the database-prompt serving middleware.
// There is no admin bypass: an admin sees their own personal prompts (not every
// user's) on the native surface and manages others via the admin API.
type PromptVisibilityConfig struct {
	Authenticator    Authenticator
	PersonasForRoles PersonasForRoles

	// ListVisible returns the caller's visible database prompts as MCP
	// descriptors, each under its scope-prefixed name (global-, <persona>-,
	// personal-) computed per-viewer. Optional; nil disables injection.
	ListVisible func(ctx context.Context, email string, personas []string) []*mcp.Prompt

	// GetByName resolves a prefixed prompt name to the caller's visible database
	// prompt and renders it with the request arguments. Returns ok=false when no
	// such visible prompt exists. Optional; nil disables database serving.
	GetByName func(ctx context.Context, email string, personas []string, name string, args map[string]string) (*mcp.GetPromptResult, bool)
}

// MCPPromptVisibilityMiddleware serves database prompts on the native MCP
// prompts surface scoped to the caller. Database prompts (global, persona,
// personal) are not in the shared static registry — the registry is keyed by
// name and cannot represent the per-viewer scope prefix or two users'
// same-named personal prompts. This middleware injects the caller's visible
// database prompts into prompts/list under their scope-prefixed names, and
// resolves a prefixed name on prompts/get. The caller only ever sees globals,
// their personas' prompts, and their own personal prompts, which is what scopes
// the surface. Built-in prompts remain in the static registry and pass through.
func MCPPromptVisibilityMiddleware(cfg PromptVisibilityConfig) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case methodPromptsList:
				result, err := next(ctx, method, req)
				if err != nil {
					return result, err
				}
				return injectDatabasePrompts(ctx, cfg, req, result), nil
			case methodPromptsGet:
				if res, ok := serveDatabaseGet(ctx, cfg, req); ok {
					return res, nil
				}
				return next(ctx, method, req)
			default:
				return next(ctx, method, req)
			}
		}
	}
}

// resolvePromptCaller resolves the caller's email and persona memberships. A nil
// PlatformContext (auth failed or absent) yields an empty identity, so only
// global database prompts (and the always-visible built-ins) are served.
func resolvePromptCaller(ctx context.Context, cfg PromptVisibilityConfig, req mcp.Request) (email string, personas []string) {
	pc := getOrAuthenticatePC(ctx, req, cfg.Authenticator, cfg.PersonasForRoles, "")
	if pc == nil {
		return "", nil
	}
	email = pc.UserEmail
	if cfg.PersonasForRoles != nil {
		personas = cfg.PersonasForRoles(pc.Roles)
	} else if pc.PersonaName != "" {
		personas = []string{pc.PersonaName}
	}
	return email, personas
}

// injectDatabasePrompts appends the caller's visible database prompts (under
// their per-viewer scope prefix) to a prompts/list result. The static result
// holds only built-in prompts, which are global/system and pass through.
func injectDatabasePrompts(ctx context.Context, cfg PromptVisibilityConfig, req mcp.Request, result mcp.Result) mcp.Result {
	listResult, ok := result.(*mcp.ListPromptsResult)
	if !ok || listResult == nil || cfg.ListVisible == nil {
		return result
	}
	email, personas := resolvePromptCaller(ctx, cfg, req)
	listResult.Prompts = append(listResult.Prompts, cfg.ListVisible(ctx, email, personas)...)
	return listResult
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
func serveDatabaseGet(ctx context.Context, cfg PromptVisibilityConfig, req mcp.Request) (mcp.Result, bool) {
	if cfg.GetByName == nil {
		return nil, false
	}
	params := getPromptParams(req)
	if params == nil || params.Name == "" {
		return nil, false
	}
	email, personas := resolvePromptCaller(ctx, cfg, req)
	res, ok := cfg.GetByName(ctx, email, personas, params.Name, params.Arguments)
	if !ok {
		return nil, false
	}
	return res, true
}
