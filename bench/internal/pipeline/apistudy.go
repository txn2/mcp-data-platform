package pipeline

// API-connection study extensions (#1027): active only when Options.Fixture
// is set (the b* arms). State and refusal grading happen here rather than
// in gradeAttempt because they need the fixture control plane and the
// episode transcript, not just the final answer.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/apistudy"
	"github.com/txn2/mcp-data-platform/bench/internal/claudecli"
	"github.com/txn2/mcp-data-platform/bench/internal/fixturectl"
	"github.com/txn2/mcp-data-platform/bench/internal/grade"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/report"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// codeModeSystem is the b2 scaffold appended to Claude Code's system
// prompt: no MCP tools exist; the model reads the spec from the workspace
// and issues HTTP calls itself.
const codeModeSystem = "You have NO MCP tools for this task. The task concerns a REST API described by the OpenAPI spec in ./spec.json in your working directory. Base URL: %s. Authenticate every request with the header X-API-Key: %s. Write and run code (curl, scripts) to call the API and answer the task."

// runCodeModeAttempt executes one b2 (code mode) attempt: `claude -p`
// with code tools in a spec-seeded workspace, no MCP server, no platform.
// There is no audit trail to correlate — the fixture access log is the
// ground record of API traffic, and grading runs through finishAPIStudy.
func (e *runEnv) runCodeModeAttempt(ctx context.Context, t task.Task, attempt int, res *report.Results) report.Attempt {
	a := report.Attempt{TaskID: t.ID, Suite: t.Suite, Attempt: attempt}
	log := e.opts.Log.With("task", t.ID, "attempt", attempt, "arm", e.opts.Arm)
	if res.Manifest.Model == "" {
		res.Manifest.Model = e.opts.ClaudeCLI.Model()
	}
	system := fmt.Sprintf(codeModeSystem, e.opts.Fixture.BaseURL(), e.opts.Fixture.APIKey()) +
		"\n" + systemScaffold + "\n" + formatInstruction(t)
	start := time.Now()
	cres, err := e.opts.ClaudeCLI.Run(ctx, claudecli.Request{System: system, Prompt: t.Prompt})
	a.WallMS = time.Since(start).Milliseconds()
	if err != nil {
		a.Error = fmt.Sprintf("claude-cli code mode: %v", err)
		return a
	}
	a.FinalAnswer = grade.ExtractFinal(cres.FinalText)
	a.ToolCalls = transcriptToolCalls(cres.Transcript)
	a.ToolErrors = transcriptToolErrors(cres.Transcript)
	a.InputTokens = cres.Usage.InputTokens
	a.OutputTokens = cres.Usage.OutputTokens
	e.writeTranscript(&a, t, cres.Transcript, log)
	if cres.IsError {
		a.Error = fmt.Sprintf("claude-cli result error (subtype %q): %.300s", cres.Subtype, cres.FinalText)
		return a
	}
	e.gradeAttempt(ctx, &a, t, log)
	e.finishAPIStudy(ctx, &a, t, cres.Transcript, log)
	return a
}

// transcriptToolCalls counts every tool invocation in a transcript (b2's
// Bash/file tools; there is no MCP prefix to count by).
func transcriptToolCalls(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.ToolCalls)
	}
	return n
}

// transcriptToolErrors counts errored tool results in a transcript.
func transcriptToolErrors(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		for _, r := range m.ToolResults {
			if r.IsError {
				n++
			}
		}
	}
	return n
}

// finishAPIStudy applies the study's per-attempt analysis: retrieval
// extraction, state / refusal grading, and failure-taxonomy
// classification. No-op for non-study runs and for attempts that already
// failed at the harness level.
func (e *runEnv) finishAPIStudy(ctx context.Context, a *report.Attempt, t task.Task, msgs []llm.Message, log *slog.Logger) {
	if e.opts.Fixture == nil || a.Error != "" {
		return
	}
	a.Retrieval = apistudy.AnalyzeRetrieval(msgs, t.GoldOperations)
	reqs, err := e.opts.Fixture.Requests(ctx)
	if err != nil {
		a.Error = fmt.Sprintf("fixture access log: %v", err)
		return
	}
	switch t.Grading.Kind {
	case task.GradeState:
		ok, detail, gerr := e.opts.Fixture.GradeState(ctx, t.Grading.StateChecks)
		if gerr != nil {
			a.Error = fmt.Sprintf("state grading: %v", gerr)
			return
		}
		a.Correct, a.GradeDetail = ok, detail
	case task.GradeRefusal:
		if !e.gradeRefusal(ctx, a, reqs) {
			return
		}
	}
	if !a.Correct {
		a.FailureClass = apistudy.Classify(apistudy.Evidence{
			Task: t, Transcript: msgs, Fixture: reqs, Retrieval: a.Retrieval,
		})
	}
	e.attachFixtureLog(a, reqs, log)
	log.Info("api study analysis",
		"correct", a.Correct, "failure_class", a.FailureClass, "retrieval", a.Retrieval != nil)
}

// attachFixtureLog persists the attempt's fixture access log into its
// transcript file, so grading and taxonomy stay re-derivable after the
// fixture process (and its in-memory log) are gone. Failure to attach is
// logged, not fatal — the graded outcome stands.
func (e *runEnv) attachFixtureLog(a *report.Attempt, reqs []fixturectl.RequestLogEntry, log *slog.Logger) {
	if a.TranscriptPath == "" {
		return
	}
	raw, err := os.ReadFile(a.TranscriptPath)
	if err != nil {
		log.Warn("attach fixture log: read transcript", "error", err)
		return
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		log.Warn("attach fixture log: parse transcript", "error", err)
		return
	}
	doc["fixture_requests"] = reqs
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		log.Warn("attach fixture log: marshal", "error", err)
		return
	}
	if err := os.WriteFile(a.TranscriptPath, out, 0o600); err != nil {
		log.Warn("attach fixture log: write", "error", err)
	}
}

// gradeRefusal grades an irrelevance task: fail deterministically on any
// state-changing call, then decide the stated-unavailability half via the
// judge when wired (recording which path graded it). Returns false when a
// harness error aborted grading.
func (e *runEnv) gradeRefusal(ctx context.Context, a *report.Attempt, reqs []fixturectl.RequestLogEntry) bool {
	judged := false
	if apistudy.WritePerformed(reqs) {
		a.Correct = false
		a.GradeDetail = "performed a state-changing call"
		a.RefusalJudged = &judged
		return true
	}
	if e.opts.RefusalJudge != nil {
		pass, err := e.opts.RefusalJudge(ctx, a.FinalAnswer)
		if err != nil {
			a.Error = fmt.Sprintf("refusal judge: %v", err)
			return false
		}
		judged = true
		a.Correct, a.RefusalJudged = pass, &judged
		return true
	}
	a.Correct = apistudy.AnswerRefuses(a.FinalAnswer)
	a.RefusalJudged = &judged
	return true
}
