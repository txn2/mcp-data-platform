package apigateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Structured error codes the gateway emits for memory-protection
// rejections. They are carried in the "error" field of the tool-error
// JSON envelope so the REST shim (pkg/gatewayhttp) can map them to the
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
	// client.
	ErrCodeBodyTooLarge = "upstream_body_too_large"

	// ErrCodeBudgetExhausted is returned by the buffered tools
	// (api_invoke_endpoint, api_export) when reserving the response
	// buffer would push the process past its global in-flight memory
	// budget. The request is refused before the buffer is allocated.
	ErrCodeBudgetExhausted = "gateway_memory_budget_exhausted"
)

// reserveBodyBudget computes the worst-case number of bytes a buffered
// read of this response could hold and tries to reserve them against
// the shared budget. It returns the amount reserved (to be released by
// the caller) and whether the reservation was granted.
//
// When the upstream declares a Content-Length below the read cap, only
// that many bytes are reserved so small (and empty) responses do not
// each tie up the full per-request cap and falsely exhaust the budget.
// This is safe because Go's HTTP client bounds resp.Body to the declared
// Content-Length — a server that writes more than it declared cannot
// make readBody buffer beyond it. Unknown/chunked responses
// (ContentLength < 0) and over-cap responses reserve the full cap, which
// is exactly what readBody may buffer. A nil/disabled budget always
// grants the reservation and Release is a no-op, so the buffered path is
// unchanged when no budget is configured.
func reserveBodyBudget(b *MemBudget, contentLength, readCap int64) (reserved int64, ok bool) {
	reserved = readCap
	if contentLength >= 0 && contentLength < readCap {
		reserved = contentLength
	}
	return reserved, b.Acquire(reserved)
}

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

// structuredErrorResult builds an IsError CallToolResult whose JSON
// body is {"error": code, ...fields}. The code occupies the same
// "error" field the plain errorResult helper uses, so existing
// consumers that read {"error": ...} keep working while the extra
// diagnostic fields ride alongside for callers that want them.
func structuredErrorResult(code string, fields map[string]any) *mcp.CallToolResult {
	payload := make(map[string]any, len(fields)+1)
	payload["error"] = code
	maps.Copy(payload, fields)
	b, err := json.Marshal(payload)
	if err != nil {
		// code is a fixed literal, so this cannot realistically fail;
		// fall back to the bare envelope rather than dropping IsError.
		b = []byte(`{"error":"` + code + `"}`)
	}
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
	}
}

// budgetOrErrorResult renders a run error from a buffered tool as either
// the structured budget-exhaustion result (mapped to 429 by the REST
// shim) or a plain error result. Shared by handleInvoke and handleExport
// so the two call sites classify a *budgetError identically.
func budgetOrErrorResult(err error) *mcp.CallToolResult {
	var be *budgetError
	if errors.As(err, &be) {
		return be.result()
	}
	return errorResult(err.Error())
}
