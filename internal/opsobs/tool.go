package opsobs

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// EndTool ends op for a tool handler's outcome: an error, or a result the
// handler answered as a tool error, counts as error; anything else as ok.
func EndTool(ctx context.Context, op *observability.Op, res *mcp.CallToolResult, err error) {
	if err == nil && res != nil && res.IsError {
		op.EndResult(ctx, resultError, observability.StatusClientErr, nil)
		return
	}
	op.End(ctx, err)
}

// resultError is the result an operation that failed is counted under.
const resultError = "error"
