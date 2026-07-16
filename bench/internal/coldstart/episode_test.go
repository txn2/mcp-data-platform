package coldstart

import (
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/agent"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
)

// TestCountMemoryWrites pins the evaluator no-self-teach validity signal:
// executed memory writes count (including the claude-cli path's namespaced
// form), while budget-refused writes, error results (a refused write landed
// nothing), read-only memory_manage commands, and non-memory tools do not.
func TestCountMemoryWrites(t *testing.T) {
	transcript := []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "search"},
			{ID: "2", Name: "memory_capture"},
			{ID: "3", Name: "mcp__bench__memory_manage", Args: map[string]any{"command": "update"}},
			{ID: "4", Name: "memory_capture"},                                         // budget-refused, never ran
			{ID: "5", Name: "memory_capture"},                                         // platform-refused (error result), wrote nothing
			{ID: "6", Name: "memory_manage", Args: map[string]any{"command": "list"}}, // read-only
		}},
		{Role: "user", ToolResults: []llm.ToolResult{
			{CallID: "1", Text: "results"},
			{CallID: "2", Text: "captured in-1"},
			{CallID: "3", Text: "updated"},
			{CallID: "4", Text: agent.BudgetRefusalText},
			{CallID: "5", Text: "SESSION_EXPIRED: mint a new handle", IsError: true},
			{CallID: "6", Text: "3 memories"},
		}},
	}
	if got := countMemoryWrites(transcript); got != 2 {
		t.Errorf("countMemoryWrites = %d, want 2 (executed capture + namespaced manage update)", got)
	}
	if got := countMemoryWrites(nil); got != 0 {
		t.Errorf("countMemoryWrites(nil) = %d, want 0", got)
	}
	clean := []llm.Message{{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "1", Name: "search"}}},
		{Role: "user", ToolResults: []llm.ToolResult{{CallID: "1", Text: "ok"}}}}
	if got := countMemoryWrites(clean); got != 0 {
		t.Errorf("countMemoryWrites(clean) = %d, want 0", got)
	}
}

// TestIsMemoryWriteCall covers the plain and namespaced tool-name forms and
// the memory_manage command split: only mutating commands are writes.
func TestIsMemoryWriteCall(t *testing.T) {
	cases := []struct {
		call llm.ToolCall
		want bool
	}{
		{llm.ToolCall{Name: "memory_capture"}, true},
		{llm.ToolCall{Name: "mcp__bench__memory_capture"}, true},
		{llm.ToolCall{Name: "memory_manage", Args: map[string]any{"command": "update"}}, true},
		{llm.ToolCall{Name: "memory_manage", Args: map[string]any{"command": "forget"}}, true},
		{llm.ToolCall{Name: "mcp__bench__memory_manage", Args: map[string]any{"command": "consolidate"}}, true},
		{llm.ToolCall{Name: "memory_manage", Args: map[string]any{"command": "list"}}, false},
		{llm.ToolCall{Name: "memory_manage", Args: map[string]any{"command": "review_stale"}}, false},
		{llm.ToolCall{Name: "memory_manage", Args: map[string]any{"command": "review_duplicates"}}, false},
		{llm.ToolCall{Name: "memory_manage"}, false}, // no command = help, read-only
		{llm.ToolCall{Name: "search"}, false},
		{llm.ToolCall{Name: "mcp__bench__search"}, false},
		{llm.ToolCall{Name: "apply_knowledge"}, false},
	}
	for _, tc := range cases {
		if got := isMemoryWriteCall(tc.call); got != tc.want {
			t.Errorf("isMemoryWriteCall(%s %v) = %v, want %v", tc.call.Name, tc.call.Args, got, tc.want)
		}
	}
}
