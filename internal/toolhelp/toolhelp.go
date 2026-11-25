// Package toolhelp answers, from a tool's advertised input schema, what the
// tool takes: the refusal of a call whose arguments do not match it (#2811),
// help on every tool that chooses its operation by a command or an action
// (#2815), and one example call fitting what a caller said it wants to do
// (#2809). Everything here reads the schema the server shows a client, so no
// answer can drift from what the tool accepts.
//
// It is its own package rather than more of pkg/middleware, which is at its
// file and exported-surface budgets.
package toolhelp

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// methodToolsCall is the MCP method these layers act on.
const methodToolsCall = "tools/call"

// codeInvalidArguments is the error contract's code for a call whose
// arguments fail the tool's input schema (pkg/middleware's argument
// refusal). The refusal layer rewrites only that refusal.
const codeInvalidArguments = "invalid_arguments"

// SchemaLookup returns a registered tool's advertised input schema as decoded
// JSON, or nil when it is not known. internal/toolschema answers it from the
// server's own tools/list.
type SchemaLookup func(ctx context.Context, tool string) map[string]any

// RefusalMiddleware rewrites an argument refusal so it names what the call got
// wrong and what the tool accepts (#2811): each property the schema does not
// declare with the accepted name it most likely meant, each value of the wrong
// type or outside an enum, and every accepted parameter with its type. The
// code and category are unchanged. Any other result, or a refusal whose tool
// schema cannot be read, is returned as it was.
//
// Placement: outer to the error contract, whose normalized refusal it reads,
// and inner to audit, which records the same code and category either way.
func RefusalMiddleware(lookup SchemaLookup) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil || method != methodToolsCall || lookup == nil {
				return result, err
			}
			return describeRefusal(ctx, lookup, req, result), nil
		}
	}
}

// describeRefusal is RefusalMiddleware's rewrite of one result.
func describeRefusal(ctx context.Context, lookup SchemaLookup, req mcp.Request, result mcp.Result) mcp.Result {
	ctr, ok := result.(*mcp.CallToolResult)
	if !ok || ctr == nil || !ctr.IsError {
		return result
	}
	var pe *middleware.PlatformError
	if !errors.As(ctr.GetError(), &pe) || pe.Code != codeInvalidArguments {
		return result
	}
	tool, args := callNameAndArgs(req)
	report, ok := describeArguments(tool, lookup(ctx, tool), args)
	if !ok {
		return result
	}
	return middleware.BuildErrorResult(middleware.ClientInputError(codeInvalidArguments, report.message(),
		"Resend the call with the accepted parameter names and types listed above. This is a problem "+
			"with the call's arguments, not a platform fault; the tool does not accept other names for them."))
}

// callNameAndArgs reads a tools/call request's tool name and raw arguments.
func callNameAndArgs(req mcp.Request) (name string, args json.RawMessage) {
	if req == nil {
		return "", nil
	}
	params, _ := req.GetParams().(*mcp.CallToolParamsRaw)
	if params == nil {
		return "", nil
	}
	return params.Name, params.Arguments
}
