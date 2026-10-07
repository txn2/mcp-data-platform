package observability

import "errors"

// Status category labels for tool calls and outbound HTTP. The set is
// closed and small so total label cardinality on counters and
// histograms stays bounded.
const (
	StatusOK       = "ok"
	StatusAuthErr  = "auth_err"
	StatusAuthzErr = "authz_err"
	// StatusGateErr is a call the platform refused before the handler ran for
	// a reason other than identity: the session gate, the search-first gate,
	// a missing session handle or purpose, or the per-user rate limit. The
	// specific gate is the error category on the audit row and on the span;
	// the metric carries one value so the label set stays closed (#1892).
	StatusGateErr = "gate_err"
	// StatusDeclined is a person answering no to an elicitation prompt. It is
	// not bad input and not a platform fault, so it has its own value rather
	// than folding into StatusValidationErr (#1892).
	StatusDeclined      = "declined"
	StatusValidationErr = "validation_err"
	StatusUpstreamErr   = "upstream_err"
	StatusInternalErr   = "internal_err"
)

// Status labels for the OAuth server's own token endpoint. A grant the client
// got wrong (an expired code, a mismatched client_id, a bad verifier) and a
// grant the platform could not complete (its token store or signer failed) are
// different operational conditions and were one upstream_err label until
// #1892; the AuthFailureSpike alert reads status!="ok", so both still count.
const (
	StatusClientErr = "client_err"
	StatusServerErr = "server_err"
)

// Audit outcome categories for upstream-proxying toolkits (e.g. the
// apigateway). These are bounded labels that distinguish gateway-level
// failure (the gateway could not reach the upstream) from
// upstream-level failure (the upstream responded with an error
// status). The two are fundamentally different operational concerns
// and should not share a status code or a success boolean:
//   - The gateway returning 502 means the gateway broke.
//   - The upstream returning 502 (proxied through the gateway as wire
//     200 with the upstream code in the body) means the upstream
//     broke. The gateway did its job.
//
// A toolkit stamps one of these on its result's _meta (MetaAuditOutcome
// below); the audit middleware records success=false on the row for
// any value but OutcomeOK, and carries the value as the category of the
// event it builds.
const (
	OutcomeOK              = "ok"
	OutcomeUpstream4xx     = "upstream_4xx"
	OutcomeUpstream5xx     = "upstream_5xx"
	OutcomeTransportErr    = "transport_err"
	OutcomeUpstreamTimeout = "upstream_timeout"
	// OutcomeUpstreamError is a 2xx whose body reports failure. A GraphQL
	// endpoint answers almost every failure as an HTTP 200 carrying an
	// errors array, and the status-line categories above cannot name
	// that; this one does, so the audit row and the call record of such
	// a call carry success=false like a 4xx does (#1678).
	OutcomeUpstreamError = "upstream_error"
)

// Well-known CallToolResult Meta keys read by the audit middleware to
// override its success / error_category derivation. Toolkits that
// proxy external services populate these so the audit row reflects
// the real upstream outcome instead of just "the MCP tool ran." Keys
// are namespaced under "audit_" to keep them out of the way of other
// _meta consumers.
const (
	// MetaAuditOutcome carries one of the Outcome* string constants
	// above. When present and not OutcomeOK, the audit middleware
	// sets success=false and uses the value as error_category.
	MetaAuditOutcome = "audit_outcome"

	// MetaAuditOutcomeMessage carries an optional human-readable
	// summary of the outcome (typically the upstream status text or
	// the scrubbed transport error). Used to populate
	// audit_logs.error_message when no other source is available.
	MetaAuditOutcomeMessage = "audit_outcome_message"

	// MetaAuditResult is the _meta key under which a tool reports facts
	// about the call's outcome for the audit row: a map the audit
	// middleware records under parameters.result. The api gateway's page
	// walk stamps its pages_fetched, items_merged, and stopped_by here,
	// which is how one call that walked 160 pages stays observable in
	// audit (issue #1535).
	MetaAuditResult = "audit_result"
)

// HTTP status class labels for outbound calls. The "other" bucket
// covers transport-level failures (status code 0) and the rarely-seen
// 1xx informational range. Recording the raw status code as a label
// would explode cardinality.
const (
	StatusClass2xx   = "2xx"
	StatusClass3xx   = "3xx"
	StatusClass4xx   = "4xx"
	StatusClass5xx   = "5xx"
	StatusClassOther = "other"
)

// CategorizedError lets call sites attach a category to an error that
// the metrics layer can read without a string-match. This mirrors the
// pattern used by pkg/middleware's PlatformError so the existing
// auth/authz/declined categories surface in metrics without a second
// classification scheme.
type CategorizedError interface {
	error
	ErrorCategory() string
}

// Category constants recognized by ClassifyToolCall when a
// CategorizedError is returned. These match the values
// pkg/middleware.ErrCategory* uses so the platform's existing error
// taxonomy maps to bounded metric labels without duplication.
const (
	CategoryAuth        = "authentication_failed"
	CategoryAuthz       = "authorization_denied"
	CategoryDeclined    = "user_declined"
	CategoryClientInput = "client_input"
	CategoryNotFound    = "not_found"
	CategoryUnavailable = "feature_unavailable"
	CategoryInternal    = "internal"
	// The gate refusals (StatusGateErr). The values are the categories the
	// gates in pkg/middleware and internal/platform/toolratelimit stamp on
	// their refusals.
	CategorySetupRequired   = "setup_required"
	CategorySearchRequired  = "search_required"
	CategorySessionRequired = "session_required"
	CategoryPurposeRequired = "purpose_required"
	CategoryRateLimited     = "rate_limited"
)

// ClassifyError maps an error returned from a tool handler (or from
// any internal stage of the call) to a bounded status_category label.
// A nil error yields StatusOK.
//
// The classifier prefers a CategorizedError's ErrorCategory() over
// string inspection so the platform's error taxonomy stays
// authoritative. Categories the metrics package does not recognize
// fall through to StatusInternalErr — a recognized-but-unmapped
// category is a signal that the taxonomy and the classifier have
// drifted; the deliberate bucket makes the drift visible in a
// dashboard.
func ClassifyError(err error) string {
	if err == nil {
		return StatusOK
	}
	var ce CategorizedError
	if errors.As(err, &ce) {
		switch ce.ErrorCategory() {
		case CategoryAuth:
			return StatusAuthErr
		case CategoryAuthz:
			return StatusAuthzErr
		case CategoryDeclined:
			return StatusDeclined
		}
	}
	return StatusInternalErr
}

// ClassifyToolCallResult maps the (err, isToolError, errCategory)
// triple from an MCP tool call to a bounded status_category. This is
// the shape pkg/middleware.MCPAuditMiddleware already computes, so
// the metrics middleware can pass through the same fields without
// re-deriving them.
//
// Logic:
//   - err != nil → ClassifyError(err) (protocol-level failure)
//   - !isToolError → StatusOK
//   - isToolError with a recognized category → mapped label; a refusal by one
//     of the platform's gates maps to StatusGateErr, a declined elicitation
//     to StatusDeclined
//   - isToolError without a category → StatusUpstreamErr
//     (most tool-level errors are upstream — Trino query failures,
//     S3 access errors, DataHub fetch errors, etc.)
func ClassifyToolCallResult(err error, isToolError bool, errCategory string) string {
	if err != nil {
		return ClassifyError(err)
	}
	if !isToolError {
		return StatusOK
	}
	switch errCategory {
	case CategoryAuth:
		return StatusAuthErr
	case CategoryAuthz:
		return StatusAuthzErr
	case CategorySetupRequired, CategorySearchRequired, CategorySessionRequired,
		CategoryPurposeRequired, CategoryRateLimited:
		return StatusGateErr
	case CategoryDeclined:
		return StatusDeclined
	// Caller- or config-correctable faults: the request cannot be served as-is
	// for a reason that is not a platform fault or a transient backend error.
	case CategoryClientInput, CategoryNotFound, CategoryUnavailable:
		return StatusValidationErr
	case CategoryInternal:
		return StatusInternalErr
	}
	// Uncategorized tool failures (category tool_error or unset) and proxied
	// backend failures are treated as downstream-of-dispatch failures.
	return StatusUpstreamErr
}

// HTTP status range boundaries. Named so revive's add-constant rule
// stops flagging the comparison literals.
const (
	httpStatus2xxLo = 200
	httpStatus3xxLo = 300
	httpStatus4xxLo = 400
	httpStatus5xxLo = 500
	httpStatus6xxLo = 600
)

// HTTPStatusClass returns the bounded class label for an HTTP status
// code. Status 0 is reserved for transport-level errors (no response
// received); it maps to StatusClassOther so it is recordable without
// inflating the 5xx bucket.
func HTTPStatusClass(status int) string {
	switch {
	case status >= httpStatus2xxLo && status < httpStatus3xxLo:
		return StatusClass2xx
	case status >= httpStatus3xxLo && status < httpStatus4xxLo:
		return StatusClass3xx
	case status >= httpStatus4xxLo && status < httpStatus5xxLo:
		return StatusClass4xx
	case status >= httpStatus5xxLo && status < httpStatus6xxLo:
		return StatusClass5xx
	default:
		return StatusClassOther
	}
}

// HTTPStatusCategory returns the status_category label for an outbound
// HTTP call. 2xx and 3xx are treated as OK; 4xx and 5xx as upstream
// errors. Transport errors (status 0) are upstream errors too — the
// upstream did not respond.
func HTTPStatusCategory(status int, transportErr error) string {
	if transportErr != nil {
		return StatusUpstreamErr
	}
	if status >= httpStatus2xxLo && status < httpStatus4xxLo {
		return StatusOK
	}
	return StatusUpstreamErr
}
