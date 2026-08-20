package knowledge

import (
	"context"
	"sort"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// SourceConnections is the provenance label for connection hits.
const SourceConnections = "connections"

// ConnectionInfo is one configured data connection, reduced to the fields a
// relevance search needs. The knowledge package defines it (rather than
// importing the platform's connection types) so the federation engine stays
// decoupled; the platform adapts its connection registry to ConnectionLister.
type ConnectionInfo struct {
	Name        string
	Kind        string
	Description string
	// Bound is the identity a persona's connections rules and the audit trail
	// use, which is what a tool call's connection argument carries. Name is the
	// instance the connection is configured and stored under, and the two
	// differ for a single-connection toolkit that sets a connection_name.
	//
	// The producer resolves it through connid rather than leaving it empty to
	// mean "same as Name": that convention put the derivation rule in a third
	// place, and discovery must key on exactly what the authorizer checks or a
	// connection an operator granted is hidden by the platform preferring the
	// other name.
	Bound string
}

// gateable reports whether this entry carries the identity the persona rules
// are matched on. An entry with no Bound is withheld rather than inferred from
// Name: inferring would put the derivation rule back in a third place, and
// guessing wrong on this predicate shows a caller a connection they were never
// granted. The producer resolves Bound through connid, so an empty one is a
// wiring fault, not a shape a real connection has.
func (c ConnectionInfo) gateable() bool { return c.Bound != "" }

// ConnectionLister enumerates the deployment's configured connections. The
// platform implements it over the same toolkit registry list_connections uses,
// so the search corpus and the connections tool stay in agreement.
type ConnectionLister interface {
	Connections() []ConnectionInfo
}

// ConnectionsProvider exposes configured connections to the router as a
// relevance search. Connections are in the default corpus by design (#645): an
// agent should discover that, say, a "stripe" or "warehouse" connection exists
// from one search, rather than having to know to call list_connections first.
// list_connections stays the full enumeration; this surfaces the connections
// relevant to a query.
//
// It is shared: connections are deployment-level, not per-user records. It is
// not unfiltered, though — results are narrowed to the connections the caller's
// persona is granted (#1108), the same connections.allow boundary the authorizer
// applies to a tool call and list_connections applies to its enumeration, so a
// persona restricted to one connection cannot discover the inventory of the
// rest.
type ConnectionsProvider struct {
	lister ConnectionLister
}

// NewConnectionsProvider builds the connections provider over a lister.
func NewConnectionsProvider(lister ConnectionLister) *ConnectionsProvider {
	return &ConnectionsProvider{lister: lister}
}

// Name returns the provenance label.
func (*ConnectionsProvider) Name() string { return SourceConnections }

// Scope marks connections shared: their metadata is already global via
// list_connections.
func (*ConnectionsProvider) Scope() Scope { return ScopeShared }

// Search returns connections whose name, kind, or description match the intent
// AND that the caller's persona may reach, ranked by a lexical token-overlap
// score. Connections carry no embeddings, so ranking is lexical; the score still
// feeds the allocator's per-source normalization. It responds to the text path
// only.
//
// A connection that matches the intent but is outside the persona's
// connections.allow is counted as withheld rather than dropped silently, so the
// coverage summary can say "present, but not yours to see". The filter runs
// before the candidate cap, so a permitted connection is never crowded out of
// the candidate list by ones the caller may not see.
func (p *ConnectionsProvider) Search(_ context.Context, q Query) ([]Hit, error) {
	if q.Intent == "" {
		return nil, nil
	}
	tokens := strings.Fields(strings.ToLower(q.Intent))
	if len(tokens) == 0 {
		return nil, nil
	}

	type scored struct {
		conn  ConnectionInfo
		score float64
	}
	var matches []scored
	withheld := 0
	for _, c := range p.lister.Connections() {
		if s := connectionScore(c, tokens); s > 0 {
			if !c.gateable() || !q.Caller.allowsConnection(c.Bound) {
				withheld++
				continue
			}
			matches = append(matches, scored{conn: c, score: s})
		}
	}
	q.Caller.withhold(withheld)
	if len(matches) == 0 {
		return nil, nil
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].conn.Name < matches[j].conn.Name
	})
	if q.Limit > 0 && len(matches) > q.Limit {
		matches = matches[:q.Limit]
	}

	hits := make([]Hit, 0, len(matches))
	for _, m := range matches {
		hits = append(hits, Hit{
			Text:      connectionHitText(m.conn),
			Source:    SourceConnections,
			Ref:       m.conn.Name,
			Score:     m.score,
			Reference: knowledgepage.ConnectionRef(m.conn.Kind, m.conn.Name),
		})
	}
	return hits, nil
}

// Fetch dereferences an mcp:connection:(kind,name) reference to the connection's
// descriptor (#694), folding what list_connections enumerates into the one fetch
// verb. It owns only the connection reference form; any other reference is declined
// (owned=false). A reference that matches no current connection, or one naming a
// connection outside the caller's persona (#1108), is ErrNotFound — the same
// answer search gives by omitting it, so a citation cannot be used to read around
// the boundary search enforces.
func (p *ConnectionsProvider) Fetch(_ context.Context, ref string, caller Caller) (*Document, bool, error) {
	parsed, err := knowledgepage.ParseEntityRef(ref)
	if err != nil || parsed.TargetType != knowledgepage.RefTargetConnection {
		// Not a connection reference: decline so the Router tries the next provider.
		return nil, false, nil //nolint:nilerr // a non-connection reference is a decline, not a failure
	}
	for _, c := range p.lister.Connections() {
		if c.Kind == parsed.ConnectionKind && c.Name == parsed.ConnectionName {
			if !c.gateable() || !caller.allowsConnection(c.Bound) {
				return nil, true, ErrNotFound
			}
			return &Document{
				Reference: ref,
				Source:    SourceConnections,
				Title:     connectionHitText(c),
				Content:   c,
			}, true, nil
		}
	}
	return nil, true, ErrNotFound
}

// connectionScore is the token-overlap score of the connection's searchable text
// (name, kind, description). Zero means no token matched and the connection is
// dropped.
func connectionScore(c ConnectionInfo, tokens []string) float64 {
	return tokenOverlapScore(strings.Join([]string{c.Name, c.Kind, c.Description}, " "), tokens)
}

// tokenOverlapScore is the fraction of query tokens that appear as a substring of
// a record's searchable text, in [0,1]. It is the lexical rule the sources with no
// backend ranking of their own share — connections and DataHub domains, neither of
// which any upstream search can rank — held once so the two cannot drift. A record
// matching more of the query ranks higher; zero means nothing matched.
func tokenOverlapScore(text string, tokens []string) float64 {
	if len(tokens) == 0 {
		return 0
	}
	hay := strings.ToLower(text)
	matched := 0
	for _, tok := range tokens {
		if strings.Contains(hay, tok) {
			matched++
		}
	}
	return float64(matched) / float64(len(tokens))
}

// connectionHitText renders a connection as a navigational snippet: its name
// and kind, plus its description when present.
func connectionHitText(c ConnectionInfo) string {
	head := c.Name
	if c.Kind != "" {
		head = c.Name + " (" + c.Kind + ")"
	}
	if c.Description == "" {
		return head
	}
	return strings.TrimSpace(head + "\n" + c.Description)
}
