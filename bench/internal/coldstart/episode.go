package coldstart

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/agent"
	"github.com/txn2/mcp-data-platform/bench/internal/auditapi"
	"github.com/txn2/mcp-data-platform/bench/internal/claudecli"
	"github.com/txn2/mcp-data-platform/bench/internal/grade"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/mcpc"
	"github.com/txn2/mcp-data-platform/bench/internal/pool"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// searchToolName marks the discovery tool for the search-called signal.
const searchToolName = "search"

// Stage names, one per episode kind.
const (
	StageTeach = "teach"
	StageEval  = "eval"
)

// teachScaffold instructs the agent to capture the stated fact and link it to
// the dataset it concerns, so the runner can verify capture and entity linkage.
// Cold-start runs only on a3, so this is not an ablation variable.
const teachScaffold = `You are a data analyst agent connected to a data platform over MCP. You work across many separate sessions and can save knowledge for later sessions to use.
Rules:
- THIS SESSION IS FOR RECORDING KNOWLEDGE. Save the definition stated below using the memory tools so a future session can recall it, and link it to the dataset it concerns. Then confirm in one line what you saved.`

// evalScaffold frames an evaluation session. The evaluator was never taught the
// facts, so its only knowledge source is what the platform surfaces — search
// results, catalog descriptions, and knowledge pages — which is exactly the
// promoted-knowledge delivery channel the curve measures. The no-self-teach
// rule keeps it that way: an evaluator that saved a memory could surface it to
// a LATER checkpoint's evaluators through shared records and confound the
// curve. The rule is a steer, not a guarantee — MemoryWrites on each attempt is
// the audit-side validity signal that catches a violation.
const evalScaffold = `You are a data analyst agent connected to a data platform over MCP.
Rules:
- Ground every answer in tool results and in the knowledge the platform surfaces (search results, catalog descriptions, knowledge pages); do not answer from prior knowledge about any specific dataset.
- Use the search tool to discover context and data before querying.
- This is an evaluation of EXISTING platform knowledge: do not save, capture, or update memories or knowledge during this session.
- When you have the answer, end your reply with a single line: "FINAL ANSWER: <answer>".`

// Per-grading-kind answer format rules, matching the deterministic graders'
// parsing convention (shared with the S1-S3 pipeline and S5 lifecycle).
const (
	numericFormat = `- The FINAL ANSWER line must contain exactly one number (USD unless the question states otherwise), for example "FINAL ANSWER: 12345.67".`
	entityFormat  = `- The FINAL ANSWER line must name the single best answer: a fully qualified table name (catalog.schema.table) for dataset questions, or the exact name requested.`
)

// evalSystem builds the evaluator's system prompt for a grading kind.
func evalSystem(kind string) string {
	format := entityFormat
	if kind == task.GradeNumeric {
		format = numericFormat
	}
	return evalScaffold + "\n" + format
}

// gradeEval scores an eval answer with the deterministic graders, reusing the
// task graders so a cold-start eval is graded exactly as an S1-S3 question.
// Only numeric and entity reach here (exec_sql is rejected at load time).
func gradeEval(finalAnswer string, g task.Grading) bool {
	final := grade.ExtractFinal(finalAnswer)
	switch g.Kind {
	case task.GradeNumeric:
		if g.Value == nil {
			return false
		}
		_, _, correct := grade.Numeric(final, *g.Value, g.AbsTolerance)
		return correct
	case task.GradeEntity:
		_, correct := grade.Entity(final, g.Aliases, g.WrongAliases)
		return correct
	default:
		return false
	}
}

// episodeSpec is one session's parameters.
type episodeSpec struct {
	stage  string
	unitID string // lesson id (teach) or task id (eval), keys the adapter + transcript
	seq    int    // pool identity sequence number
	prompt string
	system string
	budget int
}

// episodeResult is one session's raw outcome, mapped by callers into a lesson
// EpisodeRecord or an EvalAttempt.
type episodeResult struct {
	email        string
	sessionID    string
	toolCalls    int
	toolErrors   int
	searchCalled bool
	memoryWrites int
	wallMS       int64
	usage        llm.Usage
	audit        auditapi.Metrics
	// auditReadErr records a failed audit read-back on an otherwise-successful
	// episode. The attempt still grades, but its zero audit metrics must not
	// pass for "no enrichment": pooled coverage is a documented pass criterion,
	// so audit-signal loss is carried on the record instead of only logged.
	auditReadErr string
	finalAnswer  string
	err          string
}

// isMemoryWriteCall reports whether a tool call is a memory WRITE an eval
// session must never make: an evaluator that saves a memory could surface it
// to later checkpoints' evaluators through shared records, contaminating the
// curve with self-taught knowledge. memory_capture always writes; memory_manage
// writes only for its mutating commands (update, forget, consolidate) — its
// list/review commands are read-only and permitted by the eval scaffold, so
// counting them would falsely flag a clean run. The claude-cli transcript
// records the namespaced form (mcp__<server>__memory_capture), so the name is
// matched on its final "__"-separated segment.
func isMemoryWriteCall(c llm.ToolCall) bool {
	name := c.Name
	if i := strings.LastIndex(name, "__"); i >= 0 {
		name = name[i+2:]
	}
	switch name {
	case "memory_capture":
		return true
	case "memory_manage":
		cmd, _ := c.Args["command"].(string)
		return cmd == "update" || cmd == "forget" || cmd == "consolidate"
	}
	return false
}

// countMemoryWrites derives the evaluator no-self-teach validity signal from
// the transcript: the number of EXECUTED memory-write tool calls. Deriving it
// from the transcript (rather than hooking each execution path) keeps the loop
// and claude-cli paths on one definition, mirroring the lifecycle
// instrumentation. A write that never landed does not count: a budget-refused
// call never ran (its paired result is the refusal sentinel), and an error
// result (a platform refusal or a handler failure) wrote no record that a
// later checkpoint could read.
func countMemoryWrites(msgs []llm.Message) int {
	writeIDs := map[string]bool{}
	for _, m := range msgs {
		for _, c := range m.ToolCalls {
			if isMemoryWriteCall(c) {
				writeIDs[c.ID] = true
			}
		}
	}
	if len(writeIDs) == 0 {
		return 0
	}
	writes := 0
	for _, m := range msgs {
		for _, r := range m.ToolResults {
			if writeIDs[r.CallID] && !r.IsError && r.Text != agent.BudgetRefusalText {
				writes++
			}
		}
	}
	return writes
}

// runEpisode drives one fresh MCP session end to end: authenticate as the pool
// identity, mint the handle, run the agent loop against the a3 tool surface, and
// read the audit trail back (best effort — lesson state comes from the knowledge
// API and eval correctness from grading, not audit). A harness failure lands in
// the result's err; a graded outcome does not.
func (e *runEnv) runEpisode(ctx context.Context, spec episodeSpec) episodeResult {
	if e.opts.ClaudeCLI != nil {
		return e.runClaudeCLIEpisode(ctx, spec)
	}
	res := episodeResult{email: pool.Email(spec.seq)}
	client := e.attemptClient(spec.seq)

	session, err := client.Connect(ctx)
	if err != nil {
		res.err = fmt.Sprintf("connect: %v", err)
		return res
	}
	defer func() { _ = session.Close() }()

	info, err := mcpc.Mint(ctx, session)
	if err != nil {
		res.err = fmt.Sprintf("mint session handle: %v", err)
		return res
	}
	res.sessionID = info.Handle
	e.recordPlatformVersion(info.PlatformVersion)

	tools, err := mcpc.ListTools(ctx, session)
	if err != nil {
		res.err = fmt.Sprintf("list tools: %v", err)
		return res
	}

	adapter, err := e.opts.Factory(spec.unitID, spec.stage)
	if err != nil {
		res.err = fmt.Sprintf("build adapter: %v", err)
		return res
	}
	e.recordModel(adapter.Model())

	audited, indeterminate := 0, 0
	exec := func(ctx context.Context, name string, args map[string]any) llm.ToolResult {
		if name == searchToolName {
			res.searchCalled = true
		}
		r := mcpc.Call(ctx, session, name, args, info.Handle)
		if r.TransportErr != nil {
			indeterminate++
			return llm.ToolResult{Text: "transport error: " + r.TransportErr.Error(), IsError: true}
		}
		if !mcpc.PreAuditRefusal(r.ErrorCode) {
			audited++
		}
		return llm.ToolResult{Text: r.Text, IsError: r.ToolErr}
	}

	start := time.Now()
	result, runErr := agent.Run(ctx, adapter, agent.Config{
		System: spec.system, Prompt: spec.prompt, Tools: tools, Budget: spec.budget,
	}, exec)
	res.wallMS = time.Since(start).Milliseconds()
	res.toolCalls = result.ToolCalls
	res.toolErrors = result.ToolErrors
	res.usage = result.Usage
	res.finalAnswer = result.FinalAnswer
	res.memoryWrites = countMemoryWrites(result.Transcript)
	e.writeTranscript(spec, result.Transcript)
	if runErr != nil {
		res.err = fmt.Sprintf("agent loop: %v", runErr)
		return res
	}
	res.audit, res.auditReadErr = e.readAudit(ctx, info.Handle, audited, audited+indeterminate)
	return res
}

// runClaudeCLIEpisode drives one episode through a real `claude -p` client.
// Claude Code authenticates as the pool identity, mints and threads its own
// handle, and drives the tools; the harness reconstructs the transcript and
// reads audit back best effort by the threaded handle.
func (e *runEnv) runClaudeCLIEpisode(ctx context.Context, spec episodeSpec) episodeResult {
	res := episodeResult{email: pool.Email(spec.seq)}
	e.recordModel(e.opts.ClaudeCLI.Model())

	start := time.Now()
	cres, err := e.opts.ClaudeCLI.Run(ctx, claudecli.Request{
		Endpoint:   e.opts.Target.BaseURL,
		Credential: pool.Credential(e.opts.Target.Credential, spec.seq, e.opts.IdentityKeys),
		System:     spec.system,
		Prompt:     spec.prompt,
	})
	res.wallMS = time.Since(start).Milliseconds()
	if err != nil {
		res.err = fmt.Sprintf("claude-cli: %v", err)
		return res
	}
	res.sessionID = cres.Handle
	res.toolCalls = cres.MCPCalls
	res.toolErrors = cres.ToolErrors
	res.searchCalled = cres.SearchCalled
	res.memoryWrites = countMemoryWrites(cres.Transcript)
	res.usage = cres.Usage
	res.finalAnswer = cres.FinalText
	e.recordPlatformVersion(cres.PlatformVersion)
	e.writeClaudeTranscript(spec, cres.Transcript)

	if cres.IsError {
		res.err = fmt.Sprintf("claude-cli result error (subtype %q): %.300s", cres.Subtype, cres.FinalText)
		return res
	}
	if !cres.ServerConnected {
		res.err = fmt.Sprintf("bench MCP server did not connect (status %q)", cres.ServerStatus)
		return res
	}
	if cres.Handle == "" {
		// No handle means claude never minted one via platform_info. With no
		// handle it cannot have threaded a successful data call (the session-gate
		// middleware refuses un-threaded calls), so a positive success count with
		// no handle is a harness inconsistency to surface, not audit loss
		// (mirrors the S1-S3 pipeline's contract).
		if cres.SuccessfulMCPCalls > 0 {
			res.err = fmt.Sprintf("claude-cli reported %d successful tool call(s) but surfaced no dps_ handle to correlate audit (platform_info result missing or unparseable)", cres.SuccessfulMCPCalls)
		}
		return res
	}
	res.audit, res.auditReadErr = e.readAudit(ctx, cres.Handle, cres.SuccessfulMCPCalls, cres.MCPCalls)
	return res
}

// readAudit reads the session's audit trail back best effort. A missing row does
// not fail an episode: lesson state comes from the knowledge API and eval
// correctness from grading; audit only enriches the efficiency and coverage
// picture. A read failure yields zero metrics plus the error, which callers
// record on the attempt so audit-signal loss is visible in results.json (the
// coverage curve must never silently degrade to zeros).
func (e *runEnv) readAudit(ctx context.Context, handle string, minAudited, maxAudited int) (auditapi.Metrics, string) {
	events, err := e.audit.WaitForSession(ctx, handle, minAudited, maxAudited, e.opts.AuditTimeout)
	if err != nil {
		e.log.Warn("cold-start audit read-back", "handle", handle, "error", err)
		return auditapi.Metrics{}, err.Error()
	}
	return auditapi.Summarize(events), ""
}

// writeTranscript persists a loop episode's transcript for manual audit.
func (e *runEnv) writeTranscript(spec episodeSpec, msgs []llm.Message) {
	e.persistTranscript(spec, msgs)
}

// writeClaudeTranscript persists a claude-cli episode's reconstructed transcript.
func (e *runEnv) writeClaudeTranscript(spec episodeSpec, msgs []llm.Message) {
	e.persistTranscript(spec, msgs)
}

// persistTranscript writes one episode's transcript to the transcript dir.
func (e *runEnv) persistTranscript(spec episodeSpec, msgs []llm.Message) {
	if e.opts.TranscriptDir == "" {
		return
	}
	if err := os.MkdirAll(e.opts.TranscriptDir, 0o750); err != nil {
		e.log.Warn("transcript dir", "error", err)
		return
	}
	path := filepath.Join(e.opts.TranscriptDir,
		fmt.Sprintf("%s-%s-%s.json", spec.stage, spec.unitID, pool.Email(spec.seq)))
	payload := transcriptFile{Stage: spec.stage, UnitID: spec.unitID, Email: pool.Email(spec.seq), Transcript: msgs}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		e.log.Warn("marshal transcript", "error", err)
		return
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		e.log.Warn("write transcript", "error", err)
	}
}

// transcriptFile is the on-disk transcript layout.
type transcriptFile struct {
	Stage      string        `json:"stage"`
	UnitID     string        `json:"unit_id"`
	Email      string        `json:"email"`
	Transcript []llm.Message `json:"transcript"`
}
