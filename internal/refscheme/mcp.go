package refscheme

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Middleware is the MCP boundary of the scheme: every reference a request
// carries under the deployment's scheme reaches the platform in the canonical
// form, and every reference a result carries leaves under the deployment's
// scheme: tool results, tool descriptions and schemas, prompts, resources and
// the server's instructions. The platform's own wording about the reference
// form is rewritten too (Prose, #2818) wherever the text is the platform's:
// tools/list, the server's instructions, a refusal, and the result of each
// tool named in platformText (platform_info, whose agent instructions are the
// platform's). With no scheme set it passes everything through untouched.
func Middleware(platformText ...string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if !Rewrites() {
				return next(ctx, method, req)
			}
			canonRequest(req)
			res, err := next(ctx, method, req)
			if err != nil || res == nil {
				return res, err
			}
			return displayResult(res, platformTextTool(req, platformText)), nil
		}
	}
}

// canonRequest writes the references a request names in the canonical form.
func canonRequest(req mcp.Request) {
	if req == nil {
		return
	}
	switch p := req.GetParams().(type) {
	case *mcp.CallToolParamsRaw:
		p.Arguments = CanonBytes(p.Arguments)
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
func displayResult(res mcp.Result, platformText bool) mcp.Result {
	if r, ok := res.(*mcp.CallToolResult); ok {
		displayToolResult(r, platformText || r.IsError)
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

func displayToolResult(r *mcp.CallToolResult, platformText bool) {
	text, textBytes := Display, DisplayBytes
	if platformText {
		text, textBytes = Prose, ProseBytes
	}
	for _, c := range r.Content {
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
	if json.Unmarshal(textBytes(raw), &out) == nil {
		r.StructuredContent = out
	}
}
