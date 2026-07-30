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

// claudeCachedStream is a claude-cli recall transcript whose terminal result
// event reports cache tokens, so the end-to-end cache-token flow (parser ->
// Result.Usage -> EpisodeRecord) can be asserted through the real parser.
const claudeCachedStream = `{"type":"system","subtype":"init","mcp_servers":[{"name":"bench","status":"connected"}]}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"i0","name":"mcp__bench__platform_info","input":{}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"i0","is_error":false,"content":"{\"session_id\":\"dps_cc_9\",\"version\":\"fake-1.0.0\"}"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"FINAL ANSWER: 500"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"FINAL ANSWER: 500","session_id":"cc-9","usage":{"input_tokens":40,"output_tokens":9,"cache_read_input_tokens":1200,"cache_creation_input_tokens":80}}`

// TestClaudeCLIEpisodeRecordsCacheTokens verifies #966 end to end: a cached
// claude-cli run's cache tokens flow through the real parser into the lifecycle
// EpisodeRecord, so a cached run self-reports its true cost basis (cache reads
// bill far below fresh input) rather than being estimated from input/output
// totals. That a real `claude` process emits these fields is confirmed by the
// cold-start runs, which are claude-cli and record non-zero cache tokens; what
// remains covered only here is the lifecycle record's own field mapping, and
// the runner warns loudly on zero usage so a silent field move cannot pass.
func TestClaudeCLIEpisodeRecordsCacheTokens(t *testing.T) {
	fp := newFakePlatform(t)
	runner, err := claudecli.New(claudecli.Options{
		Model: "claude-sonnet-5",
		Exec: func(context.Context, claudecli.CommandSpec) ([]byte, []byte, error) {
			return []byte(claudeCachedStream), nil, nil
		},
	})
	if err != nil {
		t.Fatalf("claudecli.New: %v", err)
	}
	tgt := target.Target{BaseURL: fp.httpSrv.URL, Credential: fp.base}
	env := &runEnv{
		opts:  Options{Target: tgt, HTTPTimeout: 10 * time.Second, Arm: "a3", ClaudeCLI: runner, IdentityKeys: 32, AuditTimeout: time.Second},
		log:   slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		audit: auditapi.New(fp.httpSrv.URL, tgt.HTTPClient(10*time.Second)),
	}
	env.currentProtocolID = "p-cache"

	rec, _ := env.runClaudeCLIEpisode(context.Background(), episodeSpec{
		stage: StageRecall, identity: "teacher", seq: 1, prompt: "q", system: "s", budget: 5,
	})
	if rec.Error != "" {
		t.Fatalf("episode error: %s", rec.Error)
	}
	if rec.CacheReadTokens != 1200 || rec.CacheCreationTokens != 80 {
		t.Fatalf("EpisodeRecord cache tokens = read %d write %d, want 1200/80", rec.CacheReadTokens, rec.CacheCreationTokens)
	}
	// The aggregate must sum the per-episode cache split so a run self-reports its
	// cached cost basis.
	res := &Results{Runs: []ProtocolRun{{Episodes: []EpisodeRecord{rec}}}}
	res.Aggregate()
	if res.Metrics.TotalCacheReadTokens != 1200 || res.Metrics.TotalCacheCreationTokens != 80 {
		t.Fatalf("aggregate cache totals = read %d write %d, want 1200/80",
			res.Metrics.TotalCacheReadTokens, res.Metrics.TotalCacheCreationTokens)
	}
}

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

// claudeNoHandleRecallStream drives a successful bench call but never mints a
// dps_ handle via platform_info.
const claudeNoHandleRecallStream = `{"type":"system","subtype":"init","mcp_servers":[{"name":"bench","status":"connected"}]}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"s1","name":"mcp__bench__search","input":{"query":"threshold"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"s1","is_error":false,"content":"saved: threshold is 500"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"FINAL ANSWER: 500"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"FINAL ANSWER: 500","session_id":"cc-2","usage":{"input_tokens":30,"output_tokens":8}}`

// TestLifecycleClaudeCLIEpisodeNoHandleWithSuccesses proves a lifecycle
// claude-cli episode reporting successful tool calls but no dps_ handle is a
// harness error, not a silently zeroed audit record: the session gate refuses
// un-threaded calls, so the combination is an impossibility to surface
// (mirrors the S1-S3 pipeline and cold-start contracts).
func TestLifecycleClaudeCLIEpisodeNoHandleWithSuccesses(t *testing.T) {
	fp := newFakePlatform(t)
	runner, err := claudecli.New(claudecli.Options{
		Model: "claude-sonnet-5",
		Exec: func(context.Context, claudecli.CommandSpec) ([]byte, []byte, error) {
			return []byte(claudeNoHandleRecallStream), nil, nil
		},
	})
	if err != nil {
		t.Fatalf("claudecli.New: %v", err)
	}
	tgt := target.Target{BaseURL: fp.httpSrv.URL, Credential: fp.base}
	env := &runEnv{
		opts:  Options{Target: tgt, HTTPTimeout: 10 * time.Second, Arm: "a3", ClaudeCLI: runner, IdentityKeys: 32, AuditTimeout: time.Second},
		log:   slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		audit: auditapi.New(fp.httpSrv.URL, tgt.HTTPClient(10*time.Second)),
	}
	env.currentProtocolID = "p-nohandle"
	rec, _ := env.runClaudeCLIEpisode(context.Background(), episodeSpec{stage: StageRecall, identity: "teacher", seq: 3, prompt: "q"})
	if rec.Error == "" || !strings.Contains(rec.Error, "no dps_ handle") {
		t.Fatalf("episode error = %q, want a no-handle harness error", rec.Error)
	}
}

// TestLifecycleClaudeCLIEpisodeAuditReadFailureRecorded proves a failed audit
// read-back lands on the episode record instead of silently zeroing the audit
// metrics, and is totaled as a summary warning: the fake has no audit rows for
// the threaded handle, so WaitForSession times out.
func TestLifecycleClaudeCLIEpisodeAuditReadFailureRecorded(t *testing.T) {
	fp := newFakePlatform(t) // no audit events for dps_cc_1
	runner, err := claudecli.New(claudecli.Options{
		Model: "claude-sonnet-5",
		Exec: func(context.Context, claudecli.CommandSpec) ([]byte, []byte, error) {
			return []byte(claudeRecallStream), nil, nil
		},
	})
	if err != nil {
		t.Fatalf("claudecli.New: %v", err)
	}
	tgt := target.Target{BaseURL: fp.httpSrv.URL, Credential: fp.base}
	env := &runEnv{
		opts:  Options{Target: tgt, HTTPTimeout: 10 * time.Second, Arm: "a3", ClaudeCLI: runner, IdentityKeys: 32, AuditTimeout: 200 * time.Millisecond},
		log:   slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		audit: auditapi.New(fp.httpSrv.URL, tgt.HTTPClient(10*time.Second)),
	}
	env.currentProtocolID = "p-auditloss"
	rec, _ := env.runClaudeCLIEpisode(context.Background(), episodeSpec{stage: StageRecall, identity: "teacher", seq: 3, prompt: "q"})
	if rec.Error != "" {
		t.Fatalf("episode error = %q, want graded episode (audit loss is not a harness failure)", rec.Error)
	}
	if rec.AuditReadError == "" {
		t.Fatal("AuditReadError empty: a lost audit read-back must be recorded, not silently zeroed")
	}
	res := &Results{Runs: []ProtocolRun{{Episodes: []EpisodeRecord{rec}}}}
	res.Aggregate()
	if res.Metrics.AuditReadFailures != 1 {
		t.Fatalf("AuditReadFailures = %d, want 1", res.Metrics.AuditReadFailures)
	}
	if !strings.Contains(res.HumanSummary(), "lost their audit read-back") {
		t.Error("summary missing the audit-read-back warning")
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
