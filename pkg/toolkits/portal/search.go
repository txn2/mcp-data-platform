package portal

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// rankingLexical and rankingHybrid label which ranking path produced the
// results, so the caller knows whether semantic matching was applied.
const (
	rankingLexical = "lexical"
	rankingHybrid  = "hybrid"
	fieldRanking   = "ranking"
)

// handleSearch ranks the caller's saved assets by relevance to a free-text
// query. Owner scope is enforced server-side by the same judgment handleList
// applies (callerAssetScope), so search returns exactly the assets the caller
// can list — and fails closed: a caller with no resolved identity cannot search
// (it would otherwise scope to the shared "anonymous" bucket, and an empty
// scope at the store means every owner).
// Ranking is hybrid (semantic + lexical) when an embedding provider is
// configured and lexical-only otherwise, reported as the "ranking" field so the
// caller knows which path produced the results.
func (t *Toolkit) handleSearch(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	searcher, ok := t.assetStore.(portal.AssetSearcher)
	if !ok {
		return middleware.UnavailableResult(
			"asset search is unavailable: semantic discovery is not enabled",
			"This deployment has no embedding/search backend wired. Use action=list to page assets instead.",
		), nil, nil
	}

	query := strings.TrimSpace(input.Query)
	if query == "" {
		return middleware.MissingParameterResult("query"), nil, nil
	}

	owner, producer := callerAssetScope(ctx)
	if !owner.Identified() && !producer.Named() {
		return middleware.UnauthorizedResult(
			"a user identity is required to search assets",
			"Authenticate so the search can be scoped to your assets. This is an identity problem, not a platform outage.",
		), nil, nil
	}

	emb := embedding.EmbedForSearch(ctx, t.embedder, query)
	ranking := rankingLexical
	if len(emb) > 0 {
		ranking = rankingHybrid
	}

	// A person ranks everything they can open (#2500), in the scope they
	// name, all by default; a managed-script run ranks what it produced.
	if reach := callerReach(ctx, input.Scope, portaldomain.ScopeAll); reach != nil {
		if rs, ok := t.assetStore.(portaldomain.ReachSearcher); ok {
			return t.searchReach(ctx, rs, input, reachSearch{query: query, emb: emb, reach: reach, ranking: ranking})
		}
	}

	scored, err := searcher.SearchAssets(ctx, portal.AssetSearchQuery{
		Embedding:  emb,
		QueryText:  query,
		Owner:      owner,
		ProducedBy: producer,
		Limit:      input.Limit,
	})
	if err != nil {
		return toolkit.ErrorResult("failed to search assets: " + err.Error()), nil, nil
	}
	if scored == nil {
		scored = []portal.ScoredAsset{}
	}

	return toolkit.JSONResultTyped(map[string]any{
		"assets":     scored,
		fieldTotal:   len(scored),
		fieldRanking: ranking,
	})
}

// reachSearch is one search over a person's reach.
type reachSearch struct {
	query   string
	emb     []float32
	reach   *portaldomain.Reach
	ranking string
}

// searchReach ranks a person's reach with the filters the call names, one page
// at a time; total counts every result.
func (*Toolkit) searchReach(ctx context.Context, rs portaldomain.ReachSearcher, input manageAssetInput, q reachSearch) (*mcp.CallToolResult, any, error) {
	query, emb, reach, ranking := q.query, q.emb, q.reach, q.ranking
	limit, offset := portaldomain.RankedPage(input.Limit, input.Offset)
	scored, total, err := rs.SearchReach(ctx, portaldomain.RankedAssetQuery{
		Embedding:   emb,
		QueryText:   query,
		Reach:       reach,
		ContentType: input.ContentType,
		Tags:        input.Tags,
		Owners:      portaldomain.NormalizeOwners(input.Owners),
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		return toolkit.ErrorResult("failed to search assets: " + err.Error()), nil, nil
	}
	if scored == nil {
		scored = []portal.ScoredAsset{}
	}
	return toolkit.JSONResultTyped(map[string]any{
		"assets":     scored,
		fieldTotal:   total,
		fieldRanking: ranking,
		"scope":      reach.Scope,
		"offset":     offset,
		"limit":      limit,
	})
}
