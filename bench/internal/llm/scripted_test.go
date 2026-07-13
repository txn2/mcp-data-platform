package llm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScriptedPlayback(t *testing.T) {
	s := NewScripted([]Step{
		{ToolCalls: []ToolCall{{Name: "trino_query", Args: map[string]any{"sql": "SELECT 1"}}}},
		{FinalText: "FINAL ANSWER: {{last_result}}"},
	})
	msg, _, err := s.Complete(context.Background(), "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID == "" {
		t.Fatalf("first step should be a tool call with a synthesized id: %+v", msg)
	}
	transcript := []Message{
		{Role: "user", Text: "q"},
		msg,
		{Role: "user", ToolResults: []ToolResult{{CallID: msg.ToolCalls[0].ID, Text: "42.5"}}},
	}
	final, _, err := s.Complete(context.Background(), "", transcript, nil)
	if err != nil {
		t.Fatal(err)
	}
	if final.Text != "FINAL ANSWER: 42.5" {
		t.Errorf("placeholder not replaced: %q", final.Text)
	}
	if _, _, err := s.Complete(context.Background(), "", nil, nil); err == nil {
		t.Error("expected exhaustion error")
	}
}

func TestScriptedPlaceholderWithoutResult(t *testing.T) {
	s := NewScripted([]Step{{FinalText: "FINAL ANSWER: {{last_result}}"}})
	msg, _, err := s.Complete(context.Background(), "", []Message{{Role: "user", Text: "q"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Text != "FINAL ANSWER: " {
		t.Errorf("empty placeholder handling: %q", msg.Text)
	}
}

func TestLoadScript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "script.json")
	content := `{"t1": [{"final_text": "FINAL ANSWER: x"}]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := LoadScript(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(script["t1"]) != 1 || script["t1"][0].FinalText != "FINAL ANSWER: x" {
		t.Errorf("script parsed wrong: %+v", script)
	}
	if _, err := LoadScript(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("expected error for missing file")
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadScript(bad); err == nil {
		t.Error("expected error for malformed json")
	}
}

func TestLoadLifecycleScript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lc.json")
	content := `{"lc-a": {"teach": [{"tool_calls": [{"name": "memory_capture", "args": {}}]}], "recall": [{"final_text": "FINAL ANSWER: 42"}]}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := LoadLifecycleScript(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(script["lc-a"]["teach"]) != 1 || script["lc-a"]["recall"][0].FinalText != "FINAL ANSWER: 42" {
		t.Errorf("lifecycle script parsed wrong: %+v", script)
	}
	if _, err := LoadLifecycleScript(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("expected error for missing file")
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLifecycleScript(bad); err == nil {
		t.Error("expected error for malformed json")
	}
}
