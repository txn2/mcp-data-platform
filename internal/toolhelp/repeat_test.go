package toolhelp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

func TestRepeatMiddleware(t *testing.T) {
	session := "s1"
	mw := RepeatMiddleware(func(context.Context) string { return session })
	refuse := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return middleware.BuildErrorResult(middleware.ClientInputError("no_filter", "no filter for that", "h")), nil
	}
	ctx := context.Background()
	call := func(tool string) string {
		req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: tool, Arguments: json.RawMessage(`{}`)}}
		res, err := mw(refuse)(ctx, methodToolsCall, req)
		if err != nil {
			t.Fatal(err)
		}
		ctr := toolResult(t, res)
		if pe := platformError(t, ctr); pe == nil || strings.Contains(pe.Message, "topic=platform") {
			t.Errorf("the structured error was not kept as the tool wrote it: %+v", pe)
		}
		tc, _ := ctr.Content[0].(*mcp.TextContent)
		return tc.Text
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
	// A managed script's call is never annotated: it reads the structured
	// error to classify its failure.
	ctx = mcpcontext.WithSource(context.Background(), mcpcontext.SourceScript)
	call("manage_resource")
	if strings.Contains(call("manage_resource"), "topic=platform") {
		t.Error("a script's repeated refusal was annotated")
	}
	ctx = context.Background()
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

// TestWithSentenceOnAResultWithNoText: a refusal with no text block gets one.
func TestWithSentenceOnAResultWithNoText(t *testing.T) {
	ctr := withSentence(&mcp.CallToolResult{IsError: true})
	if len(ctr.Content) != 1 {
		t.Fatalf("content = %v", ctr.Content)
	}
	if tc, _ := ctr.Content[0].(*mcp.TextContent); tc == nil || !strings.Contains(tc.Text, "topic=platform") {
		t.Errorf("the sentence was not added: %v", ctr.Content[0])
	}
}
