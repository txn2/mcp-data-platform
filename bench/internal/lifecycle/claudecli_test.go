package lifecycle

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/auditapi"
	"github.com/txn2/mcp-data-platform/bench/internal/claudecli"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

// claudeRecallStream is a canned `claude -p` transcript for a recall episode:
// mint a handle, call search (surfacing saved knowledge), then answer.
const claudeRecallStream = `{"type":"system","subtype":"init","mcp_servers":[{"name":"bench","status":"connected"}]}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"i0","name":"mcp__bench__platform_info","input":{}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"i0","is_error":false,"content":"{\"session_id\":\"dps_cc_1\",\"version\":\"fake-1.0.0\"}"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"s1","name":"mcp__bench__search","input":{"query":"threshold"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"s1","is_error":false,"content":"saved: threshold is 500"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"FINAL ANSWER: 500"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"FINAL ANSWER: 500","session_id":"cc-1","usage":{"input_tokens":30,"output_tokens":8}}`

// TestClaudeCLIEpisode drives the lifecycle claude-cli episode path directly:
// the stubbed client returns a recall transcript that calls search, and the
// harness maps the client result, reads audit best effort by the pool
// identity's user_id, and records that search surfaced knowledge. Lifecycle
// correctness itself is verified through the knowledge API elsewhere; this
// isolates the claude-cli episode mapping.
func TestClaudeCLIEpisode(t *testing.T) {
	fp := newFakePlatform(t)
	// Seed the audit row the real search call would have produced under the dps_
	// handle the canned stream's platform_info result carries.
	fp.events = append(fp.events,
		auditapi.Event{DurationMS: 5, SessionID: "dps_cc_1", ToolName: "search", Success: true},
	)

	runner, err := claudecli.New(claudecli.Options{
		Model: "claude-sonnet-5",
		Exec: func(context.Context, claudecli.CommandSpec) ([]byte, []byte, error) {
			return []byte(claudeRecallStream), nil, nil
		},
	})
	if err != nil {
		t.Fatalf("claudecli.New: %v", err)
	}

	transcriptDir := t.TempDir()
	tgt := target.Target{BaseURL: fp.httpSrv.URL, Credential: fp.base}
	env := &runEnv{
		opts: Options{
			Target:        tgt,
			HTTPTimeout:   10 * time.Second,
			Arm:           "a3",
			ClaudeCLI:     runner,
			IdentityKeys:  32,
			AuditTimeout:  5 * time.Second,
			TranscriptDir: transcriptDir,
		},
		log:   slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		audit: auditapi.New(fp.httpSrv.URL, tgt.HTTPClient(10*time.Second)),
	}
	env.currentProtocolID = "p-test"

	rec, ans := env.runClaudeCLIEpisode(context.Background(), episodeSpec{
		stage: StageRecall, identity: "teacher", seq: 1,
		prompt: "what is the threshold?", system: "answer", budget: 5,
	})
	if rec.Error != "" {
		t.Fatalf("episode error: %s", rec.Error)
	}
	if ans != "FINAL ANSWER: 500" {
		t.Errorf("answer = %q", ans)
	}
	if !rec.SearchCalled {
		t.Error("SearchCalled = false, want true (search surfaced knowledge)")
	}
	if rec.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1 (search; platform_info excluded)", rec.ToolCalls)
	}
	if rec.SessionID != "dps_cc_1" {
		t.Errorf("SessionID = %q, want parsed dps handle", rec.SessionID)
	}
	if rec.Audit.AuditedCalls != 1 {
		t.Errorf("Audit.AuditedCalls = %d, want 1 (platform_info excluded)", rec.Audit.AuditedCalls)
	}
	if env.model != "claude-sonnet-5" {
		t.Errorf("recorded model = %q", env.model)
	}
	if env.platformVersion != "fake-1.0.0" {
		t.Errorf("recorded platform version = %q", env.platformVersion)
	}
	// The transcript file must have been written for manual review.
	entries, _ := os.ReadDir(transcriptDir)
	if len(entries) != 1 {
		t.Errorf("transcript files = %d, want 1", len(entries))
	}
}

// TestClaudeCLIEpisodeServerNotConnected records a harness failure when the
// bench MCP server never connects (claude reached no platform tools).
func TestClaudeCLIEpisodeServerNotConnected(t *testing.T) {
	fp := newFakePlatform(t)
	stream := `{"type":"system","subtype":"init","mcp_servers":[{"name":"bench","status":"failed"}]}
{"type":"result","subtype":"success","is_error":false,"result":"x","usage":{"input_tokens":1,"output_tokens":1}}`
	runner, err := claudecli.New(claudecli.Options{
		Model: "sonnet",
		Exec: func(context.Context, claudecli.CommandSpec) ([]byte, []byte, error) {
			return []byte(stream), nil, nil
		},
	})
	if err != nil {
		t.Fatalf("claudecli.New: %v", err)
	}
	tgt := target.Target{BaseURL: fp.httpSrv.URL, Credential: fp.base}
	env := &runEnv{
		opts:  Options{Target: tgt, HTTPTimeout: 5 * time.Second, ClaudeCLI: runner, IdentityKeys: 32, AuditTimeout: time.Second},
		log:   slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		audit: auditapi.New(fp.httpSrv.URL, tgt.HTTPClient(5*time.Second)),
	}
	env.currentProtocolID = "p-test"
	rec, _ := env.runClaudeCLIEpisode(context.Background(), episodeSpec{stage: StageRecall, seq: 1, prompt: "q", system: "s", budget: 5})
	if rec.Error == "" || !strings.Contains(rec.Error, "did not connect") {
		t.Fatalf("want server-not-connected error, got %q", rec.Error)
	}
}

// TestClaudeCLIEpisodeRunError surfaces a runner error (e.g. a truncated stream
// with no result event) as a harness failure.
func TestClaudeCLIEpisodeRunError(t *testing.T) {
	fp := newFakePlatform(t)
	runner, err := claudecli.New(claudecli.Options{
		Model: "sonnet",
		Exec: func(context.Context, claudecli.CommandSpec) ([]byte, []byte, error) {
			return []byte("truncated, no result event"), []byte("stderr detail"), context.DeadlineExceeded
		},
	})
	if err != nil {
		t.Fatalf("claudecli.New: %v", err)
	}
	tgt := target.Target{BaseURL: fp.httpSrv.URL, Credential: fp.base}
	env := &runEnv{
		opts:  Options{Target: tgt, HTTPTimeout: 5 * time.Second, ClaudeCLI: runner, IdentityKeys: 32, AuditTimeout: time.Second},
		log:   slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		audit: auditapi.New(fp.httpSrv.URL, tgt.HTTPClient(5*time.Second)),
	}
	env.currentProtocolID = "p-test"
	rec, _ := env.runClaudeCLIEpisode(context.Background(), episodeSpec{stage: StageRecall, seq: 1, prompt: "q", system: "s", budget: 5})
	if rec.Error == "" || !strings.Contains(rec.Error, "claude-cli") {
		t.Fatalf("want claude-cli error, got %q", rec.Error)
	}
}
