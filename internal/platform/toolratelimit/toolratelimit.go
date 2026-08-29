// Package toolratelimit wires the per-user tool-call rate limiter (#929) into
// the platform: an MCP receiving middleware that refuses authenticated
// tools/call requests exceeding a generous per-identity token-bucket limit
// before they reach the handler, audit pipeline, or upstream.
//
// A script principal is the exception (#1534): a platform run is a loop by
// construction, its calls are serial, and it runs under roles captured at the
// save through the persona filter, so an over-limit call from it is held until
// the bucket admits it rather than refused. The sustained rate still governs
// the run's throughput; what differs is that the call is delayed, not failed.
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
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/ratelimit"
)

// CodeRateLimited is the stable machine-readable error code a refused call
// carries, the one an agent branches on to back off and retry rather than
// treat the refusal as a caller-input fault or a platform outage, and the one
// the script engine (internal/platform/scriptrun) absorbs by pacing the call.
// It mirrors the taxonomy in pkg/middleware and lives with its producer, as the
// search/session gate categories do in their own files.
const CodeRateLimited = "rate_limited"

const (
	// methodToolsCall is the MCP method the limiter meters. Non-tools/call
	// methods pass through untouched.
	methodToolsCall = "tools/call"

	// categoryRateLimited is the error category a refusal carries; it names
	// the same thing the code does.
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
// handler, audit, or upstream work. A script principal's over-limit call is
// held here until a token is available and then handed on; a wait the request
// context ends first is returned as that context's error, so a run canceled
// mid-wait (its context is the call's, and the client forwards its
// cancellation to this side) ends as a canceled run rather than with a
// refusal, and the handler never runs for that call.
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
			return h.meter(ctx, method, req, next)
		}
	}
}

// meter admits, refuses, or holds one tools/call before handing it to next.
func (h *Handle) meter(ctx context.Context, method string, req mcp.Request, next mcp.MethodHandler) (mcp.Result, error) {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return next(ctx, method, req)
	}
	key := h.meteredKey(pc)
	if key == "" || h.limiter.Allow(key) {
		return next(ctx, method, req)
	}
	if pc.AuthType != middleware.AuthTypeScript {
		return h.refuse(ctx, pc), nil
	}
	if err := h.queue(ctx, pc, key); err != nil {
		return nil, err
	}
	return next(ctx, method, req)
}

// meteredKey returns the bucket a tool call is metered under, or "" for a call
// the limiter lets through without consuming a token.
func (h *Handle) meteredKey(pc *middleware.PlatformContext) string {
	// Exempt tools always pass and consume no token.
	if h.IsExempt(pc.ToolName) {
		return ""
	}
	// With no attributable identity there is nothing to meter; fail open. A
	// call that reached here has already passed auth, and refusing it on an
	// un-keyable basis would penalize legitimate un-sessioned transports more
	// than any abuser (see PlatformContext.RateLimitKey).
	return pc.RateLimitKey()
}

// refuse records an over-limit call from an interactive principal and builds
// the RATE_LIMITED result it is answered with.
func (h *Handle) refuse(ctx context.Context, pc *middleware.PlatformContext) mcp.Result {
	h.metrics.RecordRateLimited(ctx)
	slog.Warn("rate limit: tool call refused",
		"tool", logsan.SanitizeForLog(pc.ToolName),
		"user_id", logsan.SanitizeForLog(pc.UserID),
		"session_id", pc.SessionID,
		"retry_after_seconds", h.retryAfterSeconds,
	)
	return h.createError(pc.ToolName)
}

// queue holds a script principal's call until its bucket admits it. The
// distinction is the auth type: a platform run is the caller the platform knows
// the most about and has the strongest reason to let finish, and a loop of
// serial calls past the burst is exactly what the refusal was built to catch
// in an agent. Its calls are serial and the bucket refills at the sustained
// rate, so one wait is at most 1/rate and never contends with another call
// from the same principal. A queued call is not a refusal: it is counted on its
// own and logged at Info under the run's session id, so an operator can see
// that a deployment's scripts are running against the limit.
func (h *Handle) queue(ctx context.Context, pc *middleware.PlatformContext, key string) error {
	start := time.Now()
	if err := h.limiter.Wait(ctx, key); err != nil {
		return fmt.Errorf("rate limit: the run ended while %s was waiting for a token: %w",
			pc.ToolName, err)
	}
	h.metrics.RecordRateLimitQueued(ctx)
	slog.Info("rate limit: tool call queued",
		"tool", logsan.SanitizeForLog(pc.ToolName),
		"user_id", logsan.SanitizeForLog(pc.UserID),
		"session_id", pc.SessionID,
		"waited_ms", time.Since(start).Milliseconds(),
	)
	return nil
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
	// envelope itself rather than rely on normalization. The retry interval
	// travels as retry_after_seconds beside the prose that names it, so a
	// consumer that paces on it (the script engine) reads a number rather than
	// parsing a sentence.
	pe := middleware.NewToolError(CodeRateLimited, categoryRateLimited, msg, hint)
	pe.RetryAfterSeconds = h.retryAfterSeconds
	return middleware.BuildErrorResult(pe)
}
