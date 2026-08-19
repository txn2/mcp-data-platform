package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPErrorContractMiddleware guarantees that every tools/call error result is
// self-describing and uniform (issue #539). It is the backstop that makes the
// error surface consistent by construction: source-categorized errors pass
// through untouched, while any bare IsError result (a toolkit that has not been
// upgraded, an SDK input-validation failure, an upstream tool error) is enriched
// into the {code, category, message, hint} contract so an agent never receives
// an opaque, undifferentiated string it cannot branch on.
//
// It also recovers a panicking tool call into an internal-category error result,
// so a handler bug surfaces as a categorized, attributable failure instead of a
// dropped connection.
//
// Placement: this middleware must run inner to MCPAuditMiddleware and
// MCPMetricsMiddleware so they observe the normalized category, and outer to the
// tool handlers whose results it normalizes.
func MCPErrorContractMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (result mcp.Result, err error) {
			if method != methodToolsCall {
				return next(ctx, method, req)
			}
			defer func() {
				if r := recover(); r != nil {
					result, err = recoverToInternalError(ctx, r), nil
				}
			}()
			result, err = next(ctx, method, req)
			if err != nil {
				// A non-nil err is a protocol-level (JSON-RPC) failure, not a tool
				// result; leave it for the SDK to encode.
				return result, err
			}
			return normalizeErrorResult(result), nil
		}
	}
}

// recoverToInternalError logs a recovered panic and returns a categorized
// internal-error result so a handler bug surfaces as an attributable failure
// instead of a dropped connection.
func recoverToInternalError(ctx context.Context, r any) *mcp.CallToolResult {
	reqID := ""
	if pc := GetPlatformContext(ctx); pc != nil {
		reqID = pc.RequestID
	}
	slog.Error("tool call panicked; returning categorized internal error",
		"panic", r, "request_id", reqID)
	return BuildErrorResult(InternalError("the tool call failed unexpectedly"))
}

// normalizeErrorResult enriches a bare IsError result into the structured
// contract, leaving non-errors and already-structured results untouched.
func normalizeErrorResult(result mcp.Result) mcp.Result {
	ctr, ok := result.(*mcp.CallToolResult)
	if !ok || ctr == nil || !ctr.IsError || hasErrorEnvelope(ctr) {
		return result
	}
	return enrichBareErrorResult(ctr)
}

// enrichBareErrorResult promotes an IsError result that lacks the structured
// contract into one that carries it, preserving the original message. It adopts
// any category already stashed on the result (via SetError) and otherwise
// defaults to the generic tool_error category, so the result is self-describing
// even when the source could not classify it.
func enrichBareErrorResult(ctr *mcp.CallToolResult) *mcp.CallToolResult {
	msg := unwrapLegacyErrorJSON(extractMCPErrorMessage(ctr))
	if msg == "" {
		msg = "the tool call failed"
	}
	if pe := argumentValidationError(msg); pe != nil {
		return BuildErrorResult(pe)
	}
	pe := &PlatformError{
		Code:     CodeToolError,
		Category: ErrCategoryToolError,
		Message:  msg,
	}
	if category := ErrorCategory(ctr.GetError()); category != "" {
		pe.Category = category
	}
	return BuildErrorResult(pe)
}

const (
	// sdkArgumentValidationPrefix is how the MCP SDK reports a tools/call whose
	// arguments fail the tool's input schema (mcp.toolForErr wraps the jsonschema
	// verdict as `validating "arguments": ...`).
	sdkArgumentValidationPrefix = `validating "arguments":`

	// codeInvalidArguments is the agent-facing error code for that rejection. It
	// belongs to the same snake_case registry as the exported Code* values, but
	// stays unexported because the normalizer is the only producer: nothing
	// outside this package constructs an argument-validation error.
	codeInvalidArguments = "invalid_arguments"
)

// argumentValidationError categorizes an SDK input-schema rejection as the
// caller-correctable fault it is, or returns nil for any other message. Schemas
// closed to unknown arguments (issue #1057) make this the boundary an agent hits
// when it misnames a field, and a generic tool_error there invites a blind retry
// of the same call; client_input plus a corrective hint tells it to fix the
// argument name instead.
func argumentValidationError(msg string) *PlatformError {
	if !strings.HasPrefix(msg, sdkArgumentValidationPrefix) {
		return nil
	}
	return ClientInputError(codeInvalidArguments, msg,
		"The arguments do not match the tool's input schema. Read the tool's schema, "+
			"correct or drop the named property, and retry. This is a problem with the "+
			"call's arguments, not a platform fault.")
}

// unwrapLegacyErrorJSON returns the inner message from a toolkit error result
// whose text is the legacy `{"error":"..."}` envelope (the shape several
// toolkits emit via their local errorResult helper), so the normalized message
// is the human-readable text rather than a doubly-encoded JSON string. Any other
// text is returned unchanged.
func unwrapLegacyErrorJSON(text string) string {
	var legacy struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(text), &legacy); err == nil && legacy.Error != "" {
		return legacy.Error
	}
	return text
}

// resultTypeProtocolVersion is the first MCP protocol revision whose tools/call,
// prompts/get and resources/read results carry a required resultType
// (SEP-2322). The SDK keeps its own copy of this constant unexported; this one
// mirrors it, and TestMCPResultTypeMiddleware_MirrorsTheSDKForOlderClients pins
// the two to the same behavior.
const resultTypeProtocolVersion = "2026-07-28"

// MCPResultTypeMiddleware guarantees that every result the platform hands back
// on tools/call, prompts/get and resources/read carries the resultType the
// negotiated protocol revision requires (#1382, #1383).
//
// The SDK stamps resultType="complete" on a result inside its own method
// handler (Server.callTool and its prompt/resource counterparts), which is the
// innermost layer of the receiving chain. A receiving middleware that builds a
// result of its own never passes that layer: a refusal short-circuited by the
// gates (authz, session, search-first, purpose, rate limit), the error
// contract's normalized replacement of a bare error result, and the managed
// resource read all answer with a fresh result the SDK never saw. A client on
// revision 2026-07-28 rejects such a result as invalid, and the refusal text
// the platform composed for the person is discarded with it.
//
// This middleware is the outermost layer of the chain, so it sees the final
// result whatever built it, and it mirrors the SDK's own rule: for a client on
// 2026-07-28 or later the result is complete unless it carries input requests,
// and for an older client the field is left unset. The field is unexported in
// the SDK and reachable only through its wire form, so the stamp is applied by
// copying the result's exported fields onto a value decoded from that form.
func MCPResultTypeMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil || result == nil {
				return result, err
			}
			if !clientRequiresResultType(req) {
				return result, nil
			}
			switch method {
			case methodToolsCall, methodPromptsGet, methodReadResource:
				stampComplete(result)
			}
			return result, nil
		}
	}
}

// clientRequiresResultType reports whether the session's negotiated protocol
// revision requires resultType on a result. A session with no recorded
// initialize parameters is treated as the latest revision, which is the SDK's
// own default for the same question.
func clientRequiresResultType(req mcp.Request) bool {
	if req == nil {
		return false
	}
	ss, ok := req.GetSession().(*mcp.ServerSession)
	if !ok || ss == nil {
		return false
	}
	if params := ss.InitializeParams(); params != nil {
		return params.ProtocolVersion >= resultTypeProtocolVersion
	}
	return true
}

// completeCallToolResult, completeGetPromptResult and completeReadResourceResult
// are zero results decoded from the wire form that carries resultType=complete.
// They are the only way to obtain the unexported field set, and a value copy of
// one carries it.
var (
	completeCallToolResult     = mustDecodeComplete[mcp.CallToolResult]()
	completeGetPromptResult    = mustDecodeComplete[mcp.GetPromptResult]()
	completeReadResourceResult = mustDecodeComplete[mcp.ReadResourceResult]()
)

// mustDecodeComplete decodes the minimal complete-result wire form into T. A
// failure is a programming error surfaced at package initialization and covered
// by tests, mirroring regexp.MustCompile.
func mustDecodeComplete[T any]() T {
	var v T
	if err := json.Unmarshal([]byte(`{"resultType":"complete"}`), &v); err != nil {
		panic(fmt.Sprintf("middleware: decoding complete %T: %v", v, err))
	}
	return v
}

// stampComplete marks result complete in place. A result that carries input
// requests is left alone: it is the SDK's input_required answer, and the SDK
// has already typed it. A typed nil is left alone too; the SDK handles it as
// the absent result it is.
func stampComplete(result mcp.Result) {
	switch r := result.(type) {
	case *mcp.CallToolResult:
		stampCallToolResult(r)
	case *mcp.GetPromptResult:
		if r != nil && len(r.InputRequests) == 0 {
			restamp(completeGetPromptResult, r)
		}
	case *mcp.ReadResourceResult:
		if r != nil && len(r.InputRequests) == 0 {
			restamp(completeReadResourceResult, r)
		}
	}
}

// stampCallToolResult is stampComplete for a tool result, which also carries
// an unexported error the stamp must keep: it feeds GetError on the way out,
// so it is re-stashed after the copy. Content is already populated, so
// SetError leaves it untouched.
func stampCallToolResult(r *mcp.CallToolResult) {
	if r == nil || len(r.InputRequests) > 0 {
		return
	}
	err := r.GetError()
	restamp(completeCallToolResult, r)
	if err != nil {
		r.SetError(err)
	}
}

// restamp replaces *r with a copy of template carrying r's exported fields, so
// r keeps everything it said and gains the template's unexported resultType.
func restamp[T any](template T, r *T) {
	stamped := template
	copyExportedFields(&stamped, r)
	*r = stamped
}

// copyExportedFields assigns every exported field of src to dst, leaving dst's
// unexported fields as they are. dst and src must be pointers to the same
// struct type.
func copyExportedFields(dst, src any) {
	d := reflect.ValueOf(dst).Elem()
	s := reflect.ValueOf(src).Elem()
	for i := range s.NumField() {
		if s.Type().Field(i).IsExported() {
			d.Field(i).Set(s.Field(i))
		}
	}
}
