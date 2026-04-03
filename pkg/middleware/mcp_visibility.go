package middleware

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCP method names used across middleware.
const (
	methodToolsList              = "tools/list"
	methodResourcesTemplatesList = "resources/templates/list"
	methodPromptsList            = "prompts/list"
)

// ToolVisibilityConfig configures tool visibility filtering for tools/list responses.
type ToolVisibilityConfig struct {
	// GlobalAllow/GlobalDeny are static glob patterns from the config file.
	GlobalAllow []string
	GlobalDeny  []string

	// Authenticator resolves the caller's identity from the request context.
	Authenticator Authenticator

	// IsToolAllowedForPersona checks whether a tool is allowed for a persona.
	// Takes (personaName, toolName) and returns true if allowed.
	// If nil, persona-based filtering is skipped.
	IsToolAllowedForPersona func(ctx context.Context, roles []string, toolName string) bool
}

// MCPToolVisibilityMiddleware creates MCP protocol-level middleware that filters
// tools/list responses. It applies two layers of filtering:
//
//  1. Global allow/deny patterns from config (static, applies to all users)
//  2. Persona-based filtering (dynamic, based on the authenticated user's persona)
//
// The persona filter ensures agents only see tools they're authorized to use,
// reducing token waste and preventing confusion from inaccessible tools.
func MCPToolVisibilityMiddleware(cfg ToolVisibilityConfig) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil {
				return result, err
			}
			return filterToolVisibility(ctx, cfg, method, result)
		}
	}
}

// filterToolVisibility filters tools from a tools/list response based on
// global patterns and persona rules. Non-tools/list methods pass through unchanged.
func filterToolVisibility(ctx context.Context, cfg ToolVisibilityConfig, method string, result mcp.Result) (mcp.Result, error) {
	if method != methodToolsList {
		return result, nil
	}

	listResult, ok := result.(*mcp.ListToolsResult)
	if !ok || listResult == nil {
		return result, nil
	}

	roles := resolveRolesIfNeeded(ctx, cfg)
	listResult.Tools = filterTools(ctx, listResult.Tools, cfg, roles)

	return listResult, nil
}

// filterTools applies global and persona-based filtering to a tool list.
func filterTools(ctx context.Context, tools []*mcp.Tool, cfg ToolVisibilityConfig, roles []string) []*mcp.Tool {
	filtered := make([]*mcp.Tool, 0, len(tools))
	for _, tool := range tools {
		if !IsToolVisible(tool.Name, cfg.GlobalAllow, cfg.GlobalDeny) {
			continue
		}
		if roles != nil && cfg.IsToolAllowedForPersona != nil {
			if !cfg.IsToolAllowedForPersona(ctx, roles, tool.Name) {
				continue
			}
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

// resolveRolesIfNeeded resolves the caller's roles when persona filtering is configured.
func resolveRolesIfNeeded(ctx context.Context, cfg ToolVisibilityConfig) []string {
	if cfg.Authenticator == nil || cfg.IsToolAllowedForPersona == nil {
		return nil
	}
	return resolveCallerRoles(ctx, cfg.Authenticator)
}

// resolveCallerRoles extracts the caller's roles from the request context.
func resolveCallerRoles(ctx context.Context, authenticator Authenticator) []string {
	// Try pre-authenticated user first (set by HTTP-level auth middleware).
	if userInfo := GetPreAuthenticatedUser(ctx); userInfo != nil {
		return userInfo.Roles
	}

	// Fall back to authenticating from the context (e.g. token in headers).
	userInfo, err := authenticator.Authenticate(ctx)
	if err != nil || userInfo == nil {
		slog.Debug("visibility: no authenticated user for tools/list filtering")
		return nil
	}
	return userInfo.Roles
}

// IsToolVisible determines whether a tool should appear in tools/list based on
// allow/deny glob patterns. Semantics:
//   - No patterns configured: all tools visible
//   - Allow only: only matching tools pass
//   - Deny only: all pass except denied
//   - Both: allow first, then deny removes from that set
//   - Invalid glob patterns are treated as non-matching (silent skip)
func IsToolVisible(name string, allow, deny []string) bool {
	if len(allow) == 0 && len(deny) == 0 {
		return true
	}

	visible := len(allow) == 0 // If no allow rules, default to visible

	for _, pattern := range allow {
		if matched, err := filepath.Match(pattern, name); err == nil && matched {
			visible = true
			break
		}
	}

	if !visible {
		return false
	}

	for _, pattern := range deny {
		if matched, err := filepath.Match(pattern, name); err == nil && matched {
			return false
		}
	}

	return true
}
