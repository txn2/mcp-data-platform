package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Step is one scripted assistant turn: either a set of tool calls or a final
// text answer. FinalText may contain the placeholder "{{last_result}}", which
// is replaced with the text of the most recent tool result in the transcript —
// this lets the smoke script answer with whatever the seeded platform actually
// returned (validating seed data, ground truth, and grading in one pass). The
// substituted result is flattened onto one line because the graders score only
// the FINAL ANSWER line, exactly as a compliant model answers.
type Step struct {
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	FinalText string     `json:"final_text,omitempty"`
}

// Script maps a task ID to its ordered playback steps.
type Script map[string][]Step

// LifecycleScript maps a protocol ID to its per-stage playback (stage ->
// steps), for the no-API-key S5 lifecycle smoke.
type LifecycleScript map[string]Script

// LoadScript reads a Script from a JSON file.
func LoadScript(path string) (Script, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-supplied script path
	if err != nil {
		return nil, fmt.Errorf("read script: %w", err)
	}
	var s Script
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("parse script %s: %w", path, err)
	}
	return s, nil
}

// LoadLifecycleScript reads a LifecycleScript from a JSON file.
func LoadLifecycleScript(path string) (LifecycleScript, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-supplied script path
	if err != nil {
		return nil, fmt.Errorf("read lifecycle script: %w", err)
	}
	var s LifecycleScript
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("parse lifecycle script %s: %w", path, err)
	}
	return s, nil
}

// Scripted is a deterministic Adapter that plays back a fixed step sequence.
// It exists so the harness pipeline (session mint, handle threading, tool
// execution, audit read-back, grading, reporting) is provable end-to-end with
// no LLM API key and no model variance.
type Scripted struct {
	steps []Step
	pos   int
}

// NewScripted returns an adapter that plays the given steps in order.
func NewScripted(steps []Step) *Scripted {
	return &Scripted{steps: steps}
}

// Model implements Adapter.
func (s *Scripted) Model() string { return "scripted" }

// Complete implements Adapter by returning the next scripted step.
func (s *Scripted) Complete(_ context.Context, _ string, msgs []Message, _ []ToolDef) (Message, Usage, error) {
	if s.pos >= len(s.steps) {
		return Message{}, Usage{}, fmt.Errorf("scripted adapter exhausted after %d steps", len(s.steps))
	}
	step := s.steps[s.pos]
	s.pos++
	if len(step.ToolCalls) > 0 {
		calls := make([]ToolCall, len(step.ToolCalls))
		copy(calls, step.ToolCalls)
		for i := range calls {
			if calls[i].ID == "" {
				calls[i].ID = fmt.Sprintf("scripted_call_%d_%d", s.pos, i)
			}
		}
		return Message{Role: "assistant", ToolCalls: calls}, Usage{}, nil
	}
	text := strings.ReplaceAll(step.FinalText, "{{last_result}}", lastToolResult(msgs))
	return Message{Role: "assistant", Text: text}, Usage{}, nil
}

// lastToolResult returns the text of the most recent tool result in the
// transcript flattened onto one line, or "" when none exists.
func lastToolResult(msgs []Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if n := len(msgs[i].ToolResults); n > 0 {
			return strings.Join(strings.Fields(msgs[i].ToolResults[n-1].Text), " ")
		}
	}
	return ""
}
