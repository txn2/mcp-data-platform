// Package resultbudget holds a model client's tool results to its context
// budget (#1878).
//
// The budget is a property of the MCP response to a model, not of any
// toolkit. It is enforced once, here, on every tool's result, and only
// when the caller is a model over MCP: a REST gateway call, a managed
// script's run and an admin call are never fitted, because a program that
// parses a response cannot read a cut one. Those callers meet only the real
// resource limits, a connection's read cap and the in-flight memory budget.
//
// What is measured is the text of the result the client receives, which is
// what issue #1606 measured a client refusing.
//
// Only a result the model can recover the rest of is ever cut. A tool whose
// result is data with an export equivalent -- a response body, a GraphQL
// answer, a query's rows -- implements toolkit.ResultFitter, cuts in its own
// shape, flags the cut and hands back the export call that returns the
// whole. Every other result reaches the model whole: a knowledge page, a
// tool's help, a prompt or an uploaded document cut in half has no way back
// to the rest, and a model reasoning from half a manual does worse than one
// whose client spills an oversized result to a file it can still read. A
// fitter that cannot cut a result recoverably declines, and that result is
// left whole too.
//
// It lives in its own package so the platform facade gains no field or
// method for it.
package resultbudget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// DefaultMaxBytes is the context budget a tool result is held to when the
// configuration names none. Issue #1606 measured a 64,213-character tool
// result refused by a client and spilled to a file, so the ceiling is under
// 64 KiB; 32 KiB leaves room under it for a client stricter than the one
// measured.
const DefaultMaxBytes = 32 * 1024

// methodToolsCall is the only MCP method whose result is fitted.
const methodToolsCall = "tools/call"

// Config is the `tools.result_budget` section.
type Config struct {
	// MaxBytes is the most of a rendered tool result a model client is
	// handed. Zero takes DefaultMaxBytes.
	MaxBytes int `yaml:"max_bytes"`
	// PerTool overrides MaxBytes for the named tools.
	PerTool map[string]int `yaml:"per_tool"`
}

// Validate refuses a budget that is negative, or a per-tool override that
// is not positive: an override exists to name a size, and a zero one would
// read as "no budget" to one reader and "nothing inline" to another.
func (c Config) Validate() error {
	if c.MaxBytes < 0 {
		return errors.New("tools.result_budget.max_bytes must not be negative")
	}
	for tool, n := range c.PerTool {
		if n <= 0 {
			return fmt.Errorf("tools.result_budget.per_tool.%s must be positive", tool)
		}
	}
	return nil
}

// For is the budget tool's results are held to.
func (c Config) For(tool string) int {
	if n, ok := c.PerTool[tool]; ok && n > 0 {
		return n
	}
	if c.MaxBytes > 0 {
		return c.MaxBytes
	}
	return DefaultMaxBytes
}

// FitterLookup returns the fitter a tool's toolkit provides, or nil when it
// provides none, in which case the tool's results are never cut.
type FitterLookup func(tool string) toolkit.ResultFitter

// RegistryLookup resolves a tool's fitter through the toolkit registry.
// It resolves per call rather than once, because a toolkit can be added
// after startup (a first connection of a kind saved through the admin API).
func RegistryLookup(reg *registry.Registry) FitterLookup {
	return func(tool string) toolkit.ResultFitter {
		m := reg.GetToolkitForTool(tool)
		if !m.Found {
			return nil
		}
		tk, ok := reg.Get(m.Kind, m.Name)
		if !ok {
			return nil
		}
		f, _ := tk.(toolkit.ResultFitter)
		return f
	}
}

// ownResultKey carries the recorder the capture layer fills.
type ownResultKey struct{}

// ownResult is the tool's own result as the handler returned it, before
// the layers between the capture and the budget -- the call reference,
// enrichment -- appended their blocks and mirrored their keys into the
// structured content.
type ownResult struct {
	captured   bool
	blocks     int
	structured any
}

// Middleware fits a model client's tools/call results to its budget, those
// of the tools that have a fitter. It reads the PlatformContext the tool-call middleware writes, so
// it must be inner to it. It sits outer to enrichment and the call
// reference, so what it measures is what the client receives; Capture,
// innermost, records which part of that is the tool's own, and only that
// part is shaped by the tool's fitter.
func Middleware(cfg Config, lookup FitterLookup) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != methodToolsCall {
				return next(ctx, method, req)
			}
			pc := middleware.GetPlatformContext(ctx)
			if pc == nil || pc.Source != middleware.SourceMCP {
				return next(ctx, method, req)
			}
			budget := cfg.For(pc.ToolName)
			own := &ownResult{}
			ctx = context.WithValue(mcpcontext.WithResultBudget(ctx, budget), ownResultKey{}, own)
			result, err := next(ctx, method, req)
			if err != nil {
				return result, err
			}
			if res, ok := result.(*mcp.CallToolResult); ok && res != nil && !res.IsError {
				fit(fitCall{tool: pc.ToolName, args: callArguments(req), budget: budget, lookup: lookup, own: own}, res)
			}
			return result, nil
		}
	}
}

// Capture records the tool's own result for Middleware. It is registered
// innermost, next to the handler; a call Middleware does not fit carries no
// recorder, and Capture does nothing.
func Capture() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			own, _ := ctx.Value(ownResultKey{}).(*ownResult)
			if res, ok := result.(*mcp.CallToolResult); ok && res != nil && own != nil {
				own.captured, own.blocks = true, len(res.Content)
				own.structured = res.StructuredContent
				// Enrichment merges its keys into a map in place, so a map is
				// cloned; encoded JSON is replaced rather than written to, so
				// the tool's own bytes need no copy.
				if m, ok := res.StructuredContent.(map[string]any); ok {
					own.structured = maps.Clone(m)
				}
			}
			return result, err
		}
	}
}

// fitCall is one result to fit and what fitting it needs.
type fitCall struct {
	tool   string
	args   json.RawMessage
	budget int
	lookup FitterLookup
	own    *ownResult
}

// fit holds res to the budget when its tool has a fitter. The tool's own
// part is offered to the fitter with the budget less what the layers outside
// the tool appended -- but no less than half the budget, so a large
// enrichment cannot squeeze the tool's answer out. What the other layers
// appended is kept after it, whole. A tool with no fitter, and a result its
// fitter declines, are left as they are.
func fit(c fitCall, res *mcp.CallToolResult) {
	size := TextSize(res)
	if size <= c.budget || c.lookup == nil {
		return
	}
	f := c.lookup(c.tool)
	if f == nil {
		return
	}
	part, extras := splitOwn(res, c.own)
	room := max(c.budget-blocksTextSize(extras), c.budget/2)
	if TextSize(part) <= room || !f.FitResult(c.tool, c.args, part, room) {
		return
	}
	res.StructuredContent = keepOuterKeys(part.StructuredContent, c.own, res.StructuredContent)
	res.Content = append(append([]mcp.Content{}, part.Content...), extras...)
	slog.Debug("resultbudget: fitted a result in its tool's shape",
		"tool", c.tool, "budget", c.budget, "rendered", size, "fitted", TextSize(res))
}

// splitOwn separates the tool's own part of res from the blocks the layers
// outside it appended. Without a capture the whole result is the tool's.
func splitOwn(res *mcp.CallToolResult, own *ownResult) (part *mcp.CallToolResult, extras []mcp.Content) {
	if own == nil || !own.captured || own.blocks > len(res.Content) {
		return &mcp.CallToolResult{Content: res.Content, StructuredContent: res.StructuredContent, Meta: res.Meta}, nil
	}
	head := make([]mcp.Content, own.blocks)
	copy(head, res.Content[:own.blocks])
	return &mcp.CallToolResult{Content: head, StructuredContent: own.structured, Meta: res.Meta},
		res.Content[own.blocks:]
}

// keepOuterKeys is the fitted structured content with the keys the layers
// outside the tool mirrored into the final one put back: a key in final
// that the tool's own output did not have. A key the fitter dropped from
// the tool's output stays dropped. Content that is not an object is
// returned as fitted.
func keepOuterKeys(fitted any, own *ownResult, final any) any {
	if own == nil || !own.captured {
		return fitted
	}
	var fittedMap, ownMap, finalMap map[string]json.RawMessage
	if !toolkit.DecodeStructured(fitted, &fittedMap) || !toolkit.DecodeStructured(final, &finalMap) {
		return fitted
	}
	_ = toolkit.DecodeStructured(own.structured, &ownMap)
	for k, v := range finalMap {
		if _, tools := ownMap[k]; !tools {
			fittedMap[k] = v
		}
	}
	merged, err := json.Marshal(fittedMap)
	if err != nil {
		return fitted
	}
	return json.RawMessage(merged)
}

// callArguments is the raw arguments of a tools/call request, or nil.
func callArguments(req mcp.Request) json.RawMessage {
	if req == nil {
		return nil
	}
	params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
	if !ok || params == nil {
		return nil
	}
	return params.Arguments
}

// TextSize is the size of a result's text: what a client renders.
func TextSize(res *mcp.CallToolResult) int {
	return blocksTextSize(res.Content)
}

// blocksTextSize is the size of the text among blocks.
func blocksTextSize(blocks []mcp.Content) int {
	n := 0
	for _, c := range blocks {
		if t, ok := c.(*mcp.TextContent); ok {
			n += len(t.Text)
		}
	}
	return n
}
