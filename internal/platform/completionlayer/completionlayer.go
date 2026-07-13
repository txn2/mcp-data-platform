// Package completionlayer implements the MCP completion/complete handler for the
// platform: argument autocompletion for prompt arguments and resource-template
// variables. It lives in its own facade-internal package so the platform facade
// stays within its size budget and the completion logic is cohesive and
// independently testable. It owns all value providers (dataset/topic/connection
// names, catalog/schema/table, glossary terms), the persona gating that mirrors
// tools/list visibility, and the interactive latency budget and value cap.
package completionlayer

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/pkg/knowledge/federation"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// Completion argument names. Prompt arguments and resource-template variables are
// routed to a value provider by these well-known names, so a completion applies
// uniformly to a built-in prompt, a database prompt, or a resource template that
// reuses the same argument name — no per-prompt registration is required.
const (
	argDataset    = "dataset"
	argTopic      = "topic"
	argConnection = "connection"
	argCatalog    = "catalog"
	argSchemaName = "schema_name"
	argTable      = "table"
	argTerm       = "term"
)

// Resource template URI patterns whose variables this layer completes. They
// mirror the templates registered by the platform's resource layer.
const (
	schemaTemplateURI       = "schema://{catalog}.{schema_name}/{table}"
	glossaryTemplateURI     = "glossary://{term}"
	availabilityTemplateURI = "availability://{catalog}.{schema_name}/{table}"
)

// Tools whose persona access gates a completion. Completions leak names, so a
// caller only receives values it could already obtain through the corresponding
// tool: dataset/topic/glossary names require the universal discovery tool
// (search); catalog/schema/table names require the browse tool that produces the
// same metadata; connection names require the tool that lists connections. A
// persona denied the tool receives an empty completion set, mirroring tools/list
// visibility.
const (
	discoveryTool   = "search"
	browseTool      = "trino_browse"
	connectionsTool = "list_connections"
)

// dataProductEntityType is the DataHub entity type queried to enumerate data
// products for topic completions (SearchTables has no dedicated data-product
// list method).
const dataProductEntityType = "DATA_PRODUCT"

// MaxValues is the spec-mandated ceiling on the number of completion values
// returned in a single completion/complete response. A provider may find more;
// the handler truncates to this many and sets HasMore.
const MaxValues = 100

// defaultTimeout bounds the upstream lookups a completion performs. Completions
// are interactive (fired on every keystroke), so a stalled provider degrades to
// an empty result rather than blocking the client.
const defaultTimeout = time.Second

// fetchLimit bounds each upstream lookup. It is one over the value cap so a full
// page signals — honestly — that more matches exist.
const fetchLimit = MaxValues + 1

// Deps carries the platform primitives the completion handler needs, kept as
// plain values so this package never imports the platform package (which would
// cycle).
type Deps struct {
	Authenticator    middleware.Authenticator
	Authorizer       middleware.Authorizer // nil => no persona filtering (allow all)
	PersonasForRoles middleware.PersonasForRoles
	AdminPersona     string

	Semantic        semantic.Provider
	Query           query.Provider
	Registry        *registry.Registry
	PersonaRegistry *persona.Registry

	// Timeout bounds the upstream lookups a completion performs. Zero uses
	// defaultTimeout.
	Timeout time.Duration
}

// Handle owns the completion handler and its value providers.
type Handle struct {
	deps    Deps
	timeout time.Duration
}

// New builds a completion Handle from its dependencies.
func New(deps Deps) *Handle {
	timeout := deps.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Handle{deps: deps, timeout: timeout}
}

// Handler returns the completion/complete handler for ServerOptions. It resolves
// the caller (unauthenticated → empty), applies the interactive latency budget,
// and shapes the result to the spec's value cap. An unauthenticated session — or
// one whose lookups time out — receives an empty completion set, never an error.
func (h *Handle) Handler() func(context.Context, *mcp.CompleteRequest) (*mcp.CompleteResult, error) {
	return func(ctx context.Context, req *mcp.CompleteRequest) (*mcp.CompleteResult, error) {
		if req == nil || req.Params == nil || req.Params.Ref == nil {
			return emptyResult(), nil
		}
		pc := middleware.ResolvePlatformContext(ctx, req, h.deps.Authenticator, h.deps.PersonasForRoles, h.deps.AdminPersona)
		if pc == nil {
			// Completions leak names; an unauthenticated session gets nothing.
			return emptyResult(), nil
		}

		cctx, cancel := context.WithTimeout(ctx, h.timeout)
		defer cancel()

		resolved := resolvedArguments(req.Params.Context)

		// Run the provider on a goroutine and race it against the deadline so a
		// provider that ignores ctx cancellation cannot stall the response.
		resCh := make(chan outcome, 1)
		go func() {
			values, more := h.values(cctx, pc, req.Params.Ref, req.Params.Argument, resolved)
			resCh <- outcome{values: values, hasMore: more}
		}()
		select {
		case out := <-resCh:
			return shape(out.values, out.hasMore), nil
		case <-cctx.Done():
			return emptyResult(), nil
		}
	}
}

// outcome carries a value provider's result across the deadline race.
type outcome struct {
	values  []string
	hasMore bool
}

// resolvedArguments returns the previously-resolved sibling arguments carried on
// the request context, or nil.
func resolvedArguments(cc *mcp.CompleteContext) map[string]string {
	if cc == nil {
		return nil
	}
	return cc.Arguments
}

// emptyResult is the canonical empty response (never a nil Values slice, so the
// wire form is a JSON array).
func emptyResult() *mcp.CompleteResult {
	return &mcp.CompleteResult{Completion: mcp.CompletionResultDetails{Values: []string{}}}
}

// shape truncates values to MaxValues and reports HasMore and Total honestly.
// HasMore is true when the provider signaled more matches upstream OR the set was
// truncated at the cap. Total is reported only when the set is complete; a
// page-bounded provider cannot know the true total without an expensive count, so
// Total is omitted rather than under-reported as if the set were complete.
func shape(values []string, providerHasMore bool) *mcp.CompleteResult {
	count := len(values)
	hasMore := providerHasMore
	if count > MaxValues {
		values = values[:MaxValues]
		hasMore = true
	}
	if values == nil {
		values = []string{}
	}
	details := mcp.CompletionResultDetails{Values: values, HasMore: hasMore}
	if !hasMore {
		details.Total = count
	}
	return &mcp.CompleteResult{Completion: details}
}

// values routes a completion request to the provider for its reference type. The
// bool reports whether the upstream lookup was page-bounded (more matches exist).
func (h *Handle) values(
	ctx context.Context,
	pc *middleware.PlatformContext,
	ref *mcp.CompleteReference,
	arg mcp.CompleteParamsArgument,
	resolved map[string]string,
) (found []string, hasMore bool) {
	if ref == nil || pc == nil {
		return nil, false
	}
	switch ref.Type {
	case "ref/prompt":
		return h.promptArgument(ctx, pc, arg.Name, arg.Value)
	case "ref/resource":
		return h.resourceTemplate(ctx, pc, ref.URI, arg, resolved)
	default:
		return nil, false
	}
}

// promptArgument completes a prompt argument by its well-known name. Each
// provider applies its own persona gate; an unrecognized argument name has no
// completion source and returns nil.
func (h *Handle) promptArgument(ctx context.Context, pc *middleware.PlatformContext, argName, value string) (found []string, hasMore bool) {
	switch argName {
	case argDataset:
		if !h.toolAllowed(ctx, pc, discoveryTool) {
			return nil, false
		}
		return h.datasetNames(ctx, value)
	case argTopic:
		if !h.toolAllowed(ctx, pc, discoveryTool) {
			return nil, false
		}
		return h.topics(ctx, value)
	case argConnection:
		if !h.toolAllowed(ctx, pc, connectionsTool) {
			return nil, false
		}
		return h.connectionNames(pc, value), false
	default:
		return nil, false
	}
}

// resourceTemplate completes a resource-template variable. schema:// and
// availability:// share the catalog/schema/table namespace; glossary:// completes
// business terms.
func (h *Handle) resourceTemplate(ctx context.Context, pc *middleware.PlatformContext, uri string, arg mcp.CompleteParamsArgument, resolved map[string]string) (found []string, hasMore bool) {
	switch uri {
	case schemaTemplateURI, availabilityTemplateURI:
		if !h.toolAllowed(ctx, pc, browseTool) {
			return nil, false
		}
		return h.schemaVars(ctx, arg.Name, arg.Value, resolved), false
	case glossaryTemplateURI:
		if arg.Name != argTerm {
			return nil, false
		}
		if !h.toolAllowed(ctx, pc, discoveryTool) {
			return nil, false
		}
		return h.glossaryTerms(ctx, arg.Value)
	default:
		return nil, false
	}
}

// datasetNames returns dataset display names (falling back to the URN) matching
// the partial value via the semantic search index. hasMore is true when the
// search filled its page.
func (h *Handle) datasetNames(ctx context.Context, value string) (found []string, hasMore bool) {
	if h.deps.Semantic == nil {
		return nil, false
	}
	results, err := h.deps.Semantic.SearchTables(ctx, semantic.SearchFilter{Query: value, Limit: fetchLimit})
	if err != nil {
		return nil, false
	}
	names := make([]string, 0, len(results))
	for _, r := range results {
		name := r.Name
		if name == "" {
			name = r.URN
		}
		names = append(names, name)
	}
	return dedup(names), pageWasBounded(len(results))
}

// topics aggregates domains, data products, and glossary terms into a single
// deduplicated topic candidate list. The three sources are independent DataHub
// lookups, so they run concurrently to fit the interactive latency budget.
// hasMore is true when any page-bounded source filled its page.
func (h *Handle) topics(ctx context.Context, value string) (found []string, hasMore bool) {
	if h.deps.Semantic == nil {
		return nil, false
	}
	var (
		domains         []string
		glossary        []string
		products        []string
		glossaryHasMore bool
		productsHasMore bool
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { domains = h.domainCandidates(gctx, value); return nil })
	g.Go(func() error { glossary, glossaryHasMore = h.glossaryCandidates(gctx, value); return nil })
	g.Go(func() error { products, productsHasMore = h.dataProductCandidates(gctx, value); return nil })
	_ = g.Wait() // sub-tasks never return an error; they degrade to empty candidates.

	cands := make([]string, 0, len(domains)+len(glossary)+len(products))
	cands = append(cands, domains...)
	cands = append(cands, glossary...)
	cands = append(cands, products...)
	return dedupFoldSorted(cands), glossaryHasMore || productsHasMore
}

// domainCandidates lists DataHub domains filtered by the partial value (domains
// list in full, so they are filtered client-side).
func (h *Handle) domainCandidates(ctx context.Context, value string) []string {
	picker, ok := semantic.CatalogPickerFrom(h.deps.Semantic)
	if !ok {
		return nil
	}
	domains, err := picker.ListDomains(ctx)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(domains))
	for _, d := range domains {
		if containsFold(d.Name, value) {
			out = append(out, d.Name)
		}
	}
	return out
}

// glossaryCandidates ranks glossary terms by the partial value server-side.
func (h *Handle) glossaryCandidates(ctx context.Context, value string) (found []string, hasMore bool) {
	picker, ok := semantic.CatalogPickerFrom(h.deps.Semantic)
	if !ok {
		return nil, false
	}
	terms, err := picker.SearchGlossaryTerms(ctx, value, fetchLimit)
	if err != nil {
		return nil, false
	}
	return entityRefNames(terms), pageWasBounded(len(terms))
}

// dataProductCandidates ranks data products by the partial value via the
// entity-type-scoped search.
func (h *Handle) dataProductCandidates(ctx context.Context, value string) (found []string, hasMore bool) {
	products, err := h.deps.Semantic.SearchTables(ctx, semantic.SearchFilter{
		Query:       value,
		EntityTypes: []string{dataProductEntityType},
		Limit:       fetchLimit,
	})
	if err != nil {
		return nil, false
	}
	out := make([]string, 0, len(products))
	for _, dp := range products {
		out = append(out, dp.Name)
	}
	return out, pageWasBounded(len(products))
}

// connectionNames returns the configured connection names the caller's persona is
// allowed to reach, filtered by the partial value. The registry is fully
// enumerated, so there is no page-bounded "more" signal.
func (h *Handle) connectionNames(pc *middleware.PlatformContext, value string) []string {
	if h.deps.Registry == nil {
		return nil
	}
	per, ok := h.personaForContext(pc)
	if !ok {
		return nil
	}
	filter := persona.NewToolFilter(h.deps.PersonaRegistry)
	var out []string
	for _, c := range federation.NewConnectionLister(h.deps.Registry).Connections() {
		if !containsFold(c.Name, value) {
			continue
		}
		if !filter.IsConnectionAllowed(per, c.Name) {
			continue
		}
		out = append(out, c.Name)
	}
	sort.Strings(out)
	return dedup(out)
}

// schemaVars completes the catalog/schema/table variables of the schema:// and
// availability:// templates. schema_name needs a resolved catalog and table needs
// both, mirroring the template's variable order. Each listing is fully
// enumerated, so HasMore is left to the handler's cap-truncation.
func (h *Handle) schemaVars(ctx context.Context, argName, value string, resolved map[string]string) []string {
	browser, ok := query.CatalogBrowserFrom(h.deps.Query)
	if !ok {
		return nil
	}
	switch argName {
	case argCatalog:
		return browseFiltered(func() ([]string, error) { return browser.ListCatalogs(ctx) }, value)
	case argSchemaName:
		catalog := resolved[argCatalog]
		if catalog == "" {
			return nil
		}
		return browseFiltered(func() ([]string, error) { return browser.ListSchemas(ctx, catalog) }, value)
	case argTable:
		catalog, schema := resolved[argCatalog], resolved[argSchemaName]
		if catalog == "" || schema == "" {
			return nil
		}
		return browseFiltered(func() ([]string, error) { return browser.ListTables(ctx, catalog, schema) }, value)
	default:
		return nil
	}
}

// browseFiltered runs a namespace listing and filters it by the partial value,
// degrading an upstream error to no candidates.
func browseFiltered(list func() ([]string, error), value string) []string {
	items, err := list()
	if err != nil {
		return nil
	}
	return filterFold(items, value)
}

// glossaryTerms completes the glossary:// term variable via the semantic glossary
// search. hasMore is true when the search filled its page.
func (h *Handle) glossaryTerms(ctx context.Context, value string) (found []string, hasMore bool) {
	if h.deps.Semantic == nil {
		return nil, false
	}
	picker, ok := semantic.CatalogPickerFrom(h.deps.Semantic)
	if !ok {
		return nil, false
	}
	terms, err := picker.SearchGlossaryTerms(ctx, value, fetchLimit)
	if err != nil {
		return nil, false
	}
	return dedup(entityRefNames(terms)), pageWasBounded(len(terms))
}

// toolAllowed reports whether the caller may receive completions gated on the
// named tool. With no authorizer configured there is no persona filtering and
// everything is allowed; otherwise the authorizer decides. Unlike
// platform_find_tools, an empty (non-nil) roles slice is NOT short-circuited to
// allow-all: a deny-by-default deployment maps empty roles to a deny-all persona,
// and completions must honor that just as tools/list does — otherwise a zero-role
// caller could complete catalog names it cannot see (#928).
func (h *Handle) toolAllowed(ctx context.Context, pc *middleware.PlatformContext, tool string) bool {
	if h.deps.Authorizer == nil || pc == nil {
		return true
	}
	allowed, _, _ := h.deps.Authorizer.IsAuthorized(ctx, pc.UserID, pc.Roles, tool, "")
	return allowed
}

// personaForContext resolves the caller's persona, preferring an explicit persona
// name and falling back to a role match. Used for the per-connection gate, which
// the tool-level authorizer predicate cannot express on its own.
func (h *Handle) personaForContext(pc *middleware.PlatformContext) (*persona.Persona, bool) {
	if h.deps.PersonaRegistry == nil || pc == nil {
		return nil, false
	}
	if pc.PersonaName != "" {
		if per, ok := h.deps.PersonaRegistry.Get(pc.PersonaName); ok {
			return per, true
		}
	}
	return h.deps.PersonaRegistry.GetForRoles(pc.Roles)
}

// pageWasBounded reports whether an upstream search returned a full page (fetched
// count reached the fetch limit), meaning more matches likely exist. It lets the
// handler set HasMore honestly even when deduplication shrinks the returned set
// below the value cap.
func pageWasBounded(fetched int) bool {
	return fetched >= fetchLimit
}

// containsFold reports whether s contains sub case-insensitively. An empty sub
// matches everything (the client has typed no filter yet).
func containsFold(s, sub string) bool {
	if sub == "" {
		return true
	}
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

// filterFold keeps the items matching value case-insensitively, deduplicated and
// sorted for a stable completion order.
func filterFold(items []string, value string) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if containsFold(it, value) {
			out = append(out, it)
		}
	}
	sort.Strings(out)
	return dedup(out)
}

// entityRefNames extracts the non-empty display names from entity refs.
func entityRefNames(refs []semantic.EntityRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		if r.Name != "" {
			out = append(out, r.Name)
		}
	}
	return out
}

// dedupFoldSorted trims, drops empties, deduplicates case-insensitively, and
// sorts — the canonical shape for an aggregated candidate list.
func dedupFoldSorted(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it == "" {
			continue
		}
		key := strings.ToLower(it)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, it)
	}
	sort.Strings(out)
	return out
}

// dedup removes duplicate values, preserving order.
func dedup(items []string) []string {
	if len(items) == 0 {
		return items
	}
	seen := make(map[string]struct{}, len(items))
	out := items[:0]
	for _, it := range items {
		if _, ok := seen[it]; ok {
			continue
		}
		seen[it] = struct{}{}
		out = append(out, it)
	}
	return out
}
