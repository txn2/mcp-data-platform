package apigateway

import (
	"context"
	"log/slog"
	"sort"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

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
	// SavedExamples are requests promoted from real calls against this
	// connection (#1321). They differ from Examples, which are whatever the
	// spec's author declared: these are known to have worked here.
	SavedExamples []catalog.Example `json:"saved_examples,omitempty"`
	Note          string            `json:"note,omitempty"`
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
	Status       string                  `json:"status"`
	Description  string                  `json:"description,omitempty"`
	ContentTypes []string                `json:"content_types,omitempty"`
	Headers      map[string]HeaderDetail `json:"headers,omitempty"`
	Schema       any                     `json:"schema,omitempty"`
	Examples     map[string]any          `json:"examples,omitempty"`
}

// HeaderDetail is the slim shape used to surface response headers
// (Location on redirects, Retry-After on rate-limited responses,
// Link/ETag on cache-aware endpoints) so callers know which headers
// to read off the upstream response.
type HeaderDetail struct {
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Schema      any    `json:"schema,omitempty"`
}

// schemaCandidate is the disambiguation record returned when an
// operation_id resolves to more than one (spec, method, path) tuple.
type schemaCandidate struct {
	Spec   string `json:"spec"`
	Method string `json:"method"`
	Path   string `json:"path"`
}

// maxSchemaDepth caps how deep $ref-resolved schemas are walked
// before flattening. Without this, a recursive schema (a tree node
// referencing itself) would expand forever; kin-openapi resolves
// refs in-place, so following the pointer chain naively can blow
// the stack and the response size.
const maxSchemaDepth = 8

// savedExamples reads the requests promoted on this endpoint. It is
// best-effort: an unreadable example store costs the reader the examples, never
// the schema they asked for.
func (t *Toolkit) savedExamples(ctx context.Context, connection, operationID string) []catalog.Example {
	t.mu.RLock()
	store := t.exampleStore
	t.mu.RUnlock()
	if store == nil {
		return nil
	}
	examples, err := store.ListExamples(ctx, connection, operationID)
	if err != nil {
		slog.Warn("api_discover: saved examples unavailable",
			"connection", connection, "operation_id", operationID, "error", err)
		return nil
	}
	return examples
}

// operationMatch carries the resolved operation plus the surrounding
// metadata the formatter needs (the component spec name and the
// path it was found at — the *openapi3.Operation itself doesn't
// carry the path).
type operationMatch struct {
	specName string
	method   string
	path     string // full runtime path: effectiveBasePath + spec rawPath
	rawPath  string // spec-relative path, used to synthesize the operationId
	op       *openapi3.Operation
}

// resolveOperation walks the supplied parsed specs looking for the
// requested operation_id. When the operator omits spec and the id
// resolves to multiple matches, returns nil + candidates so the
// caller emits the ambiguity error.
//
// It takes the spec map rather than the connection because the browse
// surface resolves an operation out of a stored catalog spec that no
// connection references yet (#1478); the connection's own lookup passes
// c.specs and is otherwise unchanged.
func resolveOperation(specs map[string]*specState, operationID, specFilter string) (*operationMatch, []schemaCandidate) {
	matches, candidates := collectOperationMatches(specs, operationID, specFilter)
	switch {
	case len(matches) == 1:
		return matches[0], nil
	case len(matches) > 1:
		sortCandidates(candidates)
		return nil, candidates
	}
	return nil, nil
}

// collectOperationMatches iterates every supplied component spec
// (filtered by specFilter when non-empty) and returns the operations
// whose id matches operationID, plus their candidate-record form.
// Extracted so resolveOperation stays under the cognitive-complexity
// ceiling.
func collectOperationMatches(specs map[string]*specState, operationID, specFilter string) ([]*operationMatch, []schemaCandidate) {
	var (
		matches    []*operationMatch
		candidates []schemaCandidate
	)
	for specName, st := range specs {
		if specFilter != "" && specName != specFilter {
			continue
		}
		basePath := st.effectiveBasePath
		walkOperations(st.doc, func(method, path string, op *openapi3.Operation) {
			fullPath := basePath + path
			id := op.OperationID
			if id == "" {
				// Synthesize from the spec-relative path, NOT the
				// basePath-prefixed fullPath. api_discover
				// (appendItemOperations) advertises the id built from
				// the spec-relative rawPath so the id stays a property
				// of the spec content alone. Matching here on fullPath
				// instead severed every synthesized-id lookup on any
				// connection with a non-empty effectiveBasePath — the
				// built-in platform-admin spec (base path /api/v1) hit
				// this squarely: the operations level advertised
				// "GET /admin/personas" while this resolver looked up
				// "GET /api/v1/admin/personas" and returned not-found.
				// Use the one shared helper so the two sites cannot drift.
				id = synthesizedOperationID(method, path)
			}
			if id != operationID {
				return
			}
			matches = append(matches, &operationMatch{
				specName: specName, method: method, path: fullPath, rawPath: path, op: op,
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
// field agrees with the path reported at the operations level for
// the same operation. The synthesized OperationID, by contrast, is
// built from the spec-relative rawPath via the shared helper so it
// agrees with the id the operations level advertises (which is
// base-path-independent), not with m.path.
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
		out.OperationID = synthesizedOperationID(m.method, m.rawPath)
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
	detail.Headers = flattenResponseHeaders(r.Headers)
	return detail
}

// flattenResponseHeaders coerces the kin-openapi Headers map (each
// value is a HeaderRef whose Value embeds Parameter) into the slim
// HeaderDetail shape. Returns nil when the response declares no
// headers so the JSON omits the field entirely. Headers without a
// resolved value are skipped rather than emitted as empty entries.
func flattenResponseHeaders(headers openapi3.Headers) map[string]HeaderDetail {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]HeaderDetail, len(headers))
	for name, ref := range headers {
		if ref == nil || ref.Value == nil {
			continue
		}
		p := ref.Value.Parameter
		out[name] = HeaderDetail{
			Description: p.Description,
			Required:    p.Required,
			Schema:      schemaToValue(p.Schema, 0),
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
// fields (type, format, default, enum, const, required, example)
// into out. Kept separate from compound (Properties, Items,
// OneOf/AnyOf/AllOf) walks so schemaToValue stays under the
// cognitive-complexity gate.
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
	if s.Const != nil {
		out["const"] = s.Const
	}
	if len(s.Required) > 0 {
		out["required"] = s.Required
	}
	if s.Example != nil {
		out["example"] = s.Example
	}
}

// populateSchemaCompounds recurses into Properties, Items, and the
// composition keywords (oneOf / anyOf / allOf / not) at depth+1.
// Composition keywords are first-class in OpenAPI 3.1: the canonical
// "nullable reference" pattern is oneOf: [$ref, {type: "null"}], and
// polymorphic shapes lean on anyOf / allOf. Dropping them strips
// substantial parts of the contract from the model's view.
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
	if branches := flattenSchemaRefs(s.OneOf, depth); branches != nil {
		out["oneOf"] = branches
	}
	if branches := flattenSchemaRefs(s.AnyOf, depth); branches != nil {
		out["anyOf"] = branches
	}
	if branches := flattenSchemaRefs(s.AllOf, depth); branches != nil {
		out["allOf"] = branches
	}
	if s.Not != nil {
		out["not"] = schemaToValue(s.Not, depth+1)
	}
}

// flattenSchemaRefs walks a SchemaRefs slice (the underlying type
// for OneOf/AnyOf/AllOf) and returns a []any of flattened branches.
// Nil-or-empty input returns nil so the caller can omit the field
// entirely from the marshaled JSON.
func flattenSchemaRefs(refs openapi3.SchemaRefs, depth int) []any {
	if len(refs) == 0 {
		return nil
	}
	out := make([]any, 0, len(refs))
	for _, ref := range refs {
		out = append(out, schemaToValue(ref, depth+1))
	}
	return out
}

// addStringIfPresent skips zero values so the marshaled schema
// stays slim.
func addStringIfPresent(m map[string]any, key, value string) {
	if value != "" {
		m[key] = value
	}
}
