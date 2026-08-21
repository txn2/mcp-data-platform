// Package middleware provides the middleware chain for tool handlers.
package middleware

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

// enrichmentModeFull is the enrichment mode value for full (non-dedup) enrichment.
const enrichmentModeFull = "full"

// Enrichment match-kind values, recorded on PlatformContext and
// surfaced in audit_logs.enrichment_match_kind so operators can
// distinguish a confident URN-equality match from a similarity
// suggestion produced by the semantic fallback path (issue #444).
const (
	// enrichmentMatchURN means the platform resolved the target
	// table or column by exact URN equality. High confidence.
	enrichmentMatchURN = "urn"

	// enrichmentMatchSemantic means the URN-equality lookup missed
	// and the platform fell back to a similarity search. The
	// enrichment payload is a SUGGESTED match, not an asserted one;
	// operators measure false-positive rate from this value.
	enrichmentMatchSemantic = "semantic"
)

// contextKey is a private type for context keys.
type contextKey int

const (
	platformContextKey contextKey = iota
	preAuthUserKey
	sourceOverrideKey
)

// PlatformContext holds platform-specific context for a request.
type PlatformContext struct {
	// Request identification
	RequestID string
	SessionID string
	StartTime time.Time

	// EventID is the identifier of the audit event this call will write, minted
	// before the handler runs (issue #1320). The audit row is written after the
	// call returns, so nothing downstream could cite the call otherwise: the
	// call-reference middleware hands this id back to the agent as `call_id`,
	// and an asset saved later records it as the source it was built from.
	// Empty on a call assembled outside MCPToolCallMiddleware.
	EventID string

	// SessionHandleThreaded records that THIS call carried an explicit
	// platform-minted session handle as an argument, as opposed to having its
	// session resolved for it (adopted from the caller's identity, taken from
	// the transport, or minted server-side for an isolated run). It is the
	// platform's only proof that a caller is able to thread a platform-injected
	// argument, which is what the purpose requirement is conditioned on. Set by
	// the session resolver (#792), read by the purpose resolver (#1317).
	SessionHandleThreaded bool

	// Purpose is the one-sentence reason the caller gave for this call, taken
	// off the request by the purpose resolver and recorded on the audit event
	// (#1317). Empty when the tool is not gated or the caller stated none.
	Purpose string

	// User information
	UserID      string
	UserEmail   string
	UserClaims  map[string]any
	Roles       []string
	PersonaName string
	AuthType    string // "oidc", "oauth", "apikey", "anonymous", "noop"
	// OnBehalfOfEmail is the address of the person an unattended caller acts
	// for, carried from UserInfo.OnBehalfOf. A managed-script run authenticates
	// as script:<name>, which owns nothing a person owns, so an ownership check
	// against UserID alone refuses a script the very resources its author can
	// edit. Ownership checks read this so a run reaches what its author
	// reaches, which is the whole rule of the feature (#1419).
	//
	// Empty for every human caller. An empty value must never match an
	// empty owner address: absence of an identity is not a shared identity.
	OnBehalfOfEmail string

	// Tool information
	ToolName    string
	ToolkitKind string
	ToolkitName string
	Connection  string

	// Authorization
	Authorized bool
	IsAdmin    bool // user belongs to the platform's admin persona
	AuthzError string

	// Transport metadata
	Transport string // "stdio" or "http"
	Source    string // "mcp", "admin", "inspector"

	// Enrichment tracking (set by enrichment middleware, read by audit)
	EnrichmentApplied     bool
	EnrichmentTokensFull  int    // estimated tokens for full enrichment
	EnrichmentTokensDedup int    // estimated tokens for deduped enrichment (0 if full sent)
	EnrichmentMode        string // "full", "summary", "reference", "none", or "" (not enriched)

	// EnrichmentMatchKind distinguishes URN-equality matches from
	// similarity-fallback matches. Set to enrichmentMatchURN when
	// the URN lookup succeeded, enrichmentMatchSemantic when the
	// platform fell back to a similarity search. Empty when no
	// enrichment ran. See pkg/audit.Event.EnrichmentMatchKind.
	EnrichmentMatchKind string

	// EnrichmentBytes is the total serialized size, in bytes, of ALL
	// enrichment content blocks the middleware appended to this call's
	// response (semantic context, memories, knowledge pages, discovery
	// note). Read by the metrics middleware to record the per-response
	// enrichment overhead (issue #761). Zero when nothing was appended.
	//
	// This is intentionally broader than EnrichmentApplied, which flags
	// only the semantic/query/storage enrichment step: a call that appends
	// memory context but no semantic context has EnrichmentBytes > 0 while
	// EnrichmentApplied stays false. EnrichmentBytes answers "how much did
	// enrichment cost the context window", not "did semantic enrichment run".
	EnrichmentBytes int

	// Results (populated after handler)
	Success      bool
	ErrorMessage string
	Duration     time.Duration
}

// NewPlatformContext creates a new platform context.
func NewPlatformContext(requestID string) *PlatformContext {
	return &PlatformContext{
		RequestID:  requestID,
		StartTime:  time.Now(),
		UserClaims: make(map[string]any),
	}
}

// nonDistinctAuthTypes are AuthType values that do NOT identify a specific
// principal. AuthTypeAnonymous (the allowed-anonymous fallback) and AuthTypeNoop
// (auth disabled) both assign every caller the SAME UserID, so keying discovery
// on that UserID would let one caller's search open the gate for every other
// caller. The empty string is treated the same way defensively: an unset
// AuthType is not proof of a distinct identity. For these, the gate falls back
// to the per-session key instead. Keep this in sync with the AuthType constants
// (see pkg/middleware/auth.go): a new shared-identity AuthType must be added here.
var nonDistinctAuthTypes = map[string]bool{"": true, AuthTypeAnonymous: true, AuthTypeNoop: true}

// DiscoveryScopeKey returns the identifier under which the search-first gate
// records and checks discovery for this call.
//
// It prefers the authenticated user identity because that stays stable even
// when a client opens a brand-new MCP session for every tool call — claude.ai's
// web connector does exactly this, minting (and discarding) a fresh session per
// request. Keying discovery on the session ID would then record a search under
// one throwaway session and check the follow-up query under a different one, so
// the gate could never be satisfied (a 100% false SEARCH_REQUIRED). Keying on
// the user makes a search open the gate for that user's subsequent per-call
// sessions.
//
// The user identity is only used when it is genuinely distinct (a real
// authenticator identified this principal); the shared "anonymous"/"noop"
// identity used when auth is disabled is NOT distinct, so those callers fall
// back to the per-session key rather than all collapsing onto one shared scope.
//
// It also falls back to the session ID for callers with no user identity at
// all, and returns "" when neither is known. The tracker treats an empty key as
// ungateable and allows the call (fail-open): with no stable identity there is
// nothing to track discovery against, and the gate is a workflow-quality guard,
// not a security boundary. The "user:"/"session:" prefixes keep the two
// namespaces from ever colliding.
func (pc *PlatformContext) DiscoveryScopeKey() string {
	// Portal-initiated runs (admin source, issue #859) must NEVER key on the
	// operator's user identity. The portal tool runner authenticates as the
	// admin, so user-first scoping would make a portal replay of a query tool
	// advance or read that operator's OWN agent-session search-first state, and
	// mix the portal run's discovery record into the operator's user scope. Key
	// them on the isolated per-run portal session id instead, so portal runs
	// never touch the operator's agent-session gate/provenance/dedup state.
	//
	// If the minted id is absent — only on a crypto-RNG failure while minting it
	// — fall through to the EMPTY, ungateable scope, NOT the user-first branch
	// below. Returning "user:<admin>" there would reintroduce exactly the
	// pollution this special-case exists to prevent; the empty scope keeps the
	// degenerate case isolated (the gate treats it as ungateable and the
	// trackers skip it) rather than leaking.
	//
	// A managed script run (#1283) is isolated on identical grounds: its host
	// bindings drive a per-run session under the identity the run belongs to, so
	// keying on that user would let a script advance or read the person's own
	// search-first state.
	if isIsolatedRunSource(pc.Source) {
		if pc.SessionID != "" {
			return "session:" + pc.SessionID
		}
		return ""
	}
	switch {
	case pc.UserID != "" && !nonDistinctAuthTypes[pc.AuthType]:
		return "user:" + pc.UserID
	case pc.SessionID != "":
		return "session:" + pc.SessionID
	default:
		return ""
	}
}

// RateLimitKey returns the identifier under which the per-user tool-call rate
// limiter (issue #929) meters this call, or "" when the call cannot be
// attributed to a stable principal.
//
// It keys on the authenticated user identity so a runaway or compromised
// principal is bounded regardless of which MCP session or source IP its calls
// arrive on — including clients that mint a fresh session per tool call. The
// user identity is used only when it is genuinely distinct (a real
// authenticator identified this principal); the shared anonymous/noop identity
// used when auth is disabled is NOT distinct, so those callers fall back to the
// per-session key rather than all draining one shared bucket. Callers with no
// user identity at all fall back to the session ID.
//
// An empty return means there is no stable key (no distinct user and no
// session): the limiter treats that as unlimited and lets the call through
// (fail-open). Rate limiting is a safety net, not an authentication boundary —
// a call with no attributable identity has already passed auth, and refusing it
// on an un-keyable basis would penalize legitimate un-sessioned transports (a
// single local stdio client) more than any abuser. The "user:"/"session:"
// prefixes keep the two namespaces from colliding, matching DiscoveryScopeKey.
func (pc *PlatformContext) RateLimitKey() string {
	switch {
	case pc.UserID != "" && !nonDistinctAuthTypes[pc.AuthType]:
		return "user:" + pc.UserID
	case pc.SessionID != "":
		return "session:" + pc.SessionID
	default:
		return ""
	}
}

// WithPlatformContext adds platform context to the context.
func WithPlatformContext(ctx context.Context, pc *PlatformContext) context.Context {
	return context.WithValue(ctx, platformContextKey, pc)
}

// GetPlatformContext retrieves platform context from the context.
func GetPlatformContext(ctx context.Context) *PlatformContext {
	if pc, ok := ctx.Value(platformContextKey).(*PlatformContext); ok {
		return pc
	}
	return nil
}

// mustGetPlatformContext retrieves platform context or panics.
func mustGetPlatformContext(ctx context.Context) *PlatformContext {
	pc := GetPlatformContext(ctx)
	if pc == nil {
		panic("platform context not found in context")
	}
	return pc
}

// WithToken adds an authentication token to the context. The value is
// stored via mcpcontext (not a key local to this package) so toolkit
// packages can read it without importing middleware, which would form an
// import cycle.
func WithToken(ctx context.Context, token string) context.Context {
	return mcpcontext.WithAuthToken(ctx, token)
}

// GetToken retrieves an authentication token from the context.
func GetToken(ctx context.Context) string {
	return mcpcontext.GetAuthToken(ctx)
}

// WithPreAuthenticatedUser adds a pre-authenticated user to the context.
// When present, the MCP auth middleware skips token validation and uses
// this user info directly. This is used by the admin API when the HTTP
// middleware has already authenticated the user (e.g. via browser session
// cookie) and the OIDC id_token may have expired.
func WithPreAuthenticatedUser(ctx context.Context, info *UserInfo) context.Context {
	return context.WithValue(ctx, preAuthUserKey, info)
}

// GetPreAuthenticatedUser retrieves a pre-authenticated user from the context.
func GetPreAuthenticatedUser(ctx context.Context) *UserInfo {
	if info, ok := ctx.Value(preAuthUserKey).(*UserInfo); ok {
		return info
	}
	return nil
}

// WithSource tags the context with an audit source override. The MCP tool
// call middleware honors this value when populating PlatformContext.Source,
// so REST shims (e.g. internal/httpserver/gatewayhttp) can mark tool calls they initiate as
// originating from the REST API rather than from a real MCP transport.
// When unset, the middleware defaults to "mcp".
func WithSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, sourceOverrideKey, source)
}

// GetSource returns the audit source override stored on the context, or
// "" when no override has been set.
func GetSource(ctx context.Context) string {
	if s, ok := ctx.Value(sourceOverrideKey).(string); ok {
		return s
	}
	return ""
}

// withServerSession adds a ServerSession to the context.
// Delegates to mcpcontext to share context keys with toolkit packages.
func withServerSession(ctx context.Context, ss *mcp.ServerSession) context.Context {
	return mcpcontext.WithServerSession(ctx, ss)
}

// getServerSession retrieves the ServerSession from the context.
// Delegates to mcpcontext to share context keys with toolkit packages.
func getServerSession(ctx context.Context) *mcp.ServerSession {
	return mcpcontext.GetServerSession(ctx)
}

// withProgressToken adds a progress token to the context.
// Delegates to mcpcontext to share context keys with toolkit packages.
func withProgressToken(ctx context.Context, token any) context.Context {
	return mcpcontext.WithProgressToken(ctx, token)
}

// getProgressToken retrieves the progress token from the context.
// Delegates to mcpcontext to share context keys with toolkit packages.
func getProgressToken(ctx context.Context) any {
	return mcpcontext.GetProgressToken(ctx)
}
