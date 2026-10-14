package sources

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/gateway/enrichment"
)

// Catalog source operation names.
const (
	opGetDataset    = "get_dataset"
	opGetDefinition = "get_definition"
)

// CatalogReader is what the catalog source reads from the platform catalog.
type CatalogReader interface {
	GetDataset(ctx context.Context, ref catalog.Ref) (*catalog.Dataset, error)
	GetDefinition(ctx context.Context, ref catalog.Ref) (*catalog.Definition, error)
}

// CatalogSource exposes read-only platform catalog lookups for enrichment
// rules: a dataset with its columns and curation, and a glossary term,
// glossary node, tag, domain or data product.
//
// A rule is operator policy and runs as the platform, the way the trino
// source runs the rule's SQL on the connection the rule names: it reads what
// the rule names, not what the calling persona reaches. An operator who binds
// a reference from a call's arguments decides that every caller of that tool
// may read the catalog entry it resolves to.
type CatalogSource struct {
	store CatalogReader
}

// NewCatalogSource builds a source over the platform catalog.
func NewCatalogSource(store CatalogReader) *CatalogSource {
	return &CatalogSource{store: store}
}

// Name returns the canonical source name "catalog".
func (*CatalogSource) Name() string { return enrichment.SourceCatalog }

// Operations returns the read-only operation allowlist.
func (*CatalogSource) Operations() []string {
	return []string{opGetDataset, opGetDefinition}
}

// Execute dispatches the requested operation. Both read one parameter:
//
//	get_dataset     { reference string }  mcp:dataset:(kind,connection,path)
//	get_definition  { reference string }  mcp:glossary_term:<id or slug>, ...
func (s *CatalogSource) Execute(ctx context.Context, op string, params map[string]any) (any, error) {
	if op != opGetDataset && op != opGetDefinition {
		return nil, fmt.Errorf("catalog: operation %q not supported", op)
	}
	reference, err := requireString(params, "reference")
	if err != nil {
		return nil, fmt.Errorf("catalog %s: %w", op, err)
	}
	ref, err := catalog.ParseRef(reference)
	if err != nil {
		return nil, fmt.Errorf("catalog %s: %w", op, err)
	}
	var out any
	if op == opGetDataset {
		out, err = s.store.GetDataset(ctx, ref)
	} else {
		out, err = s.store.GetDefinition(ctx, ref)
	}
	if err != nil {
		return nil, fmt.Errorf("catalog %s: %w", op, err)
	}
	return out, nil
}
