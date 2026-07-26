package pkrun

import (
	"context"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/agent"
	"github.com/txn2/mcp-data-platform/bench/internal/claudecli"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/mcpc"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

// LoopRunner drives one episode through the in-process tool loop against
// the raw model API, satisfying the same EpisodeRunner seam the claude-cli
// path uses. It exists to strip the client confound: claude-cli inserts
// its own behaviors (the #1027 pilot's client-side tool search is the
// standing precedent), so a headline number is not believed until the raw
// API reproduces it. Same platform, same identity pool, same cells; only
// the thing driving the conversation changes.
type LoopRunner struct {
	adapter llm.Adapter
	model   string
	timeout time.Duration
	// budget caps tool calls per episode. The claude-cli path has no
	// enforceable cap, so this is set generously enough that no episode
	// observed on that path would have been truncated here; a tighter cap
	// would make the two paths differ in exactly the way the comparison
	// must not.
	budget int
}

// loopBudget is far above the largest episode observed on the claude-cli
// path (13 calls, the c=11 cost-sweep attempts).
const loopBudget = 40

// NewLoopRunner builds the raw-API runner.
func NewLoopRunner(model string, timeout time.Duration) (*LoopRunner, error) {
	a, err := llm.NewAnthropic(model, 4096, timeout)
	if err != nil {
		return nil, err
	}
	return &LoopRunner{adapter: a, model: model, timeout: timeout, budget: loopBudget}, nil
}

// Model names the driven model for the run manifest.
func (r *LoopRunner) Model() string { return "api:" + r.model }

// Run executes one episode: connect as the pool identity, mint the
// handle, run the loop, and shape the outcome as the claudecli result the
// rest of the runner already consumes.
func (r *LoopRunner) Run(ctx context.Context, req claudecli.Request) (claudecli.Result, error) {
	client := mcpc.New(req.Endpoint, target.Target{BaseURL: req.Endpoint, Credential: req.Credential}.HTTPClient(r.timeout))
	session, err := client.Connect(ctx)
	if err != nil {
		return claudecli.Result{}, fmt.Errorf("looprunner: connect: %w", err)
	}
	defer func() { _ = session.Close() }()
	info, err := mcpc.Mint(ctx, session)
	if err != nil {
		return claudecli.Result{}, fmt.Errorf("looprunner: mint handle: %w", err)
	}
	tools, err := mcpc.ListTools(ctx, session)
	if err != nil {
		return claudecli.Result{}, fmt.Errorf("looprunner: list tools: %w", err)
	}
	calls, errs := 0, 0
	exec := func(ctx context.Context, name string, args map[string]any) llm.ToolResult {
		res := mcpc.Call(ctx, session, name, args, info.Handle)
		if res.TransportErr != nil {
			errs++
			return llm.ToolResult{Text: "transport error: " + res.TransportErr.Error(), IsError: true}
		}
		calls++
		if res.ToolErr {
			errs++
		}
		return llm.ToolResult{Text: res.Text, IsError: res.ToolErr}
	}
	out, err := agent.Run(ctx, r.adapter, agent.Config{
		System: req.System, Prompt: req.Prompt, Tools: tools, Budget: r.budget,
	}, exec)
	if err != nil {
		return claudecli.Result{}, fmt.Errorf("looprunner: agent loop: %w", err)
	}
	return claudecli.Result{
		FinalText:          out.FinalAnswer,
		Transcript:         out.Transcript,
		Usage:              out.Usage,
		Handle:             info.Handle,
		PlatformVersion:    info.PlatformVersion,
		MCPCalls:           calls,
		SuccessfulMCPCalls: calls - errs,
		ToolErrors:         out.ToolErrors,
		ServerConnected:    true,
		Subtype:            "success",
	}, nil
}
