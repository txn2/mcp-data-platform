package semantic

import (
	"context"
	"errors"
	"testing"
)

// countingProvider is a catalog backend that clamps every page to clampAt rows —
// whatever was asked for — and reports the true match count separately, which is
// the shape of the real DataHub client (#1238).
type countingProvider struct {
	Provider
	clampAt      int
	tableMatches int
	termMatches  int
	err          error
}

func (c *countingProvider) SearchTablesCounted(_ context.Context, filter SearchFilter) ([]TableSearchResult, int, error) {
	if c.err != nil {
		return nil, TotalUnknown, c.err
	}
	n := min(c.tableMatches, min(filter.Limit, c.clampAt))
	return make([]TableSearchResult, n), c.tableMatches, nil
}

func (c *countingProvider) SearchTables(ctx context.Context, filter SearchFilter) ([]TableSearchResult, error) {
	results, _, err := c.SearchTablesCounted(ctx, filter)
	return results, err
}

func (c *countingProvider) SearchGlossaryTermsCounted(_ context.Context, _ string, limit int) ([]EntityRef, int, error) {
	if c.err != nil {
		return nil, TotalUnknown, c.err
	}
	n := min(c.termMatches, min(limit, c.clampAt))
	return make([]EntityRef, n), c.termMatches, nil
}

func (c *countingProvider) SearchGlossaryTerms(ctx context.Context, query string, limit int) ([]EntityRef, error) {
	refs, _, err := c.SearchGlossaryTermsCounted(ctx, query, limit)
	return refs, err
}

func (*countingProvider) ListDomains(context.Context) ([]EntityRef, error) { return nil, nil }

// uncountedProvider has the picker capability but cannot count, standing in for a
// backend that implements only the plain searches.
type uncountedProvider struct {
	Provider
	tables []TableSearchResult
	terms  []EntityRef
}

func (u *uncountedProvider) SearchTables(context.Context, SearchFilter) ([]TableSearchResult, error) {
	return u.tables, nil
}

func (u *uncountedProvider) SearchGlossaryTerms(context.Context, string, int) ([]EntityRef, error) {
	return u.terms, nil
}

func (*uncountedProvider) ListDomains(context.Context) ([]EntityRef, error) { return nil, nil }

func TestSearchTablesCounted(t *testing.T) {
	ctx := context.Background()
	filter := SearchFilter{Query: "orders", Limit: 100}

	t.Run("reports matches the clamped page does not hold", func(t *testing.T) {
		p := &countingProvider{Provider: NewNoopProvider(), clampAt: 10, tableMatches: 500}
		results, total, err := SearchTablesCounted(ctx, p, filter)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 10 {
			t.Fatalf("page = %d rows, want the clamp of 10", len(results))
		}
		if total != 500 {
			t.Fatalf("total = %d, want 500 — the count must survive the clamp", total)
		}
	})

	t.Run("through the caching decorator", func(t *testing.T) {
		// CachedProvider does not forward the counted search but does expose
		// Unwrap, so the count must still be reached.
		inner := &countingProvider{Provider: NewNoopProvider(), clampAt: 10, tableMatches: 42}
		_, total, err := SearchTablesCounted(ctx, NewCachedProvider(inner, CacheConfig{}), filter)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 42 {
			t.Fatalf("total = %d, want 42 through the decorator", total)
		}
	})

	t.Run("uncounted provider yields TotalUnknown", func(t *testing.T) {
		p := &uncountedProvider{Provider: NewNoopProvider(), tables: make([]TableSearchResult, 3)}
		results, total, err := SearchTablesCounted(ctx, p, filter)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 3 {
			t.Fatalf("page = %d rows, want 3", len(results))
		}
		if total != TotalUnknown {
			t.Fatalf("total = %d, want TotalUnknown", total)
		}
	})

	t.Run("nil provider", func(t *testing.T) {
		results, total, err := SearchTablesCounted(ctx, nil, filter)
		if err != nil || results != nil || total != TotalUnknown {
			t.Fatalf("nil provider = (%v, %d, %v), want (nil, TotalUnknown, nil)", results, total, err)
		}
	})

	t.Run("search failure reports no count", func(t *testing.T) {
		p := &countingProvider{Provider: NewNoopProvider(), err: errors.New("catalog down")}
		_, total, err := SearchTablesCounted(ctx, p, filter)
		if err == nil {
			t.Fatal("expected the search error to propagate")
		}
		if total != TotalUnknown {
			t.Fatalf("total = %d, want TotalUnknown on failure", total)
		}
	})
}

func TestSearchGlossaryTermsCounted(t *testing.T) {
	ctx := context.Background()

	t.Run("reports matches the clamped page does not hold", func(t *testing.T) {
		p := &countingProvider{Provider: NewNoopProvider(), clampAt: 5, termMatches: 80}
		picker, ok := CatalogPickerFrom(p)
		if !ok {
			t.Fatal("expected picker capability")
		}
		refs, total, err := SearchGlossaryTermsCounted(ctx, picker, "rev", 100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 5 || total != 80 {
			t.Fatalf("got %d refs / total %d, want 5 / 80", len(refs), total)
		}
	})

	t.Run("uncounted picker yields TotalUnknown", func(t *testing.T) {
		p := &uncountedProvider{Provider: NewNoopProvider(), terms: make([]EntityRef, 2)}
		picker, ok := CatalogPickerFrom(p)
		if !ok {
			t.Fatal("expected picker capability")
		}
		refs, total, err := SearchGlossaryTermsCounted(ctx, picker, "rev", 100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 2 || total != TotalUnknown {
			t.Fatalf("got %d refs / total %d, want 2 / TotalUnknown", len(refs), total)
		}
	})

	t.Run("nil picker", func(t *testing.T) {
		refs, total, err := SearchGlossaryTermsCounted(ctx, nil, "rev", 100)
		if err != nil || refs != nil || total != TotalUnknown {
			t.Fatalf("nil picker = (%v, %d, %v), want (nil, TotalUnknown, nil)", refs, total, err)
		}
	})

	t.Run("search failure reports no count", func(t *testing.T) {
		p := &countingProvider{Provider: NewNoopProvider(), err: errors.New("catalog down")}
		_, total, err := SearchGlossaryTermsCounted(ctx, p, "rev", 100)
		if err == nil {
			t.Fatal("expected the search error to propagate")
		}
		if total != TotalUnknown {
			t.Fatalf("total = %d, want TotalUnknown on failure", total)
		}
	})
}
