package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
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
	if r.require && !r.exempt[toolName] {
		r.recordMetric(ctx, sessionSourceNone)
		slog.Warn("session handle: missing on gated tool call",
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
//
// The re-encode uses a json.Number decoder so that removing the handle does not
// silently rewrite the other arguments' numbers (a large int64 ID would
// otherwise round-trip through float64 and lose precision). When nothing is
// removed, the arguments are left byte-identical.
func takeSessionHandleArg(req mcp.Request) (handle string, present bool) {
	callParams := toolCallParams(req)
	if callParams == nil || len(callParams.Arguments) == 0 {
		return "", false
	}
	dec := json.NewDecoder(bytes.NewReader(callParams.Arguments))
	dec.UseNumber()
	var args map[string]any
	if err := dec.Decode(&args); err != nil {
		return "", false
	}
	v, ok := args[sessionHandleArg]
	if !ok {
		return "", false
	}
	s, _ := v.(string)
	// Only consume a value that looks like a platform-minted handle; a tool's
	// own session_id argument is left in place for the handler.
	if !pkgsession.IsHandle(s) {
		return "", false
	}
	delete(args, sessionHandleArg)
	if updated, err := json.Marshal(args); err == nil {
		callParams.Arguments = updated
	}
	return s, true
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
// input schema. It replaces the Tool pointer in the result slice with a shallow
// copy carrying the augmented schema, so the server's shared tool registry is
// never mutated.
func injectSessionHandleSchema(initTool string, result mcp.Result) mcp.Result {
	listResult, ok := result.(*mcp.ListToolsResult)
	if !ok || listResult == nil {
		return result
	}
	for i, tool := range listResult.Tools {
		if tool == nil || tool.Name == initTool {
			continue
		}
		injected, ok := withSessionHandleProperty(tool.InputSchema)
		if !ok {
			continue
		}
		cp := *tool
		cp.InputSchema = injected
		listResult.Tools[i] = &cp
	}
	return listResult
}

// withSessionHandleProperty returns a copy of a tool input schema with a
// session_id string property added to its properties. It normalizes any schema
// representation (*jsonschema.Schema, json.RawMessage, or map) via a JSON
// round-trip, so one code path covers every registration style. The second
// return is false when the schema is not a JSON object (the tool is then left
// unchanged).
func withSessionHandleProperty(schema any) (any, bool) {
	if schema == nil {
		return nil, false
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, false
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}
	// Only object schemas carry named properties.
	if t, _ := obj["type"].(string); t != "" && t != "object" {
		return nil, false
	}
	existing, _ := obj["properties"].(map[string]any)
	// No capacity hint: len(existing) derives from a decoded schema, which the
	// allocation-size-overflow analysis treats as untrusted; the map grows fine.
	props := make(map[string]any)
	maps.Copy(props, existing)
	if _, exists := props[sessionHandleArg]; !exists {
		props[sessionHandleArg] = map[string]any{
			"type":        "string",
			"description": sessionHandleSchemaDescription,
		}
	}
	obj["properties"] = props
	return obj, true
}
