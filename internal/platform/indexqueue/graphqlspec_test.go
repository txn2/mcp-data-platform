package indexqueue

import (
	"context"
	"strings"
	"testing"

	apigatewaycatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalogindex"
)

// graphqlSchemaSpec is a schema with one namespaced query and one mutation,
// so the item ids are the dotted ids a connection keys its vectors on.
const graphqlSchemaSpec = `
type Query {
  catalog: CatalogQueries
}
type Mutation {
  createProduct(name: String!): Product
}
type CatalogQueries {
  "Find one product by id."
  product(id: ID!): Product
}
type Product {
  id: ID!
  name: String
}
`

// seedGraphQLCatalogStore puts one GraphQL spec in a catalog.
func seedGraphQLCatalogStore(t *testing.T, store apigatewaycatalog.Store, catalogID, specName, content string) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateCatalog(ctx, apigatewaycatalog.Catalog{
		ID: catalogID, Name: catalogID, Version: "v1",
	}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	if err := store.UpsertSpec(ctx, catalogID, apigatewaycatalog.SpecEntry{
		SpecName: specName, Content: content, SourceKind: "inline",
		SpecFormat: apigatewaycatalog.FormatGraphQL,
	}); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
}

// A GraphQL spec entry is embedded through the catalog source, keyed on the
// catalog's spec, which is what lets two connections referencing one catalog
// share a single embedding pass (#1745). The item ids must be the operation
// ids a connection looks its vectors up by, or the rows this writes are rows
// nothing reads.
func TestCatalogSourceEmbedsAGraphQLSchemasOperations(t *testing.T) {
	t.Parallel()
	store := apigatewaycatalog.NewMemoryStore()
	seedGraphQLCatalogStore(t, store, "erp", "schema", graphqlSchemaSpec)

	s := &catalogSource{store: store}
	items, err := s.LoadItems(context.Background(), catalogindex.EncodeSourceID("erp", "schema"))
	if err != nil {
		t.Fatalf("LoadItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d; want the query and the mutation: %+v", len(items), items)
	}
	ids := map[string]bool{}
	for _, it := range items {
		ids[it.ItemID] = true
		if it.Text == "" {
			t.Errorf("item %q has empty text", it.ItemID)
		}
	}
	if !ids["query:catalog.product"] || !ids["mutation:createProduct"] {
		t.Errorf("item ids = %v; want the dotted ids a connection ranks by", ids)
	}
	// The descent sorts by id, so two passes over one schema agree.
	if items[0].ItemID > items[1].ItemID {
		t.Errorf("items are not in a stable order: %v", ids)
	}
}

func TestAGraphQLSpecThatDoesNotLoadIsReportedRatherThanEmbeddedEmpty(t *testing.T) {
	t.Parallel()
	store := apigatewaycatalog.NewMemoryStore()
	seedGraphQLCatalogStore(t, store, "erp", "schema", "this is not a schema")

	s := &catalogSource{store: store}
	_, err := s.LoadItems(context.Background(), catalogindex.EncodeSourceID("erp", "schema"))
	if err == nil {
		t.Fatal("a spec that does not load embedded without error")
	}
	if !strings.Contains(err.Error(), "GraphQL") {
		t.Errorf("the failure does not say what could not be read: %v", err)
	}
}
