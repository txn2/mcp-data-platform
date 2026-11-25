package refscheme

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options is what the MCP boundary needs to know about the platform.
type Options struct {
	// PlatformText names the tools whose whole result is the platform's own
	// text (platform_info), rewritten with Prose (#2818).
	PlatformText []string
	// Canonical reports a session that receives results in the canonical
	// form: a managed script's (#2788). A script is stored code written
	// against the mcp: form, so a result under the deployment's scheme broke
	// every comparison with a literal mcp: prefix the moment an operator set
	// the key. Its requests are still made canonical.
	Canonical func(context.Context) bool
	// Verbatim names, per tool, the argument and result fields that hold code
	// and are neither canonicalized nor displayed (#2788): a script's source
	// is a tool argument, and rewriting it stored s.replace("acme://", ...)
	// as s.replace("mcp://", ...).
	Verbatim map[string][]string
}

// Middleware is the MCP boundary of the scheme: every reference a request
// carries under the deployment's scheme reaches the platform in the canonical
// form, and every reference a result carries leaves under the deployment's
// scheme: tool results, tool descriptions and schemas, prompts, resources and
// the server's instructions. The platform's own wording about the reference
// form is rewritten too (Prose, #2818) wherever the text is the platform's:
// tools/list, the server's instructions, a refusal, and the result of each
// tool named in PlatformText. A script's session reads results canonical, and
// the fields Verbatim names pass both ways untouched (#2788). With no scheme
// set it passes everything through untouched.
func Middleware(o Options) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if !Rewrites() {
				return next(ctx, method, req)
			}
			verbatim := o.verbatimFor(req)
			canonRequest(req, verbatim)
			res, err := next(ctx, method, req)
			if err != nil || res == nil {
				return res, err
			}
			if o.Canonical != nil && o.Canonical(ctx) {
				return res, nil
			}
			return displayResult(res, platformTextTool(req, o.PlatformText), verbatim), nil
		}
	}
}

// verbatimFor is the set of fields a tools/call request's tool keeps
// verbatim, nil for none.
func (o Options) verbatimFor(req mcp.Request) map[string]bool {
	if req == nil || len(o.Verbatim) == 0 {
		return nil
	}
	p, ok := req.GetParams().(*mcp.CallToolParamsRaw)
	if !ok || p == nil {
		return nil
	}
	keys := o.Verbatim[p.Name]
	if len(keys) == 0 {
		return nil
	}
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return set
}

// canonRequest writes the references a request names in the canonical form,
// leaving the verbatim fields as sent.
func canonRequest(req mcp.Request, verbatim map[string]bool) {
	if req == nil {
		return
	}
	switch p := req.GetParams().(type) {
	case *mcp.CallToolParamsRaw:
		p.Arguments = rewriteJSONBytes(p.Arguments, CanonBytes, Canon, verbatim)
	case *mcp.CallToolParams:
		p.Arguments = canonValue(p.Arguments)
	case *mcp.ReadResourceParams:
		p.URI = Canon(p.URI)
	case *mcp.GetPromptParams:
		for k, v := range p.Arguments {
			p.Arguments[k] = Canon(v)
		}
	}
}

// rewriteJSONBytes rewrites a JSON document's references with whole, or,
// when verbatim names fields, its strings one by one with each outside those
// fields' values, so code in them is never rewritten.
func rewriteJSONBytes(raw []byte, whole func([]byte) []byte, each func(string) string, verbatim map[string]bool) []byte {
	if len(verbatim) == 0 {
		return whole(raw)
	}
	v, ok := decodeNumbers(raw)
	if !ok {
		return whole(raw)
	}
	out, err := json.Marshal(rewriteExcept(v, each, verbatim))
	if err != nil {
		return raw
	}
	return out
}

// rewriteExcept is v with each string rewritten, the values of the verbatim
// keys left as they are at any depth.
func rewriteExcept(v any, each func(string) string, verbatim map[string]bool) any {
	switch t := v.(type) {
	case string:
		return each(t)
	case []any:
		for i := range t {
			t[i] = rewriteExcept(t[i], each, verbatim)
		}
		return t
	case map[string]any:
		for k, val := range t {
			if verbatim[k] {
				continue
			}
			t[k] = rewriteExcept(val, each, verbatim)
		}
		return t
	}
	return v
}

// canonValue is a decoded value with its references canonical.
func canonValue(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	fixed := CanonBytes(raw)
	if bytes.Equal(fixed, raw) {
		return v
	}
	var out any
	if json.Unmarshal(fixed, &out) != nil {
		return v
	}
	return out
}

// platformTextTool reports whether a request calls a tool whose whole result
// is the platform's own text.
func platformTextTool(req mcp.Request, tools []string) bool {
	if req == nil || len(tools) == 0 {
		return false
	}
	p, ok := req.GetParams().(*mcp.CallToolParamsRaw)
	if !ok || p == nil {
		return false
	}
	return slices.Contains(tools, p.Name)
}

// displayResult is a result with its references under the deployment's
// scheme. A tool result is rewritten in place, its parts one by one; any other
// result is rewritten through its JSON, into a new value of its type, so
// nothing the server holds (a registered tool's description) is changed.
//
// The platform's wording about the reference form is rewritten too (Prose) in
// tools/list and the initialize result, in a refusal, and in the result of a
// tool whose result is the platform's text (platformText); a prompt's or a
// tool's data keeps the rule for whole references alone.
func displayResult(res mcp.Result, platformText bool, verbatim map[string]bool) mcp.Result {
	if r, ok := res.(*mcp.CallToolResult); ok {
		displayToolResult(r, platformText || r.IsError, verbatim)
		return r
	}
	raw, err := json.Marshal(res)
	if err != nil || !bytes.Contains(raw, []byte(Prefix)) {
		return res
	}
	rewrite := DisplayBytes
	switch res.(type) {
	case *mcp.ListToolsResult, *mcp.InitializeResult:
		rewrite = ProseBytes
	}
	fixed := rewrite(raw)
	if bytes.Equal(fixed, raw) {
		return res
	}
	t := reflect.TypeOf(res)
	if t.Kind() != reflect.Pointer {
		return res
	}
	out, ok := reflect.TypeAssert[mcp.Result](reflect.New(t.Elem()))
	if !ok || json.Unmarshal(fixed, out) != nil {
		return res
	}
	return out
}

// displayContent rewrites one content block: text with the rewrite the
// result's kind takes, a resource's address and text as references.
func displayContent(c mcp.Content, text func(string) string) {
	switch v := c.(type) {
	case *mcp.TextContent:
		v.Text = text(v.Text)
	case *mcp.EmbeddedResource:
		if v.Resource != nil {
			v.Resource.URI, v.Resource.Text = Display(v.Resource.URI), Display(v.Resource.Text)
		}
	case *mcp.ResourceLink:
		v.URI, v.Description = Display(v.URI), Display(v.Description)
	}
}

func displayToolResult(r *mcp.CallToolResult, platformText bool, verbatim map[string]bool) {
	text, textBytes := Display, DisplayBytes
	if platformText {
		text, textBytes = Prose, ProseBytes
	}
	for _, c := range r.Content {
		if t, ok := c.(*mcp.TextContent); ok && len(verbatim) > 0 {
			t.Text = displayTextExcept(t.Text, text, verbatim)
			continue
		}
		displayContent(c, text)
	}
	if r.StructuredContent == nil {
		return
	}
	raw, err := json.Marshal(r.StructuredContent)
	if err != nil || !bytes.Contains(raw, []byte(Prefix)) {
		return
	}
	var out any
	if json.Unmarshal(rewriteJSONBytes(raw, textBytes, text, verbatim), &out) == nil {
		r.StructuredContent = out
	}
}

// displayTextExcept is a text block of a tool that keeps fields verbatim: a
// JSON object is rewritten field by field, as the structured content is, and
// any other text as a whole.
func displayTextExcept(s string, text func(string) string, verbatim map[string]bool) string {
	v, ok := decodeNumbers([]byte(s))
	if _, object := v.(map[string]any); !ok || !object {
		return text(s)
	}
	out, err := json.MarshalIndent(rewriteExcept(v, text, verbatim), "", "  ")
	if err != nil {
		return s
	}
	return string(out)
}

// decodeNumbers decodes one JSON document keeping each number as written, so
// re-encoding it after a rewrite changes no number (an integer past 2^53
// decoded as a float64 would).
func decodeNumbers(raw []byte) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil || dec.More() {
		return nil, false
	}
	return v, true
}
