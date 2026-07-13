// Package llm defines the provider-pluggable model adapter the benchmark agent
// loop drives. The transcript model is provider-agnostic so the same loop runs
// against a real model (anthropic.go) and a deterministic playback script
// (scripted.go); the adapter owns the translation to its provider's wire shape.
package llm

import (
	"context"
	"encoding/json"
)

// ToolDef is one tool as presented to the model: the name, description, and
// full JSON Schema for its input, taken from the live tools/list of the
// benchmark session (so each arm's persona shapes what the model sees).
type ToolDef struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// ToolCall is one tool invocation requested by the model.
type ToolCall struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// ToolResult is the outcome of executing one ToolCall, keyed by its ID.
type ToolResult struct {
	CallID  string `json:"call_id"`
	Text    string `json:"text"`
	IsError bool   `json:"is_error"`
}

// Message is one turn of the provider-agnostic transcript. An assistant turn
// carries Text and/or ToolCalls; a user turn carries Text and/or ToolResults.
type Message struct {
	Role        string       `json:"role"` // "user" or "assistant"
	Text        string       `json:"text,omitempty"`
	ToolCalls   []ToolCall   `json:"tool_calls,omitempty"`
	ToolResults []ToolResult `json:"tool_results,omitempty"`
}

// Usage counts tokens for one completion, for the run manifest and cost audit.
type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// Add accumulates another completion's usage.
func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
}

// Adapter produces one assistant turn given the system prompt, transcript, and
// available tools. Implementations must be safe to use for sequential calls
// within one task attempt; they need not be safe for concurrent use.
type Adapter interface {
	// Model identifies the model for the run manifest (e.g. "claude-opus-4-8"
	// or "scripted").
	Model() string
	// Complete returns the next assistant message.
	Complete(ctx context.Context, system string, msgs []Message, tools []ToolDef) (Message, Usage, error)
}
