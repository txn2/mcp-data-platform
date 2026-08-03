package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/bench/internal/auditapi"
	"github.com/txn2/mcp-data-platform/bench/internal/claudecli"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/report"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// fakePlatform assembles a real MCP server over streamable HTTP plus the admin
// audit API surface, so the pipeline is exercised through genuine protocol
// wiring: initialize handshake, platform_info handle mint, session_id
// threading (the audit rows are keyed by the session_id argument the tools
// receive — if threading breaks, audit read-back times out and the test
// fails), refusal classification, and audit-derived metrics.
type fakePlatform struct {
	mu         sync.Mutex
	events     []auditapi.Event
	identities map[string]bool // Authorization headers seen on MCP traffic
	minted     atomic.Int64
	httpSrv    *httptest.Server
}

func newFakePlatform(t *testing.T) *fakePlatform {
	t.Helper()
	fp := &fakePlatform{}
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-platform", Version: "1.0.0"}, nil)
	fp.addPlatformInfo(server)
	fp.addTrinoQuery(server)
	fp.addDeniedTool(server)

	fp.identities = map[string]bool{}
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/admin/audit/events", fp.serveAudit)
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fp.mu.Lock()
		fp.identities[r.Header.Get("Authorization")] = true
		fp.mu.Unlock()
		mcpHandler.ServeHTTP(w, r)
	}))
	fp.httpSrv = httptest.NewServer(mux)
	t.Cleanup(fp.httpSrv.Close)
	return fp
}

// addPlatformInfo mints dps_ handles like the real init tool.
func (fp *fakePlatform) addPlatformInfo(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "platform_info", Description: "platform orientation"},
		func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			handle := fmt.Sprintf("dps_fake_%d", fp.minted.Add(1))
			payload := map[string]any{"session_id": handle, "version": "fake-1.0.0"}
			raw, _ := json.Marshal(payload)
			return &mcp.CallToolResult{
				Content:           []mcp.Content{&mcp.TextContent{Text: string(raw)}},
				StructuredContent: payload,
			}, nil, nil
		})
}

// addTrinoQuery records an audit event under the threaded session_id and
// returns a single-value result.
func (fp *fakePlatform) addTrinoQuery(server *mcp.Server) {
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"sql":        {Type: "string"},
			"session_id": {Type: "string", Description: "platform-injected session handle"},
		},
		Required: []string{"sql", "session_id"},
	}
	mcp.AddTool(server, &mcp.Tool{Name: "trino_query", Description: "run sql", InputSchema: schema},
		func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			sessionID, _ := args["session_id"].(string)
			sql, _ := args["sql"].(string)
			fp.record(auditapi.Event{
				Timestamp: time.Now().UTC(), DurationMS: 7, SessionID: sessionID,
				ToolName: "trino_query", Success: true, EventKind: "mcp_tool_call",
				EnrichmentApplied: true, EnrichmentTokensDedup: 42,
			})
			// The numeric-task sentinel keeps the existing single-value path; any
			// other query echoes its text back as structured rows so the
			// execution-result grader sees identical rows for identical SQL and
			// different rows for different SQL.
			if sql == "SELECT 42.5" {
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: `[{"total_usd": 42.5}]`}},
				}, nil, nil
			}
			payload := map[string]any{"rows": []map[string]any{{"echo": sql}}}
			return &mcp.CallToolResult{
				Content:           []mcp.Content{&mcp.TextContent{Text: `{"rows":[{"echo":"` + sql + `"}]}`}},
				StructuredContent: payload,
			}, nil, nil
		})
}

// addDeniedTool mimics a persona-refused tool: an error result carrying the
// platform error contract's structured envelope, with NO audit row (refusals
// short-circuit outer to the audit middleware on the real platform).
func (fp *fakePlatform) addDeniedTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "denied_tool", Description: "always refused"},
		func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "Access denied"}},
				StructuredContent: map[string]any{"error": map[string]any{
					"code": "unauthorized", "category": "authorization_denied", "message": "Access denied",
				}},
			}, nil, nil
		})
}

func (fp *fakePlatform) record(e auditapi.Event) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	fp.events = append(fp.events, e)
}

// serveAudit implements the admin audit list endpoint (session_id filter).
func (fp *fakePlatform) serveAudit(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	sessionID := r.URL.Query().Get("session_id")
	fp.mu.Lock()
	var matched []auditapi.Event
	for _, e := range fp.events {
		if e.SessionID == sessionID {
			matched = append(matched, e)
		}
	}
	fp.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": matched, "total": len(matched), "page": 1, "per_page": 200,
	})
}

// writeTaskFiles writes a two-task set: a numeric trap answered via the fake
// trino_query result, and an entity discovery task answered directly.
func writeTaskFiles(t *testing.T, dir string) {
	t.Helper()
	numeric := task.Task{
		ID: "t-numeric", Suite: "s3", Prompt: "total?", Arms: []string{"a0"}, BudgetToolCalls: 5,
		ExpectedSQL: "SELECT 42.5",
		Grading:     task.Grading{Kind: task.GradeNumeric, Value: new(42.5), AbsTolerance: 0.01},
	}
	entity := task.Task{
		ID: "t-entity", Suite: "s1", Prompt: "which table?", Arms: []string{"a0"}, BudgetToolCalls: 5,
		Grading: task.Grading{Kind: task.GradeEntity, Aliases: []string{"memory.bench.orders"}},
	}
	for _, tk := range []task.Task{numeric, entity} {
		raw, err := json.Marshal(tk) // Task yaml tags match json; YAML is a JSON superset
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, tk.ID+".yaml"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// testScript plays: refused call, then the real query, then answer from the
// live result (numeric); direct answer (entity).
func testScript() llm.Script {
	return llm.Script{
		"t-numeric": {
			{ToolCalls: []llm.ToolCall{{Name: "denied_tool", Args: map[string]any{}}}},
			{ToolCalls: []llm.ToolCall{{Name: "trino_query", Args: map[string]any{"sql": "SELECT 42.5"}}}},
			{FinalText: "FINAL ANSWER: {{last_result}}"},
		},
		"t-entity": {
			{FinalText: "FINAL ANSWER: memory.bench.orders"},
		},
	}
}

// writeExecSQLTasks writes two SQL-producing tasks: one the agent answers with
// a query equivalent to the reference, one with a divergent query.
func writeExecSQLTasks(t *testing.T, dir string) {
	t.Helper()
	tasks := []task.Task{
		{ID: "t-exec-ok", Suite: "s2", Prompt: "write sql", Arms: []string{"a0"}, BudgetToolCalls: 5,
			ExpectedSQL: "SELECT region, COUNT(*) FROM t GROUP BY region",
			Grading:     task.Grading{Kind: task.GradeExecSQL}},
		{ID: "t-exec-bad", Suite: "s2", Prompt: "write sql", Arms: []string{"a0"}, BudgetToolCalls: 5,
			ExpectedSQL: "SELECT tier, COUNT(*) FROM t GROUP BY tier",
			Grading:     task.Grading{Kind: task.GradeExecSQL}},
	}
	for _, tk := range tasks {
		raw, err := json.Marshal(tk)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, tk.ID+".yaml"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestExecSQLGrading drives the execution-result grader through the real
// pipeline: the agent's produced query is executed and its result set compared
// to the reference query's. The "ok" task answers with the reference query
// (equal rows -> correct); the "bad" task answers with a different query
// (different rows -> incorrect). Neither is a harness failure.
func TestExecSQLGrading(t *testing.T) {
	fp := newFakePlatform(t)
	tasksDir := t.TempDir()
	writeExecSQLTasks(t, tasksDir)
	script := llm.Script{
		"t-exec-ok":  {{FinalText: "FINAL ANSWER: SELECT region, COUNT(*) FROM t GROUP BY region"}},
		"t-exec-bad": {{FinalText: "FINAL ANSWER: SELECT status, COUNT(*) FROM t GROUP BY status"}},
	}
	res, err := Run(context.Background(), Options{
		Target:       target.Target{BaseURL: fp.httpSrv.URL, Credential: "test-key"},
		HTTPTimeout:  10 * time.Second,
		Arm:          "a0",
		K:            1,
		TasksDir:     tasksDir,
		Factory:      func(tk task.Task) (llm.Adapter, error) { return llm.NewScripted(script[tk.ID]), nil },
		LLMProvider:  "scripted",
		AuditTimeout: 5 * time.Second,
		IdentityKeys: 32,
		Log:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := map[string]bool{}
	for _, a := range res.Attempts {
		if a.Error != "" {
			t.Errorf("%s: harness error %q", a.TaskID, a.Error)
		}
		got[a.TaskID] = a.Correct
	}
	if !got["t-exec-ok"] {
		t.Error("t-exec-ok: equivalent query graded incorrect")
	}
	if got["t-exec-bad"] {
		t.Error("t-exec-bad: divergent query graded correct")
	}
}

// claudeNumericStream is a canned `claude -p` transcript answering the numeric
// task: mint a handle, run the query, answer 42.5.
const claudeNumericStream = `{"type":"system","subtype":"init","mcp_servers":[{"name":"bench","status":"connected"}]}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"i0","name":"mcp__bench__platform_info","input":{}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"i0","is_error":false,"content":"{\"session_id\":\"dps_cc_1\",\"version\":\"fake-1.0.0\"}"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"q1","name":"mcp__bench__trino_query","input":{"sql":"SELECT 42.5"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"q1","is_error":false,"content":"[{\"total_usd\": 42.5}]"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"FINAL ANSWER: 42.5"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"FINAL ANSWER: 42.5","session_id":"cc-1","usage":{"input_tokens":50,"output_tokens":10}}`

// TestClaudeCLIAttempt drives the claude-cli path end to end with a stubbed
// process: the runner returns a canned transcript (one successful data call),
// the harness correlates audit rows by the attempt's pool user_id, and grades
// the numeric answer. It proves the branch maps the client result, reads audit
// by identity (not handle), and produces a graded, non-harness-failed attempt.
func TestClaudeCLIAttempt(t *testing.T) {
	fp := newFakePlatform(t)
	// The stubbed client never touches the MCP server, so seed the audit row the
	// real client's successful trino_query would have produced, under the dps_
	// handle the canned stream's platform_info result carries.
	fp.record(auditapi.Event{
		Timestamp: time.Now().UTC(), DurationMS: 9, SessionID: "dps_cc_1",
		ToolName: "trino_query", Success: true, EventKind: "mcp_tool_call",
	})

	tasksDir := t.TempDir()
	writeTaskFiles(t, tasksDir) // t-numeric (s3) + t-entity (s1)

	disallowed, err := claudecli.DisallowTools("ToolSearch")
	if err != nil {
		t.Fatalf("DisallowTools: %v", err)
	}
	runner, err := claudecli.New(claudecli.Options{
		Model:           "claude-sonnet-5",
		DisallowedTools: disallowed,
		Exec: func(context.Context, claudecli.CommandSpec) ([]byte, []byte, error) {
			return []byte(claudeNumericStream), nil, nil
		},
	})
	if err != nil {
		t.Fatalf("claudecli.New: %v", err)
	}

	res, err := Run(context.Background(), Options{
		Target:        target.Target{BaseURL: fp.httpSrv.URL, Credential: "test-key"},
		HTTPTimeout:   10 * time.Second,
		Arm:           "a0",
		Suite:         "s3", // only the numeric task, so seq=1 -> bench-agent-001
		K:             1,
		TasksDir:      tasksDir,
		ClaudeCLI:     runner,
		ClientVersion: "2.1.208 (Claude Code)",
		LLMProvider:   "claude-cli",
		AuditTimeout:  5 * time.Second,
		IdentityKeys:  32,
		Log:           slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Manifest.ClientVersion != "2.1.208 (Claude Code)" {
		t.Errorf("ClientVersion = %q", res.Manifest.ClientVersion)
	}
	// The archive has to say what tool surface the arm ran under, and it must
	// come off the runner: an arm whose transcript shows a tool the manifest
	// claims was forbidden is a reproducibility failure the archive must be
	// able to expose.
	if !slices.Equal(res.Manifest.DisallowedTools, runner.DisallowedTools()) {
		t.Errorf("DisallowedTools = %v, want the runner's effective list %v",
			res.Manifest.DisallowedTools, runner.DisallowedTools())
	}
	if !slices.Contains(res.Manifest.DisallowedTools, "ToolSearch") {
		t.Errorf("the arm's added tool is missing from the manifest: %v", res.Manifest.DisallowedTools)
	}
	if res.Manifest.Model != "claude-sonnet-5" {
		t.Errorf("Model = %q", res.Manifest.Model)
	}
	if res.Manifest.PlatformVersion != "fake-1.0.0" {
		t.Errorf("PlatformVersion = %q, want parsed from platform_info", res.Manifest.PlatformVersion)
	}
	if len(res.Attempts) != 1 {
		t.Fatalf("got %d attempts, want 1", len(res.Attempts))
	}
	a := res.Attempts[0]
	if a.Error != "" {
		t.Fatalf("harness error: %s", a.Error)
	}
	if !a.Correct {
		t.Errorf("numeric attempt graded incorrect (final %q)", a.FinalAnswer)
	}
	if a.SessionID != "dps_cc_1" {
		t.Errorf("SessionID = %q, want parsed dps handle", a.SessionID)
	}
	if a.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1 (platform_info excluded)", a.ToolCalls)
	}
	// Audit correlated by user_id, platform_info excluded.
	if a.Audit.AuditedCalls != 1 {
		t.Errorf("Audit.AuditedCalls = %d, want 1", a.Audit.AuditedCalls)
	}
}

// TestClaudeCLIServerNotConnected records a harness failure (not a wrong
// answer) when the bench MCP server never connected, so a misconfigured target
// is surfaced loudly rather than scored as a miss.
func TestClaudeCLIServerNotConnected(t *testing.T) {
	fp := newFakePlatform(t)
	tasksDir := t.TempDir()
	writeTaskFiles(t, tasksDir)
	stream := `{"type":"system","subtype":"init","mcp_servers":[{"name":"bench","status":"failed"}]}
{"type":"result","subtype":"success","is_error":false,"result":"FINAL ANSWER: 42.5","usage":{"input_tokens":1,"output_tokens":1}}`
	runner, err := claudecli.New(claudecli.Options{
		Model: "sonnet",
		Exec: func(context.Context, claudecli.CommandSpec) ([]byte, []byte, error) {
			return []byte(stream), nil, nil
		},
	})
	if err != nil {
		t.Fatalf("claudecli.New: %v", err)
	}
	res, runErr := Run(context.Background(), Options{
		Target:       target.Target{BaseURL: fp.httpSrv.URL, Credential: "test-key"},
		HTTPTimeout:  10 * time.Second,
		Arm:          "a0",
		Suite:        "s3",
		K:            1,
		TasksDir:     tasksDir,
		ClaudeCLI:    runner,
		LLMProvider:  "claude-cli",
		AuditTimeout: 2 * time.Second,
		IdentityKeys: 32,
		Log:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if runErr == nil {
		t.Fatal("expected a harness-level failure to surface in the returned error")
	}
	if len(res.Attempts) != 1 || res.Attempts[0].Error == "" ||
		!strings.Contains(res.Attempts[0].Error, "did not connect") {
		t.Fatalf("want server-not-connected harness error, got %+v", res.Attempts)
	}
}

// TestClaudeCLISuccessfulCallsButNoHandle surfaces a harness inconsistency when
// the client reports successful tool calls but no dps_ handle to correlate them
// (there is nothing to read audit back against, so it must fail loudly rather
// than silently report zero-audit metrics).
func TestClaudeCLISuccessfulCallsButNoHandle(t *testing.T) {
	fp := newFakePlatform(t)
	tasksDir := t.TempDir()
	writeTaskFiles(t, tasksDir)
	// A successful trino_query but platform_info never returned a handle.
	stream := `{"type":"system","subtype":"init","mcp_servers":[{"name":"bench","status":"connected"}]}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"q1","name":"mcp__bench__trino_query","input":{}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"q1","is_error":false,"content":"42"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"FINAL ANSWER: 42.5","usage":{"input_tokens":1,"output_tokens":1}}`
	runner, err := claudecli.New(claudecli.Options{
		Model: "sonnet",
		Exec: func(context.Context, claudecli.CommandSpec) ([]byte, []byte, error) {
			return []byte(stream), nil, nil
		},
	})
	if err != nil {
		t.Fatalf("claudecli.New: %v", err)
	}
	res, runErr := Run(context.Background(), Options{
		Target:       target.Target{BaseURL: fp.httpSrv.URL, Credential: "test-key"},
		HTTPTimeout:  10 * time.Second,
		Arm:          "a0",
		Suite:        "s3",
		K:            1,
		TasksDir:     tasksDir,
		ClaudeCLI:    runner,
		LLMProvider:  "claude-cli",
		AuditTimeout: 2 * time.Second,
		IdentityKeys: 32,
		Log:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if runErr == nil {
		t.Fatal("expected a harness failure")
	}
	if len(res.Attempts) != 1 || !strings.Contains(res.Attempts[0].Error, "no dps_ handle") {
		t.Fatalf("want no-handle harness error, got %+v", res.Attempts)
	}
}

// TestCheckpointFlushesEachAttempt verifies the results are aggregated and
// handed to OnAttempt after every attempt, so an interruption never discards
// completed, paid-for work.
func TestCheckpointFlushesEachAttempt(t *testing.T) {
	fp := newFakePlatform(t)
	tasksDir := t.TempDir()
	writeTaskFiles(t, tasksDir) // two tasks
	script := testScript()
	var snapshots []int
	_, err := Run(context.Background(), Options{
		Target:       target.Target{BaseURL: fp.httpSrv.URL, Credential: "test-key"},
		HTTPTimeout:  10 * time.Second,
		Arm:          "a0",
		K:            1,
		TasksDir:     tasksDir,
		Factory:      func(tk task.Task) (llm.Adapter, error) { return llm.NewScripted(script[tk.ID]), nil },
		LLMProvider:  "scripted",
		AuditTimeout: 5 * time.Second,
		IdentityKeys: 32,
		OnAttempt:    func(r *report.Results) { snapshots = append(snapshots, len(r.Attempts)) },
		Log:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(snapshots) != 2 || snapshots[0] != 1 || snapshots[1] != 2 {
		t.Fatalf("checkpoints = %v, want [1 2]", snapshots)
	}
}

// TestRunRefusesUndersizedIdentityPool verifies the run fails before spending
// any session when attempts would share a discovery scope.
func TestRunRefusesUndersizedIdentityPool(t *testing.T) {
	fp := newFakePlatform(t)
	tasksDir := t.TempDir()
	writeTaskFiles(t, tasksDir)
	_, err := Run(context.Background(), Options{
		Target:       target.Target{BaseURL: fp.httpSrv.URL, Credential: "test-key"},
		HTTPTimeout:  time.Second,
		Arm:          "a0",
		K:            2,
		TasksDir:     tasksDir,
		Factory:      func(task.Task) (llm.Adapter, error) { return llm.NewScripted(nil), nil },
		LLMProvider:  "scripted",
		AuditTimeout: time.Second,
		IdentityKeys: 3, // 2 tasks x k=2 = 4 attempts > 3 keys
		Log:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err == nil || !strings.Contains(err.Error(), "identity pool") {
		t.Fatalf("want identity-pool error, got %v", err)
	}
	if fp.minted.Load() != 0 {
		t.Errorf("sessions were minted despite the pool refusal")
	}
}

// TestRunEndToEnd wires the real pipeline against the fake platform.
func TestRunEndToEnd(t *testing.T) {
	fp := newFakePlatform(t)
	tasksDir := t.TempDir()
	writeTaskFiles(t, tasksDir)
	outDir := t.TempDir()
	script := testScript()

	res, err := Run(context.Background(), Options{
		Target:        target.Target{BaseURL: fp.httpSrv.URL, Credential: "test-key"},
		HTTPTimeout:   10 * time.Second,
		Arm:           "a0",
		K:             2,
		TasksDir:      tasksDir,
		TranscriptDir: filepath.Join(outDir, "transcripts"),
		Factory: func(tk task.Task) (llm.Adapter, error) {
			return llm.NewScripted(script[tk.ID]), nil
		},
		LLMProvider:  "scripted",
		GitCommit:    "deadbeef",
		AuditTimeout: 5 * time.Second,
		IdentityKeys: 32,
		Log:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Every attempt must authenticate as its own pool identity (the
	// search-first gate keys discovery on the user, so shared credentials
	// would contaminate attempts).
	fp.mu.Lock()
	identities := len(fp.identities)
	wantRotated := map[bool]bool{}
	for id := range fp.identities {
		wantRotated[strings.HasPrefix(id, "Bearer test-key-")] = true
	}
	fp.mu.Unlock()
	if identities != 4 { // 2 tasks x k=2
		t.Errorf("distinct MCP identities = %d, want 4", identities)
	}
	if !wantRotated[true] || wantRotated[false] {
		t.Errorf("identities not drawn from the pool: %v", fp.identities)
	}

	if got := len(res.Attempts); got != 4 { // 2 tasks x k=2
		t.Fatalf("attempts = %d, want 4", got)
	}
	for _, a := range res.Attempts {
		if a.Error != "" {
			t.Errorf("%s attempt %d: harness error %q", a.TaskID, a.Attempt, a.Error)
		}
		if !a.Correct {
			t.Errorf("%s attempt %d: graded incorrect (final %q)", a.TaskID, a.Attempt, a.FinalAnswer)
		}
		if !strings.HasPrefix(a.SessionID, "dps_fake_") {
			t.Errorf("%s attempt %d: session id %q not minted", a.TaskID, a.Attempt, a.SessionID)
		}
		if a.TranscriptPath == "" {
			t.Errorf("%s attempt %d: no transcript written", a.TaskID, a.Attempt)
		}
	}
	verifyNumericAttempt(t, res)
	verifyManifestAndAggregates(t, res)
}

// verifyNumericAttempt checks the refusal-vs-audit accounting: the denied call
// executed (2 tool calls, 1 error) but only trino_query is audited, carrying
// the enrichment volume.
func verifyNumericAttempt(t *testing.T, res *report.Results) {
	t.Helper()
	for _, a := range res.Attempts {
		if a.TaskID != "t-numeric" {
			continue
		}
		if a.ToolCalls != 2 || a.ToolErrors != 1 {
			t.Errorf("t-numeric: tool_calls=%d errors=%d, want 2/1", a.ToolCalls, a.ToolErrors)
		}
		if a.Audit.AuditedCalls != 1 || a.Audit.EnrichmentTokensDedup != 42 {
			t.Errorf("t-numeric: audit=%+v, want 1 audited call with 42 dedup tokens", a.Audit)
		}
		if a.GotValue == nil || *a.GotValue != 42.5 {
			t.Errorf("t-numeric: got value %v, want 42.5", a.GotValue)
		}
	}
}

// verifyManifestAndAggregates checks manifest capture and roll-ups.
func verifyManifestAndAggregates(t *testing.T, res *report.Results) {
	t.Helper()
	m := res.Manifest
	if m.PlatformVersion != "fake-1.0.0" || m.Model != "scripted" || m.GitCommit != "deadbeef" || m.TaskSetHash == "" {
		t.Errorf("manifest incomplete: %+v", m)
	}
	if len(res.Tasks) != 2 || len(res.Suites) != 2 {
		t.Fatalf("aggregates: %d tasks, %d suites, want 2/2", len(res.Tasks), len(res.Suites))
	}
	for _, s := range res.Suites {
		if s.Accuracy != 1.0 || s.PassKRate != 1.0 {
			t.Errorf("suite %s: accuracy=%.2f passk=%.2f, want 1.0/1.0", s.Suite, s.Accuracy, s.PassKRate)
		}
	}
	if sum := res.HumanSummary(); !strings.Contains(sum, "arm=a0") || !strings.Contains(sum, "t-numeric") {
		t.Errorf("human summary missing expected content:\n%s", sum)
	}
}
