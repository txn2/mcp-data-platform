package refscheme

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Middleware is the MCP boundary of the scheme: every reference a request
// carries under the deployment's scheme reaches the platform in the canonical
// form, and every reference a result carries leaves under the deployment's
// scheme: tool results, tool descriptions and schemas, prompts, resources and
// the server's instructions. With no scheme set it passes everything through
// untouched.
func Middleware() mcp.Middleware {
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
			return displayResult(res), nil
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

// displayResult is a result with its references under the deployment's
// scheme. A tool result is rewritten in place, its parts one by one; any other
// result is rewritten through its JSON, into a new value of its type, so
// nothing the server holds (a registered tool's description) is changed.
func displayResult(res mcp.Result) mcp.Result {
	if r, ok := res.(*mcp.CallToolResult); ok {
		displayToolResult(r)
		return r
	}
	raw, err := json.Marshal(res)
	if err != nil || !bytes.Contains(raw, []byte(Prefix)) {
		return res
	}
	fixed := DisplayBytes(raw)
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

func displayToolResult(r *mcp.CallToolResult) {
	for _, c := range r.Content {
		switch v := c.(type) {
		case *mcp.TextContent:
			v.Text = Display(v.Text)
		case *mcp.EmbeddedResource:
			if v.Resource != nil {
				v.Resource.URI, v.Resource.Text = Display(v.Resource.URI), Display(v.Resource.Text)
			}
		case *mcp.ResourceLink:
			v.URI, v.Description = Display(v.URI), Display(v.Description)
		}
	}
	if r.StructuredContent == nil {
		return
	}
	raw, err := json.Marshal(r.StructuredContent)
	if err != nil || !bytes.Contains(raw, []byte(Prefix)) {
		return
	}
	var out any
	if json.Unmarshal(DisplayBytes(raw), &out) == nil {
		r.StructuredContent = out
	}
}
