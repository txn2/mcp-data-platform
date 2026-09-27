// Package scriptcallsite names where in a managed script a host call was made
// (#1907), so a run's calls can be drawn on the card of the Flow diagram
// (#1906) that makes them.
//
// A call site is the source position of every call on the script's stack when
// the call was made, outermost first, each written "line:col" at the call's
// opening parenthesis: the top-level call of main() and every function call on
// the way down to the platform.* call itself. That is what the Starlark thread
// reports (Thread.CallStack) and what the flow graph records on each card, so
// the two meet on equality: a helper called from two places is two call sites
// and two cards.
//
// It travels three ways. Inside a run it rides the call's context (With,
// From). Across the in-process MCP session to the server it rides the request's
// _meta under MetaKey, which the audit middleware records on the call's audit
// row. And a failed run's backtrace names the site it failed at
// (FromBacktrace).
package scriptcallsite

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"go.starlark.net/starlark"
)

// MetaKey is the _meta key a script's tool call carries its call site under.
const MetaKey = "mcp-data-platform/call_site"

// Of is the call site of the call being made on thread: the positions of the
// script's frames, outermost first, leaving out the builtin being called. A
// builtin's frame has no position, which is how it is told from the script's;
// the script's own frames carry whatever name the script runs under.
func Of(thread *starlark.Thread) []string {
	if thread == nil {
		return nil
	}
	stack := thread.CallStack()
	out := make([]string, 0, len(stack))
	for _, f := range stack {
		if f.Pos.Line == 0 {
			continue
		}
		out = append(out, fmt.Sprintf("%d:%d", f.Pos.Line, f.Pos.Col))
	}
	return out
}

type ctxKey struct{}

// With returns ctx carrying a call site.
func With(ctx context.Context, site []string) context.Context {
	if len(site) == 0 {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, site)
}

// From is the call site ctx carries, or nil.
func From(ctx context.Context) []string {
	site, _ := ctx.Value(ctxKey{}).([]string)
	return site
}

// FromMeta reads a call site from a request's _meta, or nil when it carries
// none or carries something that is not one.
func FromMeta(meta map[string]any) []string {
	raw, ok := meta[MetaKey].([]any)
	if !ok {
		if typed, isTyped := meta[MetaKey].([]string); isTyped {
			return valid(typed)
		}
		return nil
	}
	site := make([]string, 0, len(raw))
	for _, v := range raw {
		s, isString := v.(string)
		if !isString {
			return nil
		}
		site = append(site, s)
	}
	return valid(site)
}

// sitePart is one "line:col" position.
var sitePart = regexp.MustCompile(`^[1-9]\d{0,6}:[1-9]\d{0,4}$`)

// maxDepth bounds a call site read from outside the process: a stack deeper
// than this is not one a script makes.
const maxDepth = 64

// valid keeps a call site only when every position is well formed.
func valid(site []string) []string {
	if len(site) == 0 || len(site) > maxDepth {
		return nil
	}
	for _, s := range site {
		if !sitePart.MatchString(s) {
			return nil
		}
	}
	return site
}

// frameLine is one frame of a Starlark backtrace with a position: a frame of
// the script, under whatever name it runs as. A builtin's frame has none.
var frameLine = regexp.MustCompile(`(?m)^\s*\S+:(\d+):(\d+): in `)

// FromBacktrace is the call site a failed run's backtrace names: the script
// frames it lists, outermost first. The last is the call the run failed in.
func FromBacktrace(text string) []string {
	var out []string
	for _, m := range frameLine.FindAllStringSubmatch(text, -1) {
		line, errLine := strconv.Atoi(m[1])
		col, errCol := strconv.Atoi(m[2])
		if errLine != nil || errCol != nil {
			continue
		}
		out = append(out, fmt.Sprintf("%d:%d", line, col))
	}
	return valid(out)
}
