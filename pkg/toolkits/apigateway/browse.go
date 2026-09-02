package apigateway

import (
	"context"
	"fmt"
	"sort"

	"github.com/getkin/kin-openapi/openapi3"
)

// The browse surface is the read path a person uses instead of an
// agent: it answers "what does this upstream expose" without handing
// back the spec document. It resolves operations through the same
// helpers api_discover uses, so a page and a tool call describe one
// operation identically (#1478).

// BrowseConnection is one api-kind connection as a browse surface
// presents it: what it points at, how it authenticates, and how many
// operations THIS caller reaches. The counts are route-policy filtered,
// so a connection whose deny rules hide half its catalog reports the
// half that is left rather than the catalog's total.
type BrowseConnection struct {
	Name        string
	Description string
	BaseURL     string
	// AuthMode is the connection's credential mode ("none", "bearer",
	// "api_key", "basic", an oauth2 mode). The credential itself is
	// never part of this surface.
	AuthMode       string
	CatalogID      string
	OperationCount int
	Specs          []SpecSummary
}

// BrowseConnection describes one connection for the calling context.
// The context carries the caller's identity, which is what the route
// policy resolves roles from; a context with no caller gets whatever
// the policy grants an anonymous one.
func (t *Toolkit) BrowseConnection(ctx context.Context, name string) (*BrowseConnection, error) {
	t.mu.RLock()
	c, ok := t.connections[name]
	policy := t.routePolicy
	t.mu.RUnlock()
	if !ok {
		return nil, ErrConnectionNotFound
	}
	visible := filterByRoutePolicy(ctx, policy, name, c.operations)
	return &BrowseConnection{
		Name:           name,
		Description:    connectionDescription(c.cfg),
		BaseURL:        c.cfg.BaseURL,
		AuthMode:       c.cfg.AuthMode,
		CatalogID:      c.cfg.CatalogID,
		OperationCount: len(visible),
		Specs:          browseSpecSummaries(c, visible),
	}, nil
}

// browseSpecSummaries is buildSpecSummaries with the per-spec counts
// taken from the operations the caller reaches rather than from the
// spec's own total. A spec whose every operation the route policy
// denies stays in the list with a count of zero: the reader is told
// the spec is loaded and that they reach none of it, which is a
// different fact from the spec being absent.
func browseSpecSummaries(c *conn, visible []OperationSummary) []SpecSummary {
	counts := make(map[string]int, len(c.specs))
	for _, op := range visible {
		counts[op.Spec]++
	}
	out := buildSpecSummaries(c)
	for i := range out {
		out[i].OperationCount = counts[out[i].Name]
	}
	return out
}

// BrowseOperations lists the operations of one connection that the
// caller reaches, in stable (spec, path, method) order. Unlike
// api_discover it neither ranks nor caps: a browser renders the
// whole index and filters it in the page.
func (t *Toolkit) BrowseOperations(ctx context.Context, connection string) ([]OperationSummary, error) {
	t.mu.RLock()
	c, ok := t.connections[connection]
	policy := t.routePolicy
	t.mu.RUnlock()
	if !ok {
		return nil, ErrConnectionNotFound
	}
	// The copy is not an optimization to skip: filterByRoutePolicy returns
	// the connection's OWN slice when no policy is installed, and sorting
	// that in place would reorder the toolkit's live index from a read
	// path, concurrently with every other reader of it.
	visible := append([]OperationSummary(nil), filterByRoutePolicy(ctx, policy, connection, c.operations)...)
	// c.operations is per-spec runs appended in map-iteration order, so its
	// cross-spec order is whatever Go's map gave that process. Sort here
	// rather than relying on it: a browse index that reshuffles between
	// replicas makes a deep link land on a different row.
	sortOperations(visible)
	return visible, nil
}

// CatalogConnections returns every connection this toolkit serves with the
// operations its catalog declares, in stable name order, narrowed by nothing.
//
// It is the authoring view, not a caller's: the persona editor has to show the
// operations a persona is being granted or refused, including the ones the
// EDITOR'S own persona is refused. Every other browse method on this type
// answers "what does this caller reach" and is route-policy filtered, which is
// the wrong set to draw a rule against — an operator cannot grant back an
// operation the surface hid from them.
//
// Counts and specs are the catalog's totals for the same reason.
func (t *Toolkit) CatalogConnections() []CatalogConnection {
	t.mu.RLock()
	defer t.mu.RUnlock()
	names := make([]string, 0, len(t.connections))
	for name := range t.connections {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]CatalogConnection, 0, len(names))
	for _, name := range names {
		c := t.connections[name]
		ops := append([]OperationSummary(nil), c.operations...)
		sortOperations(ops)
		out = append(out, CatalogConnection{
			Name:        name,
			Description: connectionDescription(c.cfg),
			BaseURL:     c.cfg.BaseURL,
			AuthMode:    c.cfg.AuthMode,
			CatalogID:   c.cfg.CatalogID,
			Operations:  ops,
		})
	}
	return out
}

// CatalogConnection is one connection with the whole operation index its
// catalog declares. Distinct from BrowseConnection, which reports what one
// caller reaches and carries counts rather than the operations themselves.
type CatalogConnection struct {
	Name        string
	Description string
	BaseURL     string
	AuthMode    string
	CatalogID   string
	// Operations is the connection's full index. Empty for a connection with
	// no catalog, which is callable only by method and path and can therefore
	// carry no operation-derived rule.
	Operations []OperationSummary
}

// sortOperations orders operations by (spec, path, method), the order
// a grouped index reads in.
func sortOperations(ops []OperationSummary) {
	sort.Slice(ops, func(i, j int) bool {
		a, b := ops[i], ops[j]
		if a.Spec != b.Spec {
			return a.Spec < b.Spec
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Method < b.Method
	})
}

// BrowseOperation returns the full detail for one operation of one
// connection: the same EndpointSchemaOutput api_discover serves at its
// operation level, including the requests promoted on this endpoint.
// spec is optional and disambiguates an id defined by more than one
// component spec.
//
// An operation the route policy denies answers ErrOperationNotFound,
// matching its absence from BrowseOperations.
func (t *Toolkit) BrowseOperation(ctx context.Context, connection, operationID, spec string) (*EndpointSchemaOutput, error) {
	t.mu.RLock()
	c, ok := t.connections[connection]
	policy := t.routePolicy
	t.mu.RUnlock()
	if !ok {
		return nil, ErrConnectionNotFound
	}
	match, candidates := resolveOperation(c.specs, operationID, spec)
	if match == nil {
		if len(candidates) > 1 {
			return nil, ambiguousOperationError(operationID, candidates)
		}
		return nil, ErrOperationNotFound
	}
	if policy != nil {
		if allowed, _ := policy.Allow(ctx, connection, match.method, match.path, match.path); !allowed {
			return nil, ErrOperationNotFound
		}
	}
	out := buildEndpointSchemaOutput(match)
	out.SavedExamples = t.savedExamples(ctx, connection, out.OperationID)
	return &out, nil
}

// SpecOperations parses one stored catalog spec and returns its
// operations in stable (path, method) order, plus the prefix every
// returned path carries. It is the catalog-side browse path: a spec is
// readable this way before any connection references it, which is what
// the operator's view of a catalog needs.
//
// basePathOverride is the operator-set per-spec prefix; empty defers to
// the spec's own declared server path. There is no connection here, so
// there is no base URL to dedupe the prefix against — the path each
// operation reports is the one the spec documents, which is what a
// catalog view is describing. The prefix is returned rather than left
// for the caller to re-derive, because "empty override" and "the spec
// declares no server path" are different inputs with the same stored
// value.
func SpecOperations(content, specName, basePathOverride string) (ops []OperationSummary, basePath string, err error) {
	doc, basePath, err := parseSpecForBrowse(content, basePathOverride)
	if err != nil {
		return nil, "", err
	}
	ops, _ = buildOperationIndex(doc, specName, basePath)
	if ops == nil {
		ops = []OperationSummary{}
	}
	return ops, basePath, nil
}

// SpecOperation returns the full detail for one operation of one
// stored catalog spec, resolved through the same helpers
// api_discover uses. Saved examples are not included: those
// are promoted against a connection, and this path has none.
func SpecOperation(content, specName, basePathOverride, operationID string) (*EndpointSchemaOutput, error) {
	doc, basePath, err := parseSpecForBrowse(content, basePathOverride)
	if err != nil {
		return nil, err
	}
	specs := map[string]*specState{specName: {doc: doc, effectiveBasePath: basePath}}
	match, candidates := resolveOperation(specs, operationID, "")
	if match == nil {
		// OpenAPI requires an operationId to be unique across a document and
		// the parse above validates that, so a second candidate means the
		// document changed under a rule this code does not enforce. Report it
		// as the ambiguity it is rather than as an id that does not exist.
		if len(candidates) > 1 {
			return nil, ambiguousOperationError(operationID, candidates)
		}
		return nil, ErrOperationNotFound
	}
	out := buildEndpointSchemaOutput(match)
	return &out, nil
}

// parseSpecForBrowse parses spec content and resolves the prefix its
// operations carry when read outside any connection.
func parseSpecForBrowse(content, basePathOverride string) (doc *openapi3.T, basePath string, err error) {
	parsed, perr := parseOpenAPISpec(content)
	if perr != nil {
		return nil, "", fmt.Errorf("browse spec: %w", perr)
	}
	sources := []string{basePathOverride}
	if basePathOverride == "" {
		sources = specBasePaths(parsed)
	}
	return parsed, computeEffectiveBasePath("", sources), nil
}
