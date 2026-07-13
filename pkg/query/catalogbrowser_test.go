package query

import (
	"context"
	"testing"
)

// browserProvider embeds the noop provider and adds the CatalogBrowser
// capability.
type browserProvider struct {
	*NoopProvider
}

func (browserProvider) ListCatalogs(context.Context) ([]string, error) { return []string{"hive"}, nil }
func (browserProvider) ListSchemas(context.Context, string) ([]string, error) {
	return []string{"sales"}, nil
}

func (browserProvider) ListTables(context.Context, string, string) ([]string, error) {
	return []string{"orders"}, nil
}

// unwrapProvider wraps another provider and exposes Unwrap, so the helper must
// see through it to the inner browser.
type unwrapProvider struct {
	Provider
}

func (u unwrapProvider) Unwrap() Provider { return u.Provider }

func TestCatalogBrowserFrom(t *testing.T) {
	t.Run("bare browser", func(t *testing.T) {
		b, ok := CatalogBrowserFrom(browserProvider{NewNoopProvider()})
		if !ok {
			t.Fatal("expected browser capability")
		}
		cats, _ := b.ListCatalogs(context.Background())
		if len(cats) != 1 || cats[0] != "hive" {
			t.Fatalf("unexpected catalogs %v", cats)
		}
	})

	t.Run("through decorator", func(t *testing.T) {
		wrapped := unwrapProvider{Provider: browserProvider{NewNoopProvider()}}
		if _, ok := CatalogBrowserFrom(wrapped); !ok {
			t.Fatal("expected browser capability through decorator")
		}
	})

	t.Run("no browser", func(t *testing.T) {
		if _, ok := CatalogBrowserFrom(NewNoopProvider()); ok {
			t.Fatal("noop provider must not report browser capability")
		}
	})
}
