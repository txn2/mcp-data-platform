package semantic

import (
	"context"
	"testing"
)

// pickerProvider embeds the noop provider and adds the CatalogPicker capability,
// standing in for the real DataHub adapter.
type pickerProvider struct {
	Provider
	domains []EntityRef
	terms   []EntityRef
}

func (p *pickerProvider) ListDomains(context.Context) ([]EntityRef, error) {
	return p.domains, nil
}

func (p *pickerProvider) SearchGlossaryTerms(context.Context, string, int) ([]EntityRef, error) {
	return p.terms, nil
}

func TestCatalogPickerFrom(t *testing.T) {
	t.Run("bare picker provider", func(t *testing.T) {
		prov := &pickerProvider{Provider: NewNoopProvider(), domains: []EntityRef{{Name: "finance"}}}
		picker, ok := CatalogPickerFrom(prov)
		if !ok {
			t.Fatal("expected picker capability")
		}
		domains, _ := picker.ListDomains(context.Background())
		if len(domains) != 1 || domains[0].Name != "finance" {
			t.Fatalf("unexpected domains %v", domains)
		}
	})

	t.Run("through caching decorator", func(t *testing.T) {
		// CachedProvider does not forward the picker methods but does expose
		// Unwrap, so CatalogPickerFrom must reach the inner provider.
		inner := &pickerProvider{Provider: NewNoopProvider(), terms: []EntityRef{{Name: "revenue"}}}
		cached := NewCachedProvider(inner, CacheConfig{})
		picker, ok := CatalogPickerFrom(cached)
		if !ok {
			t.Fatal("expected picker capability through cache decorator")
		}
		terms, _ := picker.SearchGlossaryTerms(context.Background(), "rev", 10)
		if len(terms) != 1 || terms[0].Name != "revenue" {
			t.Fatalf("unexpected terms %v", terms)
		}
	})

	t.Run("provider without picker", func(t *testing.T) {
		if _, ok := CatalogPickerFrom(NewNoopProvider()); ok {
			t.Fatal("noop provider must not report picker capability")
		}
	})
}
