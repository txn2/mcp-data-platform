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
// It is shared: endpoints are deployment-level, not per-user records. Two
// filters narrow them. Each searcher applies its own per-(method, path) route
// policy, which is a no-op for a connection no APIRoutes rule mentions; the
// provider then applies the caller's connection boundary (#1108), so a persona
// that is not granted an API connection never sees that connection's operation
// inventory — the route policy alone cannot express that, because it defers to
// the connection-level gate the discovery path was missing.
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
// configured API gateway and narrowed to the connections the caller's persona
// may reach. It responds to the text path only; a query with no intent yields
// nothing. A single searcher erroring is logged and skipped so one unhealthy
// gateway does not blank the endpoints group.
//
// The connection filter runs on the aggregate before the per-source cap, so a
// permitted gateway's operations are never crowded out of the candidate list by
// a gateway the caller may not see.
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
	cands = permittedEndpoints(cands, q.Caller)
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

// permittedEndpoints drops the candidates whose API connection the caller's
// persona may not reach and records the count on the caller's connection gate,
// so the coverage summary reports "present, but not yours to see" rather than
// letting the endpoints group silently shorten.
func permittedEndpoints(cands []EndpointCandidate, caller Caller) []EndpointCandidate {
	out := make([]EndpointCandidate, 0, len(cands))
	withheld := 0
	for _, c := range cands {
		if !caller.allowsConnection(c.Connection) {
			withheld++
			continue
		}
		out = append(out, c)
	}
	caller.withhold(withheld)
	return out
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
