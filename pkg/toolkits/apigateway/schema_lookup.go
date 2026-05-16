package apigateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolGetEndpointSchema is the MCP tool name for the per-endpoint
// detail lookup. Exported so audit code and tests reference the
// same literal as the registration site.
const ToolGetEndpointSchema = "api_get_endpoint_schema"

// GetEndpointSchemaInput is the parsed argument shape for the
// api_get_endpoint_schema tool.
type GetEndpointSchemaInput struct {
	Connection  string `json:"connection"`
	OperationID string `json:"operation_id"`
	Spec        string `json:"spec,omitempty"`
}

// EndpointSchemaOutput is the structured response. Fields are
// omitted from JSON when empty so the typical "GET /things"
// operation doesn't waste context on absent request_body or
// examples.
type EndpointSchemaOutput struct {
	Spec        string             `json:"spec,omitempty"`
	OperationID string             `json:"operation_id"`
	Method      string             `json:"method"`
	Path        string             `json:"path"`
	Summary     string             `json:"summary,omitempty"`
	Description string             `json:"description,omitempty"`
	Parameters  []ParameterDetail  `json:"parameters,omitempty"`
	RequestBody *RequestBodyDetail `json:"request_body,omitempty"`
	Responses   []ResponseDetail   `json:"responses,omitempty"`
	Examples    map[string]any     `json:"examples,omitempty"`
	Note        string             `json:"note,omitempty"`
}

// ParameterDetail mirrors OpenAPI's parameter shape, stripped to
// the fields the model needs to construct a call.
type ParameterDetail struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
	Schema      any    `json:"schema,omitempty"`
}

// RequestBodyDetail describes the request body shape.
type RequestBodyDetail struct {
	Required     bool           `json:"required,omitempty"`
	Description  string         `json:"description,omitempty"`
	ContentTypes []string       `json:"content_types,omitempty"`
	Schema       any            `json:"schema,omitempty"`
	Examples     map[string]any `json:"examples,omitempty"`
}

// ResponseDetail describes one response status's shape.
type ResponseDetail struct {
	Status       string         `json:"status"`
	Description  string         `json:"description,omitempty"`
	ContentTypes []string       `json:"content_types,omitempty"`
	Schema       any            `json:"schema,omitempty"`
	Examples     map[string]any `json:"examples,omitempty"`
}

// schemaCandidate is the disambiguation record returned when an
// operation_id resolves to more than one (spec, method, path) tuple.
type schemaCandidate struct {
	Spec   string `json:"spec"`
	Method string `json:"method"`
	Path   string `json:"path"`
}

// ambiguousSchemaError is the error-result payload for ambiguous
// operation_id. JSON-serialized into the tool's IsError response so
// the model can react programmatically.
type ambiguousSchemaError struct {
	Error      string            `json:"error"`
	Candidates []schemaCandidate `json:"candidates"`
}

// maxSchemaDepth caps how deep $ref-resolved schemas are walked
// before flattening. Without this, a recursive schema (a tree node
// referencing itself) would expand forever; kin-openapi resolves
// refs in-place, so following the pointer chain naively can blow
// the stack and the response size.
const maxSchemaDepth = 8

// maxResponseChars caps the marshaled response payload. Spec-heavy
// APIs (Salesforce, Microsoft Graph) routinely have multi-megabyte
// schemas; surfacing one would devour the model's context. The
// truncation note tells the model that a partial result was
// returned so it can fall back to api_invoke_endpoint to probe
// shape.
const maxResponseChars = 50000

func (t *Toolkit) handleGetEndpointSchema(_ context.Context, _ *mcp.CallToolRequest, in GetEndpointSchemaInput) (*mcp.CallToolResult, any, error) {
	if in.Connection == "" {
		return errorResult("connection is required"), nil, nil
	}
	if in.OperationID == "" {
		return errorResult("operation_id is required"), nil, nil
	}
	t.mu.RLock()
	c, ok := t.connections[in.Connection]
	t.mu.RUnlock()
	if !ok {
		return errorResult(fmt.Sprintf("connection %q not found", in.Connection)), nil, nil
	}
	if len(c.specs) == 0 {
		return errorResult("connection has no catalog specs configured"), nil, nil
	}
	match, candidates := resolveOperation(c, in.OperationID, in.Spec)
	if match == nil {
		if len(candidates) > 1 {
			return ambiguousResult(in.OperationID, candidates), nil, nil
		}
		return errorResult(fmt.Sprintf("operation_id %q not found", in.OperationID)), nil, nil
	}
	out := buildEndpointSchemaOutput(match)
	return cappedJSONResult(out), out, nil
}

// operationMatch carries the resolved operation plus the surrounding
// metadata the formatter needs (the component spec name and the
// path it was found at — the *openapi3.Operation itself doesn't
// carry the path).
type operationMatch struct {
	specName string
	method   string
	path     string
	op       *openapi3.Operation
}

// resolveOperation walks the connection's parsed specs looking for
// the requested operation_id. When the operator omits spec and the
// id resolves to multiple matches, returns nil + candidates so the
// caller emits the ambiguity error.
func resolveOperation(c *conn, operationID, specFilter string) (*operationMatch, []schemaCandidate) {
	matches, candidates := collectOperationMatches(c, operationID, specFilter)
	switch {
	case len(matches) == 1:
		return matches[0], nil
	case len(matches) > 1:
		sortCandidates(candidates)
		return nil, candidates
	}
	return nil, nil
}

// collectOperationMatches iterates every component spec on the
// connection (filtered by specFilter when non-empty) and returns
// the operations whose id matches operationID, plus their
// candidate-record form. Extracted so resolveOperation stays under
// the cognitive-complexity ceiling.
func collectOperationMatches(c *conn, operationID, specFilter string) ([]*operationMatch, []schemaCandidate) {
	var (
		matches    []*operationMatch
		candidates []schemaCandidate
	)
	for specName, st := range c.specs {
		if specFilter != "" && specName != specFilter {
			continue
		}
		basePath := st.effectiveBasePath
		walkOperations(st.doc, func(method, path string, op *openapi3.Operation) {
			fullPath := basePath + path
			id := op.OperationID
			if id == "" {
				// Synthesized id must match what buildOperationIndex
				// emits or the operationID returned by
				// api_list_endpoints will not resolve here. Both
				// sites use METHOD + " " + full upstream path.
				id = method + " " + fullPath
			}
			if id != operationID {
				return
			}
			matches = append(matches, &operationMatch{
				specName: specName, method: method, path: fullPath, op: op,
			})
			candidates = append(candidates, schemaCandidate{
				Spec: specName, Method: method, Path: fullPath,
			})
		})
	}
	return matches, candidates
}

// sortCandidates orders the ambiguity-error candidate list for
// stable output across runs.
func sortCandidates(candidates []schemaCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Spec != candidates[j].Spec {
			return candidates[i].Spec < candidates[j].Spec
		}
		if candidates[i].Method != candidates[j].Method {
			return candidates[i].Method < candidates[j].Method
		}
		return candidates[i].Path < candidates[j].Path
	})
}

// walkOperations invokes fn for every method/path/operation in doc.
// Centralizes the iteration so resolveOperation doesn't duplicate
// the verb loop that buildOperationIndex uses.
func walkOperations(doc *openapi3.T, fn func(method, path string, op *openapi3.Operation)) {
	if doc == nil || doc.Paths == nil {
		return
	}
	for path, item := range doc.Paths.Map() {
		if item == nil {
			continue
		}
		for _, m := range pathItemMethods {
			if op := m.get(item); op != nil {
				fn(m.method, path, op)
			}
		}
	}
}

// buildEndpointSchemaOutput composes the response payload from the
// resolved operation, stripping security/server metadata and
// flattening schemas to a fixed depth.
//
// m.path is already the full upstream path (the spec's base path
// prepended at collectOperationMatches time) so the output's Path
// field agrees with the path reported by api_list_endpoints for
// the same operation. Same for the synthesized OperationID.
func buildEndpointSchemaOutput(m *operationMatch) EndpointSchemaOutput {
	out := EndpointSchemaOutput{
		Spec:        m.specName,
		OperationID: m.op.OperationID,
		Method:      m.method,
		Path:        m.path,
		Summary:     m.op.Summary,
		Description: m.op.Description,
	}
	if out.OperationID == "" {
		out.OperationID = m.method + " " + m.path
	}
	out.Parameters = flattenParameters(m.op.Parameters)
	out.RequestBody = flattenRequestBody(m.op.RequestBody)
	out.Responses = flattenResponses(m.op.Responses)
	return out
}

// flattenParameters reduces each parameter to the slim shape the
// model needs. Vendor extensions (x-*) and full $ref-chained
// schemas are flattened to depth-capped maps.
func flattenParameters(params openapi3.Parameters) []ParameterDetail {
	out := make([]ParameterDetail, 0, len(params))
	for _, ref := range params {
		if ref == nil || ref.Value == nil {
			continue
		}
		p := ref.Value
		out = append(out, ParameterDetail{
			Name:        p.Name,
			In:          p.In,
			Required:    p.Required,
			Description: p.Description,
			Schema:      schemaToValue(p.Schema, 0),
		})
	}
	return out
}

// flattenRequestBody returns nil when the operation has no request
// body, otherwise a slim representation with content-types listed.
//
// When the operation declares multiple content types we pick the
// schema deterministically: application/json wins when present
// (the dominant case), otherwise the alphabetically-first
// content-type. Without this, Go's randomized map iteration would
// return different schemas across calls for the same operation —
// flaky model behavior with no diagnostic.
func flattenRequestBody(ref *openapi3.RequestBodyRef) *RequestBodyDetail {
	if ref == nil || ref.Value == nil {
		return nil
	}
	rb := ref.Value
	out := &RequestBodyDetail{
		Required:    rb.Required,
		Description: rb.Description,
	}
	for ct := range rb.Content {
		out.ContentTypes = append(out.ContentTypes, ct)
	}
	sort.Strings(out.ContentTypes)
	if pick := preferredContentType(out.ContentTypes); pick != "" {
		if mt := rb.Content[pick]; mt != nil {
			out.Schema = schemaToValue(mt.Schema, 0)
			if len(mt.Examples) > 0 {
				out.Examples = flattenExamples(mt.Examples)
			}
		}
	}
	return out
}

// flattenResponses returns one ResponseDetail per status code.
// Stable status-code ordering keeps the JSON output diff-friendly
// across runs.
func flattenResponses(responses *openapi3.Responses) []ResponseDetail {
	if responses == nil {
		return nil
	}
	out := make([]ResponseDetail, 0, len(responses.Map()))
	for status, ref := range responses.Map() {
		if ref == nil || ref.Value == nil {
			continue
		}
		out = append(out, buildResponseDetail(status, ref.Value))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Status < out[j].Status })
	return out
}

// buildResponseDetail constructs the per-status ResponseDetail used
// by flattenResponses. Schema selection uses the same deterministic
// content-type preference as flattenRequestBody.
func buildResponseDetail(status string, r *openapi3.Response) ResponseDetail {
	detail := ResponseDetail{Status: status}
	if r.Description != nil {
		detail.Description = *r.Description
	}
	for ct := range r.Content {
		detail.ContentTypes = append(detail.ContentTypes, ct)
	}
	sort.Strings(detail.ContentTypes)
	if pick := preferredContentType(detail.ContentTypes); pick != "" {
		if mt := r.Content[pick]; mt != nil {
			detail.Schema = schemaToValue(mt.Schema, 0)
			if len(mt.Examples) > 0 {
				detail.Examples = flattenExamples(mt.Examples)
			}
		}
	}
	return detail
}

// preferredContentType returns the content-type from sorted whose
// schema we should surface. application/json wins when present (the
// vast majority of REST upstreams), otherwise the first sorted
// entry. Empty input returns "" so the caller can short-circuit.
func preferredContentType(sorted []string) string {
	if len(sorted) == 0 {
		return ""
	}
	for _, ct := range sorted {
		if ct == "application/json" {
			return ct
		}
	}
	return sorted[0]
}

// flattenExamples coerces openapi3.ExampleRef map into a plain map
// the JSON encoder can handle directly.
func flattenExamples(ex map[string]*openapi3.ExampleRef) map[string]any {
	out := make(map[string]any, len(ex))
	for k, ref := range ex {
		if ref == nil || ref.Value == nil {
			continue
		}
		out[k] = ref.Value.Value
	}
	return out
}

// schemaToValue converts an openapi3.SchemaRef into a plain
// map/slice tree the JSON encoder can serialize. Recurses up to
// maxSchemaDepth, replacing deeper nodes with a {"truncated": true}
// stub so a recursive type doesn't blow context or stack.
func schemaToValue(ref *openapi3.SchemaRef, depth int) any {
	if ref == nil || ref.Value == nil {
		return nil
	}
	if depth >= maxSchemaDepth {
		return map[string]any{"truncated": true, "reason": "max depth reached"}
	}
	out := map[string]any{}
	populateSchemaScalars(out, ref.Value)
	populateSchemaCompounds(out, ref.Value, depth)
	return out
}

// populateSchemaScalars copies the scalar-valued OpenAPI schema
// fields (type, format, default, enum, required, example) into out.
// Kept separate from compound (Properties, Items) walks so
// schemaToValue stays under the cognitive-complexity gate.
func populateSchemaScalars(out map[string]any, s *openapi3.Schema) {
	if types := s.Type.Slice(); len(types) > 0 {
		if len(types) == 1 {
			out["type"] = types[0]
		} else {
			out["type"] = types
		}
	}
	addStringIfPresent(out, "format", s.Format)
	addStringIfPresent(out, "description", s.Description)
	if s.Default != nil {
		out["default"] = s.Default
	}
	if len(s.Enum) > 0 {
		out["enum"] = s.Enum
	}
	if len(s.Required) > 0 {
		out["required"] = s.Required
	}
	if s.Example != nil {
		out["example"] = s.Example
	}
}

// populateSchemaCompounds recurses into Properties and Items at
// depth+1 — the recursion that the maxSchemaDepth guard caps.
func populateSchemaCompounds(out map[string]any, s *openapi3.Schema, depth int) {
	if len(s.Properties) > 0 {
		props := make(map[string]any, len(s.Properties))
		for name, sub := range s.Properties {
			props[name] = schemaToValue(sub, depth+1)
		}
		out["properties"] = props
	}
	if s.Items != nil {
		out["items"] = schemaToValue(s.Items, depth+1)
	}
}

// addStringIfPresent skips zero values so the marshaled schema
// stays slim.
func addStringIfPresent(m map[string]any, key, value string) {
	if value != "" {
		m[key] = value
	}
}

// cappedJSONResult marshals out and returns a tool result with the
// JSON body truncated to maxResponseChars when needed. The note
// field is patched onto the output before marshal so the model can
// see truncation happened.
func cappedJSONResult(out EndpointSchemaOutput) *mcp.CallToolResult {
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return errorResult("internal: marshal endpoint schema: " + err.Error())
	}
	if len(encoded) <= maxResponseChars {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
		}
	}
	// Re-marshal with a truncation note. Drop the bulky parameters /
	// response schemas and keep the surface fields; the model can
	// fall back to api_invoke_endpoint to probe.
	out.Parameters = nil
	out.RequestBody = nil
	out.Responses = nil
	out.Note = fmt.Sprintf("schema details elided (full size %d chars exceeds %d-char cap)",
		len(encoded), maxResponseChars)
	encoded, _ = json.MarshalIndent(out, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
	}
}

// ambiguousResult builds the structured error for the
// "operation_id appears in N specs" case. The candidate list is
// alphabetized to keep the response stable across runs.
func ambiguousResult(operationID string, candidates []schemaCandidate) *mcp.CallToolResult {
	payload := ambiguousSchemaError{
		Error:      fmt.Sprintf("operation_id %q is ambiguous; pass spec to disambiguate", operationID),
		Candidates: candidates,
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return errorResult("operation_id is ambiguous")
	}
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
	}
}
