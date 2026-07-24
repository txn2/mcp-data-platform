package pipeline

// b2 (code mode) attempt-path test: a stubbed claude process performs the
// agent's HTTP call against the REAL fixture service and returns a canned
// stream. The pipeline must build a no-MCP invocation with the spec in the
// workspace, grade the mutation from the fixture state dump, and record
// code-tool metrics — with no audit correlation (there is no platform).

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
	"github.com/txn2/mcp-data-platform/bench/internal/apisvc"
	"github.com/txn2/mcp-data-platform/bench/internal/claudecli"
	"github.com/txn2/mcp-data-platform/bench/internal/fixturectl"
	"github.com/txn2/mcp-data-platform/bench/internal/gen"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// codeModeStream renders the canned b2 transcript: one Bash call and the
// final answer.
func codeModeStream(cmd string) string {
	return `{"type":"system","subtype":"init","mcp_servers":[]}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":` + fmt.Sprintf("%q", cmd) + `}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","is_error":false,"content":"{\"status\":\"canceled\"}"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"FINAL ANSWER: done"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"FINAL ANSWER: done","session_id":"cc-2","usage":{"input_tokens":80,"output_tokens":12}}`
}

func TestCodeModeAttempt(t *testing.T) {
	fixture := httptest.NewServer(apisvc.New(apisvc.Options{APIKey: "fixture-key"}))
	t.Cleanup(fixture.Close)

	// The state task's target: the first pending order (the generated
	// p3-cancel-order exemplar).
	var pending gen.Order
	for _, o := range gen.Generate().Orders {
		if o.Status == "pending" {
			pending = o
			break
		}
	}
	stateTask := task.Task{
		ID: "b2-cancel", Suite: "p3", Prompt: "Cancel order id " + strconv.Itoa(pending.ID) + ".",
		Arms: []string{"b2"}, BudgetToolCalls: 10,
		GoldOperations: []string{"cancel_order"},
		Grading: task.Grading{Kind: task.GradeState, StateChecks: []task.StateCheck{
			{Resource: "orders", ID: int64(pending.ID), Fields: map[string]any{"status": "canceled"}},
		}},
	}
	dir := writeStudyTasks(t, []task.Task{stateTask})

	spec, err := apigen.BuildCatalog().SpecJSON(apigen.Tier0)
	if err != nil {
		t.Fatal(err)
	}

	// The stub asserts the invocation shape, verifies the workspace, and
	// performs the agent's HTTP call for real.
	var gotArgs []string
	exec := func(ctx context.Context, spec claudecli.CommandSpec) ([]byte, []byte, error) {
		gotArgs = spec.Args
		if _, err := os.Stat(filepath.Join(spec.Dir, "spec.json")); err != nil {
			t.Errorf("workspace spec.json missing: %v", err)
		}
		cancelURL := fmt.Sprintf("%s/commerce/orders/%d:cancel", fixture.URL, pending.ID)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, cancelURL, bytes.NewReader([]byte("{}")))
		req.Header.Set("X-API-Key", "fixture-key")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("stub cancel: %v", err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("stub cancel status %d", res.StatusCode)
		}
		return []byte(codeModeStream("curl -X POST " + cancelURL)), nil, nil
	}
	runner, err := claudecli.New(claudecli.Options{
		Model:     "claude-sonnet-5",
		CodeMode:  true,
		Workspace: map[string][]byte{"spec.json": spec},
		Exec:      exec,
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := Run(context.Background(), Options{
		Target:       target.Target{BaseURL: fixture.URL, Credential: "unused"},
		HTTPTimeout:  10 * time.Second,
		Arm:          "b2",
		Tier:         "t0",
		K:            1,
		TasksDir:     dir,
		ClaudeCLI:    runner,
		LLMProvider:  "claude-cli",
		AuditTimeout: 5 * time.Second,
		Fixture:      fixturectl.New(fixture.URL, "fixture-key", 10*time.Second),
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	a := res.Attempts[0]
	if a.Error != "" {
		t.Fatalf("attempt error: %s", a.Error)
	}
	if !a.Correct {
		t.Fatalf("state grading failed: %s", a.GradeDetail)
	}
	if a.ToolCalls != 1 || a.InputTokens != 80 {
		t.Errorf("tool_calls=%d input_tokens=%d, want 1/80", a.ToolCalls, a.InputTokens)
	}
	if a.Retrieval != nil {
		t.Errorf("code mode recorded retrieval %+v; there is no discovery tool", a.Retrieval)
	}
	if res.Manifest.Tier != "t0" {
		t.Errorf("manifest tier = %q", res.Manifest.Tier)
	}
	if slices.Contains(gotArgs, "--mcp-config") {
		t.Error("code mode passed --mcp-config")
	}
	if !slices.Contains(gotArgs, "--allowedTools") {
		t.Error("code mode missing --allowedTools")
	}
	for i, arg := range gotArgs {
		if arg == "--allowedTools" && gotArgs[i+1] != "Bash,Read,Write,Edit,Glob,Grep" {
			t.Errorf("allowed tools = %q", gotArgs[i+1])
		}
	}
}
