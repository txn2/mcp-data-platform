package knowledge

import (
	"context"
	"log/slog"
	"sort"
	"strings"
)

// SourceEndpoints is the provenance label for API-endpoint hits.
const SourceEndpoints = "endpoints"

// EndpointCandidate is one API operation matched by an endpoint searcher,
// already ranked and scoped within its API gateway connection. The knowledge
// package defines it (rather than importing the apigateway concrete) so the
// federation engine stays decoupled from any one toolkit; the platform adapts
// each apigateway toolkit to EndpointSearcher.
type EndpointCandidate struct {
	Connection  string
	OperationID string
	Method      string
	Path        string
	Summary     string
	Spec        string
	Score       float64
}

// EndpointSearcher ranks API operations across the connections of one API
// gateway toolkit, applying that toolkit's per-connection route policy so a
// caller never sees an operation their persona could not invoke. The platform
// wires one EndpointSearcher per apigateway toolkit.
type EndpointSearcher interface {
	SearchEndpoints(ctx context.Context, intent string, limit int) ([]EndpointCandidate, error)
}

// EndpointsProvider exposes API endpoints to the router as a relevance search,
// aggregated across every API gateway toolkit. API endpoints are in the default
// corpus by design (#645): an agent searching "customer retention" should see a
// relevant operation next to the dataset and the insight without first having
// to know an API gateway exists, list connections, and search each one.
// api_list_endpoints stays the scoped drill-down, the way datahub_browse is the
// scoped counterpart to catalog search.
//
// It is shared: endpoints are global to the deployment, and each searcher
// enforces its own per-connection route policy fail-closed, so the provider
// needs no caller identity of its own.
type EndpointsProvider struct {
	searchers []EndpointSearcher
}

// NewEndpointsProvider builds the endpoints provider over one or more endpoint
// searchers (one per API gateway toolkit).
func NewEndpointsProvider(searchers ...EndpointSearcher) *EndpointsProvider {
	return &EndpointsProvider{searchers: searchers}
}

// Name returns the provenance label.
func (*EndpointsProvider) Name() string { return SourceEndpoints }

// Scope marks endpoints shared (always queried); each searcher self-filters
// operations to those the caller's persona may invoke.
func (*EndpointsProvider) Scope() Scope { return ScopeShared }

// Search returns API operations relevant to the intent, aggregated across every
// configured API gateway. It responds to the text path only; a query with no
// intent yields nothing. A single searcher erroring is logged and skipped so
// one unhealthy gateway does not blank the endpoints group.
func (p *EndpointsProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	if q.Intent == "" {
		return nil, nil
	}

	var cands []EndpointCandidate
	for _, s := range p.searchers {
		got, err := s.SearchEndpoints(ctx, q.Intent, q.Limit)
		if err != nil {
			slog.Warn("endpoint searcher failed", "error", err)
			continue
		}
		cands = append(cands, got...)
	}
	if len(cands) == 0 {
		return nil, nil
	}

	// Order the aggregated candidates by score so the per-source cap keeps the
	// most relevant operations across all gateways, not the first gateway's.
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].Score != cands[j].Score {
			return cands[i].Score > cands[j].Score
		}
		return endpointRef(cands[i]) < endpointRef(cands[j])
	})
	if q.Limit > 0 && len(cands) > q.Limit {
		cands = cands[:q.Limit]
	}

	hits := make([]Hit, 0, len(cands))
	for _, c := range cands {
		hits = append(hits, Hit{
			Text:   endpointHitText(c),
			Source: SourceEndpoints,
			Ref:    endpointRef(c),
			Score:  c.Score,
		})
	}
	return hits, nil
}

// endpointRef renders a stable, navigational reference for an operation:
// connection plus its operation id (or method+path when the spec declares no
// operation id). It is what the agent carries into api_invoke_endpoint.
func endpointRef(c EndpointCandidate) string {
	id := c.OperationID
	if id == "" {
		id = strings.TrimSpace(c.Method + " " + c.Path)
	}
	return c.Connection + ":" + id
}

// endpointHitText renders an operation as a navigational snippet: its
// method+path and its summary when present. The agent drills in with
// api_invoke_endpoint; the snippet is a pointer, not a payload.
func endpointHitText(c EndpointCandidate) string {
	line := strings.TrimSpace(c.Method + " " + c.Path)
	if line == "" {
		line = c.OperationID
	}
	if c.Summary == "" {
		return line
	}
	return strings.TrimSpace(line + "\n" + c.Summary)
}
