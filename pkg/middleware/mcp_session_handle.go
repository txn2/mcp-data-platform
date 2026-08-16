package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// ErrCategorySessionRequired is the error category for explicit session-handle
// violations (missing or expired handle).
const ErrCategorySessionRequired = "session_required"

// sessionHandleArg is the tool-call argument name carrying the explicit session
// handle minted by platform_info (issue #792).
const sessionHandleArg = "session_id"

// sessionHandleSchemaDescription is the description advertised on the injected
// session_id property so the model learns where the handle comes from.
const sessionHandleSchemaDescription = "Session handle returned by platform_info. Call platform_info first, " +
	"then pass the session_id it returns on every subsequent tool call so the platform can " +
	"associate your calls, enforce workflow gates, and attribute audit and provenance records."

// Session resolution sources, reported as the "source" label on the
// mcp_session_resolution counter (Prometheus mcp_session_resolution_total{source},
// pkg/observability/metrics.go). With require on (issue #800) only "explicit" and
// "none" occur on gated tools; "transport" and "stdio" remain reachable on exempt
// tools and when require is off, so operators can still see how much traffic
// relies on a transport session.
const (
	sessionSourceExplicit  = "explicit"
	sessionSourceTransport = "transport"
	sessionSourceStdio     = "stdio"
	sessionSourceNone      = "none"
)

// refreshTimeout bounds the detached handle-refresh upsert.
const refreshTimeout = 5 * time.Second

// SessionResolverConfig configures a SessionResolver.
type SessionResolverConfig struct {
	// Enabled activates handle extraction, validation, and enforcement. When
	// false the resolver is a no-op and transport resolution stands alone.
	Enabled bool

	// Require refuses any gated call that does not carry a valid
	// platform_info-minted handle with SESSION_REQUIRED (issue #800). A
	// transport session is not a fallback: it is the per-call value the handle
	// exists to replace. The InitTool is always exempt (it mints the handle);
	// tools in ExemptTools also bypass the refusal.
	Require bool

	// TTL is the handle lifetime applied on refresh-on-use. Non-positive
	// disables refresh (the mint-time expiry stands).
	TTL time.Duration

	// InitTool is the tool that mints handles (platform_info); it is never
	// gated, since the handle does not exist until it runs.
	InitTool string

	// ExemptTools are additional tools that bypass the SESSION_REQUIRED refusal
	// (they may still carry and validate a handle). This carries the legacy
	// session gate's exempt_tools forward when explicit handles supersede it, so
	// an operator's discovery/orientation exemptions (e.g. list_connections) are
	// honored.
	ExemptTools []string

	// Metric records the resolution source per tool call. May be nil.
	Metric func(ctx context.Context, source string)
}

// SessionResolver extracts, validates, strips, and enforces the explicit
// session_id handle on tools/call requests (issue #792). It is consulted by
// MCPToolCallMiddleware after authentication so the caller's identity is
// available for the same-identity check.
//
// A nil *SessionResolver is a valid no-op, so callers need not branch on
// whether handles are configured.
type SessionResolver struct {
	store    pkgsession.Store
	enabled  bool
	require  bool
	ttl      time.Duration
	initTool string
	exempt   map[string]bool
	metric   func(ctx context.Context, source string)
}

// NewSessionResolver builds a SessionResolver. store may be nil, in which case
// the resolver behaves as disabled.
func NewSessionResolver(store pkgsession.Store, cfg SessionResolverConfig) *SessionResolver {
	var exempt map[string]bool
	if len(cfg.ExemptTools) > 0 {
		exempt = make(map[string]bool, len(cfg.ExemptTools))
		for _, t := range cfg.ExemptTools {
			exempt[t] = true
		}
	}
	return &SessionResolver{
		store:    store,
		enabled:  cfg.Enabled,
		require:  cfg.Require,
		ttl:      cfg.TTL,
		initTool: cfg.InitTool,
		exempt:   exempt,
		metric:   cfg.Metric,
	}
}

// resolve applies explicit-handle resolution for a tools/call request. It
// returns a non-nil error result when the call must be refused (SESSION_REQUIRED
// or SESSION_EXPIRED); nil means the call may proceed, with pc.SessionID set to
// the resolved handle and the session_id argument stripped from the request.
//
// It runs after authentication so pc.UserID is populated for the same-identity
// check, and before the tool call is recorded for workflow tracking so the gate
// keys on the explicit handle.
func (r *SessionResolver) resolve(ctx context.Context, req mcp.Request, pc *PlatformContext, toolName string) mcp.Result {
	if r == nil || !r.enabled || r.store == nil {
		return nil
	}

	// Always take (and strip) the handle before the tool handler or any
	// gateway-proxied upstream server can observe the platform-injected arg.
	handle, present := takeSessionHandleArg(req)
	// Record that the caller threaded a handle itself, before any of the
	// branches below replace pc.SessionID with a resolved or adopted one. This
	// is what the purpose requirement keys on (#1317): a caller that threaded
	// one argument can thread another, and a caller that could not thread this
	// one cannot be asked for the other.
	pc.SessionHandleThreaded = present && handle != ""

	// The init tool mints the handle in its own handler, so it is never gated
	// OR validated. This must precede the explicit-handle branch: an agent told
	// to thread the handle on every call may still send a now-expired one on a
	// platform_info re-call, and platform_info is the recovery path from
	// SESSION_EXPIRED — validating it would refuse the very call that mints a
	// fresh handle, deadlocking the agent.
	if toolName == r.initTool {
		r.recordMetric(ctx, transportSource(pc.SessionID))
		return nil
	}

	if present && handle != "" {
		return r.resolveExplicit(ctx, pc, toolName, handle)
	}

	// No valid handle presented. When required, only a platform_info-minted
	// handle satisfies the gate (issue #800): the transport session is the
	// churning, per-call Mcp-Session-Id that #792 exists to stop trusting, and
	// the stdio sentinel collapses every run into one bucket — neither is a
	// usable session identity. Refusing here (instead of accepting the transport
	// session as a fallback) makes the requirement real for the exact clients the
	// feature targets, and self-heals: a compliant caller sees SESSION_REQUIRED,
	// calls platform_info, and threads the handle.
	//
	// Stateless in-memory shim callers are exempt (issue #811): the gateway HTTP
	// shim (Source=rest — NiFi's InvokeHTTP, cronjobs, curl) and the admin
	// "Call a tool" shim (Source=admin — the portal tool runner) both drive the
	// same assembled server over a fresh in-memory MCP session per HTTP request.
	// There is no platform_info call to mint a handle and no way to thread one
	// across separate, independent HTTP requests, so the handle requirement — which
	// exists to make MCP AGENTS thread a durable session identity — is inapplicable
	// by construction to them. This is not an agent-reachable bypass: a real MCP
	// transport call always resolves to Source=mcp (resolveSource's default), and
	// the shim sources are set only in-process via middleware.WithSource on the
	// server connection context (internal/httpserver/gatewayhttp, pkg/admin), which an external
	// caller cannot inject. An unset/unknown source stays gated (fail closed). The
	// authenticator, not the handle, remains the security boundary; shim callers
	// still authenticate and remain subject to persona authorization, route policy,
	// and audit.
	if r.require && !r.exempt[toolName] && !isStatelessShimSource(pc.Source) {
		// An authenticated caller that presents no handle is not necessarily a
		// non-compliant agent: an MCP App runs in a sandbox that cannot thread
		// the handle on its own calls (issue #1040), and those calls are already
		// authenticated. Adopt the caller's own established session, resolved
		// from their authenticated identity, rather than refuse it. The
		// authenticator is the security boundary; the handle is a scoping key.
		// Only a caller with no session at all is refused, which keeps the
		// platform_info-first requirement (#800) for genuinely fresh agents.
		if r.adoptByIdentity(ctx, pc) {
			return nil
		}
		r.recordMetric(ctx, sessionSourceNone)
		slog.Warn("session handle: missing and no session to adopt",
			logKeyTool, toolName, logKeyUserID, pc.UserID)
		return createSessionRequiredError(r.initTool)
	}

	// Not required (or an exempt tool): keep whatever session identity the
	// transport could supply so pc.SessionID stays populated for best-effort
	// session-scoping. An empty SessionID means no session at all.
	if pc.SessionID != "" {
		r.recordMetric(ctx, transportSource(pc.SessionID))
		return nil
	}
	r.recordMetric(ctx, sessionSourceNone)
	return nil
}

// adoptByIdentity resolves the caller's own most-recently-active session from
// their authenticated identity and adopts it, so a handle-less but
// authenticated call (an MCP App's sandboxed call) is scoped to the session the
// caller already established rather than refused. It reports whether the call
// may proceed.
//
// It returns true (proceed) when a session is adopted, and also when the store
// lookup fails: an infrastructure error must not refuse an authenticated
// caller, matching resolveExplicit's deliberate fail-open, since the
// authenticator, not the handle, is the security boundary. It returns false
// (refuse) only for a caller with no live session at all, one that never called
// the init tool.
func (r *SessionResolver) adoptByIdentity(ctx context.Context, pc *PlatformContext) bool {
	if pc.UserID == "" {
		return false
	}
	sess, err := r.store.LatestHandleForUser(ctx, pc.UserID)
	if err != nil {
		slog.Warn("session handle: identity lookup failed, allowing unadopted",
			logKeyUserID, pc.UserID, logKeyError, err)
		r.recordMetric(ctx, sessionSourceNone)
		return true
	}
	if sess == nil {
		return false
	}
	pc.SessionID = sess.ID
	r.recordMetric(ctx, sessionSourceExplicit)
	r.refresh(sess)
	return true
}

// resolveExplicit validates a presented handle and, on success, adopts it as
// the session ID and refreshes its TTL. Unknown, expired, and cross-identity
// handles are rejected identically so a caller cannot probe handle existence.
func (r *SessionResolver) resolveExplicit(ctx context.Context, pc *PlatformContext, toolName, handle string) mcp.Result {
	sess, err := r.store.Get(ctx, handle)
	if err != nil {
		// Fail OPEN on an infrastructure error, matching the search-first gate's
		// deliberate fail-open (docs/reference/middleware.md): a transient
		// session-store outage must degrade to allowing the call rather than
		// locking out every handle-bearing caller. The authenticator, not the
		// handle, is the security boundary; the handle is a session key, so an
		// unvalidated call during an outage risks only session-scoping coherence,
		// which self-corrects when the store recovers. The handle is adopted
		// best-effort so audit/provenance stay coherent.
		slog.Warn("session handle: store unavailable, allowing call unvalidated",
			logKeyTool, toolName, logKeyError, err)
		pc.SessionID = handle
		r.recordMetric(ctx, sessionSourceExplicit)
		return nil
	}
	if sess == nil || sess.UserID != pc.UserID {
		slog.Warn("session handle: rejected (unknown, expired, or identity mismatch)",
			logKeyTool, toolName, logKeyUserID, pc.UserID)
		r.recordMetric(ctx, sessionSourceNone)
		return createSessionExpiredError(r.initTool)
	}
	pc.SessionID = handle
	r.recordMetric(ctx, sessionSourceExplicit)
	r.refresh(sess)
	return nil
}

// refresh extends a validated handle's expiry to now+TTL via a detached upsert,
// preserving the handle's identity, creation time, and state. Detached so the
// refresh outlives the request that triggered it.
//
// The write is throttled to the second half of the handle's lifetime: a handle
// still more than TTL/2 from expiry is left alone, so an active session triggers
// at most one upsert per TTL/2 rather than a full-row write on every tool call.
func (r *SessionResolver) refresh(sess *pkgsession.Session) {
	if r.ttl <= 0 || time.Until(sess.ExpiresAt) > r.ttl/2 {
		return
	}
	extended := *sess
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
		defer cancel()
		now := time.Now()
		extended.LastActiveAt = now
		extended.ExpiresAt = now.Add(r.ttl)
		if err := r.store.Create(ctx, &extended); err != nil {
			slog.Debug("session handle: refresh failed", logKeyError, err)
		}
	}()
}

func (r *SessionResolver) recordMetric(ctx context.Context, source string) {
	if r.metric != nil {
		r.metric(ctx, source)
	}
}

// isStatelessShimSource reports whether an audit source belongs to one of the
// stateless in-memory shims that drive the assembled server over a fresh
// per-request MCP session (the gateway REST shim, Source=rest; the admin
// tool-runner shim, Source=admin; and a managed script's host bindings,
// Source=script). Such callers cannot perform the platform_info session
// handshake, so they are exempt from the SESSION_REQUIRED gate (issue #811). A
// real MCP-transport agent resolves to Source=mcp and is NOT exempt; an
// unset/unknown source is likewise not exempt, so the gate fails closed.
//
// A script is exempt for the same structural reason as the other two, not as a
// concession: the handshake is a model-facing workflow step and there is no
// model inside a script run. What the search-first gate steers an agent toward
// — discovering the data before querying it — happened when a person authored
// and reviewed the script, which is a stronger check than the gate performs.
func isStatelessShimSource(source string) bool {
	return source == SourceREST || source == SourceAdmin || source == SourceScript
}

// isIsolatedRunSource reports whether a source drives a fresh in-memory session
// per run and therefore needs a minted session id of its own: without one, the
// run would either collapse onto the empty session scope or, worse, key its
// gate, provenance, and dedup state on the operating USER's scope and pollute
// it (issue #859).
func isIsolatedRunSource(source string) bool {
	return source == SourceAdmin || source == SourceScript
}

// mintIsolatedRunSessionID generates the per-run session id for an isolated
// run source. The two populations get distinct prefixes so an operator reading
// an audit row can tell a portal replay from a script run without joining
// anything.
func mintIsolatedRunSessionID(source string) (string, error) {
	mint := pkgsession.GeneratePortalSessionID
	if source == SourceScript {
		mint = pkgsession.GenerateScriptSessionID
	}
	id, err := mint()
	if err != nil {
		return "", fmt.Errorf("minting a %s run session id: %w", source, err)
	}
	return id, nil
}

// transportSource classifies a transport-derived session ID for the metric.
func transportSource(sid string) string {
	switch sid {
	case "":
		return sessionSourceNone
	case defaultSessionID:
		return sessionSourceStdio
	default:
		return sessionSourceTransport
	}
}

// takeSessionHandleArg removes a platform-minted session handle from a
// tools/call request's arguments and returns it. present is true only when the
// session_id argument held a value with the platform handle prefix; that value
// is removed so upstream tool handlers and gateway-proxied servers never see it.
//
// A session_id argument whose value is NOT a platform handle (a tool that
// legitimately defines its own session_id parameter, e.g. a proxied upstream
// tool) is left untouched and reported absent, so the handler still receives it.
// Platform handles are prefix-tagged and unguessable, so this cannot be spoofed
// by an ordinary caller value.
func takeSessionHandleArg(req mcp.Request) (handle string, present bool) {
	return takeStringArg(req, sessionHandleArg, pkgsession.IsHandle)
}

// toolCallParams returns the raw tool-call params, or nil if the request does
// not carry them.
func toolCallParams(req mcp.Request) *mcp.CallToolParamsRaw {
	if req == nil {
		return nil
	}
	params := req.GetParams()
	if params == nil {
		return nil
	}
	cp, _ := params.(*mcp.CallToolParamsRaw)
	return cp
}

// createSessionRequiredError builds a SESSION_REQUIRED error result. It emits
// the full self-describing envelope because the resolver short-circuits before
// the error-contract normalizer (which is registered inner to it).
func createSessionRequiredError(initTool string) mcp.Result {
	msg := fmt.Sprintf(
		"SESSION_REQUIRED: Call %s first. It returns a session_id you must pass as the "+
			"session_id argument on every subsequent tool call.",
		initTool,
	)
	return BuildErrorResult(NewToolError(
		CodeSessionRequired, ErrCategorySessionRequired, msg,
		fmt.Sprintf("Call %s first, then retry with the session_id it returns. "+
			"This is a session-setup requirement, not a platform outage.", initTool),
	))
}

// createSessionExpiredError builds a SESSION_EXPIRED error result for a handle
// that is unknown, expired, or presented by a different identity.
func createSessionExpiredError(initTool string) mcp.Result {
	msg := fmt.Sprintf(
		"SESSION_EXPIRED: Your session_id is unknown or expired. Call %s again to mint a "+
			"fresh session_id, then pass it on every subsequent tool call.",
		initTool,
	)
	return BuildErrorResult(NewToolError(
		CodeSessionExpired, ErrCategorySessionRequired, msg,
		fmt.Sprintf("Call %s to mint a fresh session_id, then retry. "+
			"This is a session-setup requirement, not a platform outage.", initTool),
	))
}

// MCPSessionHandleSchemaMiddleware creates MCP protocol-level middleware that
// injects a session_id string property into every tool's input schema on
// tools/list responses, except the init tool (which mints the handle and takes
// no session_id). It mirrors the MCPAppsMetadataMiddleware / description-override
// decorators: it touches only the list response, so upstream toolkits
// (mcp-trino, mcp-datahub, mcp-s3, gateway-proxied tools) are never modified.
func MCPSessionHandleSchemaMiddleware(initTool string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil {
				return result, err
			}
			if method != methodToolsList {
				return result, nil
			}
			return injectSessionHandleSchema(initTool, result), nil
		}
	}
}

// injectSessionHandleSchema adds the session_id property to each listed tool's
// input schema, except the init tool's.
func injectSessionHandleSchema(initTool string, result mcp.Result) mcp.Result {
	return injectListedToolProperty(result, sessionHandleArg, map[string]any{
		"type":        "string",
		"description": sessionHandleSchemaDescription,
	}, func(toolName string) bool { return toolName != initTool })
}

// withSessionHandleProperty returns a copy of a tool input schema with a
// session_id string property added to its properties.
func withSessionHandleProperty(schema any) (any, bool) {
	return withSchemaProperty(schema, sessionHandleArg, map[string]any{
		"type":        "string",
		"description": sessionHandleSchemaDescription,
	})
}
