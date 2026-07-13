// Package agent runs the benchmark's model-driven tool loop: present the
// arm's tools, execute the model's tool calls against the live MCP session,
// and stop at a final text answer or the tool-call budget.
package agent

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/bench/internal/llm"
)

// extraIterations bounds the wind-down after budget exhaustion: the model gets
// this many further completions to produce a final answer before the loop
// stops with whatever text it has.
const extraIterations = 2

// ToolExecutor executes one tool call against the live session and returns its
// result. Transport-level failures are reported as error results so the model
// can adapt, mirroring how a real client surfaces them.
type ToolExecutor func(ctx context.Context, name string, args map[string]any) llm.ToolResult

// Config is one task attempt's loop parameters.
type Config struct {
	// System is the fixed prompt scaffold (identical across arms).
	System string
	// Prompt is the task prompt.
	Prompt string
	// Tools is the arm's tool surface.
	Tools []llm.ToolDef
	// Budget is the maximum number of tool calls executed.
	Budget int
}

// Result is the outcome of one attempt's loop.
type Result struct {
	// FinalAnswer is the model's last assistant text.
	FinalAnswer string
	// Transcript is the full provider-agnostic conversation, for the
	// per-attempt transcript file (manual rubric review reads these).
	Transcript []llm.Message
	// ToolCalls counts tool calls executed (budget-refused calls excluded).
	ToolCalls int
	// ToolErrors counts executed calls whose result was an error.
	ToolErrors int
	// BudgetExhausted is true when the loop hit the tool-call budget.
	BudgetExhausted bool
	// Usage is the summed token usage across completions.
	Usage llm.Usage
}

// Run drives the loop to completion. It returns an error only for adapter or
// protocol failures; a wrong answer is a graded outcome, not an error.
func Run(ctx context.Context, a llm.Adapter, cfg Config, exec ToolExecutor) (Result, error) {
	res := Result{Transcript: []llm.Message{{Role: "user", Text: cfg.Prompt}}}
	maxIterations := cfg.Budget + extraIterations + 1
	windDown := 0
	for iter := range maxIterations {
		// The wind-down bound is a separate counter, not derived from the
		// iteration cap: a model that burns the whole budget with multi-call
		// turns would otherwise keep receiving completions (each answered
		// only with budget-refusal results) until the iteration cap.
		if res.BudgetExhausted {
			windDown++
			if windDown > extraIterations {
				return res, fmt.Errorf("no final answer within %d completions after budget exhaustion", extraIterations)
			}
		}
		msg, usage, err := a.Complete(ctx, cfg.System, res.Transcript, cfg.Tools)
		if err != nil {
			return res, fmt.Errorf("completion %d: %w", iter+1, err)
		}
		res.Usage.Add(usage)
		res.Transcript = append(res.Transcript, msg)
		if len(msg.ToolCalls) == 0 {
			res.FinalAnswer = msg.Text
			return res, nil
		}
		res.Transcript = append(res.Transcript, executeCalls(ctx, msg.ToolCalls, cfg.Budget, exec, &res))
	}
	return res, fmt.Errorf("loop exceeded %d iterations without a final answer", maxIterations)
}

// executeCalls runs the requested calls up to the budget and builds the user
// turn answering them. Calls past the budget receive error results, and the
// turn carries an instruction to answer with what was gathered.
func executeCalls(ctx context.Context, calls []llm.ToolCall, budget int, exec ToolExecutor, res *Result) llm.Message {
	reply := llm.Message{Role: "user"}
	for _, call := range calls {
		if res.ToolCalls >= budget {
			res.BudgetExhausted = true
			reply.ToolResults = append(reply.ToolResults, llm.ToolResult{
				CallID:  call.ID,
				Text:    "tool-call budget exhausted; no further tool calls are executed",
				IsError: true,
			})
			continue
		}
		res.ToolCalls++
		result := exec(ctx, call.Name, call.Args)
		result.CallID = call.ID
		if result.IsError {
			res.ToolErrors++
		}
		reply.ToolResults = append(reply.ToolResults, result)
	}
	if res.BudgetExhausted {
		reply.Text = "The tool-call budget is exhausted. Give your FINAL ANSWER now using the information gathered so far."
	}
	return reply
}
