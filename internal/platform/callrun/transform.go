package callrun

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
	"github.com/txn2/mcp-data-platform/pkg/audit"
)

// TransformError is a transform that failed a call (#2634): its script, and
// what went wrong, which the caller is told.
type TransformError struct {
	Script string
	Err    error
}

func (e *TransformError) Error() string {
	return fmt.Sprintf("the transform %s failed, so the call does not return its result: %v", e.Script, e.Err)
}

func (e *TransformError) Unwrap() error { return e.Err }

// ordered is the transforms in the order an administrator set, then by when
// each was added.
func ordered(list []callhandler.Handler) []callhandler.Handler {
	out := slices.Clone(list)
	slices.SortStableFunc(out, func(a, b callhandler.Handler) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), a.CreatedAt.Compare(b.CreatedAt))
	})
	return out
}

// Transform runs transforms over result in order and returns what the caller
// gets. A transform that fails fails the call, naming it, unless it chose to
// return what it was given. dry is the test panel's: nothing is recorded.
func (e *Evaluator) Transform(ctx context.Context, c *callhandler.Call, transforms []callhandler.Handler, result map[string]any, dry bool) (map[string]any, []audit.HandlerRun, error) {
	var runs []audit.HandlerRun
	for _, h := range ordered(transforms) {
		next, rec, err := e.transformOne(ctx, h, c, result, dry)
		runs = append(runs, rec)
		if err != nil {
			return nil, runs, err
		}
		result = next
	}
	return result, runs, nil
}

func (e *Evaluator) transformOne(ctx context.Context, h callhandler.Handler, c *callhandler.Call, result map[string]any, dry bool) (map[string]any, audit.HandlerRun, error) {
	callMap := CallMap(c, &h)
	rec := audit.HandlerRun{ID: h.ID, Kind: h.Kind, Script: e.scriptName(ctx, h)}
	var r run
	callValue, err := starlarkconv.ToStarlark(callMap)
	var resultValue starlark.Value
	if err == nil {
		resultValue, err = starlarkconv.ToStarlark(result)
	}
	if err == nil {
		r, err = e.invoke(ctx, h, callMap, entry{name: callhandler.TransformName, args: starlark.Tuple{callValue, resultValue}})
	}
	rec.MS = r.ms
	var out map[string]any
	if err == nil {
		out, err = readResult(r.value)
	}
	if err != nil {
		rec.Outcome = OutcomeError
		if !dry {
			e.recordFailure(ctx, h, c, r, err)
		}
		if h.OnFailure == callhandler.FailOriginal && len(h.Protected) == 0 {
			rec.Outcome = OutcomeOriginal
			return result, rec, nil
		}
		return nil, rec, &TransformError{Script: rec.Script, Err: err}
	}
	rec.Outcome = OutcomeTransformed
	return out, rec, nil
}

// readResult reads what transform(call, result) returned: the result, a dict.
func readResult(v starlark.Value) (map[string]any, error) {
	raw, err := starlarkconv.FromStarlark(v)
	if err != nil {
		return nil, fmt.Errorf("transform(call, result)'s value could not be read: %w", err)
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("transform(call, result) returned %s; return the result, a dict", typeName(v))
	}
	return m, nil
}

// resultMap is a tool result as a transform is given it: its structured
// content, else its text read as a JSON object, else {"text": text}.
func resultMap(res *mcp.CallToolResult) map[string]any {
	if res.StructuredContent != nil {
		if raw, err := json.Marshal(res.StructuredContent); err == nil {
			var m map[string]any
			if json.Unmarshal(raw, &m) == nil && m != nil {
				return m
			}
		}
	}
	text := firstText(res)
	var m map[string]any
	if json.Unmarshal([]byte(text), &m) == nil && m != nil {
		return m
	}
	return map[string]any{"text": text}
}

// replaceResult makes res carry what the transforms returned, in its
// structured content and as its one text block: every other block the tool
// wrote held the result before it was transformed.
func replaceResult(res *mcp.CallToolResult, out map[string]any) {
	raw, err := wirejson.Marshal(out)
	if err != nil {
		raw = []byte("{}")
	}
	if text, ok := out["text"].(string); ok && len(out) == 1 {
		res.Content = []mcp.Content{&mcp.TextContent{Text: text}}
		res.StructuredContent = nil
		return
	}
	res.Content = []mcp.Content{&mcp.TextContent{Text: string(raw)}}
	if res.StructuredContent != nil {
		res.StructuredContent = out
	}
}

func firstText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			return t.Text
		}
	}
	return ""
}

// transformer applies the transforms covering one call on the paths a result
// reaches its caller other than the tool result: an export's pages and a
// script's rows.
type transformer struct {
	e          *Evaluator
	call       *callhandler.Call
	transforms []callhandler.Handler
	record     func([]audit.HandlerRun)
}

// Rows is rows as the transforms leave them.
func (t *transformer) Rows(ctx context.Context, columns []string, rows []map[string]any) ([]map[string]any, error) {
	list := make([]any, 0, len(rows))
	for _, r := range rows {
		list = append(list, r)
	}
	out, runs, err := t.e.Transform(ctx, t.call, t.transforms, map[string]any{"columns": stringsAny(columns), "rows": list}, false)
	t.record(runs)
	if err != nil {
		return nil, err
	}
	got, _ := out["rows"].([]any)
	back := make([]map[string]any, 0, len(got))
	for _, g := range got {
		if m, ok := g.(map[string]any); ok {
			back = append(back, m)
		}
	}
	return back, nil
}

// Body is body as the transforms leave it.
func (t *transformer) Body(ctx context.Context, body any) (any, error) {
	out, runs, err := t.e.Transform(ctx, t.call, t.transforms, map[string]any{"body": body}, false)
	t.record(runs)
	if err != nil {
		return nil, err
	}
	return out["body"], nil
}

func stringsAny(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}
