package trino

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"
)

func TestExecuteReadNote(t *testing.T) {
	m := ExecuteReadNote{}
	ctx, err := m.Before(context.Background(), nil)
	if err != nil || ctx == nil {
		t.Fatal("Before changed the call")
	}
	note := func(tool trinotools.ToolName, input any, res *mcp.CallToolResult, herr error) bool {
		got, _ := m.After(context.Background(), &trinotools.ToolContext{Name: tool, Input: input}, res, herr)
		if got == nil {
			return false
		}
		for _, c := range got.Content {
			if tc, ok := c.(*mcp.TextContent); ok && tc.Text == readThroughExecuteNote {
				return true
			}
		}
		return false
	}
	ok := func() *mcp.CallToolResult { return &mcp.CallToolResult{} }
	if !note(trinotools.ToolExecute, trinotools.ExecuteInput{SQL: "SELECT 1"}, ok(), nil) {
		t.Error("a read through trino_execute got no note")
	}
	if !note(trinotools.ToolExecute, &trinotools.ExecuteInput{SQL: "SHOW TABLES"}, ok(), nil) {
		t.Error("a pointer input got no note")
	}
	if note(trinotools.ToolExecute, trinotools.ExecuteInput{SQL: "INSERT INTO t VALUES (1)"}, ok(), nil) {
		t.Error("a write got the note")
	}
	if note(trinotools.ToolQuery, trinotools.ExecuteInput{SQL: "SELECT 1"}, ok(), nil) {
		t.Error("trino_query got the note")
	}
	if note(trinotools.ToolExecute, trinotools.ExecuteInput{SQL: "SELECT 1"}, &mcp.CallToolResult{IsError: true}, nil) {
		t.Error("a failed read got the note")
	}
	if note(trinotools.ToolExecute, trinotools.ExecuteInput{SQL: "SELECT 1"}, ok(), errors.New("x")) {
		t.Error("a handler error got the note")
	}
	if note(trinotools.ToolExecute, "not an input", ok(), nil) {
		t.Error("an unknown input got the note")
	}
}
