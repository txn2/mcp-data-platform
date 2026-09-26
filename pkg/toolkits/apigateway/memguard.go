package apigateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/membudget"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// MemBudget is the shared in-flight memory budget (internal/membudget),
// the type the platform wires through SetMemBudget.
type MemBudget = membudget.Budget

// NewMemBudget returns a budget capping concurrently-committed body
// bytes at maxBytes; maxBytes <= 0 is unlimited.
func NewMemBudget(maxBytes int64) *MemBudget { return membudget.New(maxBytes) }

// Structured error codes the gateway emits for memory-protection
// rejections. They are carried in the "error" field of the tool-error
// JSON envelope so the REST shim (internal/httpserver/gatewayhttp) can map them to the
// correct HTTP status with the correct retry semantics:
//
//   - ErrCodeBodyTooLarge      -> 413 Payload Too Large  (non-retryable)
//   - ErrCodeBudgetExhausted   -> 429 Too Many Requests   (retryable)
//
// The split mirrors the #533/#534 retry discipline: a permanent
// size-limit rejection (413) must not be retried, while a transient
// budget-exhaustion rejection (429) is safe to retry with backoff.
const (
	// ErrCodeBodyTooLarge is returned by the raw passthrough path when
	// the upstream's declared Content-Length exceeds the configured
	// all-or-nothing limit, before any bytes are streamed to the
	// client, and by api_invoke_endpoint when a caller that is not a
	// model reads a response past the connection's max_response_bytes
	// (#1878): that caller is refused rather than handed a cut body.
	ErrCodeBodyTooLarge = "upstream_body_too_large"

	// ErrCodeBudgetExhausted is returned by the buffered tools
	// (api_invoke_endpoint, api_export) when reserving the response
	// buffer would push the process past its global in-flight memory
	// budget. The request is refused before the buffer is allocated.
	ErrCodeBudgetExhausted = "gateway_memory_budget_exhausted"

	// ErrCodeBodyNotInlineable is returned by api_invoke_endpoint when
	// the upstream response is a binary / non-text body that cannot be
	// returned inline through the MCP/JSON channel without risking an
	// OOM (JSON-escape amplification) and that is useless to the model
	// anyway. The body is refused before it is buffered; the caller is
	// steered to api_export, which streams it to a portal asset. The
	// REST shim maps it to 415 (permanent, non-retryable): retrying the
	// same inline call cannot succeed, a different tool is required.
	ErrCodeBodyNotInlineable = "upstream_body_not_inlineable"
)

// budgetError is the typed error the buffered tools return when a body
// buffer reservation is refused. handleInvoke / handleExport detect it
// (errors.As) and render the structured 429 envelope; the REST shim
// maps that envelope to HTTP 429. It implements error so it can also
// flow through plain error returns.
type budgetError struct {
	limit      int64
	requested  int64
	inUse      int64
	connection string
	path       string
}

func (e *budgetError) Error() string {
	return fmt.Sprintf("%s: reserving %d bytes would exceed the gateway in-flight memory budget of %d (in use %d)",
		ErrCodeBudgetExhausted, e.requested, e.limit, e.inUse)
}

// result renders the budget rejection as a structured tool error whose
// "error" field is ErrCodeBudgetExhausted (so the REST classifier maps
// it to 429) plus the diagnostic byte counts and request coordinates.
func (e *budgetError) result() *mcp.CallToolResult {
	return structuredErrorResult(ErrCodeBudgetExhausted, map[string]any{
		"limit_bytes":     e.limit,
		"requested_bytes": e.requested,
		"in_use_bytes":    e.inUse,
		"connection":      e.connection,
		"path":            e.path,
	})
}

// bodyTooLargeResult renders the raw passthrough size rejection as a
// structured tool error whose "error" field is ErrCodeBodyTooLarge (so
// the REST classifier maps it to 413) plus the limit, the actual
// declared size, and the request coordinates.
func bodyTooLargeResult(connection, path string, limit, actual int64) *mcp.CallToolResult {
	return structuredErrorResult(ErrCodeBodyTooLarge, map[string]any{
		"limit_bytes":  limit,
		"actual_bytes": actual,
		"connection":   connection,
		"path":         path,
	})
}

// responseTooLargeError is the typed error api_invoke_endpoint returns
// when a response runs past the connection's read cap and the caller is
// not a model (#1878). A program parsing the response cannot read a cut
// one, so the call fails with 413 on the REST route rather than returning
// a prefix under a 200. declared is the upstream's Content-Length, or -1
// when it sent none.
type responseTooLargeError struct {
	connection string
	path       string
	limit      int64
	declared   int64
}

func (e *responseTooLargeError) Error() string {
	return fmt.Sprintf("%s: the upstream response is larger than the connection's max_response_bytes (%d)",
		ErrCodeBodyTooLarge, e.limit)
}

// result renders the refusal as a structured tool error whose "error"
// field is ErrCodeBodyTooLarge, which the REST shim maps to 413.
func (e *responseTooLargeError) result() *mcp.CallToolResult {
	fields := map[string]any{
		"limit_bytes": e.limit,
		"connection":  e.connection,
		"path":        e.path,
		fieldHint: "The response is larger than the most this connection reads of one response. Raise max_response_bytes " +
			"on the connection, or fetch it through a route that streams it: /invoke-raw on the REST gateway, or api_export.",
	}
	if e.declared > 0 {
		fields["actual_bytes"] = e.declared
	}
	return structuredErrorResult(ErrCodeBodyTooLarge, fields)
}

// nonInlineableBodyError is the typed error api_invoke_endpoint returns
// when the upstream Content-Type is a binary / non-text type the tool
// refuses to buffer and inline. size is the upstream's declared
// Content-Length, or -1 when the upstream did not declare one. It
// implements error so it can flow through the buffered tool's plain
// error return alongside *budgetError.
type nonInlineableBodyError struct {
	connection  string
	path        string
	contentType string
	size        int64
}

func (e *nonInlineableBodyError) Error() string {
	return fmt.Sprintf("%s: response Content-Type %q cannot be returned inline by api_invoke_endpoint; use api_export to stream it to an asset",
		ErrCodeBodyNotInlineable, e.contentType)
}

// result renders the rejection as a structured tool error whose "error"
// field is ErrCodeBodyNotInlineable (so the REST shim maps it to 415).
// hasExport tailors the hint: when api_export is registered on this
// deployment it is the recommended path; otherwise the model is told the
// body simply cannot be retrieved inline (the streaming raw route is
// REST-only and not reachable as an MCP tool).
func (e *nonInlineableBodyError) result(hasExport bool) *mcp.CallToolResult {
	fields := map[string]any{
		"connection":   e.connection,
		"path":         e.path,
		"content_type": e.contentType,
	}
	if e.size >= 0 {
		fields["size_bytes"] = e.size
	}
	if hasExport {
		fields[fieldHint] = "This is a binary/non-text response that api_invoke_endpoint cannot return inline. Use api_export with the same connection, method, and path to stream it into a portal asset (no model-context cost), then read or presign the asset."
	} else {
		fields[fieldHint] = "This is a binary/non-text response that api_invoke_endpoint cannot return inline. Retrieve it through the gateway's raw passthrough REST route instead of an inline tool call."
	}
	return structuredErrorResult(ErrCodeBodyNotInlineable, fields)
}

// structuredErrorResult builds an IsError CallToolResult whose JSON
// body is {"error": code, ...fields}. The code occupies the same
// "error" field the plain errorResult helper uses, so existing
// consumers that read {"error": ...} keep working while the extra
// diagnostic fields ride alongside for callers that want them.
func structuredErrorResult(code string, fields map[string]any) *mcp.CallToolResult {
	// Sized from the fields alone rather than fields+1: a capacity hint is
	// advisory, the one extra key costs at most a single growth on a map of
	// four, and arithmetic on a length in an allocation is what
	// go/allocation-size-overflow refuses.
	payload := make(map[string]any, len(fields))
	payload["error"] = code
	maps.Copy(payload, fields)
	b, err := json.Marshal(payload)
	if err != nil {
		// code is a fixed literal, so this cannot realistically fail;
		// fall back to the bare envelope rather than dropping IsError.
		b = []byte(`{"error":"` + code + `"}`)
	}
	res := &mcp.CallToolResult{
		IsError:           true,
		Content:           []mcp.Content{&mcp.TextContent{Text: string(b)}},
		StructuredContent: contractEnvelope(code, fields),
	}
	res.SetError(refusal{code: code})
	return res
}

// contractEnvelope is the structured content a refusal carries: the
// platform's error contract under "error" -- code, category, message,
// hint -- beside the diagnostic fields. A result that already carries the
// contract is left as it is by the platform's error-contract middleware,
// which otherwise rewrites a bare error result's text and drops every
// field but the message. The text is kept as the {"error": code, ...}
// object because the REST shim reads the fields from it: without this, a
// 413 or 429 reached a REST caller carrying no limit_bytes (#1878).
func contractEnvelope(code string, fields map[string]any) map[string]any {
	hint, _ := fields[fieldHint].(string)
	envelope := make(map[string]any, len(fields))
	for k, v := range fields {
		if k != fieldHint {
			envelope[k] = v
		}
	}
	envelope["error"] = map[string]any{
		"code":     code,
		"category": errCategoryToolError,
		"message":  code,
		fieldHint:  hint,
	}
	return envelope
}

// fieldHint is the key a refusal's corrective guidance is carried under,
// in the text object and in the contract envelope alike.
const fieldHint = "hint"

// errCategoryToolError is the error contract's category for a tool that
// failed on its own terms, the one the error-contract middleware assigns a
// result it cannot classify. Spelled here because this package cannot
// import pkg/middleware, which imports the toolkits.
const errCategoryToolError = "tool_error"

// refusal is the error a structured refusal is stamped with, so audit and
// metrics read its category as they read one the error contract stamps.
type refusal struct{ code string }

// Error is the refusal's code.
func (r refusal) Error() string { return r.code }

// ErrorCategory is the error contract's category for the refusal.
func (refusal) ErrorCategory() string { return errCategoryToolError }

// ErrorCode is the refusal's stable code.
func (r refusal) ErrorCode() string { return r.code }

// budgetOrErrorResult renders a run error from a buffered tool as either
// the structured budget-exhaustion result (mapped to 429 by the REST
// shim) or a plain error result. Shared by handleInvoke and handleExport
// so the two call sites classify a *budgetError identically.
func budgetOrErrorResult(err error) *mcp.CallToolResult {
	var be *budgetError
	if errors.As(err, &be) {
		return be.result()
	}
	var tl *responseTooLargeError
	if errors.As(err, &tl) {
		return tl.result()
	}
	res := toolkit.ErrorResult(err.Error())
	var te *transportError
	if errors.As(err, &te) {
		// The same outcome an api_invoke_endpoint transport failure is
		// stamped with, which the error contract reads as an upstream that
		// did not answer (#1859).
		res.Meta = mcp.Meta{observability.MetaAuditOutcome: te.outcome()}
	}
	return res
}

// transportError is an upstream that could not be reached or did not answer
// in time, as opposed to one that answered with a failure.
type transportError struct{ msg string }

func (e *transportError) Error() string { return e.msg }

// outcome is the audit outcome the failure is classified as.
func (e *transportError) outcome() string {
	if isTimeoutErrorMessage(e.msg) {
		return observability.OutcomeUpstreamTimeout
	}
	return observability.OutcomeTransportErr
}
