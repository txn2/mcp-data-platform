package observability

import "errors"

// Status category labels for tool calls and outbound HTTP. The set is
// closed and small so total label cardinality on counters and
// histograms stays bounded.
const (
	StatusOK            = "ok"
	StatusAuthErr       = "auth_err"
	StatusAuthzErr      = "authz_err"
	StatusValidationErr = "validation_err"
	StatusUpstreamErr   = "upstream_err"
	StatusInternalErr   = "internal_err"
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
	CategoryAuth     = "authentication_failed"
	CategoryAuthz    = "authorization_denied"
	CategoryDeclined = "user_declined"
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
			return StatusValidationErr
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
//   - isToolError with a recognized category → mapped label
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
	case CategoryDeclined:
		return StatusValidationErr
	}
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
