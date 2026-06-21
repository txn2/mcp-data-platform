package knowledge

import (
	"context"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// SourceAssets is the provenance label for asset-provider hits.
const SourceAssets = "assets"

// assetSearcher is the relevance-search capability of the portal asset store.
// It matches portal.AssetSearcher; declared locally so the provider depends on
// the capability and tests can supply a fake.
type assetSearcher interface {
	SearchAssets(ctx context.Context, q portal.AssetSearchQuery) ([]portal.ScoredAsset, error)
}

// AssetsProvider exposes a caller's managed assets (saved artifacts) to the
// router. It is per-user: results are restricted to assets the caller owns
// (assets.owner_id == caller UUID), which is why it keys on Caller.UserID
// rather than the email the memory and insight providers use.
type AssetsProvider struct {
	searcher assetSearcher
}

// NewAssetsProvider builds the assets provider over an asset searcher.
func NewAssetsProvider(searcher assetSearcher) *AssetsProvider {
	return &AssetsProvider{searcher: searcher}
}

// Name returns the provenance label.
func (*AssetsProvider) Name() string { return SourceAssets }

// Scope marks this provider per-user; the router supplies the caller identity
// and must skip it when that identity is absent.
func (*AssetsProvider) Scope() Scope { return ScopePerUser }

// Search returns the caller's assets ranked by relevance. It fails closed on a
// missing caller UUID rather than searching across all owners.
func (p *AssetsProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	if q.Caller.UserID == "" {
		return nil, nil
	}

	scored, err := p.searcher.SearchAssets(ctx, portal.AssetSearchQuery{
		Embedding: q.Embedding,
		QueryText: q.Intent,
		OwnerID:   q.Caller.UserID,
		Limit:     q.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("asset search: %w", err)
	}

	hits := make([]Hit, 0, len(scored))
	for i := range scored {
		hits = append(hits, Hit{
			Text:   assetHitText(scored[i].Asset),
			Source: SourceAssets,
			Ref:    scored[i].Asset.ID,
			Score:  scored[i].Score,
		})
	}
	return hits, nil
}

// assetHitText renders an asset as a knowledge snippet: its name, and its
// description when present, so a hit conveys what the asset is without a
// follow-up fetch.
func assetHitText(a portal.Asset) string {
	if a.Description == "" {
		return a.Name
	}
	return strings.TrimSpace(a.Name + "\n" + a.Description)
}
