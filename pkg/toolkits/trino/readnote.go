package trino

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"

	"github.com/txn2/mcp-data-platform/internal/sqltables"
)

// readThroughExecuteNote is what a read sent through trino_execute is told
// beside its result (#2812).
const readThroughExecuteNote = "Note: this statement only reads, so trino_query is the tool for it. " +
	"trino_query is held to reading on every connection; trino_execute is for statements that write."

// ExecuteReadNote runs a statement that only reads (sqltables.IsRead) when it
// is sent to trino_execute, as it always has, and adds a note to its result
// that trino_query is the read tool (#2812). It changes nothing else: the
// statement and the result's data are as they were.
type ExecuteReadNote struct{}

// executeSQL is the statement of a trino_execute input, by value or pointer.
func executeSQL(input any) (string, bool) {
	switch in := input.(type) {
	case trinotools.ExecuteInput:
		return in.SQL, true
	case *trinotools.ExecuteInput:
		if in != nil {
			return in.SQL, true
		}
	}
	return "", false
}

// Before changes nothing.
func (ExecuteReadNote) Before(ctx context.Context, _ *trinotools.ToolContext) (context.Context, error) {
	return ctx, nil
}

// After adds the note to a successful read sent through trino_execute.
func (ExecuteReadNote) After(
	_ context.Context, tc *trinotools.ToolContext, result *mcp.CallToolResult, handlerErr error,
) (*mcp.CallToolResult, error) {
	if handlerErr != nil || result == nil || result.IsError || tc == nil || tc.Name != trinotools.ToolExecute {
		return result, handlerErr
	}
	if sql, ok := executeSQL(tc.Input); !ok || !sqltables.IsRead(sql) {
		return result, handlerErr
	}
	result.Content = append(result.Content, &mcp.TextContent{Text: readThroughExecuteNote})
	return result, handlerErr
}
