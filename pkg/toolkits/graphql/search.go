package graphql

import (
	"context"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/gqlschema"
	"github.com/txn2/mcp-data-platform/internal/opranking"
)

// RankedConnectionOperation is one operation matched by
// SearchOperations, tagged with the connection it belongs to and its
// score under the ranking that produced it. The score is comparable
// within a connection; across connections it is best-effort, because an
// indexed connection scores by cosine while one that fell back to
// lexical scores positionally.
type RankedConnectionOperation struct {
	Connection string
	Operation  gqlschema.Operation
	Score      float64
}

// SearchOperations ranks operations across every connection on this
// toolkit against a free-form query, returning up to perConnLimit per
// connection. It is the federation seam behind the universal search
// tool's endpoints group: the same ranking graphql_discover exposes,
// aggregated across connections instead of scoped to one.
//
// Per-connection route policy is applied first, so a federated search
// never surfaces an operation a scoped graphql_discover call would have
// hidden.
func (t *Toolkit) SearchOperations(ctx context.Context, query string, perConnLimit int) []RankedConnectionOperation {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	if perConnLimit <= 0 {
		perConnLimit = defaultDiscoverLimit
	}
	t.mu.RLock()
	policy := t.routePolicy
	conns := make([]*conn, 0, len(t.connections))
	for _, c := range t.connections {
		conns = append(conns, c)
	}
	t.mu.RUnlock()

	var out []RankedConnectionOperation
	for _, c := range conns {
		out = append(out, t.searchConn(ctx, policy, c, query, perConnLimit)...)
	}
	return out
}

// searchConn ranks one connection's policy-visible operations. It
// always emits a score — positional when the semantic path is
// unavailable — so the aggregate carries a relevance signal into the
// search allocator.
func (t *Toolkit) searchConn(ctx context.Context, policy RoutePolicy, c *conn, query string, limit int) []RankedConnectionOperation {
	c.schemaMu.RLock()
	ops := c.operations
	c.schemaMu.RUnlock()
	visible := filterByRoutePolicy(ctx, policy, c.cfg.ConnectionName, ops)
	if len(visible) == 0 {
		return nil
	}
	byID, cands := candidates(c, visible)
	queryVec, err := t.queryVectorFor(ctx, c, query)
	if err != nil {
		return tag(c.cfg.ConnectionName, resolve(byID, opranking.Filter(cands, query, limit)))
	}
	scored := opranking.Score(cands, query, queryVec, opranking.ModeHybrid)
	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}
	return tag(c.cfg.ConnectionName, resolve(byID, scored))
}

// tag attaches the connection name to each scored operation.
func tag(connection string, scored []scoredOperation) []RankedConnectionOperation {
	out := make([]RankedConnectionOperation, 0, len(scored))
	for _, s := range scored {
		out = append(out, RankedConnectionOperation{
			Connection: connection, Operation: s.op, Score: s.score,
		})
	}
	return out
}
