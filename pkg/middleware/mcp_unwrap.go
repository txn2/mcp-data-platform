package middleware

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// unwrapJSONTools lists tool names whose arguments should receive
// the unwrap_json default. Only trino_query and trino_execute support
// the parameter upstream.
var unwrapJSONTools = map[string]struct{}{
	"trino_query":   {},
	"trino_execute": {},
}

// MCPUnwrapJSONMiddleware creates MCP protocol-level middleware that
// injects "unwrap_json": true into the raw arguments of trino_query
// and trino_execute calls when the caller has not explicitly set it.
//
// This eliminates double-encoded VARCHAR-of-JSON responses from Trino
// table functions like system.raw_query() on OpenSearch/Elasticsearch
// connectors, without requiring every caller to learn the parameter.
//
// The upstream fall-through is graceful: if the result is not a single
// row with a single string column containing valid JSON, the response
// is byte-identical to unwrap_json=false.
func MCPUnwrapJSONMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != methodToolsCall {
				return next(ctx, method, req)
			}

			injectUnwrapJSONDefault(req)

			return next(ctx, method, req)
		}
	}
}

// injectUnwrapJSONDefault sets "unwrap_json": true in the raw arguments
// of a tools/call request when the tool supports it and the caller has
// not already provided a value.
func injectUnwrapJSONDefault(req mcp.Request) {
	if req == nil {
		return
	}
	params := req.GetParams()
	if params == nil {
		return
	}
	callParams, ok := params.(*mcp.CallToolParamsRaw)
	if !ok || callParams == nil {
		return
	}

	// Only inject for tools that support unwrap_json.
	if _, supported := unwrapJSONTools[callParams.Name]; !supported {
		return
	}

	// Parse existing arguments.
	var args map[string]any
	if len(callParams.Arguments) > 0 {
		if err := json.Unmarshal(callParams.Arguments, &args); err != nil {
			return // Malformed JSON — let the handler deal with it.
		}
	} else {
		args = make(map[string]any)
	}

	// Don't override an explicit caller value.
	if _, exists := args["unwrap_json"]; exists {
		return
	}

	args["unwrap_json"] = true

	updated, err := json.Marshal(args)
	if err != nil {
		return // Shouldn't happen, but don't break the request.
	}
	callParams.Arguments = updated
}
