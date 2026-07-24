// Package epmcp builds the study's b0 baseline (issue #1027): an MCP
// server exposing one tool per OpenAPI operation, the market-default
// pattern (Azure APIM / Kong / FastMCP style). It is generated at startup
// from the identical spec fixture the b1 arms register with the platform,
// and every tool call proxies to the fixture HTTP service. Tool count
// therefore equals catalog size, which is exactly the variable the study
// measures.
package epmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxResponseBytes caps a proxied response body.
const maxResponseBytes = 10 << 20

// requestTimeout caps one proxied HTTP call.
const requestTimeout = 30 * time.Second

// Options configures the generated server.
type Options struct {
	// TargetBaseURL is the fixture service base URL, e.g.
	// "http://127.0.0.1:8110".
	TargetBaseURL string
	// APIKey is sent as X-API-Key on every proxied call.
	APIKey string
	// HTTPClient overrides the outbound client (tests). Nil uses a
	// default client with requestTimeout.
	HTTPClient *http.Client
}

// operation is one spec operation compiled for proxying.
type operation struct {
	method     string // uppercase
	path       string // template with {param} segments
	pathParams []string
	query      []string
	bodyFields []string
}

// BuildServer parses the spec and returns an MCP server with one tool per
// operation.
func BuildServer(specJSON []byte, opts Options) (*mcp.Server, error) {
	loader := &openapi3.Loader{Context: context.Background(), IsExternalRefsAllowed: false}
	doc, err := loader.LoadFromData(specJSON)
	if err != nil {
		return nil, fmt.Errorf("epmcp: parse spec: %w", err)
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: requestTimeout}
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "epmcp", Version: "0.1.0"}, nil)
	for _, path := range sortedPaths(doc) {
		item := doc.Paths.Value(path)
		for method, op := range item.Operations() {
			if err := addOperationTool(server, path, method, op, opts); err != nil {
				return nil, err
			}
		}
	}
	return server, nil
}

// sortedPaths returns the document's paths in deterministic order.
func sortedPaths(doc *openapi3.T) []string {
	paths := make([]string, 0, doc.Paths.Len())
	for p := range doc.Paths.Map() {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// addOperationTool registers one operation as an MCP tool.
func addOperationTool(server *mcp.Server, path, method string, op *openapi3.Operation, opts Options) error {
	if op.OperationID == "" {
		return fmt.Errorf("epmcp: %s %s has no operationId", method, path)
	}
	compiled := operation{method: strings.ToUpper(method), path: path}
	schema := &jsonschema.Schema{
		Type:       "object",
		Properties: map[string]*jsonschema.Schema{},
	}
	for _, pref := range op.Parameters {
		p := pref.Value
		schema.Properties[p.Name] = paramSchema(p)
		switch p.In {
		case "path":
			compiled.pathParams = append(compiled.pathParams, p.Name)
			schema.Required = append(schema.Required, p.Name)
		case "query":
			compiled.query = append(compiled.query, p.Name)
			if p.Required {
				schema.Required = append(schema.Required, p.Name)
			}
		}
	}
	for name, prop := range bodyProperties(op) {
		if _, clash := schema.Properties[name]; clash {
			return fmt.Errorf("epmcp: %s: body field %s collides with a parameter", op.OperationID, name)
		}
		compiled.bodyFields = append(compiled.bodyFields, name)
		schema.Properties[name] = prop
	}
	sort.Strings(compiled.bodyFields)
	tool := &mcp.Tool{
		Name:        op.OperationID,
		Description: op.Summary,
		InputSchema: schema,
	}
	server.AddTool(tool, proxyHandler(compiled, opts))
	return nil
}

// paramSchema maps one OpenAPI parameter to a JSON schema property.
func paramSchema(p *openapi3.Parameter) *jsonschema.Schema {
	s := &jsonschema.Schema{Type: "string", Description: p.Description}
	if p.Schema != nil && p.Schema.Value != nil {
		if t := p.Schema.Value.Type; t != nil && len(*t) > 0 {
			s.Type = (*t)[0]
		}
	}
	return s
}

// bodyProperties extracts the request body's object properties.
func bodyProperties(op *openapi3.Operation) map[string]*jsonschema.Schema {
	if op.RequestBody == nil || op.RequestBody.Value == nil {
		return nil
	}
	media := op.RequestBody.Value.Content.Get("application/json")
	if media == nil || media.Schema == nil || media.Schema.Value == nil {
		return nil
	}
	out := map[string]*jsonschema.Schema{}
	for name, propRef := range media.Schema.Value.Properties {
		prop := &jsonschema.Schema{Type: "string"}
		if propRef.Value != nil {
			if t := propRef.Value.Type; t != nil && len(*t) > 0 {
				prop.Type = (*t)[0]
			}
			prop.Description = propRef.Value.Description
		}
		out[name] = prop
	}
	return out
}

// proxyHandler returns the tool handler that forwards one call to the
// fixture service.
func proxyHandler(op operation, opts Options) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args map[string]any
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return errorResult("invalid arguments: " + err.Error()), nil
			}
		}
		httpReq, err := buildRequest(ctx, op, opts, args)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		res, err := opts.HTTPClient.Do(httpReq)
		if err != nil {
			return errorResult("upstream request failed: " + err.Error()), nil
		}
		defer func() { _ = res.Body.Close() }()
		body, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes))
		if err != nil {
			return errorResult("read upstream response: " + err.Error()), nil
		}
		text := strings.TrimSpace(string(body))
		if res.StatusCode >= http.StatusBadRequest {
			return errorResult(fmt.Sprintf("HTTP %d: %s", res.StatusCode, text)), nil
		}
		if text == "" {
			text = fmt.Sprintf("HTTP %d", res.StatusCode)
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
	}
}

// buildRequest assembles the proxied HTTP request from tool arguments.
func buildRequest(ctx context.Context, op operation, opts Options, args map[string]any) (*http.Request, error) {
	target, err := buildURL(op, opts, args)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if payload := bodyPayload(op, args); payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, op.method, target, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if opts.APIKey != "" {
		req.Header.Set("X-API-Key", opts.APIKey)
	}
	return req, nil
}

// buildURL renders the target URL: path parameters substituted, query
// parameters appended.
func buildURL(op operation, opts Options, args map[string]any) (string, error) {
	path := op.path
	for _, name := range op.pathParams {
		v, ok := args[name]
		if !ok {
			return "", fmt.Errorf("missing required path parameter %s", name)
		}
		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(fmt.Sprint(v)))
	}
	q := url.Values{}
	for _, name := range op.query {
		if v, ok := args[name]; ok {
			q.Set(name, fmt.Sprint(v))
		}
	}
	target := opts.TargetBaseURL + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	return target, nil
}

// bodyPayload collects the body fields present in the arguments. Returns
// nil when the operation has no body fields; an empty object when it has
// body fields but none were supplied (the upstream validates
// requiredness).
func bodyPayload(op operation, args map[string]any) map[string]any {
	if len(op.bodyFields) == 0 {
		return nil
	}
	payload := map[string]any{}
	for _, name := range op.bodyFields {
		if v, ok := args[name]; ok {
			payload[name] = v
		}
	}
	return payload
}

// errorResult renders a tool error.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}
