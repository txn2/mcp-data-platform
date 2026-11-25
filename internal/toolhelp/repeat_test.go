package toolhelp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

func TestRepeatMiddleware(t *testing.T) {
	session := "s1"
	mw := RepeatMiddleware(func(context.Context) string { return session })
	refuse := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return middleware.BuildErrorResult(middleware.ClientInputError("no_filter", "no filter for that", "h")), nil
	}
	call := func(tool string) string {
		req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: tool, Arguments: json.RawMessage(`{}`)}}
		res, err := mw(refuse)(context.Background(), methodToolsCall, req)
		if err != nil {
			t.Fatal(err)
		}
		return toolResult(t, res).GetError().Error()
	}
	if strings.Contains(call("manage_asset"), "topic=platform") {
		t.Error("the first refusal was annotated")
	}
	if !strings.Contains(call("manage_asset"), "topic=platform") {
		t.Error("the second identical refusal was not annotated")
	}
	if strings.Contains(call("manage_table"), "topic=platform") {
		t.Error("another tool's first refusal was annotated")
	}
	call("manage_feedback")
	if strings.Contains(call("manage_feedback"), "topic=platform") {
		t.Error("manage_feedback's own refusal was annotated")
	}
	session = ""
	call("manage_asset")
	if strings.Contains(call("manage_asset"), "topic=platform") {
		t.Error("a session-less refusal was annotated")
	}
}

func TestRepeatSetForgetsTheOldest(t *testing.T) {
	s := &repeatSet{keys: map[string]struct{}{}}
	for i := range repeatCapacity + 1 {
		s.seenBefore(string(rune(i)) + "k")
	}
	if len(s.keys) != repeatCapacity || len(s.order) != repeatCapacity {
		t.Errorf("held %d keys, want %d", len(s.keys), repeatCapacity)
	}
}
