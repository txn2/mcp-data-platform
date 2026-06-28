package knowledge

import (
	"context"
	"errors"
	"testing"
)

// stubFetcher is a Provider that also implements Fetcher, owning references whose
// form it recognizes via ownsPrefix and resolving them from a fixed map.
type stubFetcher struct {
	name    string
	scope   Scope
	prefix  string
	docs    map[string]*Document // ref -> content (absent ref owned but not-found)
	fetched bool                 // set when Fetch did real work (owned)
}

func (s *stubFetcher) Name() string                               { return s.name }
func (s *stubFetcher) Scope() Scope                               { return s.scope }
func (*stubFetcher) Search(context.Context, Query) ([]Hit, error) { return nil, nil }

func (s *stubFetcher) Fetch(_ context.Context, ref string, _ Caller) (*Document, bool, error) {
	if len(ref) < len(s.prefix) || ref[:len(s.prefix)] != s.prefix {
		return nil, false, nil
	}
	s.fetched = true
	if d, ok := s.docs[ref]; ok {
		return d, true, nil
	}
	return nil, true, ErrNotFound
}

// searchOnlyProvider implements Provider but NOT Fetcher; the router must skip it.
type searchOnlyProvider struct{ scope Scope }

func (searchOnlyProvider) Name() string                                 { return "search_only" }
func (p searchOnlyProvider) Scope() Scope                               { return p.scope }
func (searchOnlyProvider) Search(context.Context, Query) ([]Hit, error) { return nil, nil }

func TestRouter_Fetch(t *testing.T) {
	pages := &stubFetcher{
		name: "knowledge_pages", scope: ScopeShared, prefix: "mcp:knowledge_page:",
		docs: map[string]*Document{"mcp:knowledge_page:kp1": {Reference: "mcp:knowledge_page:kp1", Source: "knowledge_pages", Body: "body"}},
	}
	assets := &stubFetcher{
		name: "assets", scope: ScopePerUser, prefix: "mcp:asset:",
		docs: map[string]*Document{"mcp:asset:a1": {Reference: "mcp:asset:a1", Source: "assets"}},
	}

	t.Run("routes a reference to its owning provider", func(t *testing.T) {
		r := NewRouter(nil, nil, searchOnlyProvider{ScopeShared}, pages, assets)
		doc, err := r.Fetch(context.Background(), "mcp:knowledge_page:kp1", Caller{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if doc.Body != "body" {
			t.Errorf("doc = %+v", doc)
		}
	})

	t.Run("trims whitespace before dispatch", func(t *testing.T) {
		r := NewRouter(nil, nil, pages)
		if _, err := r.Fetch(context.Background(), "  mcp:knowledge_page:kp1\n", Caller{}); err != nil {
			t.Errorf("a padded reference should still resolve: %v", err)
		}
	})

	t.Run("empty reference is not-found without consulting providers", func(t *testing.T) {
		pages.fetched = false
		r := NewRouter(nil, nil, pages)
		if _, err := r.Fetch(context.Background(), "   ", Caller{}); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
		if pages.fetched {
			t.Error("no provider should be consulted for an empty reference")
		}
	})

	t.Run("a reference no provider owns is not-found", func(t *testing.T) {
		r := NewRouter(nil, nil, pages, assets)
		if _, err := r.Fetch(context.Background(), "urn:li:dataset:(x)", Caller{UserID: "u"}); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("an owned-but-missing reference is not-found", func(t *testing.T) {
		r := NewRouter(nil, nil, pages)
		if _, err := r.Fetch(context.Background(), "mcp:knowledge_page:gone", Caller{}); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("a per-user provider is skipped for an anonymous caller", func(t *testing.T) {
		assets.fetched = false
		r := NewRouter(nil, nil, assets)
		// Anonymous caller: the per-user assets provider is never consulted, so its
		// reference resolves to not-found exactly as it would be absent from search.
		if _, err := r.Fetch(context.Background(), "mcp:asset:a1", Caller{}); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
		if assets.fetched {
			t.Error("a per-user provider must not be consulted for an anonymous caller")
		}
	})

	t.Run("a per-user provider is consulted for an identified caller", func(t *testing.T) {
		assets.fetched = false
		r := NewRouter(nil, nil, assets)
		doc, err := r.Fetch(context.Background(), "mcp:asset:a1", Caller{UserID: "u"})
		if err != nil || doc.Source != "assets" {
			t.Fatalf("doc=%+v err=%v", doc, err)
		}
		if !assets.fetched {
			t.Error("the per-user provider should be consulted for an identified caller")
		}
	})

	t.Run("a real fetch error is propagated, not masked as not-found", func(t *testing.T) {
		boom := &errFetcher{}
		r := NewRouter(nil, nil, boom)
		err := func() error { _, e := r.Fetch(context.Background(), "boom:x", Caller{}); return e }()
		if err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want a real error", err)
		}
	})
}

// errFetcher owns its reference but always fails with a non-not-found error.
type errFetcher struct{}

func (errFetcher) Name() string                                 { return "boom" }
func (errFetcher) Scope() Scope                                 { return ScopeShared }
func (errFetcher) Search(context.Context, Query) ([]Hit, error) { return nil, nil }
func (errFetcher) Fetch(context.Context, string, Caller) (*Document, bool, error) {
	return nil, true, errors.New("backend down")
}
