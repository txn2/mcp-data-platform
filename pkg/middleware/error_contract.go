package middleware

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Error categories an agent branches on. The existing ErrCategoryAuth /
// ErrCategoryAuthz / ErrCategoryDeclined (mcp.go) and ErrCategorySetupRequired
// (mcp_session_gate.go) keep their string values for audit backward
// compatibility; these add the rest of the taxonomy. The category tells the
// caller whose fault a failure is:
//
//   - client_input / not_found / unauthorized / unauthenticated / user_declined /
//     setup_required / feature_unavailable: caller-correctable (adjust the call,
//     credentials, or setup).
//   - internal: a platform fault; not the caller's fault, retrying the same call
//     will not help.
//   - tool_error: generic fallback for an uncategorized tool failure (the
//     normalizer applies this so a result is never a bare opaque string).
const (
	ErrCategoryClientInput = "client_input"
	ErrCategoryNotFound    = "not_found"
	ErrCategoryUnavailable = "feature_unavailable"
	ErrCategoryInternal    = "internal"
	ErrCategoryToolError   = "tool_error"
)

// Stable, machine-readable error codes (snake_case). Agents may branch on these.
const (
	CodeMissingParameter   = "missing_required_parameter"
	CodeNotFound           = "not_found"
	CodeUnauthenticated    = "unauthenticated"
	CodeUnauthorized       = "unauthorized"
	CodeSetupRequired      = "setup_required"
	CodeFeatureUnavailable = "feature_unavailable"
	CodeInternalError      = "internal_error"
	CodeToolError          = "tool_error"
)

// errorEnvelopeKey is the key under which the structured error object is placed
// in CallToolResult.StructuredContent. A result carrying this key is treated as
// already conforming to the contract by the normalizer.
const errorEnvelopeKey = "error"

// errorPayload is the machine-readable error object surfaced in
// StructuredContent so an agent can branch on code/category without parsing
// prose. It is serialized over the wire as part of the tool result.
type errorPayload struct {
	Code     string `json:"code"`
	Category string `json:"category"`
	Message  string `json:"message"`
	Hint     string `json:"hint,omitempty"`
}

// NewToolError builds a fully specified categorized error. Prefer the thin
// helpers (ClientInputError, NotFoundError, ...) for the common categories.
func NewToolError(code, category, message, hint string) *PlatformError {
	return &PlatformError{Code: code, Category: category, Message: message, Hint: hint}
}

// ClientInputError is a caller-correctable input fault (missing/invalid/unknown
// argument, malformed payload).
func ClientInputError(code, message, hint string) *PlatformError {
	return NewToolError(code, ErrCategoryClientInput, message, hint)
}

// NotFoundError is a reference the caller named that does not exist (unknown
// tool, unresolved connection, missing resource).
func NotFoundError(code, message, hint string) *PlatformError {
	return NewToolError(code, ErrCategoryNotFound, message, hint)
}

// UnavailableError is a feature that is not configured or enabled on this
// deployment.
func UnavailableError(message, hint string) *PlatformError {
	return NewToolError(CodeFeatureUnavailable, ErrCategoryUnavailable, message, hint)
}

// InternalError is a platform fault the caller cannot correct.
func InternalError(message string) *PlatformError {
	return NewToolError(CodeInternalError, ErrCategoryInternal, message,
		"This is a platform-side error, not a problem with your request. Do not retry with modified input; report it if it persists.")
}

// authzHint returns the corrective hint for an authorization denial, naming the
// caller's persona and the tool when known so the agent reports an
// access-policy decision rather than a platform fault.
func authzHint(persona, tool string) string {
	if persona == "" {
		return fmt.Sprintf("Your identity is not permitted to call %q. Request access from an administrator. This is an access-policy decision, not a platform outage.", tool)
	}
	return fmt.Sprintf("Persona %q is not permitted to call %q. Request access from an administrator or use a tool your persona allows. This is an access-policy decision, not a platform outage.", persona, tool)
}

// agentText renders the self-describing message the model sees in the result's
// text content: the message, the machine code, and the corrective hint.
func (e *PlatformError) agentText() string {
	text := e.Message
	if e.Code != "" {
		text = fmt.Sprintf("%s (code: %s)", e.Message, e.Code)
	}
	if e.Hint != "" {
		text += " Hint: " + e.Hint
	}
	return text
}

// BuildErrorResult renders a PlatformError into a self-describing tool result:
// a text content the model always sees (message + code + hint), a
// machine-readable structuredContent.error object an agent can branch on, and
// the error stashed via SetError so GetError()/ErrorCategory() keep feeding
// audit and metrics. An empty Category defaults to the generic tool_error so a
// result is never an uncategorized opaque string.
func BuildErrorResult(pe *PlatformError) *mcp.CallToolResult {
	if pe.Category == "" {
		pe.Category = ErrCategoryToolError
	}
	if pe.Code == "" {
		pe.Code = CodeToolError
	}
	result := &mcp.CallToolResult{
		StructuredContent: map[string]any{
			errorEnvelopeKey: errorPayload{
				Code:     pe.Code,
				Category: pe.Category,
				Message:  pe.Message,
				Hint:     pe.Hint,
			},
		},
		// Set the self-describing text first so SetError (which only fills empty
		// content) does not overwrite it with the bare message.
		Content: []mcp.Content{&mcp.TextContent{Text: pe.agentText()}},
	}
	result.SetError(pe)
	return result
}

// The following return *mcp.CallToolResult directly so toolkit handlers can
// categorize a correctable fault at the source in one call. They are shared
// across every toolkit (no per-kind fork); uncategorized faults still reach the
// agent self-describingly via MCPErrorContractMiddleware.

// MissingParameterResult reports a required argument the caller omitted.
func MissingParameterResult(param string) *mcp.CallToolResult {
	return BuildErrorResult(ClientInputError(CodeMissingParameter,
		fmt.Sprintf("the %q parameter is required", param),
		fmt.Sprintf("Supply %q and retry. This is a problem with the call's arguments, not a platform fault.", param)))
}

// NotFoundResult reports that a reference the caller named does not exist.
func NotFoundResult(message, hint string) *mcp.CallToolResult {
	return BuildErrorResult(NotFoundError(CodeNotFound, message, hint))
}

// UnavailableResult reports a feature that is not configured or enabled on this
// deployment.
func UnavailableResult(message, hint string) *mcp.CallToolResult {
	return BuildErrorResult(UnavailableError(message, hint))
}

// UnauthorizedResult reports that the caller's identity may not perform the
// action (for example, mutating a resource they do not own).
func UnauthorizedResult(message, hint string) *mcp.CallToolResult {
	return BuildErrorResult(NewToolError(CodeUnauthorized, ErrCategoryAuthz, message, hint))
}

// hasErrorEnvelope reports whether a result already carries the structured
// error contract, so the normalizer leaves source-categorized results untouched.
func hasErrorEnvelope(result *mcp.CallToolResult) bool {
	if result == nil {
		return false
	}
	if sc, ok := result.StructuredContent.(map[string]any); ok {
		if _, present := sc[errorEnvelopeKey]; present {
			return true
		}
	}
	return false
}
