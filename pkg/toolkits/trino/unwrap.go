package trino

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"
)

// UnwrapJSONMiddleware is a trinotools.ToolMiddleware that defaults
// UnwrapJSON to true on trino_query and trino_execute inputs when the
// caller did not explicitly set it. This eliminates double-encoded
// VARCHAR-of-JSON responses from Trino table functions like
// system.raw_query() on the OpenSearch and Elasticsearch connectors.
//
// The middleware only touches tools that have an UnwrapJSON field.
// The upstream fall-through is graceful: if the result doesn't match
// (multi-column, multi-row, non-string, invalid JSON, scalar JSON),
// the response is byte-identical to unwrap_json=false.
type UnwrapJSONMiddleware struct{}

// Before sets UnwrapJSON=true on QueryInput and ExecuteInput when the
// platform default is enabled. Because the field uses `omitempty` and
// is a bool, the zero value (false) is indistinguishable from "not
// sent" — this is acceptable because the fall-through is safe.
func (*UnwrapJSONMiddleware) Before(ctx context.Context, tc *trinotools.ToolContext) (context.Context, error) {
	switch input := tc.Input.(type) {
	case *trinotools.QueryInput:
		input.UnwrapJSON = true
	case *trinotools.ExecuteInput:
		input.UnwrapJSON = true
	}
	return ctx, nil
}

// After is a no-op.
func (*UnwrapJSONMiddleware) After(
	_ context.Context,
	_ *trinotools.ToolContext,
	result *mcp.CallToolResult,
	handlerErr error,
) (*mcp.CallToolResult, error) {
	return result, handlerErr
}
