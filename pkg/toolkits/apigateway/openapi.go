package apigateway

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// pathSep is the URL path separator. Named so revive's add-constant
// rule does not flag the repeated literal across the base-path
// derivation and dedupe helpers.
const pathSep = "/"

// OperationSummary is the slim per-operation view returned by
// api_list_endpoints. Designed to be cheap on context: the model gets
// enough to decide whether an operation is relevant (operation_id,
// method, path, summary, tags) without paying for the full request /
// response schema. Per-endpoint detail is fetched on demand via
// api_get_endpoint_schema.
//
// Spec names the component spec inside the connection's catalog
// (e.g. "users", "orders"). Omitted from JSON when empty so
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
func buildOperationIndex(doc *openapi3.T, specName, basePath string) (ops []OperationSummary, embedTexts []string) {
	if doc == nil || doc.Paths == nil {
		return nil, nil
	}
	for rawPath, item := range doc.Paths.Map() {
		ops, embedTexts = appendItemOperations(ops, embedTexts, item, itemOpsCtx{
			basePath: basePath, rawPath: rawPath, specName: specName,
		})
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

// synthesizedOperationID builds the operationId used for an operation
// that declares none. method must already be upper-cased; rawPath is
// spec-relative. Shared by appendItemOperations (which advertises the id
// via api_list_endpoints) and the metric operation resolver so the
// listed id and the metric label can never diverge.
func synthesizedOperationID(method, rawPath string) string {
	return method + " " + rawPath
}

// listableMethod reports whether m (upper-cased) is an HTTP method
// api_list_endpoints advertises, i.e. one of pathItemMethods. The
// resolver only synthesizes ids for these: the router also matches
// OPTIONS/TRACE/CONNECT, which pathItemMethods omits, so synthesizing
// for them would invent a metric label no catalog entry carries.
func listableMethod(m string) bool {
	for _, pm := range pathItemMethods {
		if pm.method == m {
			return true
		}
	}
	return false
}

// itemOpsCtx bundles the per-path-item context appendItemOperations
// needs. Kept as a struct so the function stays under revive's
// argument-limit ceiling.
type itemOpsCtx struct {
	basePath string // basePath prefix applied to the runtime path
	rawPath  string // spec-relative path (used for synthesized operationIds)
	specName string // component spec name on each emitted OperationSummary
}

// appendItemOperations adds every operation defined on a PathItem
// to the running summary slice. Operations without operationId get
// a synthesized "METHOD rawPath" id — note the spec-relative
// rawPath, NOT the basePath-prefixed runtime path: the synthesized
// id must be a property of the spec content alone so the
// (catalog_id, spec_name, operation_id) embedding key produced at
// spec-write time matches the lookup key built at connection
// registration regardless of which basePath the registering
// connection resolves. The Path field still carries the
// basePath-prefixed runtime path so api_list_endpoints reports the
// full URL the model passes to api_invoke_endpoint. The parallel
// embedTexts slice carries the per-operation text used by semantic
// ranking — kept off OperationSummary so descriptions (often
// paragraphs) don't bloat the JSON response.
func appendItemOperations(ops []OperationSummary, embedTexts []string, item *openapi3.PathItem, c itemOpsCtx) (outOps []OperationSummary, outTexts []string) {
	if item == nil {
		return ops, embedTexts
	}
	fullPath := c.basePath + c.rawPath
	for _, m := range pathItemMethods {
		op := m.get(item)
		if op == nil {
			continue
		}
		id := op.OperationID
		if id == "" {
			id = synthesizedOperationID(m.method, c.rawPath)
		}
		summary := OperationSummary{
			OperationID: id,
			Method:      m.method,
			Path:        fullPath,
			Summary:     op.Summary,
			Tags:        op.Tags,
			Spec:        c.specName,
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

// specBasePath returns the path component of the first declared
// servers[].url, with any trailing slash stripped. Vendors that ship
// each component spec under a distinct version segment (for example
// a connection whose connection.base_url is the host and whose specs
// each declare "https://host/foo/v1", "https://host/bar/v2") rely on
// this so api_list_endpoints reports the full path the model should
// pass to api_invoke_endpoint, not the spec-relative path that 404s
// when the segment is missing from the connection's base_url.
//
// Returns "" when doc has no servers entry, when the first entry's
// URL fails to parse, or when the parsed path is empty or just "/".
// In that case operations carry their spec-relative paths and the
// connection's base_url is the only prefix the toolkit joins.
//
// Only the path component is extracted; the scheme and host on
// servers[0].url are ignored because the connection's base_url is
// the operator's authoritative choice of host (one spec author's
// preferred host should not override an operator who pointed the
// connection at a sandbox or proxied endpoint).
func specBasePath(doc *openapi3.T) string {
	if doc == nil || len(doc.Servers) == 0 {
		return ""
	}
	raw := doc.Servers[0].URL
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	p := strings.TrimSuffix(u.Path, pathSep)
	if p == "" {
		return ""
	}
	// OpenAPI 3.0 explicitly allows relative server URLs whose path
	// has no leading slash (e.g. servers: [{url: "v1"}]). The
	// downstream invoke validator rejects paths that do not start
	// with "/", so any synthesized OperationSummary.Path built from
	// such a base would 400 at invoke time. Prepend the slash so
	// the synthesized full path is a valid request path.
	if !strings.HasPrefix(p, pathSep) {
		return pathSep + p
	}
	return p
}

// computeEffectiveBasePath returns the prefix to apply to every
// operation's spec-relative path so the toolkit's invoke-time URL
// join produces the upstream URL without doubling segments. The
// rule: drop the spec's servers[0] path component when the
// connection's base_url path already contains it as a suffix
// (which is exactly the case where an operator configured the
// connection to point at the spec's documented base, then attached
// the same spec to the connection). In every other case the
// spec's base path is preserved.
//
// Examples:
//
//	conn=https://api.example.com         spec=/v1   -> "/v1"
//	conn=https://api.example.com/v1      spec=/v1   -> ""
//	conn=https://api.example.com/api/v2  spec=/api/v2 -> ""
//	conn=https://api.example.com/legacy  spec=/v1   -> "/v1"
//
// Empty inputs short-circuit: an empty spec base means there is
// nothing to prefix, an unparseable connection URL falls back to
// the spec base verbatim (preserves the pre-existing behavior of
// every connection that ships before this dedupe landed).
func computeEffectiveBasePath(connBaseURL, specBase string) string {
	if specBase == "" {
		return ""
	}
	u, err := url.Parse(connBaseURL)
	if err != nil {
		return specBase
	}
	connPath := strings.TrimSuffix(u.Path, pathSep)
	if connPath == "" {
		return specBase
	}
	if strings.HasSuffix(connPath, specBase) {
		return ""
	}
	return specBase
}

// rankOperations returns the subset of ops whose searchable fields
// contain EVERY whitespace-separated token in query (case-insensitive
// substring match per token; the tokens combine with AND). Searchable
// fields are operation_id, path, summary, spec name, and tags. When
// the query is empty the full slice is returned in its existing
// order. The result is capped at limit (or len(ops) when limit ≤ 0).
//
// Always returns a non-nil slice so a zero-match query produces
// "operations": [] in the JSON response rather than "operations":
// null. Clients that switch on truthiness or array-length without
// the null guard would otherwise have to handle two empty shapes.
//
// Per-token AND matches what operators expect when typing "gift
// list" or "create user": each word should narrow the result, not be
// treated as a single phrase that fails when the spec author wrote
// the fields in a different order.
func rankOperations(ops []OperationSummary, query string, limit int) []OperationSummary {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return capSlice(ops, limit)
	}
	tokens := strings.Fields(q)
	matched := make([]OperationSummary, 0, len(ops))
	for _, op := range ops {
		if operationMatchesAllTokens(op, tokens) {
			matched = append(matched, op)
		}
	}
	return capSlice(matched, limit)
}

// operationMatchesAllTokens reports whether every token appears as
// a substring of at least one of the operation's searchable fields.
// Tokens are pre-lowercased by the caller; fields are lowercased
// per check (cheap relative to alternatives like caching a struct
// of lowercased fields per op).
func operationMatchesAllTokens(op OperationSummary, tokens []string) bool {
	for _, tok := range tokens {
		if !operationFieldsContain(op, tok) {
			return false
		}
	}
	return true
}

// operationFieldsContain reports whether tok appears as a substring
// of any one of the operation's searchable fields. Spec name is
// included so operators can navigate a multi-spec catalog by
// vendor-supplied section (e.g. "constituent", "gift") that does
// not otherwise appear in the operation's id, path, or tags.
func operationFieldsContain(op OperationSummary, tok string) bool {
	if strings.Contains(strings.ToLower(op.OperationID), tok) ||
		strings.Contains(strings.ToLower(op.Path), tok) ||
		strings.Contains(strings.ToLower(op.Summary), tok) ||
		strings.Contains(strings.ToLower(op.Spec), tok) {
		return true
	}
	for _, tag := range op.Tags {
		if strings.Contains(strings.ToLower(tag), tok) {
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
