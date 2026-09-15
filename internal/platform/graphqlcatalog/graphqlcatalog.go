// Package graphqlcatalog reads an API catalog as the graphql kind reads
// it: the one spec entry holding a GraphQL schema, and the operation
// embeddings written for it (#1745).
//
// It is the seam that lets a graphql connection reference the same
// catalog an api connection does without the graphql toolkit importing
// the api gateway's catalog package. The toolkit declares what it needs —
// a schema by catalog id, and that schema's vectors — and this resolves
// it against the catalog store the platform already has.
package graphqlcatalog

import (
	"context"
	"errors"
	"fmt"

	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// SpecStore is the catalog store as this package reads it.
type SpecStore interface {
	ListSpecs(ctx context.Context, catalogID string) ([]apicatalog.SpecEntry, error)
	ListOperationEmbeddings(ctx context.Context, catalogID, specName string) ([]apicatalog.OperationEmbedding, error)
}

// Store adapts a catalog store to the one a graphql connection reads its
// schema from. A nil store adapts to nil, leaving connections reading
// their own endpoints.
func Store(specs SpecStore) graphqlkit.CatalogStore {
	if specs == nil {
		return nil
	}
	return store{specs: specs}
}

// store is Store's adapter.
type store struct{ specs SpecStore }

// CatalogSchema returns the GraphQL schema a catalog holds.
//
// A catalog carries one, which is what lets several connections share a
// stored copy and an embedding pass. A catalog holding more than one is
// refused by name rather than resolved by guess: which schema a
// connection answers with is not something to decide by map order, and
// the operator who added the second entry is the one who can say.
func (s store) CatalogSchema(ctx context.Context, catalogID string) (graphqlkit.CatalogSchema, error) {
	entry, err := s.schemaSpec(ctx, catalogID)
	if err != nil {
		return graphqlkit.CatalogSchema{}, err
	}
	return graphqlkit.CatalogSchema{SpecName: entry.SpecName, SDL: entry.Effective()}, nil
}

// CatalogVectors returns the operation embeddings written for that
// schema, keyed by operation id.
func (s store) CatalogVectors(ctx context.Context, catalogID string) (map[string][]float32, error) {
	entry, err := s.schemaSpec(ctx, catalogID)
	if err != nil {
		return nil, err
	}
	rows, err := s.specs.ListOperationEmbeddings(ctx, catalogID, entry.SpecName)
	if err != nil {
		return nil, fmt.Errorf("graphqlcatalog: reading the embeddings of %s/%s: %w", catalogID, entry.SpecName, err)
	}
	vectors := make(map[string][]float32, len(rows))
	for _, row := range rows {
		vectors[row.OperationID] = row.Embedding
	}
	return vectors, nil
}

// schemaSpec finds the one spec entry in a catalog whose format is
// graphql.
func (s store) schemaSpec(ctx context.Context, catalogID string) (apicatalog.SpecEntry, error) {
	entries, err := s.specs.ListSpecs(ctx, catalogID)
	if err != nil {
		if errors.Is(err, apicatalog.ErrNotFound) {
			return apicatalog.SpecEntry{}, fmt.Errorf("graphqlcatalog: %s: %w", catalogID, graphqlkit.ErrCatalogSchemaNotFound)
		}
		return apicatalog.SpecEntry{}, fmt.Errorf("graphqlcatalog: reading catalog %s: %w", catalogID, err)
	}
	var found []apicatalog.SpecEntry
	for _, e := range entries {
		if e.Format() == apicatalog.FormatGraphQL {
			found = append(found, e)
		}
	}
	switch len(found) {
	case 0:
		return apicatalog.SpecEntry{}, fmt.Errorf("graphqlcatalog: %s: %w", catalogID, graphqlkit.ErrCatalogSchemaNotFound)
	case 1:
		return found[0], nil
	default:
		return apicatalog.SpecEntry{}, fmt.Errorf(
			"graphqlcatalog: catalog %s holds %d GraphQL schemas (%s and others); a connection takes one, so leave one in the catalog",
			catalogID, len(found), found[0].SpecName)
	}
}
