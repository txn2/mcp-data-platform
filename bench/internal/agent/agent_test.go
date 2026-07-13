package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/llm"
)

// fakeAdapter plays canned assistant turns.
type fakeAdapter struct {
	turns []llm.Message
	pos   int
	seen  [][]llm.Message // transcript snapshot per Complete call
}

func (f *fakeAdapter) Model() string { return "fake" }

func (f *fakeAdapter) Complete(_ context.Context, _ string, msgs []llm.Message, _ []llm.ToolDef) (llm.Message, llm.Usage, error) {
	snapshot := make([]llm.Message, len(msgs))
	copy(snapshot, msgs)
	f.seen = append(f.seen, snapshot)
	if f.pos >= len(f.turns) {
		return llm.Message{}, llm.Usage{}, errors.New("no more turns")
	}
	m := f.turns[f.pos]
	f.pos++
	return m, llm.Usage{InputTokens: 10, OutputTokens: 5}, nil
}

// okExec answers every call successfully.
func okExec(_ context.Context, name string, _ map[string]any) llm.ToolResult {
	return llm.ToolResult{Text: "result of " + name}
}

func TestRunToolLoopThenFinal(t *testing.T) {
	a := &fakeAdapter{turns: []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "1", Name: "search", Args: map[string]any{"intent": "x"}}}},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "2", Name: "trino_query", Args: map[string]any{"sql": "SELECT 1"}}}},
		{Role: "assistant", Text: "FINAL ANSWER: 42"},
	}}
	res, err := Run(context.Background(), a, Config{Prompt: "q", Budget: 5}, okExec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalAnswer != "FINAL ANSWER: 42" || res.ToolCalls != 2 || res.ToolErrors != 0 || res.BudgetExhausted {
		t.Errorf("unexpected result: %+v", res)
	}
	if res.Usage.InputTokens != 30 || res.Usage.OutputTokens != 15 {
		t.Errorf("usage not accumulated: %+v", res.Usage)
	}
	// The tool result must be visible to the next completion.
	last := a.seen[len(a.seen)-1]
	if got := last[len(last)-1].ToolResults[0].Text; got != "result of trino_query" {
		t.Errorf("tool result not threaded: %q", got)
	}
}

func TestRunBudgetExhaustion(t *testing.T) {
	twoCalls := []llm.ToolCall{
		{ID: "a", Name: "t1", Args: map[string]any{}},
		{ID: "b", Name: "t2", Args: map[string]any{}},
	}
	a := &fakeAdapter{turns: []llm.Message{
		{Role: "assistant", ToolCalls: twoCalls},
		{Role: "assistant", ToolCalls: twoCalls}, // second call of this turn exceeds budget 3
		{Role: "assistant", Text: "FINAL ANSWER: partial"},
	}}
	res, err := Run(context.Background(), a, Config{Prompt: "q", Budget: 3}, okExec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.BudgetExhausted || res.ToolCalls != 3 {
		t.Errorf("budget accounting wrong: %+v", res)
	}
	// The over-budget call must be answered with an error result plus the
	// wind-down instruction.
	turn := a.seen[2][len(a.seen[2])-1]
	if len(turn.ToolResults) != 2 || !turn.ToolResults[1].IsError || !strings.Contains(turn.Text, "FINAL ANSWER") {
		t.Errorf("budget turn malformed: %+v", turn)
	}
	if res.FinalAnswer != "FINAL ANSWER: partial" {
		t.Errorf("final answer: %q", res.FinalAnswer)
	}
}

func TestRunToolErrorCounted(t *testing.T) {
	a := &fakeAdapter{turns: []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "1", Name: "bad", Args: map[string]any{}}}},
		{Role: "assistant", Text: "FINAL ANSWER: none"},
	}}
	exec := func(context.Context, string, map[string]any) llm.ToolResult {
		return llm.ToolResult{Text: "boom", IsError: true}
	}
	res, err := Run(context.Background(), a, Config{Prompt: "q", Budget: 5}, exec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ToolErrors != 1 {
		t.Errorf("tool errors = %d, want 1", res.ToolErrors)
	}
}

func TestRunAdapterErrorPropagates(t *testing.T) {
	a := &fakeAdapter{} // immediately exhausted
	if _, err := Run(context.Background(), a, Config{Prompt: "q", Budget: 1}, okExec); err == nil {
		t.Fatal("expected adapter error")
	}
}

func TestRunWindDownBounded(t *testing.T) {
	// A model that burns the whole budget with one multi-call turn and keeps
	// requesting tools gets at most extraIterations completions after
	// exhaustion, not the whole iteration cap.
	burst := make([]llm.ToolCall, 6)
	for i := range burst {
		burst[i] = llm.ToolCall{ID: "b", Name: "t", Args: map[string]any{}}
	}
	keepCalling := llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "x", Name: "t", Args: map[string]any{}}}}
	a := &fakeAdapter{turns: []llm.Message{
		{Role: "assistant", ToolCalls: burst}, // consumes budget 6 in one turn
		keepCalling,                           // first refused call flags exhaustion
		keepCalling, keepCalling, keepCalling, // would run to the iteration cap without the wind-down bound
	}}
	_, err := Run(context.Background(), a, Config{Prompt: "q", Budget: 6}, okExec)
	if err == nil || !strings.Contains(err.Error(), "budget exhaustion") {
		t.Fatalf("want wind-down bound error, got %v", err)
	}
	// Burst turn + the turn that trips exhaustion + extraIterations wind-down
	// completions; the iteration cap alone would have allowed 9.
	if got := len(a.seen); got != 4 {
		t.Errorf("completions = %d, want 4 (burst + tripping turn + 2 wind-down)", got)
	}
}

func TestRunNeverEndingLoopBounded(t *testing.T) {
	call := llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "x", Name: "t", Args: map[string]any{}}}}
	turns := make([]llm.Message, 50)
	for i := range turns {
		turns[i] = call
	}
	a := &fakeAdapter{turns: turns}
	_, err := Run(context.Background(), a, Config{Prompt: "q", Budget: 2}, okExec)
	if err == nil || !strings.Contains(err.Error(), "iterations") {
		t.Fatalf("expected iteration bound error, got %v", err)
	}
}
