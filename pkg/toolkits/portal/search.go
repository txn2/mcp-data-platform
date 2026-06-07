package portal

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// rankingLexical and rankingHybrid label which ranking path produced the
// results, so the caller knows whether semantic matching was applied.
const (
	rankingLexical = "lexical"
	rankingHybrid  = "hybrid"
	fieldRanking   = "ranking"
)

// handleSearch ranks the caller's saved assets by relevance to a free-text
// query. Owner scope is enforced server-side by owner_id — the same key
// handleList and the ownership checks use, so search returns exactly the assets
// the caller can list — and fails closed: a caller with no resolved identity
// cannot search (it would otherwise scope to the shared "anonymous" bucket).
// Ranking is hybrid (semantic + lexical) when an embedding provider is
// configured and lexical-only otherwise, reported as the "ranking" field so the
// caller knows which path produced the results.
func (t *Toolkit) handleSearch(ctx context.Context, input manageArtifactInput) (*mcp.CallToolResult, any, error) {
	searcher, ok := t.assetStore.(portal.AssetSearcher)
	if !ok {
		return errorResult("asset search is unavailable: semantic discovery is not enabled"), nil, nil
	}

	query := strings.TrimSpace(input.Query)
	if query == "" {
		return errorResult("query is required for search action"), nil, nil
	}

	ownerID := resolveOwnerID(ctx)
	if strings.TrimSpace(ownerID) == "" || ownerID == anonymousUserName {
		return errorResult("a user identity is required to search assets"), nil, nil
	}

	emb := embedding.EmbedForSearch(ctx, t.embedder, query)
	ranking := rankingLexical
	if len(emb) > 0 {
		ranking = rankingHybrid
	}

	scored, err := searcher.SearchAssets(ctx, portal.AssetSearchQuery{
		Embedding: emb,
		QueryText: query,
		OwnerID:   ownerID,
		Limit:     input.Limit,
	})
	if err != nil {
		return errorResult("failed to search assets: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}
	if scored == nil {
		scored = []portal.ScoredAsset{}
	}

	return jsonResult(map[string]any{
		"assets":     scored,
		fieldTotal:   len(scored),
		fieldRanking: ranking,
	})
}
