package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/agent"
	"github.com/txn2/mcp-data-platform/bench/internal/auditapi"
	"github.com/txn2/mcp-data-platform/bench/internal/claudecli"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/mcpc"
	"github.com/txn2/mcp-data-platform/bench/internal/pool"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// searchToolName is the discovery tool whose invocation marks that the agent
// surfaced saved knowledge itself (unprompted surfacing).
const searchToolName = "search"

// baseScaffold is the fixed prompt scaffold for lifecycle episodes. S5 runs only
// on the a3 arm, so this is not an ablation variable; it establishes that the
// agent works across sessions and should save and recall knowledge.
const baseScaffold = `You are a data analyst agent connected to a data platform over MCP. You work across many separate sessions and can save knowledge for later and recall it.

Rules:
- Ground every answer in tool results or in knowledge saved in an earlier session; do not answer from prior knowledge about any specific dataset.
- Use the search tool to discover saved knowledge, curated context, and data before querying.`

// Per-grading-kind answer format rules, matching the deterministic graders'
// parsing convention (shared with the S1-S3 pipeline).
const (
	numericFormat = `- The FINAL ANSWER line must contain exactly one number (USD unless the question states otherwise), for example "FINAL ANSWER: 12345.67".`
	entityFormat  = `- The FINAL ANSWER line must name the single best answer: a fully qualified table name (catalog.schema.table) for dataset questions, or the exact name requested.`
)

// teachSystem instructs the agent to capture the stated fact and link it to the
// dataset it concerns, so the runner can verify capture and entity linkage.
const teachSystem = baseScaffold + `
- THIS SESSION IS FOR RECORDING KNOWLEDGE. Save the fact stated below using the memory tools so a future session can recall it, and link it to the dataset it concerns. Then confirm in one line what you saved.`

// abstainSystem forbids guessing so a never-taught fact is abstained, not
// fabricated.
const abstainSystem = baseScaffold + `
- If the tools and any saved knowledge do not contain what is needed to answer, do NOT guess, estimate, or infer a value. Answer with exactly: "FINAL ANSWER: INSUFFICIENT INFORMATION".`

// recallSystem builds the system prompt for a question episode, appending the
// grader's format rule.
func recallSystem(kind string) string {
	format := entityFormat
	if kind == task.GradeNumeric {
		format = numericFormat
	}
	return baseScaffold + "\n- When you have the answer, end your reply with a single line: \"FINAL ANSWER: <answer>\".\n" + format
}

// poolEmail returns the captured_by email for a pool identity sequence number.
func poolEmail(seq int) string {
	return pool.Email(seq)
}

// episodeSpec is one session's parameters.
type episodeSpec struct {
	stage    string
	identity string // "teacher" or "learner"
	seq      int    // pool identity sequence number
	prompt   string
	system   string
	budget   int
}

// runEpisode drives one fresh MCP session end to end: authenticate as the pool
// identity, mint the handle, run the agent loop against the a3 tool surface,
// read the audit trail back (best effort — S5 correctness comes from the
// knowledge API, not audit), and return the record plus the raw final answer.
// A harness failure lands in the record's Error; a graded outcome does not.
func (e *runEnv) runEpisode(ctx context.Context, spec episodeSpec) (EpisodeRecord, string) {
	if e.opts.ClaudeCLI != nil {
		return e.runClaudeCLIEpisode(ctx, spec)
	}
	rec := EpisodeRecord{Stage: spec.stage, Identity: spec.identity, Email: poolEmail(spec.seq)}
	client := e.attemptClient(spec.seq)

	session, err := client.Connect(ctx)
	if err != nil {
		rec.Error = fmt.Sprintf("connect: %v", err)
		return rec, ""
	}
	defer func() { _ = session.Close() }()

	info, err := mcpc.Mint(ctx, session)
	if err != nil {
		rec.Error = fmt.Sprintf("mint session handle: %v", err)
		return rec, ""
	}
	rec.SessionID = info.Handle
	e.recordPlatformVersion(info.PlatformVersion)

	tools, err := mcpc.ListTools(ctx, session)
	if err != nil {
		rec.Error = fmt.Sprintf("list tools: %v", err)
		return rec, ""
	}

	adapter, err := e.opts.Factory(e.currentProtocolID, spec.stage)
	if err != nil {
		rec.Error = fmt.Sprintf("build adapter: %v", err)
		return rec, ""
	}
	e.recordModel(adapter.Model())

	audited, indeterminate := 0, 0
	exec := func(ctx context.Context, name string, args map[string]any) llm.ToolResult {
		if name == searchToolName {
			rec.SearchCalled = true
		}
		r := mcpc.Call(ctx, session, name, args, info.Handle)
		if r.TransportErr != nil {
			indeterminate++
			return llm.ToolResult{Text: "transport error: " + r.TransportErr.Error(), IsError: true}
		}
		if !preAuditRefusal(r.ErrorCode) {
			audited++
		}
		return llm.ToolResult{Text: r.Text, IsError: r.ToolErr}
	}

	start := time.Now()
	result, runErr := agent.Run(ctx, adapter, agent.Config{
		System: spec.system,
		Prompt: spec.prompt,
		Tools:  tools,
		Budget: spec.budget,
	}, exec)
	rec.WallMS = time.Since(start).Milliseconds()
	rec.ToolCalls = result.ToolCalls
	rec.ToolErrors = result.ToolErrors
	rec.InputTokens = result.Usage.InputTokens
	rec.OutputTokens = result.Usage.OutputTokens
	final := result.FinalAnswer
	rec.FinalAnswer = final
	e.writeTranscript(spec, result)
	if runErr != nil {
		rec.Error = fmt.Sprintf("agent loop: %v", runErr)
		return rec, final
	}
	rec.Audit = e.readAudit(ctx, info.Handle, audited, audited+indeterminate)
	return rec, final
}

// runClaudeCLIEpisode drives one lifecycle episode through a real `claude -p`
// client. Claude Code authenticates as the episode's pool identity, mints and
// threads its own handle, and drives the tools; the harness reconstructs the
// transcript, reads audit back best effort by the identity's user_id, and
// returns the record plus the raw final answer. Lifecycle correctness is
// verified through the knowledge API (keyed on the identity's email), which is
// independent of how the episode was driven.
func (e *runEnv) runClaudeCLIEpisode(ctx context.Context, spec episodeSpec) (EpisodeRecord, string) {
	rec := EpisodeRecord{Stage: spec.stage, Identity: spec.identity, Email: poolEmail(spec.seq)}
	e.recordModel(e.opts.ClaudeCLI.Model())

	start := time.Now()
	cres, err := e.opts.ClaudeCLI.Run(ctx, claudecli.Request{
		Endpoint:   e.opts.Target.BaseURL,
		Credential: pool.Credential(e.opts.Target.Credential, spec.seq, e.opts.IdentityKeys),
		System:     spec.system,
		Prompt:     spec.prompt,
	})
	rec.WallMS = time.Since(start).Milliseconds()
	if err != nil {
		rec.Error = fmt.Sprintf("claude-cli: %v", err)
		return rec, ""
	}
	rec.SessionID = cres.Handle
	rec.ToolCalls = cres.MCPCalls
	rec.ToolErrors = cres.ToolErrors
	rec.SearchCalled = cres.SearchCalled
	rec.InputTokens = cres.Usage.InputTokens
	rec.OutputTokens = cres.Usage.OutputTokens
	rec.FinalAnswer = cres.FinalText
	e.recordPlatformVersion(cres.PlatformVersion)
	e.writeClaudeTranscript(spec, cres.Transcript)

	if cres.IsError {
		rec.Error = fmt.Sprintf("claude-cli result error (subtype %q): %.300s", cres.Subtype, cres.FinalText)
		return rec, cres.FinalText
	}
	if !cres.ServerConnected {
		rec.Error = fmt.Sprintf("bench MCP server did not connect (status %q)", cres.ServerStatus)
		return rec, cres.FinalText
	}
	// Correlate by the dps_ handle claude threaded (each episode mints its own),
	// so consecutive same-identity stages never fold into one another. Best
	// effort like the loop path: S5 correctness comes from the knowledge API.
	if cres.Handle != "" {
		rec.Audit = e.readAudit(ctx, cres.Handle, cres.SuccessfulMCPCalls, cres.MCPCalls)
	}
	return rec, cres.FinalText
}

// readAudit reads the session's audit trail back best effort. Unlike the S1-S3
// pipeline, a missing audit row does not fail an S5 run: the lifecycle state is
// verified through the knowledge API, and audit here only enriches the
// efficiency picture. A read failure yields zero metrics and is logged.
func (e *runEnv) readAudit(ctx context.Context, handle string, minAudited, maxAudited int) auditapi.Metrics {
	events, err := e.audit.WaitForSession(ctx, handle, minAudited, maxAudited, e.opts.AuditTimeout)
	if err != nil {
		e.log.Warn("lifecycle audit read-back", "handle", handle, "error", err)
		return auditapi.Metrics{}
	}
	return auditapi.Summarize(events)
}

// writeClaudeTranscript persists a claude-cli episode's reconstructed transcript
// for manual audit, reusing the loop path's file layout.
func (e *runEnv) writeClaudeTranscript(spec episodeSpec, msgs []llm.Message) {
	if e.opts.TranscriptDir == "" {
		return
	}
	if err := os.MkdirAll(e.opts.TranscriptDir, 0o750); err != nil {
		e.log.Warn("transcript dir", "error", err)
		return
	}
	path := filepath.Join(e.opts.TranscriptDir,
		fmt.Sprintf("%s-%s-%s.json", e.currentProtocolID, spec.stage, poolEmail(spec.seq)))
	payload := lifecycleTranscript{
		ProtocolID: e.currentProtocolID, Stage: spec.stage, Identity: spec.identity,
		Email: poolEmail(spec.seq), Transcript: msgs,
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		e.log.Warn("marshal transcript", "error", err)
		return
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		e.log.Warn("write transcript", "error", err)
	}
}

// preAuditRefusal reports whether a structured error code marks a platform
// refusal issued outer to the audit middleware (so it leaves no audit row).
// Mirrors the S1-S3 pipeline's classification.
func preAuditRefusal(code string) bool {
	switch code {
	case "unauthenticated", "unauthorized", "session_required", "session_expired",
		"search_required", "setup_required", "rate_limited":
		return true
	}
	return false
}
