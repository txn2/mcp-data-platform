package knowledge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

type fakeAssetSearcher struct {
	scored   []portal.ScoredAsset
	err      error
	got      portal.AssetSearchQuery
	called   bool
	asset    *portal.Asset // Get result
	getErr   error
	gotGetID string
}

func (f *fakeAssetSearcher) SearchAssets(_ context.Context, q portal.AssetSearchQuery) ([]portal.ScoredAsset, error) {
	f.called = true
	f.got = q
	return f.scored, f.err
}

func (f *fakeAssetSearcher) Get(_ context.Context, id string) (*portal.Asset, error) {
	f.gotGetID = id
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.asset, nil
}

func TestAssetsProvider_Metadata(t *testing.T) {
	p := NewAssetsProvider(&fakeAssetSearcher{})
	if p.Name() != SourceAssets {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Scope() != ScopePerUser {
		t.Errorf("Scope = %v, want per-user", p.Scope())
	}
}

func TestAssetsProvider_FailsClosedWithoutAnIdentity(t *testing.T) {
	s := &fakeAssetSearcher{}
	p := NewAssetsProvider(s)
	hits, err := p.Search(context.Background(), Query{Caller: Caller{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != nil {
		t.Errorf("expected no hits, got %+v", hits)
	}
	if s.called {
		t.Error("searcher must not run for a caller with neither identifier")
	}
}

// An address alone is an identity: it is the key a managed script's output is
// recorded under for the person who owns the script (#1551), so discovery must
// scope on it rather than refuse.
func TestAssetsProvider_ScopesOnTheAddressWhenThereIsNoUserID(t *testing.T) {
	s := &fakeAssetSearcher{}
	p := NewAssetsProvider(s)
	if _, err := p.Search(context.Background(),
		Query{Caller: Caller{Email: "email-only@example.com"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.called {
		t.Fatal("the searcher must run for a caller carrying an address")
	}
	if s.got.Owner.Email != "email-only@example.com" || s.got.Owner.UserID != "" {
		t.Errorf("Owner = %+v, want scoped to the address alone", s.got.Owner)
	}
}

// A managed-script run's search stays its own inventory: neither the script
// owner's address it carries for accountability nor the author's address it
// acts for widens the ranking to a person's whole library. Acting on a named
// asset is the widened path (assetOwnerOf), and it is checked below.
func TestAssetsProvider_SearchScopesAnUnattendedCallerToItsOwnOutputs(t *testing.T) {
	s := &fakeAssetSearcher{}
	p := NewAssetsProvider(s)
	if _, err := p.Search(context.Background(), Query{Caller: Caller{
		UserID: "script:weekly", Email: "owner@example.com", OnBehalfOf: "author@example.com",
	}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.got.Owner.Email != "" {
		t.Errorf("Owner.Email = %q, want no address on an unattended search", s.got.Owner.Email)
	}
	if s.got.Owner.UserID != "script:weekly" {
		t.Errorf("Owner.UserID = %q, want the run's own principal", s.got.Owner.UserID)
	}
}

// Naming an asset is the widened path: a run reaches what the person it acts
// for owns.
func TestAssetsProvider_FetchScopesAnUnattendedCallerToTheAddressItActsFor(t *testing.T) {
	owner := assetOwnerOf(Caller{
		UserID: "script:weekly", Email: "owner@example.com", OnBehalfOf: "author@example.com",
	})
	if owner.Email != "author@example.com" {
		t.Errorf("Owner.Email = %q, want the address the run acts for", owner.Email)
	}
}

func TestAssetsProvider_ScopesAndMaps(t *testing.T) {
	s := &fakeAssetSearcher{
		scored: []portal.ScoredAsset{
			{Asset: portal.Asset{ID: "a1", Name: "Q4 Dashboard", Description: "revenue by region"}, Score: 0.6},
			{Asset: portal.Asset{ID: "a2", Name: "No Desc"}, Score: 0.5},
		},
	}
	p := NewAssetsProvider(s)
	hits, err := p.Search(context.Background(), Query{
		Intent: "revenue",
		Caller: Caller{UserID: "uuid-1"},
		Limit:  4,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.got.Owner.UserID != "uuid-1" {
		t.Errorf("Owner.UserID = %q, want scoped to caller UUID", s.got.Owner.UserID)
	}
	if len(hits) != 2 {
		t.Fatalf("len = %d, want 2", len(hits))
	}
	if hits[0].Source != SourceAssets || hits[0].Ref != "a1" || hits[0].Text != "Q4 Dashboard\nrevenue by region" {
		t.Errorf("unexpected hit[0] mapping: %+v", hits[0])
	}
	if hits[0].Reference != "mcp:asset:a1" {
		t.Errorf("canonical reference = %q, want mcp:asset:a1", hits[0].Reference)
	}
	// Asset with no description renders as just its name.
	if hits[1].Text != "No Desc" {
		t.Errorf("hit[1] text = %q, want %q", hits[1].Text, "No Desc")
	}
}

func TestAssetsProvider_SearchError(t *testing.T) {
	s := &fakeAssetSearcher{err: errors.New("boom")}
	p := NewAssetsProvider(s)
	_, err := p.Search(context.Background(), Query{Intent: "q", Caller: Caller{UserID: "uuid-1"}})
	if err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestAssetsProvider_Fetch(t *testing.T) {
	owner := "11111111-1111-1111-1111-111111111111"
	ref := knowledgepage.AssetRef("a1")

	t.Run("returns owned asset metadata", func(t *testing.T) {
		s := &fakeAssetSearcher{asset: &portal.Asset{ID: "a1", OwnerID: owner, Name: "Q3 export"}}
		doc, owned, err := NewAssetsProvider(s).Fetch(context.Background(), ref, Caller{UserID: owner})
		if !owned || err != nil {
			t.Fatalf("owned=%v err=%v", owned, err)
		}
		if s.gotGetID != "a1" {
			t.Errorf("Get id = %q", s.gotGetID)
		}
		if doc.Source != SourceAssets || doc.Title != "Q3 export" {
			t.Errorf("doc = %+v", doc)
		}
		if a, ok := doc.Content.(*portal.Asset); !ok || a.ID != "a1" {
			t.Errorf("Content = %+v, want the asset", doc.Content)
		}
		if doc.References != nil {
			t.Errorf("an asset saved outside a session points at nothing: %+v", doc.References)
		}
	})

	// The session id an asset has carried since #1318 becomes the way back to
	// the work that produced it (#1322).
	t.Run("points back at the session that produced it", func(t *testing.T) {
		s := &fakeAssetSearcher{asset: &portal.Asset{
			ID: "a1", OwnerID: owner, Name: "Q3 export", SessionID: "dps_9f2c",
		}}
		doc, _, err := NewAssetsProvider(s).Fetch(context.Background(), ref, Caller{UserID: owner})
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if len(doc.References) != 1 {
			t.Fatalf("References = %+v", doc.References)
		}
		if doc.References[0].Reference != "mcp:session:dps_9f2c" ||
			doc.References[0].Type != knowledgepage.RefTargetSession {
			t.Errorf("References[0] = %+v, want the session reference", doc.References[0])
		}
	})

	t.Run("declines a non-asset reference", func(t *testing.T) {
		s := &fakeAssetSearcher{}
		_, owned, err := NewAssetsProvider(s).Fetch(context.Background(), "mcp:prompt:p1", Caller{UserID: owner})
		if owned || err != nil {
			t.Errorf("owned=%v err=%v, want declined", owned, err)
		}
		if s.gotGetID != "" {
			t.Errorf("Get must not be called for a non-asset reference")
		}
	})

	t.Run("anonymous caller is not-found (fail closed)", func(t *testing.T) {
		s := &fakeAssetSearcher{asset: &portal.Asset{ID: "a1", OwnerID: owner}}
		_, owned, err := NewAssetsProvider(s).Fetch(context.Background(), ref, Caller{})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want owned + ErrNotFound", owned, err)
		}
		if s.gotGetID != "" {
			t.Errorf("Get must not be called without a caller identity")
		}
	})

	t.Run("another owner's asset is not-found (no leak)", func(t *testing.T) {
		s := &fakeAssetSearcher{asset: &portal.Asset{ID: "a1", OwnerID: "someone-else", Name: "secret"}}
		_, owned, err := NewAssetsProvider(s).Fetch(context.Background(), ref, Caller{UserID: owner})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want owned + ErrNotFound for another owner's asset", owned, err)
		}
	})

	t.Run("soft-deleted asset is not-found", func(t *testing.T) {
		del := time.Now()
		s := &fakeAssetSearcher{asset: &portal.Asset{ID: "a1", OwnerID: owner, DeletedAt: &del}}
		_, owned, err := NewAssetsProvider(s).Fetch(context.Background(), ref, Caller{UserID: owner})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want owned + ErrNotFound", owned, err)
		}
	})

	t.Run("a stale citation (store ErrNoRows) is not-found, not a failure", func(t *testing.T) {
		// The real asset store reports a missing row as a wrapped sql.ErrNoRows; a
		// deleted/stale citation must read as a clean not-found.
		s := &fakeAssetSearcher{getErr: fmt.Errorf("querying asset: %w", sql.ErrNoRows)}
		_, owned, err := NewAssetsProvider(s).Fetch(context.Background(), ref, Caller{UserID: owner})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want owned + ErrNotFound", owned, err)
		}
	})

	t.Run("a genuine store error surfaces as a real error", func(t *testing.T) {
		s := &fakeAssetSearcher{getErr: errors.New("connection refused")}
		_, owned, err := NewAssetsProvider(s).Fetch(context.Background(), ref, Caller{UserID: owner})
		if !owned || err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want owned + a non-not-found error", owned, err)
		}
	})
}

// TestAssetsProvider_CarriesTheTableReference is the cross-component
// assertion: the lookup bound by the composition root actually reaches a
// search hit and a fetched document through the real provider, with the
// subject built from the asset's own bucket and key so staleness can be judged
// against them (#1327).
func TestAssetsProvider_CarriesTheTableReference(t *testing.T) {
	asset := portal.Asset{
		ID: "a1", Name: "Vendor keys", OwnerID: "u1",
		S3Bucket: "portal-assets", S3Key: "artifacts/u1/a1/content.csv",
		ContentType: "text/csv",
	}
	searcher := &fakeAssetSearcher{
		scored: []portal.ScoredAsset{{Asset: asset, Score: 0.9}},
		asset:  &asset,
	}
	lookup := &stubLookup{tables: map[string]*HitTable{
		"a1": {Connection: "scratch", Table: "scratch.uploads.analyst_vendor_keys"},
	}}

	p := NewAssetsProvider(searcher)
	p.SetTableLookup(lookup)

	hits, err := p.Search(context.Background(),
		Query{Intent: "vendor keys", Caller: Caller{UserID: "u1"}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Table == nil {
		t.Fatalf("hit carries no table reference: %+v", hits)
	}
	if hits[0].Table.Table != "scratch.uploads.analyst_vendor_keys" {
		t.Errorf("table = %q", hits[0].Table.Table)
	}

	// The subject is built from the asset's own location, which is what lets a
	// moved head key be reported as stale.
	if len(lookup.seen) != 1 || lookup.seen[0].Bucket != "portal-assets" ||
		lookup.seen[0].HeadKey != "artifacts/u1/a1/content.csv" {
		t.Errorf("subject = %+v; want the asset's bucket and head key", lookup.seen)
	}
	if lookup.seen[0].Kind != TableKindAsset {
		t.Errorf("kind = %q; want %q", lookup.seen[0].Kind, TableKindAsset)
	}

	// Fetch carries the same marker, so a record read in full says it can be
	// joined as plainly as its snippet did.
	doc, owned, err := p.Fetch(context.Background(), "mcp:asset:a1", Caller{UserID: "u1"})
	if err != nil || !owned {
		t.Fatalf("Fetch: owned=%v err=%v", owned, err)
	}
	if doc.Table == nil || doc.Table.Table != "scratch.uploads.analyst_vendor_keys" {
		t.Errorf("document carries no table reference: %+v", doc.Table)
	}
}

// TestAssetsProvider_WithoutALookupServesTheHitsItAlwaysDid.
func TestAssetsProvider_WithoutALookup(t *testing.T) {
	asset := portal.Asset{ID: "a1", Name: "Vendor keys", OwnerID: "u1"}
	p := NewAssetsProvider(&fakeAssetSearcher{
		scored: []portal.ScoredAsset{{Asset: asset, Score: 0.9}},
	})

	hits, err := p.Search(context.Background(),
		Query{Intent: "vendor keys", Caller: Caller{UserID: "u1"}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Table != nil {
		t.Errorf("a deployment with no registration mechanism carries no reference: %+v", hits)
	}
}
