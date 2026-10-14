package sources

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/catalogfake"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/gateway/enrichment"
)

func TestCatalogSourceReadsDatasetsAndDefinitions(t *testing.T) {
	store := catalogfake.New()
	ds := store.AddDataset("acme", "hive.sales.orders", "amount")
	store.SetDescription(ds.String(), "Orders placed online.")
	term, err := store.PutDefinition(context.Background(), catalog.DefinitionInput{Kind: catalog.KindGlossaryTerm, Name: "Revenue"})
	if err != nil {
		t.Fatal(err)
	}
	src := NewCatalogSource(store)
	if src.Name() != enrichment.SourceCatalog || len(src.Operations()) != 2 {
		t.Fatalf("name %q operations %v", src.Name(), src.Operations())
	}

	out, err := src.Execute(context.Background(), opGetDataset, map[string]any{"reference": ds.String()})
	if err != nil {
		t.Fatal(err)
	}
	if d, ok := out.(*catalog.Dataset); !ok || d.Aspects.Description != "Orders placed online." {
		t.Fatalf("get_dataset returned %#v", out)
	}
	out, err = src.Execute(context.Background(), opGetDefinition, map[string]any{"reference": "mcp:glossary_term:" + term.Slug})
	if err != nil {
		t.Fatal(err)
	}
	if d, ok := out.(*catalog.Definition); !ok || d.Name != "Revenue" {
		t.Fatalf("get_definition returned %#v", out)
	}
}

func TestCatalogSourceRefusals(t *testing.T) {
	src := NewCatalogSource(catalogfake.New())
	for _, tc := range []struct {
		name, op string
		params   map[string]any
		want     string
	}{
		{"unknown operation", "search", map[string]any{"reference": "mcp:tag:x"}, "not supported"},
		{"no reference", opGetDataset, map[string]any{}, "reference"},
		{"a urn:li URN", opGetDataset, map[string]any{"reference": "urn:li:dataset:x"}, "invalid catalog reference"},
		{"not in the catalog", opGetDefinition, map[string]any{"reference": "mcp:domain:none"}, "not found"},
	} {
		_, err := src.Execute(context.Background(), tc.op, tc.params)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.want)
		}
	}
	_, err := src.Execute(context.Background(), opGetDataset, map[string]any{"reference": "mcp:dataset:(trino,acme,a.b.c)"})
	if !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("a missing dataset: %v, want ErrNotFound wrapped", err)
	}
}
