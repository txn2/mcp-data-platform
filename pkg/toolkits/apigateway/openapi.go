package apigateway

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// OperationSummary is the slim per-operation view returned by
// api_list_endpoints. Designed to be cheap on context: the model gets
// enough to decide whether an operation is relevant (operation_id,
// method, path, summary, tags) without paying for the full request /
// response schema. Schemas are fetched on demand via a follow-up
// api_get_endpoint_schema tool (deferred — see RFC #364).
type OperationSummary struct {
	OperationID string   `json:"operation_id"`
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Summary     string   `json:"summary,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// parseOpenAPISpec validates and loads a raw OpenAPI 3.x document
// (YAML or JSON). Returns an error with the underlying parser's
// diagnostic so admin UIs can surface line/path. The loader is
// configured to skip remote $ref resolution: a malicious or
// careless spec containing $ref: "https://..." would otherwise let
// connection registration trigger an outbound HTTP call to an
// arbitrary host.
func parseOpenAPISpec(raw string) (*openapi3.T, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("apigateway: openapi_spec is empty")
	}
	loader := &openapi3.Loader{
		Context:               context.Background(),
		IsExternalRefsAllowed: false,
	}
	doc, err := loader.LoadFromData([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("apigateway: parsing openapi_spec: %w", err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		return nil, fmt.Errorf("apigateway: invalid openapi_spec: %w", err)
	}
	return doc, nil
}

// buildOperationIndex flattens an OpenAPI document into the slim
// summary slice api_list_endpoints returns. Operations without an
// explicit operationId synthesize one as "<METHOD> <path>" so
// downstream tools can address them; this matches what most
// codegen pipelines do.
//
// The returned slice is sorted by (path, method) for stable output
// across runs — the model's training distribution prefers stable
// ordering when comparing tool catalogs across turns.
func buildOperationIndex(doc *openapi3.T) []OperationSummary {
	if doc == nil || doc.Paths == nil {
		return nil
	}
	var ops []OperationSummary
	for path, item := range doc.Paths.Map() {
		ops = appendItemOperations(ops, path, item)
	}
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return ops[i].Method < ops[j].Method
	})
	return ops
}

// pathItemMethods enumerates the (method, *Operation) pairs on a
// PathItem. Centralized so the iteration order is consistent
// everywhere we walk PathItems.
//
//nolint:gochecknoglobals // intentionally a package-level constant set
var pathItemMethods = []struct {
	method string
	get    func(*openapi3.PathItem) *openapi3.Operation
}{
	{"GET", func(p *openapi3.PathItem) *openapi3.Operation { return p.Get }},
	{"POST", func(p *openapi3.PathItem) *openapi3.Operation { return p.Post }},
	{"PUT", func(p *openapi3.PathItem) *openapi3.Operation { return p.Put }},
	{"DELETE", func(p *openapi3.PathItem) *openapi3.Operation { return p.Delete }},
	{"PATCH", func(p *openapi3.PathItem) *openapi3.Operation { return p.Patch }},
	{"HEAD", func(p *openapi3.PathItem) *openapi3.Operation { return p.Head }},
}

// appendItemOperations adds every operation defined on a PathItem
// to the running summary slice. Operations without operationId get
// a synthesized "METHOD path" id so they remain addressable.
func appendItemOperations(ops []OperationSummary, path string, item *openapi3.PathItem) []OperationSummary {
	if item == nil {
		return ops
	}
	for _, m := range pathItemMethods {
		op := m.get(item)
		if op == nil {
			continue
		}
		id := op.OperationID
		if id == "" {
			id = m.method + " " + path
		}
		ops = append(ops, OperationSummary{
			OperationID: id,
			Method:      m.method,
			Path:        path,
			Summary:     op.Summary,
			Tags:        op.Tags,
		})
	}
	return ops
}

// rankOperations returns the subset of ops whose operation_id,
// path, summary, or any tag contains the query (case-insensitive
// substring match). When the query is empty the full slice is
// returned in its existing order. The result is capped at limit
// (or len(ops) if limit ≤ 0).
//
// v1 ranking is deliberately lexical — semantic / embedding-based
// ranking is deferred to issue #371 to keep this PR scoped.
// Callers documenting the tool should set expectations accordingly:
// the model should write queries in the API's own vocabulary
// (paths, tags, summaries) rather than free-form intent.
func rankOperations(ops []OperationSummary, query string, limit int) []OperationSummary {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return capSlice(ops, limit)
	}
	var matched []OperationSummary
	for _, op := range ops {
		if operationMatches(op, q) {
			matched = append(matched, op)
		}
	}
	return capSlice(matched, limit)
}

// operationMatches reports whether any of the searchable fields on
// the operation contain the lowercased query string.
func operationMatches(op OperationSummary, q string) bool {
	if strings.Contains(strings.ToLower(op.OperationID), q) ||
		strings.Contains(strings.ToLower(op.Path), q) ||
		strings.Contains(strings.ToLower(op.Summary), q) {
		return true
	}
	for _, tag := range op.Tags {
		if strings.Contains(strings.ToLower(tag), q) {
			return true
		}
	}
	return false
}

// capSlice returns ops truncated to limit, treating limit ≤ 0 as
// "no cap". Returns the input directly when no truncation is needed
// to avoid an extraneous allocation.
func capSlice(ops []OperationSummary, limit int) []OperationSummary {
	if limit <= 0 || len(ops) <= limit {
		return ops
	}
	return ops[:limit]
}
