package persona

import (
	"context"
	"path/filepath"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// ToolFilter filters tools based on persona rules.
type ToolFilter struct {
	registry *Registry
}

// NewToolFilter creates a new tool filter.
func NewToolFilter(registry *Registry) *ToolFilter {
	return &ToolFilter{registry: registry}
}

// IsAllowed checks if a tool is allowed for a persona.
func (*ToolFilter) IsAllowed(persona *Persona, toolName string) bool {
	allowed, _, _ := evaluateToolAccess(persona, toolName)
	return allowed
}

// AccessSource indicates which clause of a persona's tool rules
// produced an allow/deny decision. Used by the admin Tools-detail
// endpoint so operators can see WHY a persona allows or denies a tool.
type AccessSource string

const (
	// AccessSourceAllow — an explicit allow pattern matched.
	AccessSourceAllow AccessSource = "allow"
	// AccessSourceDeny — an explicit deny pattern matched (takes precedence over allow).
	AccessSourceDeny AccessSource = "deny"
	// AccessSourceDefault — no allow pattern matched; falls through to fail-closed default.
	AccessSourceDefault AccessSource = "default"
)

// AccessDecision is the per-tool decision a persona produces for a
// specific tool name, with the matching pattern and source recorded
// so the admin UI can render "why".
type AccessDecision struct {
	Allowed        bool         `json:"allowed"`
	MatchedPattern string       `json:"matched_pattern"`
	Source         AccessSource `json:"source"`
}

// WhyAllowed returns the access decision for a tool name with the
// matched pattern and source recorded. Mirrors IsAllowed's logic
// (deny first, allow second, default deny last) but surfaces which
// clause produced the decision so callers can explain it to operators.
func (*ToolFilter) WhyAllowed(persona *Persona, toolName string) AccessDecision {
	allowed, pattern, source := evaluateToolAccess(persona, toolName)
	return AccessDecision{
		Allowed:        allowed,
		MatchedPattern: pattern,
		Source:         source,
	}
}

// evaluateToolAccess is the shared core of IsAllowed and WhyAllowed.
func evaluateToolAccess(persona *Persona, toolName string) (allowed bool, pattern string, source AccessSource) {
	if persona == nil {
		// DENY if no persona — fail closed.
		return false, "", AccessSourceDefault
	}

	// Check deny rules first (they take precedence).
	for _, p := range persona.Tools.Deny {
		if matchPattern(p, toolName) {
			return false, p, AccessSourceDeny
		}
	}

	// Check allow rules.
	for _, p := range persona.Tools.Allow {
		if matchPattern(p, toolName) {
			return true, p, AccessSourceAllow
		}
	}

	// Default deny if no allow rules match.
	return false, "", AccessSourceDefault
}

// IsConnectionAllowed checks if a connection is allowed for a persona.
// Connections are deny-by-default: a persona must carry an explicit
// ConnectionRules.Allow pattern that matches the connection name. An empty
// Allow list grants no connections. Deny rules take precedence over allow.
// Empty connection names are platform-level (always allowed). A nil persona
// is denied (fail-closed).
//
// This mirrors the tool axis (evaluateToolAccess), which also defaults to
// deny. There is no "empty Allow means all connections" shortcut and no
// special-cased connection category — the operator grants exactly the
// connections each persona should reach.
func (*ToolFilter) IsConnectionAllowed(persona *Persona, connectionName string) bool {
	if persona == nil {
		return false
	}

	// Empty connection name = platform-level tools (always allowed).
	if connectionName == "" {
		return true
	}

	// Check deny rules first (they take precedence).
	for _, pattern := range persona.Connections.Deny {
		if matchPattern(pattern, connectionName) {
			return false
		}
	}

	// Deny-by-default: require an explicit allow match.
	for _, pattern := range persona.Connections.Allow {
		if matchPattern(pattern, connectionName) {
			return true
		}
	}
	return false
}

// IsAPIRouteAllowed reports whether the persona may invoke (method, path)
// on the named connection through the HTTP API gateway. Layered on top
// of IsConnectionAllowed: callers MUST also pass the connection-level
// gate; this function adds per-(method, path) granularity for kind=api
// connections.
//
// Semantics (also documented on APIRouteRule):
//   - If no APIRoutes entry's Connection glob matches the connection, the
//     check is a no-op (returns true) — the existing connection-level
//     check is the sole gate, preserving backward-compat for personas
//     written before APIRoutes existed.
//   - If at least one APIRoutes entry matches the connection, the call
//     must pass: no matching deny rule, AND at least one matching allow
//     rule.
//   - A nil persona always denies (fail-closed).
func (*ToolFilter) IsAPIRouteAllowed(persona *Persona, connection, method, path string) bool {
	if persona == nil {
		return false
	}
	relevant := matchingRouteRules(persona.APIRoutes, connection)
	if len(relevant) == 0 {
		// No rule touches this connection — the route check is a no-op
		// and the existing connection-level gate decides.
		return true
	}
	for _, rule := range relevant {
		if rule.Action == ActionDeny && routeRuleMatches(rule, method, path) {
			return false
		}
	}
	for _, rule := range relevant {
		if rule.Action != ActionDeny && routeRuleMatches(rule, method, path) {
			return true
		}
	}
	return false
}

// matchingRouteRules returns the subset of rules whose Connection glob
// matches the given connection name. Extracted so callers can pre-filter
// the rule set once instead of re-scanning per check.
func matchingRouteRules(rules []APIRouteRule, connection string) []APIRouteRule {
	var out []APIRouteRule
	for _, r := range rules {
		if r.Connection != "" && matchPattern(r.Connection, connection) {
			out = append(out, r)
		}
	}
	return out
}

// routeRuleMatches reports whether a rule's Methods and Paths globs
// both match the given method and path. Empty Methods or Paths means
// "any value" for that dimension.
func routeRuleMatches(rule APIRouteRule, method, path string) bool {
	return matchAny(rule.Methods, method) && matchAny(rule.Paths, path)
}

// matchAny returns true if patterns is empty (treated as "any") or if
// any pattern in the slice matches the value.
func matchAny(patterns []string, value string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if matchPattern(p, value) {
			return true
		}
	}
	return false
}

// FilterTools filters a list of tools based on persona rules.
func (f *ToolFilter) FilterTools(persona *Persona, tools []string) []string {
	if persona == nil {
		return nil // DENY ALL if no persona - fail closed
	}

	var allowed []string
	for _, tool := range tools {
		if f.IsAllowed(persona, tool) {
			allowed = append(allowed, tool)
		}
	}
	return allowed
}

// matchPattern checks if a name matches a glob pattern.
// Supports glob-style patterns with * wildcard.
func matchPattern(pattern, name string) bool {
	matched, err := filepath.Match(pattern, name)
	if err != nil {
		return false
	}
	return matched
}

// Authorizer implements middleware.Authorizer using personas.
type Authorizer struct {
	registry   *Registry
	roleMapper RoleMapper
	filter     *ToolFilter
}

// NewAuthorizer creates a new persona-based authorizer.
func NewAuthorizer(registry *Registry, mapper RoleMapper) *Authorizer {
	return &Authorizer{
		registry:   registry,
		roleMapper: mapper,
		filter:     NewToolFilter(registry),
	}
}

// IsAuthorized checks if the user is authorized for the tool on the given connection.
// Both the tool and the connection must be allowed by the persona's rules.
// Returns the resolved persona name for audit logging.
func (a *Authorizer) IsAuthorized(ctx context.Context, _ string, roles []string, toolName, connectionName string) (allowed bool, personaName, reason string) {
	// Get persona for roles
	persona, err := a.roleMapper.MapToPersona(ctx, roles)
	if err != nil {
		return false, "", "failed to determine persona"
	}

	if persona != nil {
		personaName = persona.Name
	}

	// Check if tool is allowed
	if !a.filter.IsAllowed(persona, toolName) {
		return false, personaName, "tool not allowed for persona: " + personaName
	}

	// Check if connection is allowed
	if !a.filter.IsConnectionAllowed(persona, connectionName) {
		return false, personaName, "connection not allowed for persona: " + personaName
	}

	return true, personaName, ""
}

// IsAPIRouteAllowed authorizes (method, path) on the named HTTP API
// gateway connection for the user with the given roles. Layered on
// top of IsAuthorized: the caller (the api gateway toolkit) is
// expected to have already passed the tool/connection-level gate via
// the standard MCP middleware, and this method adds per-route
// granularity. Returns the resolved persona name (for audit) and a
// reason on denial.
func (a *Authorizer) IsAPIRouteAllowed(ctx context.Context, roles []string, connection, method, path string) (allowed bool, personaName, reason string) {
	per, err := a.roleMapper.MapToPersona(ctx, roles)
	if err != nil {
		return false, "", "failed to determine persona"
	}
	if per != nil {
		personaName = per.Name
	}
	if !a.filter.IsAPIRouteAllowed(per, connection, method, path) {
		return false, personaName, "persona " + personaName + " disallows " + method + " " + path + " on connection " + connection
	}
	return true, personaName, ""
}

// Verify interface compliance.
var _ middleware.Authorizer = (*Authorizer)(nil)
