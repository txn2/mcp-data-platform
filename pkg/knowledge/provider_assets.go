package knowledge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// SourceAssets is the provenance label for asset-provider hits.
const SourceAssets = "assets"

// AssetSearcher is what the provider needs from the portal asset store: relevance
// search over a caller's assets (the text path) and a by-id read (fetch). The
// concrete postgres asset store satisfies it; declared here so the provider depends
// on the capability and the platform asserts one authority for "a searchable,
// fetchable asset store".
type AssetSearcher interface {
	SearchAssets(ctx context.Context, q portal.AssetSearchQuery) ([]portal.ScoredAsset, error)
	Get(ctx context.Context, id string) (*portal.Asset, error)
}

// AssetsProvider exposes a caller's managed assets (saved artifacts) to the
// router. It is per-user: results are restricted to assets the caller owns
// (assets.owner_id == caller UUID), which is why it keys on Caller.UserID
// rather than the email the memory and insight providers use.
type AssetsProvider struct {
	searcher AssetSearcher
}

// NewAssetsProvider builds the assets provider over an asset searcher.
func NewAssetsProvider(searcher AssetSearcher) *AssetsProvider {
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
			Text:      assetHitText(scored[i].Asset),
			Source:    SourceAssets,
			Ref:       scored[i].Asset.ID,
			Score:     scored[i].Score,
			Reference: knowledgepage.AssetRef(scored[i].Asset.ID),
		})
	}
	return hits, nil
}

// Fetch dereferences an mcp:asset:<id> reference to the asset's full metadata
// (#694), folding what manage_artifact's get returns into the one fetch verb. It
// owns only the asset reference form; any other reference is declined
// (owned=false). Assets are per-user, so the read is scoped to the caller exactly
// as Search is: an asset the caller does not own, a missing id, or a soft-deleted
// asset all return ErrNotFound, so fetch never reveals another owner's asset (or
// even its existence). The blob bytes live in S3 and are reached with s3_get_object
// / s3_presign_url; this returns the metadata record (name, description, tags, S3
// location, size, provenance).
func (p *AssetsProvider) Fetch(ctx context.Context, ref string, caller Caller) (*Document, bool, error) {
	parsed, err := knowledgepage.ParseEntityRef(ref)
	if err != nil || parsed.TargetType != knowledgepage.RefTargetAsset {
		// Not an asset reference (another scheme or malformed): decline so the Router
		// tries the next provider. The parse error is intentionally not propagated.
		return nil, false, nil //nolint:nilerr // a non-asset reference is a decline, not a failure
	}
	if caller.UserID == "" {
		return nil, true, ErrNotFound
	}
	asset, err := p.searcher.Get(ctx, parsed.AssetID)
	if err != nil {
		// The asset store reports a missing row as a wrapped sql.ErrNoRows; a stale or
		// deleted citation must be a clean not-found, not a hard failure (the same way
		// the catalog and document paths treat a miss).
		if errors.Is(err, sql.ErrNoRows) {
			return nil, true, ErrNotFound
		}
		return nil, true, fmt.Errorf("getting asset %s: %w", parsed.AssetID, err)
	}
	// Fail closed on ownership: a missing, deleted, or other-owner asset is
	// indistinguishable to the caller (all ErrNotFound), so fetch leaks neither the
	// content nor the existence of an asset the caller could not have searched.
	if asset == nil || asset.DeletedAt != nil || asset.OwnerID != caller.UserID {
		return nil, true, ErrNotFound
	}
	return &Document{
		Reference: ref,
		Source:    SourceAssets,
		Title:     asset.Name,
		Content:   asset,
	}, true, nil
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
