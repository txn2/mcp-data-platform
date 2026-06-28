package knowledge

import (
	"context"
	"errors"
	"testing"
)

// stubBrowser is a Provider that also implements Browser, enumerating a fixed set.
type stubBrowser struct {
	name    string
	scope   Scope
	total   int
	err     error
	gotQ    BrowseQuery
	browsed bool
}

func (s *stubBrowser) Name() string                               { return s.name }
func (s *stubBrowser) Scope() Scope                               { return s.scope }
func (*stubBrowser) Search(context.Context, Query) ([]Hit, error) { return nil, nil }

func (s *stubBrowser) Browse(_ context.Context, q BrowseQuery) (BrowsePage, error) {
	s.browsed = true
	s.gotQ = q
	if s.err != nil {
		return BrowsePage{}, s.err
	}
	hits := make([]Hit, 0, q.Limit)
	for i := q.Offset; i < q.Offset+q.Limit && i < s.total; i++ {
		hits = append(hits, Hit{Source: s.name, Ref: "r"})
	}
	return BrowsePage{Hits: hits, Total: s.total}, nil
}

func TestRouter_Browse(t *testing.T) {
	t.Run("enumerates a browsable source and stamps effective offset/limit", func(t *testing.T) {
		pages := &stubBrowser{name: SourceKnowledgePages, scope: ScopeShared, total: 7}
		r := NewRouter(nil, nil, searchOnlyProvider{ScopeShared}, pages)
		page, err := r.Browse(context.Background(), SourceKnowledgePages, BrowseQuery{Offset: 5, Limit: 3})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if page.Total != 7 || page.Offset != 5 || page.Limit != 3 {
			t.Errorf("page meta = %+v, want total 7 offset 5 limit 3", page)
		}
		if len(page.Hits) != 2 { // members 5 and 6 of 0..6
			t.Errorf("len(hits) = %d, want 2", len(page.Hits))
		}
	})

	t.Run("floors a negative offset and clamps the limit", func(t *testing.T) {
		b := &stubBrowser{name: SourceKnowledgePages, scope: ScopeShared, total: 5}
		r := NewRouter(nil, nil, b)
		page, err := r.Browse(context.Background(), SourceKnowledgePages, BrowseQuery{Offset: -10, Limit: 9999})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if b.gotQ.Offset != 0 || b.gotQ.Limit != maxBrowseLimit {
			t.Errorf("provider got offset=%d limit=%d, want 0 and %d", b.gotQ.Offset, b.gotQ.Limit, maxBrowseLimit)
		}
		if page.Offset != 0 || page.Limit != maxBrowseLimit {
			t.Errorf("echoed offset/limit = %d/%d", page.Offset, page.Limit)
		}
	})

	t.Run("defaults the limit when unset", func(t *testing.T) {
		b := &stubBrowser{name: SourceKnowledgePages, scope: ScopeShared, total: 1}
		r := NewRouter(nil, nil, b)
		if _, err := r.Browse(context.Background(), SourceKnowledgePages, BrowseQuery{}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if b.gotQ.Limit != defaultBrowseLimit {
			t.Errorf("default limit = %d, want %d", b.gotQ.Limit, defaultBrowseLimit)
		}
	})

	t.Run("source name matching is case-insensitive, like search", func(t *testing.T) {
		b := &stubBrowser{name: SourceKnowledgePages, scope: ScopeShared, total: 1}
		r := NewRouter(nil, nil, b)
		if _, err := r.Browse(context.Background(), "  Knowledge_Pages  ", BrowseQuery{}); err != nil {
			t.Errorf("a mixed-case, padded source should resolve like search: %v", err)
		}
	})

	t.Run("unknown source is ErrUnknownSource", func(t *testing.T) {
		r := NewRouter(nil, nil, &stubBrowser{name: SourceKnowledgePages, scope: ScopeShared})
		_, err := r.Browse(context.Background(), "definitely-not-a-source", BrowseQuery{})
		if !errors.Is(err, ErrUnknownSource) {
			t.Errorf("err = %v, want ErrUnknownSource", err)
		}
	})

	t.Run("a known but search-only source is not browsable", func(t *testing.T) {
		// memory is a known source name but the provider here does not implement Browser.
		r := NewRouter(nil, nil, &nameOnlyProvider{name: SourceMemory, scope: ScopePerUser})
		_, err := r.Browse(context.Background(), SourceMemory, BrowseQuery{Caller: Caller{Email: "a@b.c"}})
		if !errors.Is(err, ErrSourceNotBrowsable) {
			t.Errorf("err = %v, want ErrSourceNotBrowsable", err)
		}
	})

	t.Run("a known source with no provider on this deployment is not browsable", func(t *testing.T) {
		// catalog is a known source name, but no provider for it is registered here.
		r := NewRouter(nil, nil, &stubBrowser{name: SourceKnowledgePages, scope: ScopeShared})
		_, err := r.Browse(context.Background(), SourceCatalog, BrowseQuery{})
		if !errors.Is(err, ErrSourceNotBrowsable) {
			t.Errorf("err = %v, want ErrSourceNotBrowsable", err)
		}
	})

	t.Run("a per-user source is not browsable for an anonymous caller", func(t *testing.T) {
		b := &stubBrowser{name: SourceAssets, scope: ScopePerUser, total: 3}
		r := NewRouter(nil, nil, b)
		_, err := r.Browse(context.Background(), SourceAssets, BrowseQuery{})
		if !errors.Is(err, ErrSourceNotBrowsable) {
			t.Errorf("err = %v, want ErrSourceNotBrowsable", err)
		}
		if b.browsed {
			t.Error("a per-user provider must not be browsed for an anonymous caller")
		}
	})

	t.Run("a provider error is propagated", func(t *testing.T) {
		r := NewRouter(nil, nil, &stubBrowser{name: SourceKnowledgePages, scope: ScopeShared, err: errors.New("db down")})
		if _, err := r.Browse(context.Background(), SourceKnowledgePages, BrowseQuery{}); err == nil {
			t.Error("expected the provider error to propagate")
		}
	})
}

func TestRouter_BrowsableSources(t *testing.T) {
	r := NewRouter(nil, nil,
		&stubBrowser{name: SourceContextDocuments, scope: ScopeShared},
		searchOnlyProvider{ScopeShared}, // not a Browser
		&stubBrowser{name: SourceKnowledgePages, scope: ScopeShared},
	)
	got := r.BrowsableSources()
	// Sorted, and only the Browser-implementing providers.
	if len(got) != 2 || got[0] != SourceContextDocuments || got[1] != SourceKnowledgePages {
		t.Errorf("BrowsableSources = %v, want [%s %s] sorted", got, SourceContextDocuments, SourceKnowledgePages)
	}
}

// nameOnlyProvider is a Provider with a given name that does NOT implement Browser.
type nameOnlyProvider struct {
	name  string
	scope Scope
}

func (p *nameOnlyProvider) Name() string                               { return p.name }
func (p *nameOnlyProvider) Scope() Scope                               { return p.scope }
func (*nameOnlyProvider) Search(context.Context, Query) ([]Hit, error) { return nil, nil }
