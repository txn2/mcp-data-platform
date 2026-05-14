package apigateway

import (
	"fmt"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// OperationSummary is the slim per-operation view returned by
// api_list_endpoints. Designed to be cheap on context: the model gets
// enough to decide whether an operation is relevant (operation_id,
// method, path, summary, tags) without paying for the full request /
// response schema. Per-endpoint detail is fetched on demand via
// api_get_endpoint_schema.
//
// Spec names the component spec inside the connection's catalog
// (e.g. "constituent", "gift"). Omitted from JSON when empty so
// connections with no catalog or a single anonymous spec stay slim
// on context.
type OperationSummary struct {
	OperationID string   `json:"operation_id"`
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Summary     string   `json:"summary,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Spec        string   `json:"spec,omitempty"`
}

// parseOpenAPISpec delegates to catalog.ParseSpec — the single
// source of truth for OpenAPI parsing across the toolkit and the
// admin handler. Kept as a package-local alias so the rest of this
// file's call sites read naturally.
func parseOpenAPISpec(raw string) (*openapi3.T, error) {
	doc, err := catalog.ParseSpec(raw)
	if err != nil {
		return nil, fmt.Errorf("apigateway: %w", err)
	}
	return doc, nil
}

// buildOperationIndex flattens an OpenAPI document into the slim
// summary slice api_list_endpoints returns. Operations without an
// explicit operationId synthesize one as "<METHOD> <path>" so
// downstream tools can address them; this matches what most
// codegen pipelines do.
//
// Each returned summary's Spec field is set to specName so callers
// merging multiple specs in one catalog can distinguish which
// component spec an op came from. Pass "" when the catalog has a
// single anonymous spec — the field is omitted from JSON in that
// case.
//
// The returned slice is sorted by (path, method) for stable output
// across runs — the model's training distribution prefers stable
// ordering when comparing tool catalogs across turns.
//
// embedTexts is a parallel slice (same indices, same length) whose
// entries are the per-operation text that semantic ranking embeds.
// Kept off OperationSummary so the JSON response shape stays slim
// and the description (often paragraphs long) doesn't bloat the
// model's context. nil when doc has no operations.
func buildOperationIndex(doc *openapi3.T, specName string) (ops []OperationSummary, embedTexts []string) {
	if doc == nil || doc.Paths == nil {
		return nil, nil
	}
	for path, item := range doc.Paths.Map() {
		ops, embedTexts = appendItemOperations(ops, embedTexts, path, item, specName)
	}
	indices := make([]int, len(ops))
	for i := range indices {
		indices[i] = i
	}
	sort.Slice(indices, func(i, j int) bool {
		a, b := ops[indices[i]], ops[indices[j]]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Method < b.Method
	})
	sortedOps := make([]OperationSummary, len(ops))
	sortedTexts := make([]string, len(embedTexts))
	for newIdx, oldIdx := range indices {
		sortedOps[newIdx] = ops[oldIdx]
		sortedTexts[newIdx] = embedTexts[oldIdx]
	}
	return sortedOps, sortedTexts
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
// a synthesized "METHOD path" id so they remain addressable. The
// parallel embedTexts slice carries the per-operation text used by
// semantic ranking — kept off OperationSummary so descriptions
// (often paragraphs) don't bloat the JSON response.
func appendItemOperations(ops []OperationSummary, embedTexts []string, path string, item *openapi3.PathItem, specName string) (outOps []OperationSummary, outTexts []string) {
	if item == nil {
		return ops, embedTexts
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
		summary := OperationSummary{
			OperationID: id,
			Method:      m.method,
			Path:        path,
			Summary:     op.Summary,
			Tags:        op.Tags,
			Spec:        specName,
		}
		ops = append(ops, summary)
		embedTexts = append(embedTexts, buildEmbedText(summary, op.Description))
	}
	return ops, embedTexts
}

// buildEmbedText composes the text fed to the embedding provider.
// Concatenates the fields the model is most likely to phrase a
// query around: summary first (the natural-language description an
// API author writes), then description (richer detail when the
// author bothered), then path (so domain nouns leak into the
// embedding), then tags (categories). Method is excluded — the
// HTTP verb is rarely semantically meaningful relative to a query
// like "list orders" or "create user".
func buildEmbedText(op OperationSummary, description string) string {
	parts := make([]string, 0, 4)
	if op.Summary != "" {
		parts = append(parts, op.Summary)
	}
	if description != "" {
		parts = append(parts, description)
	}
	if op.Path != "" {
		parts = append(parts, op.Path)
	}
	if len(op.Tags) > 0 {
		parts = append(parts, strings.Join(op.Tags, " "))
	}
	return strings.Join(parts, " ")
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
