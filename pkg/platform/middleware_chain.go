package platform

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
	"github.com/txn2/mcp-data-platform/internal/platform/mwchain"
	"github.com/txn2/mcp-data-platform/internal/platform/provenance"
	"github.com/txn2/mcp-data-platform/internal/platform/toolargs"
	"github.com/txn2/mcp-data-platform/internal/platform/toolratelimit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// mwName identifies a receiving middleware within the canonical chain order.
// Names are stable identifiers used both to declare ordering dependencies and
// to produce named errors when the declared order is invalid.
type mwName = mwchain.Name

const (
	mwResultType          mwName = "result_type"
	mwIcons               mwName = "icons"
	mwDescriptionOverride mwName = "description_override"
	mwPromptVisibility    mwName = "prompt_visibility"
	mwToolVisibility      mwName = "tool_visibility"
	mwSessionHandleSchema mwName = "session_handle_schema"
	mwPurposeSchema       mwName = "purpose_schema"
	mwOutputSchema        mwName = "output_schema"
	mwMCPApps             mwName = "mcp_apps"
	mwToolCall            mwName = "tool_call" // auth/authz; writes PlatformContext via context.WithValue
	mwSessionGate         mwName = "session_gate"
	mwWorkflowGate        mwName = "workflow_gate"
	mwRateLimit           mwName = "rate_limit"
	mwReflexiveCapture    mwName = "reflexive_capture"
	mwTracing             mwName = "tracing"
	mwMetrics             mwName = "metrics"
	mwAudit               mwName = "audit"
	mwErrorContract       mwName = "error_contract"
	mwClientLogging       mwName = "client_logging"
	mwManagedResource     mwName = "managed_resource"
	mwCallReference       mwName = "call_reference"
	mwEnrichment          mwName = "enrichment"
	mwUnwrapJSON          mwName = "unwrap_json"
)

// mwSpec declares one receiving middleware's identity, the middlewares that
// MUST be outer to it (run earlier on a tools/call request), and how to
// register it on the server. A middleware that reads a value another writes
// via context.WithValue — the canonical case is every PlatformContext reader
// depending on the auth/authz middleware — must never be positioned outer to
// its writer, or the value is invisible downstream.
type mwSpec = mwchain.Spec

// receivingMiddlewareChain declares the canonical receiving-middleware chain in
// execution order: outermost (index 0, runs first on a tools/call request) to
// innermost (runs last, closest to the handler). The declared order and the
// per-middleware Requires list make the ordering a checked invariant rather
// than a comment (issue #758): mwchain.Validate rejects any arrangement in
// which a required (outer) middleware is positioned inner to the middleware
// that depends on it.
//
// Register funcs may skip their AddReceivingMiddleware call when the feature is
// disabled; the declared position is what the invariant protects, independent
// of which middlewares are enabled at runtime.
func (p *Platform) receivingMiddlewareChain() []mwSpec {
	return []mwSpec{
		// Result type (outermost): types every result the layers below hand back, theirs included (#1382, #1383).
		{Name: mwResultType, Register: func() { p.mcpServer.AddReceivingMiddleware(middleware.MCPResultTypeMiddleware()) }},

		// List decorators: shape tools/list, prompts/list, and
		// resources/list responses. They do not touch PlatformContext.
		{Name: mwIcons, Register: p.addIconMiddleware},
		{Name: mwDescriptionOverride, Register: p.addDescriptionOverrideMiddleware},
		{Name: mwPromptVisibility, Register: p.addPromptVisibilityMiddleware},
		{Name: mwToolVisibility, Register: p.addToolVisibilityMiddleware},
		{Name: mwSessionHandleSchema, Register: p.addSessionHandleSchemaMiddleware},
		// Advertises the purpose argument (#1317) on the gated tools' input
		// schemas, from the same resolver the tool-call path enforces with, so
		// the advertised set and the enforced set are one predicate. Owned by
		// the purposegate seam, so the facade gains no field or method for it.
		{Name: mwPurposeSchema, Register: func() {
			if r := toolargs.BuildPurposeResolver(p.config.Purpose, p.toolkitRegistry); r != nil {
				p.mcpServer.AddReceivingMiddleware(middleware.MCPPurposeSchemaMiddleware(r))
			}
		}},
		// Opens every advertised output schema to the keys the chain below adds
		// to structuredContent (#1381).
		{Name: mwOutputSchema, Register: func() { p.mcpServer.AddReceivingMiddleware(middleware.MCPOutputSchemaMiddleware()) }},
		{Name: mwMCPApps, Register: p.addMCPAppsMiddleware},

		// Auth/authz writes PlatformContext; it must be outer to every reader below.
		{Name: mwToolCall, Register: p.addToolCallMiddleware},

		// Gates: block tools before they reach audit/enrichment. Both read
		// PlatformContext, and the workflow gate is inner to the session gate so
		// platform_info takes precedence.
		{Name: mwSessionGate, Requires: []mwName{mwToolCall}, Register: p.addSessionGateMiddleware},
		{Name: mwWorkflowGate, Requires: []mwName{mwToolCall, mwSessionGate}, Register: p.addWorkflowGateMiddleware},

		// Per-user rate limiter (#929): a safety net that refuses tools/call
		// requests over a generous per-identity limit before they reach the
		// observers, handler, or upstream. Inner to the gates so it only meters
		// calls that pass them and would actually execute; outer to
		// audit/metrics/tracing/enrichment so a refused call consumes none of
		// their work. Reads PlatformContext (identity), so it requires the
		// auth/authz middleware that writes it. The limiter is owned by the
		// toolratelimit seam and its lifetime is tracked on the lifecycle, so
		// registration is an inline closure rather than a *Platform method: the
		// platform facade gains no field or method for this subsystem.
		{Name: mwRateLimit, Requires: []mwName{mwToolCall}, Register: func() {
			if !p.config.RateLimit.IsEnabled() {
				return
			}
			rpm := p.config.RateLimit.EffectiveRPM()
			burst := p.config.RateLimit.EffectiveBurst()
			h := toolratelimit.New(rpm, burst, p.config.RateLimit.ExemptTools, p.obs.Metrics())
			p.mcpServer.AddReceivingMiddleware(h.Middleware())
			// Close the limiter's eviction goroutine on shutdown via the
			// lifecycle, so no Platform field is needed to hold it for teardown.
			p.lifecycle.OnStop(func(context.Context) error {
				h.Close()
				return nil
			})
			slog.Info("tool-call rate limiter enabled",
				"requests_per_minute", rpm,
				"burst", burst,
				"exempt_tools", p.config.RateLimit.ExemptTools,
			)
		}},

		// Observers: read PlatformContext (identity/session/tool metadata), so
		// they require the auth/authz middleware that writes it. Reflexive
		// capture observes the tool result on the way out and must see the
		// normalized error the error contract produces, so it is outer to it
		// (encoded on mwErrorContract's Requires below).
		{Name: mwReflexiveCapture, Requires: []mwName{mwToolCall}, Register: p.addReflexiveCaptureMiddleware},
		{Name: mwTracing, Requires: []mwName{mwToolCall}, Register: p.addTracingMiddleware},
		{Name: mwMetrics, Requires: []mwName{mwToolCall}, Register: p.addMetricsMiddleware},
		{Name: mwAudit, Requires: []mwName{mwToolCall}, Register: p.addAuditMiddleware},

		// Error contract normalizes handler errors; it is inner to audit/metrics
		// and reflexive capture so they observe the normalized {code, category}
		// on the way out rather than the raw handler error.
		{Name: mwErrorContract, Requires: []mwName{mwAudit, mwMetrics, mwReflexiveCapture}, Register: p.addErrorContractMiddleware},

		{Name: mwClientLogging, Register: p.addClientLoggingMiddleware},
		{Name: mwManagedResource, Register: p.addManagedResourceMiddleware},

		// The call reference stamps a data call's own audit event id onto its
		// result. It reads PlatformContext (pc.EventID, pc.ToolkitKind), so it
		// requires the auth/authz middleware that writes them.
		// Only when audit records: without a stored call there is nothing to refer to (#1320).
		{Name: mwCallReference, Requires: []mwName{mwToolCall}, Register: func() {
			if p.audit.Recording() {
				// A call the catalog declines gets no reference to cite: the
				// id would resolve to nothing (#1614, #1624).
				excluded := callrecord.NewExclusion(p.config.Calls.ExcludePersonas)
				p.mcpServer.AddReceivingMiddleware(
					middleware.MCPCallReferenceMiddleware(provenance.SourceToolkitKinds(), excluded.Excludes))
			}
		}},

		// Enrichment reads PlatformContext (session dedup) and sets
		// EnrichmentApplied on the way out. The observers that record it —
		// audit, tracing, and client logging — read it after next() returns, so
		// they must be outer to enrichment (enrichment inner to them), or they
		// record enrichment as not-applied. Metrics does not read the flag, so
		// it is intentionally absent here.
		{Name: mwEnrichment, Requires: []mwName{mwToolCall, mwTracing, mwAudit, mwClientLogging}, Register: p.addEnrichmentMiddleware},

		// Unwrap JSON (innermost): rewrites tool arguments before the handler runs.
		{Name: mwUnwrapJSON, Register: p.addUnwrapJSONMiddleware},
	}
}

// --- Receiving-middleware registration helpers ---
//
// Each helper performs the AddReceivingMiddleware call for one entry in the
// canonical chain, applying that middleware's enablement condition. They are
// invoked by finalizeSetup in innermost-first order so the declared
// outermost-first chain is realized on the server.

// addUnwrapJSONMiddleware injects unwrap_json=true into trino_query and
// trino_execute arguments so single-row VARCHAR-of-JSON results are returned as
// parsed objects. Innermost so the modified arguments reach the handler.
func (p *Platform) addUnwrapJSONMiddleware() {
	if p.config.Enrichment.IsUnwrapJSONEnabled() {
		p.mcpServer.AddReceivingMiddleware(middleware.MCPUnwrapJSONMiddleware())
	}
}

// addClientLoggingMiddleware sends enrichment info to the client via session.Log().
func (p *Platform) addClientLoggingMiddleware() {
	if p.config.ClientLogging.IsEnabled() {
		p.mcpServer.AddReceivingMiddleware(
			middleware.MCPClientLoggingMiddleware(middleware.ClientLoggingConfig{
				Enabled: true,
			}),
		)
	}
}

// addErrorContractMiddleware normalizes every tools/call error result into a
// self-describing {code, category, message, hint} envelope and recovers a
// panicking handler into a categorized internal error (#539). Registered inner
// to Audit/Metrics so they observe the normalized category, and outer to the
// handler whose results it normalizes. Always on: an uncategorized error result
// must never reach the agent as an opaque string.
func (p *Platform) addErrorContractMiddleware() {
	p.mcpServer.AddReceivingMiddleware(
		middleware.MCPErrorContractMiddleware(),
	)
}

// addAuditMiddleware logs tool calls, reading PlatformContext set by
// auth/authz. Must be inner to MCPToolCallMiddleware.
func (p *Platform) addAuditMiddleware() {
	if p.config.Audit.IsToolCallLoggingEnabled() {
		p.mcpServer.AddReceivingMiddleware(
			middleware.MCPAuditMiddleware(
				p.audit.Logger(),
				middleware.WithParameterLogging(p.config.Audit.IsParameterLoggingEnabled()),
				middleware.WithRedactKeys(p.config.Audit.RedactKeys),
			),
		)
	}
}

// addMetricsMiddleware records tool_calls_total / tool_call_duration_seconds
// and the in-flight gauge. Reads PlatformContext (tool, toolkit_kind, persona)
// populated by MCPToolCallMiddleware, so it must be inner to that middleware.
// Safe to register unconditionally: the middleware short-circuits on a
// nil-or-disabled recorder.
func (p *Platform) addMetricsMiddleware() {
	if p.obs.Metrics().Enabled() {
		p.mcpServer.AddReceivingMiddleware(
			middleware.MCPMetricsMiddleware(p.obs.Metrics()),
		)
	}
}

// addTracingMiddleware opens the per-tool-call OTel span that becomes the
// parent of every downstream adapter span via context propagation. Like Metrics
// it reads PlatformContext and so sits inner to MCPToolCallMiddleware and outer
// to the handler. Safe to register unconditionally: the middleware
// short-circuits on a nil/disabled tracer.
func (p *Platform) addTracingMiddleware() {
	if p.obs.Tracer().Enabled() {
		p.mcpServer.AddReceivingMiddleware(
			middleware.MCPTracingMiddleware(p.obs.Tracer()),
		)
	}
}

// addWorkflowGateMiddleware refuses query tools until search has been called in
// the session (issue #787). It short-circuits a blocked call with a
// SEARCH_REQUIRED error result and never runs the handler. Inner to the session
// gate so platform_info takes precedence, but outer to Audit/enrichment so
// blocked calls don't produce audit events or enrichment.
func (p *Platform) addWorkflowGateMiddleware() {
	if p.workflowTracker != nil {
		p.mcpServer.AddReceivingMiddleware(
			middleware.MCPWorkflowGateMiddleware(p.workflowTracker),
		)
	}
}

// addSessionGateMiddleware blocks non-exempt tools until platform_info is
// called. Inner to Auth/Authz so PlatformContext is available; outer to Audit
// so gated calls don't produce audit events.
func (p *Platform) addSessionGateMiddleware() {
	if p.sessionGate != nil {
		p.mcpServer.AddReceivingMiddleware(
			middleware.MCPSessionGateMiddleware(p.sessionGate),
		)
	}
}

// addToolCallMiddleware authenticates and authorizes users and creates
// PlatformContext. Must be outer to Audit (and every other PlatformContext
// reader) so PlatformContext is available in the ctx they receive. The
// session-handle resolver (#792) runs inside it, adopting the explicit handle
// onto pc.SessionID before the gates and audit observe it, followed by the
// purpose resolver (#1317), which takes the stated purpose off the request onto
// pc.Purpose for the same observers.
func (p *Platform) addToolCallMiddleware() {
	p.mcpServer.AddReceivingMiddleware(
		middleware.MCPToolCallMiddleware(p.authenticator, p.authorizer, p.toolkitRegistry, middleware.ToolCallConfig{
			Transport:       p.config.Server.Transport,
			AdminPersona:    p.config.Admin.Persona,
			WorkflowTracker: p.workflowTracker,
			SessionResolver: p.buildSessionResolver(),
			PurposeResolver: toolargs.BuildPurposeResolver(p.config.Purpose, p.toolkitRegistry),
		}),
	)
}
