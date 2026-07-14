package llm

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// TestBuildParamsCachingLive makes two real Messages API turns and verifies the
// prompt cache actually engages: turn 1 writes the constant system+prefix to
// cache, turn 2 reads it back. Skipped without ANTHROPIC_API_KEY (so it never
// runs in `make bench-test`); costs a few cents when run. This is the
// end-to-end proof that the cache breakpoints cut real input-token cost, not
// just that the request is shaped right (TestBuildParamsSetsCacheBreakpoints).
func TestBuildParamsCachingLive(t *testing.T) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set; skipping live cache verification")
	}
	a, err := NewAnthropic("claude-sonnet-5", 256, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// System prompt large enough to clear the model's minimum cacheable length.
	system := "You are a benchmark harness. " + strings.Repeat("Ground every answer in tool results and be terse. ", 200)

	turn1 := []Message{{Role: "user", Text: "Reply with exactly the word: one"}}
	p1, err := a.buildParams(system, turn1, nil)
	if err != nil {
		t.Fatal(err)
	}
	r1, err := a.client.Messages.New(ctx, p1)
	if err != nil {
		t.Fatalf("turn1: %v", err)
	}

	turn2 := append(turn1,
		Message{Role: "assistant", Text: fromAPIContent(r1).Text},
		Message{Role: "user", Text: "Reply with exactly the word: two"},
	)
	p2, err := a.buildParams(system, turn2, nil)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := a.client.Messages.New(ctx, p2)
	if err != nil {
		t.Fatalf("turn2: %v", err)
	}

	t.Logf("turn1: input=%d cache_creation=%d cache_read=%d",
		r1.Usage.InputTokens, r1.Usage.CacheCreationInputTokens, r1.Usage.CacheReadInputTokens)
	t.Logf("turn2: input=%d cache_creation=%d cache_read=%d",
		r2.Usage.InputTokens, r2.Usage.CacheCreationInputTokens, r2.Usage.CacheReadInputTokens)

	if r1.Usage.CacheCreationInputTokens == 0 {
		t.Error("turn1 wrote nothing to cache — caching not engaging (system below min cacheable length?)")
	}
	if r2.Usage.CacheReadInputTokens == 0 {
		t.Error("turn2 read nothing from cache — the rolling/system breakpoints are not producing cache hits")
	}
}

// buildParams is constructed directly (not via NewAnthropic) so the test needs
// no ANTHROPIC_API_KEY and makes no API call — it inspects only the request
// shape, in particular the prompt-cache breakpoints.
func TestBuildParamsSetsCacheBreakpoints(t *testing.T) {
	a := &Anthropic{model: "claude-sonnet-5", maxTokens: 1024}
	msgs := []Message{
		{Role: "user", Text: "What is the revenue?"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "trino_query", Args: map[string]any{"sql": "SELECT 1"}}}},
		{Role: "user", ToolResults: []ToolResult{{CallID: "c1", Text: "42"}}},
	}
	tools := []ToolDef{{Name: "trino_query", Description: "run sql", InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}},"required":["sql"]}`)}}

	params, err := a.buildParams("You are an analyst.", msgs, tools)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}

	// System prompt carries a cache breakpoint (caches tools+system prefix).
	if len(params.System) != 1 || params.System[0].CacheControl.Type != "ephemeral" {
		t.Errorf("system block missing cache_control: %+v", params.System)
	}

	// The rolling breakpoint sits on the LAST block of the LAST message only.
	if len(params.Messages) != 3 {
		t.Fatalf("got %d messages, want 3", len(params.Messages))
	}
	lastMsg := params.Messages[len(params.Messages)-1]
	lastBlock := lastMsg.Content[len(lastMsg.Content)-1]
	if cc := lastBlock.GetCacheControl(); cc == nil || cc.Type != "ephemeral" {
		t.Errorf("last message block missing rolling cache_control: %+v", cc)
	}
	// An earlier message must NOT carry a breakpoint (only the last does).
	first := params.Messages[0]
	if cc := first.Content[len(first.Content)-1].GetCacheControl(); cc != nil && cc.Type == "ephemeral" {
		t.Error("first message should not carry a cache breakpoint")
	}
	if len(params.Tools) != 1 {
		t.Errorf("got %d tools, want 1", len(params.Tools))
	}

	// Confirm the breakpoints reach the WIRE payload: exactly two cache_control
	// blocks (system + last message), so the request actually asks the API to
	// cache — not just struct fields that never serialize.
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if n := bytesCount(raw, `"cache_control"`); n != 2 {
		t.Errorf("wire payload has %d cache_control breakpoints, want 2\n%s", n, raw)
	}
}

// bytesCount counts non-overlapping occurrences of sub in b.
func bytesCount(b []byte, sub string) int {
	n, s := 0, string(b)
	for i := 0; i+len(sub) <= len(s); {
		if s[i:i+len(sub)] == sub {
			n++
			i += len(sub)
		} else {
			i++
		}
	}
	return n
}

func TestBuildParamsNoSystem(t *testing.T) {
	a := &Anthropic{model: "claude-sonnet-5", maxTokens: 1024}
	params, err := a.buildParams("", []Message{{Role: "user", Text: "hi"}}, nil)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	if len(params.System) != 0 {
		t.Errorf("expected no system blocks, got %d", len(params.System))
	}
	// The rolling breakpoint still lands on the sole user message.
	last := params.Messages[len(params.Messages)-1]
	if cc := last.Content[len(last.Content)-1].GetCacheControl(); cc == nil || cc.Type != "ephemeral" {
		t.Error("rolling cache breakpoint not set with no system prompt")
	}
}

func TestMarkRollingCacheBreakpointEmpty(t *testing.T) {
	// No panic on an empty transcript.
	markRollingCacheBreakpoint(nil)
}
