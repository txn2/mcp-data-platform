package catalogimport

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// Source is what the import reads from DataHub.
type Source interface {
	DatasetHeads(ctx context.Context) ([]DatasetHead, error)
	Dataset(ctx context.Context, urn string) (*DatasetRecord, error)
	Definitions(ctx context.Context) ([]Definition, error)
}

// Catalog is what the import needs from the catalog store.
type Catalog interface {
	GetDataset(ctx context.Context, ref catalog.Ref) (*catalog.Dataset, error)
	GetDefinition(ctx context.Context, ref catalog.Ref) (*catalog.Definition, error)
	PutDefinition(ctx context.Context, in catalog.DefinitionInput) (*catalog.Definition, error)
	History(ctx context.Context, ref catalog.Ref, name catalog.AspectName) ([]catalog.Change, error)
	WriteAspects(ctx context.Context, edits []catalog.AspectEdit, by string) ([]catalog.Change, error)
	AddEdges(ctx context.Context, edges []catalog.Edge) error
	DatasetsByPath(ctx context.Context, connectionKind, path string) ([]string, error)
}

// Mapping is the import's record of what each DataHub URN became.
type Mapping interface {
	Map(ctx context.Context) (map[string]string, error)
	Record(ctx context.Context, urn, reference string) error
}

// Rewrite is what rewrites the stored references once the import has run.
type Rewrite interface {
	Rewrite(ctx context.Context, resolve Resolver, report *Report) error
}
