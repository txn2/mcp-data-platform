package knowledge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/producedby"
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

// AssetsProvider exposes a caller's managed portal assets to the
// router. It is per-user: results are restricted to the assets the caller owns,
// judged on the pair of identifiers an asset records (assetOwnerOf), so an
// output a managed script wrote for its owner is discoverable by that person
// and not by anybody else (#1551).
type AssetsProvider struct {
	searcher AssetSearcher
	tables   TableLookup
	produced ScriptProducerLookup
}

// ScriptProducerLookup reports whether the asset was produced by the managed
// script with this id, read from the producer relation the write recorded
// (content_producers). It is what keeps this provider's fetch able to
// dereference everything its own search returns to a run.
//
// A nil lookup answers false everywhere, which is what a deployment keeping no
// producer record gets: a run then reaches only what the person it acts for
// owns, and its search returns nothing to dereference in the first place.
type ScriptProducerLookup func(ctx context.Context, assetID, scriptID string) bool

// AssetProducerReader is the optional capability an asset store implements to
// answer whether one producer wrote one asset. The PostgreSQL store does,
// because it holds the producer record it writes on every insert; federation
// asserts it off the store exactly as it asserts AssetSearcher, so a store
// without it leaves the provider serving what it always did.
type AssetProducerReader interface {
	AssetHasProducer(ctx context.Context, assetID, producerKind, producerID string) (bool, error)
}

// NewAssetsProvider builds the assets provider over an asset searcher.
func NewAssetsProvider(searcher AssetSearcher) *AssetsProvider {
	return &AssetsProvider{searcher: searcher}
}

// SetProducerLookup binds the lookup that tells a fetch whether an asset was
// produced by the script an unattended caller is a run of. Called after
// construction for the reason SetTableLookup is: a provider without it serves
// what it always did.
func (p *AssetsProvider) SetProducerLookup(lookup ScriptProducerLookup) {
	p.produced = lookup
}

// SetTableLookup binds the lookup that tells a hit whether the asset behind it
// is readable as a query-engine table (#1327). Called after construction
// because registration wiring resolves later than search federation; a
// provider with no lookup serves the hits it always did.
func (p *AssetsProvider) SetTableLookup(lookup TableLookup) {
	p.tables = lookup
}

// Name returns the provenance label.
func (*AssetsProvider) Name() string { return SourceAssets }

// Scope marks this provider per-user; the router supplies the caller identity
// and must skip it when that identity is absent.
func (*AssetsProvider) Scope() Scope { return ScopePerUser }

// Search returns the caller's assets ranked by relevance. It fails closed on a
// caller with no identity at all rather than searching across all owners.
func (p *AssetsProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	owner, producer := assetScopeOf(q.Caller)
	if !owner.Identified() && !producer.Named() {
		return nil, nil
	}

	scored, err := p.searcher.SearchAssets(ctx, portal.AssetSearchQuery{
		Embedding:  q.Embedding,
		QueryText:  q.Intent,
		Owner:      owner,
		ProducedBy: producer,
		Limit:      q.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("asset search: %w", err)
	}

	hits := make([]Hit, 0, len(scored))
	byID := make(map[string]portal.Asset, len(scored))
	for i := range scored {
		byID[scored[i].Asset.ID] = scored[i].Asset
		hits = append(hits, Hit{
			Text:      assetHitText(scored[i].Asset),
			Source:    SourceAssets,
			Ref:       scored[i].Asset.ID,
			Score:     scored[i].Score,
			Reference: knowledgepage.AssetRef(scored[i].Asset.ID),
		})
	}
	attachTables(ctx, p.tables, hits, func(h Hit) (TableSubject, bool) {
		a, ok := byID[h.Ref]
		if !ok {
			return TableSubject{}, false
		}
		return TableSubject{
			Kind: TableKindAsset, ID: a.ID, Bucket: a.S3Bucket, HeadKey: a.S3Key,
		}, true
	})
	return hits, nil
}

// Fetch dereferences an mcp:asset:<id> reference to the asset's full metadata
// (#694), folding what manage_asset's get returns into the one fetch verb. It
// owns only the asset reference form; any other reference is declined
// (owned=false). Assets are per-user, so the read is scoped to the caller exactly
// as Search is: an asset the caller does not own, a missing id, or a soft-deleted
// asset all return ErrNotFound, so fetch never reveals another owner's asset (or
// even its existence). The blob bytes live in S3 and are reached with s3_object (get or
// presign); this returns the metadata record (name, description, tags, S3
// location, size, provenance).
func (p *AssetsProvider) Fetch(ctx context.Context, ref string, caller Caller) (*Document, bool, error) {
	parsed, err := knowledgepage.ParseEntityRef(ref)
	if err != nil || parsed.TargetType != knowledgepage.RefTargetAsset {
		// Not an asset reference (another scheme or malformed): decline so the Router
		// tries the next provider. The parse error is intentionally not propagated.
		return nil, false, nil //nolint:nilerr // a non-asset reference is a decline, not a failure
	}
	owner := assetOwnerOf(caller)
	if !owner.Identified() && caller.ProducerID == "" {
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
	if asset == nil || asset.DeletedAt != nil || !p.mayFetch(ctx, caller, owner, asset) {
		return nil, true, ErrNotFound
	}
	return &Document{
		Reference:  ref,
		Source:     SourceAssets,
		Title:      asset.Name,
		Content:    asset,
		References: assetOutboundRefs(*asset),
		Table: lookupOneTable(ctx, p.tables, TableSubject{
			Kind: TableKindAsset, ID: asset.ID, Bucket: asset.S3Bucket, HeadKey: asset.S3Key,
		}),
	}, true, nil
}

// mayFetch reports whether this caller may dereference a reference to this
// asset: it belongs to the person the caller acts as, or -- for a managed-script
// run -- this run's own script produced it.
//
// The second arm exists so fetch can dereference everything search returned. A
// run's search is scoped by its producer rather than by either identifier on the
// row, because the owner id is the principal every same-named script shares and
// the owner_email is the script owner's address as of the row's insert, which a
// transfer does not rewrite (#1579). Without the matching arm here, a run whose
// author is not its script's owner -- what a transfer or an administrator's edit
// leaves -- would rank its own output and then be refused the reference to it.
//
// It admits nothing the search does not already return: both read the same
// producer relation, and a person carries no producer id at all.
func (p *AssetsProvider) mayFetch(ctx context.Context, caller Caller, owner portaldomain.AssetOwner, asset *portal.Asset) bool {
	if owner.OwnsAsset(asset) {
		return true
	}
	return caller.ProducerID != "" && p.produced != nil &&
		p.produced(ctx, asset.ID, caller.ProducerID)
}

// assetOwnerOf is the ownership identity a caller is judged by when it names an
// asset.
//
// The id is what a person carries. The address is what makes discovery agree
// with the rest of the asset surfaces: a managed script's output records the
// script principal as its owner id and the script owner's address as
// owner_email, so an id-only check hides from a person the very asset a run
// produced for them (#1551). For an unattended caller the address is the one it
// acts for, which is the same person its authority came from, so a run reaches
// what its author reaches and nothing else (#1419) -- and only that address,
// since a script principal is unique only within its owner and reading it would
// admit another person's same-named script's outputs (#1579).
func assetOwnerOf(c Caller) portaldomain.AssetOwner {
	return portaldomain.NewAssetOwner(c.UserID, c.Email).ActingFor(c.OnBehalfOf)
}

// assetScopeOf is what a SEARCH is limited to: for a person their own library,
// and for an unattended caller the assets its own script produced. Exactly one
// of the two is returned.
//
// Ranking a run on the address it carries would return the whole of that
// person's library to every run of their script. A run's inventory is the
// outputs it produced; acting on a named asset its author owns is the widened
// path, and it is the one above.
//
// A run is scoped by the PRODUCER recorded for every write it made
// (content_producers), not by either identifier on the row: the owner id is the
// principal script:<name>, which two owners' same-named scripts share (#1579),
// and the owner_email is the script owner's address as of the row's insert,
// which a transfer does not rewrite. The producer id is the script's own uuid.
func assetScopeOf(c Caller) (portaldomain.AssetOwner, portaldomain.ContentProducer) {
	if c.OnBehalfOf != "" {
		return portaldomain.AssetOwner{}, portaldomain.NewContentProducer(producedby.KindScript, c.ProducerID)
	}
	return portaldomain.NewAssetOwner(c.UserID, c.Email), portaldomain.ContentProducer{}
}

// assetOutboundRefs are the links an asset declares: the session that produced
// it. The asset has carried that session id since #1318, but as a bare string
// the reader could do nothing with; as a reference it is the way back to the
// work — the calls, their purposes, and what else came of them (#1322). Nil for
// an asset saved outside any session, which has nothing to point at.
func assetOutboundRefs(a portal.Asset) []DocumentRef {
	ref := knowledgepage.SessionRef(a.SessionID)
	if ref == "" {
		return nil
	}
	return []DocumentRef{{Reference: ref, Type: knowledgepage.RefTargetSession}}
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
