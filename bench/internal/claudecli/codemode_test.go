package claudecli

// Code-mode (b2, #1027) invocation shape: no MCP config, code tools
// allowed, web tools disallowed, workspace files materialized.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// codeStream is a minimal code-mode stream: one Bash call, final answer.
const codeStream = `{"type":"system","subtype":"init","mcp_servers":[]}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"curl http://fixture/x"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","is_error":false,"content":"ok"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"FINAL ANSWER: 7"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"FINAL ANSWER: 7","session_id":"cc-3","usage":{"input_tokens":9,"output_tokens":4}}`

func TestCodeModeRun(t *testing.T) {
	var workspaceSpec []byte
	cr := &captureRunner{stdout: codeStream}
	wrapped := func(ctx context.Context, spec CommandSpec) ([]byte, []byte, error) {
		// The workspace file must exist while the process runs (the
		// attempt dir is removed afterward).
		workspaceSpec, _ = os.ReadFile(filepath.Join(spec.Dir, "spec.json"))
		return cr.run(ctx, spec)
	}
	r, err := New(Options{
		Model:     "sonnet",
		CodeMode:  true,
		Workspace: map[string][]byte{"spec.json": []byte(`{"openapi":"3.0.3"}`)},
		Exec:      wrapped,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !r.CodeMode() {
		t.Fatal("CodeMode() = false")
	}
	res, err := r.Run(context.Background(), Request{System: "sys", Prompt: "task"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalText != "FINAL ANSWER: 7" || res.Usage.InputTokens != 9 {
		t.Errorf("result = %+v", res)
	}
	if string(workspaceSpec) != `{"openapi":"3.0.3"}` {
		t.Errorf("workspace spec.json = %q", workspaceSpec)
	}
	args := cr.last.Args
	if slices.Contains(args, "--mcp-config") {
		t.Error("code mode passed --mcp-config")
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--allowedTools Bash,Read,Write,Edit,Glob,Grep") {
		t.Errorf("allowed tools missing: %s", joined)
	}
	if !strings.Contains(joined, "--disallowedTools WebFetch,WebSearch,Task,TodoWrite,NotebookEdit") {
		t.Errorf("disallowed tools missing: %s", joined)
	}
	if !strings.Contains(joined, "--append-system-prompt sys") {
		t.Errorf("system prompt missing: %s", joined)
	}
}
