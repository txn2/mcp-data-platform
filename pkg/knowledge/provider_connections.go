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
}

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
// It is shared: connection metadata (name, kind, description) is already
// globally visible through list_connections, so there is no per-user record to
// scope here. The security-sensitive boundary is which connections a persona
// may use, and that is enforced fail-closed at tool-call time on the scoped
// tools (trino_query, api_invoke_endpoint, ...), unchanged by this provider.
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

// Search returns connections whose name, kind, or description match the intent,
// ranked by a lexical token-overlap score. Connections carry no embeddings, so
// ranking is lexical; the score still feeds the allocator's per-source
// normalization. It responds to the text path only.
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
	for _, c := range p.lister.Connections() {
		if s := connectionScore(c, tokens); s > 0 {
			matches = append(matches, scored{conn: c, score: s})
		}
	}
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

// connectionScore is the fraction of query tokens that appear as a substring of
// the connection's searchable text (name, kind, description). A connection that
// matches more of the query ranks higher; zero means no token matched and the
// connection is dropped.
func connectionScore(c ConnectionInfo, tokens []string) float64 {
	hay := strings.ToLower(strings.Join([]string{c.Name, c.Kind, c.Description}, " "))
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
