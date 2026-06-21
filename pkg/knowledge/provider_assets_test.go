package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/portal"
)

type fakeAssetSearcher struct {
	scored []portal.ScoredAsset
	err    error
	got    portal.AssetSearchQuery
	called bool
}

func (f *fakeAssetSearcher) SearchAssets(_ context.Context, q portal.AssetSearchQuery) ([]portal.ScoredAsset, error) {
	f.called = true
	f.got = q
	return f.scored, f.err
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

func TestAssetsProvider_FailsClosedWithoutUserID(t *testing.T) {
	s := &fakeAssetSearcher{}
	p := NewAssetsProvider(s)
	hits, err := p.Search(context.Background(), Query{Caller: Caller{Email: "email-only@example.com"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != nil {
		t.Errorf("expected no hits, got %+v", hits)
	}
	if s.called {
		t.Error("searcher must not run without a caller UUID")
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
	if s.got.OwnerID != "uuid-1" {
		t.Errorf("OwnerID = %q, want scoped to caller UUID", s.got.OwnerID)
	}
	if len(hits) != 2 {
		t.Fatalf("len = %d, want 2", len(hits))
	}
	if hits[0].Source != SourceAssets || hits[0].Ref != "a1" || hits[0].Text != "Q4 Dashboard\nrevenue by region" {
		t.Errorf("unexpected hit[0] mapping: %+v", hits[0])
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
