// Package toolratelimit wires the per-user tool-call rate limiter (#929) into
// the platform: an MCP receiving middleware that refuses authenticated
// tools/call requests exceeding a generous per-identity token-bucket limit
// before they reach the handler, audit pipeline, or upstream.
//
// It lives in its own package so the platform facade stays within its
// field/method budget and so the ratelimit dependency stays localized to a
// cohesive, independently-testable seam (mirroring reflexivecapture and the
// other internal/platform middleware seams). Platform builds a Handle inside the
// middleware-chain registration and registers Close on the lifecycle, so no
// Platform field or method is added.
//
// The limiter is a safety net, not a throughput throttle: with the generous
// platform defaults, ordinary interactive and agent use never touches it, but a
// runaway agent loop or a compromised account is bounded before it can saturate
// shared resources. See pkg/platform.RateLimitConfig for the keying and
// per-replica rationale.
package toolratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"math"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/ratelimit"
)

const (
	// methodToolsCall is the MCP method the limiter meters. Non-tools/call
	// methods pass through untouched.
	methodToolsCall = "tools/call"

	// codeRateLimited and categoryRateLimited are the stable machine-readable
	// error code and category an agent branches on to back off and retry, rather
	// than treat the refusal as a caller-input fault or a platform outage. They
	// mirror the taxonomy in pkg/middleware; the constant lives with its sole
	// producer, as the search/session gate categories do in their own files.
	codeRateLimited     = "rate_limited"
	categoryRateLimited = "rate_limited"

	// platformInfoTool is always exempt so a throttled agent can still re-read
	// the platform guidance it needs to back off intelligently. The session gate
	// steers every agent to call it first; refusing it under load would leave a
	// throttled agent unable to recover its bearings.
	platformInfoTool = "platform_info"
)

// Handle is the per-user tool-call rate limiter. It wraps a token bucket keyed
// on the authenticated principal (middleware.PlatformContext.RateLimitKey) and
// refuses over-limit tools/call requests with the self-describing error contract
// before they reach the handler, audit pipeline, or upstream.
type Handle struct {
	limiter           *ratelimit.Limiter
	exempt            map[string]bool
	metrics           *observability.Metrics
	retryAfterSeconds int
}

// New builds a Handle enforcing requestsPerMinute (sustained) with a bucket
// depth of burst per user. platform_info is always exempt; the tools in
// extraExempt are exempt in addition to it. metrics may be nil (the refusal
// counter is then a no-op). Call Close to stop the background eviction goroutine.
func New(requestsPerMinute, burst int, extraExempt []string, metrics *observability.Metrics) *Handle {
	limiter := ratelimit.New(ratelimit.Config{
		RequestsPerMinute: requestsPerMinute,
		BurstSize:         burst,
	})

	exempt := make(map[string]bool, len(extraExempt)+1)
	exempt[platformInfoTool] = true
	for _, t := range extraExempt {
		exempt[t] = true
	}

	// Retry-After for a single token is 1/rate seconds. ratelimit.New guarantees
	// a positive refill rate (it defaults a non-positive rpm), so the division is
	// well-defined; rounding up yields at least one second for any rate — the
	// hint never suggests an immediate retry that would just be refused again.
	retryAfter := int(math.Ceil(1 / limiter.Rate()))

	return &Handle{
		limiter:           limiter,
		exempt:            exempt,
		metrics:           metrics,
		retryAfterSeconds: retryAfter,
	}
}

// Close stops the underlying limiter's background eviction goroutine. Idempotent
// and nil-safe.
func (h *Handle) Close() {
	if h != nil && h.limiter != nil {
		h.limiter.Close()
	}
}

// IsExempt reports whether a tool bypasses the limiter.
func (h *Handle) IsExempt(toolName string) bool {
	return h.exempt[toolName]
}

// Middleware returns the MCP receiving middleware enforcing the per-user limit.
// On a refusal it short-circuits with a RATE_LIMITED error result and never
// invokes the underlying tool handler, so the throttled call consumes no
// handler, audit, or upstream work.
//
// The middleware must be positioned INNER to MCPToolCallMiddleware so the
// PlatformContext (identity, tool name) is available, and OUTER to audit,
// metrics, tracing, and enrichment so a refused call never reaches those layers.
// It is positioned INNER to the session and workflow gates so it only meters
// calls that pass those gates and would actually execute — a gate-refused call
// is already cheap and self-limiting and should not consume a rate-limit token.
func (h *Handle) Middleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != methodToolsCall {
				return next(ctx, method, req)
			}
			pc := middleware.GetPlatformContext(ctx)
			if pc == nil {
				return next(ctx, method, req)
			}
			if errResult := h.check(ctx, pc); errResult != nil {
				return errResult, nil
			}
			return next(ctx, method, req)
		}
	}
}

// check evaluates whether a tool call is within the per-user limit. It returns
// nil when the call is allowed (consuming one token in that case) and an error
// result when the principal is over its limit.
func (h *Handle) check(ctx context.Context, pc *middleware.PlatformContext) mcp.Result {
	// Exempt tools always pass and consume no token.
	if h.IsExempt(pc.ToolName) {
		return nil
	}

	// With no attributable identity there is nothing to meter; fail open. A
	// call that reached here has already passed auth, and refusing it on an
	// un-keyable basis would penalize legitimate un-sessioned transports more
	// than any abuser (see PlatformContext.RateLimitKey).
	key := pc.RateLimitKey()
	if key == "" {
		return nil
	}

	if h.limiter.Allow(key) {
		return nil
	}

	h.metrics.RecordRateLimited(ctx)
	slog.Warn("rate limit: tool call refused",
		"tool", pc.ToolName,
		"user_id", pc.UserID,
		"session_id", pc.SessionID,
		"retry_after_seconds", h.retryAfterSeconds,
	)
	return h.createError(pc.ToolName)
}

// createError builds a RATE_LIMITED error result carrying a retry hint so an
// agent backs off for the suggested interval rather than hot-looping.
func (h *Handle) createError(blockedTool string) mcp.Result {
	msg := fmt.Sprintf(
		"RATE_LIMITED: too many tool calls in a short window; %s was refused to protect "+
			"shared platform resources. Wait about %d second(s) and retry. This throttle is "+
			"per authenticated user and is a safety net against runaway loops, not a per-request quota.",
		blockedTool, h.retryAfterSeconds,
	)
	hint := fmt.Sprintf(
		"Pause for at least %d second(s) before retrying, and slow your call rate. This is a "+
			"rate-limit backstop, not a platform outage or an access-policy denial.",
		h.retryAfterSeconds,
	)

	// Build the full self-describing contract here: this middleware
	// short-circuits before the error-contract normalizer (it is registered
	// outer to it), so it must emit the {code, category, message, hint}
	// envelope itself rather than rely on normalization.
	return middleware.BuildErrorResult(middleware.NewToolError(
		codeRateLimited, categoryRateLimited, msg, hint,
	))
}
