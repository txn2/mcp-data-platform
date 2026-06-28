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
