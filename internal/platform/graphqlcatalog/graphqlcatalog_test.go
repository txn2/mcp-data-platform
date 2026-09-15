package graphqlcatalog

import (
	"context"
	"errors"
	"strings"
	"testing"

	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// sdl is a schema small enough to read in a failure message.
const sdl = `type Query { product(id: ID!): Product }
type Product { id: ID! name: String }`

// specs is a catalog store over a map.
type specs struct {
	entries    map[string][]apicatalog.SpecEntry
	embeddings map[string][]apicatalog.OperationEmbedding
	listErr    error
	embedErr   error
}

// ListSpecs returns one catalog's spec entries.
func (s *specs) ListSpecs(_ context.Context, catalogID string) ([]apicatalog.SpecEntry, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.entries[catalogID], nil
}

// ListOperationEmbeddings returns one spec's embedding rows.
func (s *specs) ListOperationEmbeddings(_ context.Context, _, specName string) ([]apicatalog.OperationEmbedding, error) {
	if s.embedErr != nil {
		return nil, s.embedErr
	}
	return s.embeddings[specName], nil
}

// oneSchema is a catalog holding one GraphQL spec beside an OpenAPI one,
// which is the shape a mixed catalog has.
func oneSchema() *specs {
	return &specs{entries: map[string][]apicatalog.SpecEntry{
		"erp": {
			{SpecName: "rest", Content: "openapi: 3.0.0", SpecFormat: apicatalog.FormatOpenAPI},
			{SpecName: "schema", Content: sdl, SpecFormat: apicatalog.FormatGraphQL},
		},
	}}
}

func TestACatalogsGraphQLSchemaIsTheEntryWhoseFormatSaysSo(t *testing.T) {
	held, err := Store(oneSchema()).CatalogSchema(context.Background(), "erp")
	if err != nil {
		t.Fatalf("reading the catalog's schema: %v", err)
	}
	if held.SpecName != "schema" {
		t.Errorf("the schema was read out of spec %q", held.SpecName)
	}
	if held.SDL != sdl {
		t.Errorf("the schema read back as %q", held.SDL)
	}
}

func TestACatalogWithNoGraphQLSchemaSaysSo(t *testing.T) {
	store := Store(&specs{entries: map[string][]apicatalog.SpecEntry{
		"erp": {{SpecName: "rest", SpecFormat: apicatalog.FormatOpenAPI}},
	}})

	_, err := store.CatalogSchema(context.Background(), "erp")
	if !errors.Is(err, graphqlkit.ErrCatalogSchemaNotFound) {
		t.Errorf("a catalog with no schema gave %v", err)
	}
}

func TestACatalogNobodyCreatedHasNoSchema(t *testing.T) {
	store := Store(&specs{listErr: apicatalog.ErrNotFound})

	_, err := store.CatalogSchema(context.Background(), "absent")
	if !errors.Is(err, graphqlkit.ErrCatalogSchemaNotFound) {
		t.Errorf("an unknown catalog gave %v", err)
	}
}

func TestACatalogStoreThatCannotAnswerIsNotAMissingSchema(t *testing.T) {
	store := Store(&specs{listErr: errors.New("the database is unreachable")})

	_, err := store.CatalogSchema(context.Background(), "erp")
	if err == nil || errors.Is(err, graphqlkit.ErrCatalogSchemaNotFound) {
		t.Errorf("a store failure gave %v; want a failure that is not a missing schema", err)
	}
}

func TestACatalogHoldingTwoSchemasIsRefusedByName(t *testing.T) {
	store := Store(&specs{entries: map[string][]apicatalog.SpecEntry{
		"erp": {
			{SpecName: "prod", Content: sdl, SpecFormat: apicatalog.FormatGraphQL},
			{SpecName: "sandbox", Content: sdl, SpecFormat: apicatalog.FormatGraphQL},
		},
	}})

	_, err := store.CatalogSchema(context.Background(), "erp")
	if err == nil {
		t.Fatal("a catalog holding two schemas resolved to one of them")
	}
	// Which one a connection would answer with is not a thing to decide
	// by map order, so the refusal has to say what is wrong.
	if !strings.Contains(err.Error(), "2 GraphQL schemas") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
}

func TestACatalogsVectorsAreItsSchemaSpecsVectors(t *testing.T) {
	store := oneSchema()
	store.embeddings = map[string][]apicatalog.OperationEmbedding{
		"schema": {{OperationID: "query:product", Embedding: []float32{0.5, 0.25}}},
	}

	vectors, err := Store(store).CatalogVectors(context.Background(), "erp")
	if err != nil {
		t.Fatalf("reading the catalog's vectors: %v", err)
	}
	if len(vectors) != 1 || len(vectors["query:product"]) != 2 {
		t.Errorf("the vectors read back as %v", vectors)
	}
}

func TestVectorsOfACatalogWithNoSchemaAreNotRead(t *testing.T) {
	store := Store(&specs{entries: map[string][]apicatalog.SpecEntry{"erp": nil}})

	if _, err := store.CatalogVectors(context.Background(), "erp"); !errors.Is(err, graphqlkit.ErrCatalogSchemaNotFound) {
		t.Errorf("vectors of a catalog with no schema gave %v", err)
	}
}

func TestAnEmbeddingReadThatFailsIsReported(t *testing.T) {
	store := oneSchema()
	store.embedErr = errors.New("the database is unreachable")

	if _, err := Store(store).CatalogVectors(context.Background(), "erp"); err == nil {
		t.Error("an embedding read that failed reported no error")
	}
}

func TestADeploymentWithNoCatalogStoreAdaptsToNothing(t *testing.T) {
	if Store(nil) != nil {
		t.Error("a nil catalog store adapted to a reader")
	}
}
