// Package federation adapts the platform's live toolkit registry to the
// knowledge package's source interfaces, so the universal search router can
// federate API endpoints and connections without the knowledge engine depending
// on any concrete toolkit. It is the wiring seam between the running platform
// (registry, API gateway toolkits) and the decoupled knowledge.Provider set.
//
// Keeping these adapters here rather than in pkg/platform also keeps that
// package within its structural size budget: the federation glue is cohesive
// enough to live on its own.
package federation

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/connid"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// fallbackConnectionKinds are the single-connection toolkit kinds that do not
// implement toolkit.ConnectionLister but still represent a data connection. It
// mirrors the same set the list_connections tool falls back to, so the search
// connections corpus and that tool report the same connections.
var fallbackConnectionKinds = map[string]bool{
	"trino":   true,
	"datahub": true,
	"s3":      true,
}

// EndpointSearchers adapts every operation-serving gateway toolkit registered
// in reg to knowledge.EndpointSearcher, so the search router can federate remote
// operations into its endpoints group. Both kinds that hold operations are here:
// the HTTP API gateway's OpenAPI endpoints and the graphql kind's schema
// operations. They share one group because they answer one question — which
// remote operation serves this intent — and a caller narrowing a search to
// "endpoints" should not have to know which kind their connection is.
//
// Each adapter delegates to its toolkit's SearchOperations, which applies that
// toolkit's per-connection route policy, so the per-source access scope is
// enforced by the gateway itself.
func EndpointSearchers(reg *registry.Registry) []knowledge.EndpointSearcher {
	var out []knowledge.EndpointSearcher
	for _, tk := range reg.GetByKind(apigatewaykit.Kind) {
		if api, ok := tk.(*apigatewaykit.Toolkit); ok {
			out = append(out, apiEndpointSearcher{tk: api})
		}
	}
	for _, tk := range reg.GetByKind(graphqlkit.Kind) {
		if gql, ok := tk.(*graphqlkit.Toolkit); ok {
			out = append(out, graphqlEndpointSearcher{tk: gql})
		}
	}
	return out
}

// graphqlEndpointSearcher adapts a graphql toolkit to
// knowledge.EndpointSearcher. A GraphQL operation carries the same three
// coordinates an OpenAPI one does — an id, a method and a path — because that
// is the space its persona rules are written in: the method is the operation
// kind (QUERY or MUTATION) and the path is the dotted id with dots as slashes.
type graphqlEndpointSearcher struct {
	tk *graphqlkit.Toolkit
}

// SearchEndpoints ranks operations across the toolkit's connections and maps
// them onto knowledge.EndpointCandidate. It never returns an error: the
// underlying SearchOperations degrades to a lexical fallback rather than
// failing.
func (g graphqlEndpointSearcher) SearchEndpoints(ctx context.Context, intent string, limit int) ([]knowledge.EndpointCandidate, error) {
	ranked := g.tk.SearchOperations(ctx, intent, limit)
	out := make([]knowledge.EndpointCandidate, 0, len(ranked))
	for _, r := range ranked {
		out = append(out, knowledge.EndpointCandidate{
			Connection:  r.Connection,
			OperationID: r.Operation.ID,
			Method:      string(r.Operation.Kind),
			Path:        r.Operation.PolicyPath(),
			Summary:     r.Operation.Description,
			Score:       r.Score,
		})
	}
	return out, nil
}

// apiEndpointSearcher adapts an API gateway toolkit to knowledge.EndpointSearcher,
// translating apigateway.RankedOperation into the knowledge package's
// EndpointCandidate so the knowledge engine stays free of the apigateway concrete.
type apiEndpointSearcher struct {
	tk *apigatewaykit.Toolkit
}

// SearchEndpoints ranks operations across the toolkit's connections and maps
// them onto knowledge.EndpointCandidate. It never returns an error: the
// underlying SearchOperations degrades to a lexical fallback rather than failing.
func (a apiEndpointSearcher) SearchEndpoints(ctx context.Context, intent string, limit int) ([]knowledge.EndpointCandidate, error) {
	ranked := a.tk.SearchOperations(ctx, intent, limit)
	out := make([]knowledge.EndpointCandidate, 0, len(ranked))
	for _, r := range ranked {
		out = append(out, knowledge.EndpointCandidate{
			Connection:  r.Connection,
			OperationID: r.Operation.OperationID,
			Method:      r.Operation.Method,
			Path:        r.Operation.Path,
			Summary:     r.Operation.Summary,
			Spec:        r.Operation.Spec,
			Score:       r.Score,
		})
	}
	return out, nil
}

// ConnectionLister adapts the toolkit registry to knowledge.ConnectionLister,
// walking it the same way the list_connections tool does so the search corpus
// and that tool report the same set. It holds the registry (not a snapshot), so
// connections added later through the admin API remain searchable.
type ConnectionLister struct {
	reg *registry.Registry
}

// NewConnectionLister builds a connection lister over the toolkit registry.
func NewConnectionLister(reg *registry.Registry) ConnectionLister {
	return ConnectionLister{reg: reg}
}

// Connections enumerates the deployment's configured connections as
// knowledge.ConnectionInfo records. Non-data toolkits (no ConnectionLister and
// not one of the fallback data kinds) are skipped.
func (l ConnectionLister) Connections() []knowledge.ConnectionInfo {
	toolkits := l.reg.All()
	infos := make([]knowledge.ConnectionInfo, 0, len(toolkits))
	for _, tk := range toolkits {
		if lister, ok := tk.(toolkit.ConnectionLister); ok {
			for _, conn := range lister.ListConnections() {
				infos = append(infos, knowledge.ConnectionInfo{
					Name:        conn.Name,
					Kind:        tk.Kind(),
					Description: conn.Description,
					Bound:       conn.Name,
					ReadOnly:    conn.ReadOnly,
				})
			}
			continue
		}
		if !fallbackConnectionKinds[tk.Kind()] {
			continue
		}
		// A single-connection toolkit's persona/audit identity is its configured
		// connection name, which may differ from its instance name; connid
		// derives it so discovery filters on exactly what the authorizer checks.
		c := connid.NewResolver([]registry.Toolkit{tk}, nil).ByInstance(tk.Kind(), connid.Instance(tk.Name()))
		infos = append(infos, knowledge.ConnectionInfo{
			Name: tk.Name(), Kind: tk.Kind(), Bound: string(c.Bound),
		})
	}
	return infos
}
