package exportasync

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/wirejson"
)

// The caller's side of a background export: what a call that starts one, or
// names its key, is answered with.

// Answer is what a background export's caller is handed, at the start and on
// every read of a run not yet completed.
type Answer struct {
	RunID          string `json:"run_id"`
	Status         string `json:"status"`
	IdempotencyKey string `json:"idempotency_key"`
	PagesFetched   int    `json:"pages_fetched"`
	ItemsMerged    int    `json:"items_merged"`
	// StartedAt and UpdatedAt are when the run started and last reported.
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Message   string    `json:"message"`
}

// AnswerOf renders a run for its caller; how names the call that reads it
// again ("call graphql_export again with idempotency_key=...").
func AnswerOf(run *Run, key, how string) Answer {
	msg := run.Message
	switch run.Status {
	case StatusRunning:
		msg = "The export is running in the background. " + how
	case StatusFailed:
		msg = "The background export failed: " + run.Message
	}
	return Answer{
		RunID: run.ID, Status: run.Status, IdempotencyKey: key,
		PagesFetched: run.Progress.PagesFetched, ItemsMerged: run.Progress.ItemsMerged,
		StartedAt: run.StartedAt, UpdatedAt: run.UpdatedAt, Message: msg,
	}
}

// Resume answers a call that names the idempotency key of one of its
// caller's background exports: with the run's progress while it runs, and
// with the export's own answer once it completed. A run that failed is
// returned as prior with handled false, and the call goes on to run the
// export again; the new run's answer names the failure. A key no background
// export has, and a deployment with no runner, are not handled.
func Resume(ctx context.Context, runner Runner, tool, key, how string) (res *mcp.CallToolResult, prior *Run, handled bool) {
	if runner == nil || key == "" {
		return nil, nil, false
	}
	run, err := runner.Find(ctx, tool, key)
	if err != nil {
		return nil, nil, false
	}
	switch run.Status {
	case StatusRunning:
		return answerResult(AnswerOf(run, key, how)), nil, true
	case StatusCompleted:
		out, err := Result(run)
		if err != nil {
			return errorResult(err.Error()), nil, true
		}
		return out, nil, true
	default:
		return nil, run, false
	}
}

// Wanted reports whether an export runs in the background: asked for with
// async, or, when async is not given, a page walk from a model client, whose
// tool-call timeout a long walk outlasts. Nothing runs in the background
// without a runner.
func Wanted(runner Runner, async *bool, walk, modelClient bool) bool {
	if runner == nil {
		return false
	}
	if async != nil {
		return *async
	}
	return walk && modelClient
}

// Background is one export to start in the background.
type Background struct {
	Runner Runner
	Tool   string
	Key    string
	// How is the sentence telling the caller how to read the run again.
	How string
	// Prior is a failed run Resume found under the key, which the answer
	// names.
	Prior *Run
}

// Begin starts work as a background export under b.Key, or joins the one
// already running under it, and answers the caller with the run.
func Begin(ctx context.Context, b Background, work func(ctx context.Context) (*mcp.CallToolResult, error)) *mcp.CallToolResult {
	runner, tool, key, how, prior := b.Runner, b.Tool, b.Key, b.How, b.Prior
	run, started, err := runner.Start(ctx, tool, key, work)
	if err != nil {
		return errorResult(err.Error())
	}
	answer := AnswerOf(run, key, how)
	switch {
	case !started:
		answer.Message = "An export under this idempotency key is already running; this call joined it rather than starting a second walk. " + how
	case prior != nil:
		answer.Message = "The previous background export under this key (" + prior.ID + ") failed: " + prior.Message +
			" It was started again. " + how
	}
	return answerResult(answer)
}

// NewKey mints the idempotency key a background export the caller named
// none for is read again by.
func NewKey() (string, error) {
	b := make([]byte, keyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("minting an idempotency key: %w", err)
	}
	return "bg-" + hex.EncodeToString(b), nil
}

// keyBytes is a minted key's entropy.
const keyBytes = 12

// answerResult is an answer as a tool result.
func answerResult(a Answer) *mcp.CallToolResult {
	raw, err := wirejson.Marshal(a)
	if err != nil {
		return errorResult(err.Error())
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}
}

// errorResult is a failed tool result carrying msg.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

// RunnerOf is the runner a late-bound getter answers, nil when there is no
// getter: the export tools are wired before the task layer is installed.
func RunnerOf(get func() Runner) Runner {
	if get == nil {
		return nil
	}
	return get()
}

// Launch is one export a tool starts in the background: the fields of its
// own arguments the launch fills in, and how the deployment bounds it.
type Launch struct {
	Runner Runner
	Tool   string
	Prior  *Run
	// Key is the call's idempotency_key, minted when it names none. Pointing
	// it at the argument the export writes its asset under means a call
	// naming the key after the run is gone still finds the asset.
	Key *string
	// Timeout is the call's timeout_seconds, set to MaxTimeout when it names
	// none: nobody is waiting on the call.
	Timeout    *int
	MaxTimeout time.Duration
}

// Start fills in the launch's key and timeout and begins work in the
// background, answering with the run.
func Start(ctx context.Context, l Launch, work func(ctx context.Context) (*mcp.CallToolResult, error)) *mcp.CallToolResult {
	if *l.Key == "" {
		minted, err := NewKey()
		if err != nil {
			return errorResult(err.Error())
		}
		*l.Key = minted
	}
	if *l.Timeout <= 0 {
		*l.Timeout = int(l.MaxTimeout / time.Second)
	}
	return Begin(ctx, Background{Runner: l.Runner, Tool: l.Tool, Key: *l.Key, How: HowToReadAgain(l.Tool, *l.Key), Prior: l.Prior}, work)
}

// HowToReadAgain is the sentence a call naming a key is told how to read its
// run with.
func HowToReadAgain(tool, key string) string {
	return "Call " + tool + " again with idempotency_key=" + key + " to read its progress and, once done, its answer."
}
