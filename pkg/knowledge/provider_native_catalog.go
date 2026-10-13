package knowledge

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// CatalogReader is what the native catalog provider reads from the platform's
// catalog store (#1917).
type CatalogReader interface {
	GetDataset(ctx context.Context, ref catalog.Ref) (*catalog.Dataset, error)
	GetDefinition(ctx context.Context, ref catalog.Ref) (*catalog.Definition, error)
}

// LineageReader walks the platform catalog's lineage edges. The catalog store
// satisfies it; a reader without it fetches datasets without their lineage.
type LineageReader interface {
	Lineage(ctx context.Context, ref string, direction catalog.Direction, maxDepth int) ([]catalog.Hop, error)
}

// lineageDepth is how far a fetched dataset's lineage is walked each way.
const lineageDepth = 3

// datasetDocument is what fetching a dataset returns: the dataset, and the
// lineage edges a walk reaches upstream and downstream of it.
type datasetDocument struct {
	*catalog.Dataset
	Upstream   []catalog.Hop `json:"upstream"`
	Downstream []catalog.Hop `json:"downstream"`
}

// BoundName returns the name a persona's connection rules match for the
// connection a catalog reference names by kind and instance. The platform
// supplies it from connid.Resolver, the one place those two names are crossed.
type BoundName func(connectionKind, instance string) string

// NativeCatalogProvider dereferences the platform catalog's own references:
// mcp:dataset:(...), mcp:column:(...), and the glossary terms, glossary nodes,
// tags, domains and data products curation points at.
//
// A dataset or column belongs to the connection that sees it, so it is
// dereferenced only for a caller whose persona may reach that connection, the
// same boundary search and every tool call apply. A definition is
// organization-wide and is readable by anyone who can fetch.
type NativeCatalogProvider struct {
	store CatalogReader
	bound BoundName
}

// NewNativeCatalogProvider builds the provider over a catalog store. A nil
// bound treats the instance name as the name persona rules match.
func NewNativeCatalogProvider(store CatalogReader, bound BoundName) *NativeCatalogProvider {
	return &NativeCatalogProvider{store: store, bound: bound}
}

// Name returns the provenance label, the one the catalog's datasets carry.
func (*NativeCatalogProvider) Name() string { return SourceCatalog }

// Scope marks the catalog shared: it is the same for every caller, narrowed
// only by the connection boundary.
func (*NativeCatalogProvider) Scope() Scope { return ScopeShared }

// Search contributes nothing: ranked discovery of catalog datasets is served by
// the catalog-datasets index, which this store does not feed.
func (*NativeCatalogProvider) Search(context.Context, Query) ([]Hit, error) {
	return nil, nil
}

// columnDocument is what fetching a column returns: the column with its
// curation, and the dataset it belongs to.
type columnDocument struct {
	Dataset string         `json:"dataset"`
	Column  catalog.Column `json:"column"`
}

// Fetch dereferences a catalog reference. Any other reference is declined.
func (p *NativeCatalogProvider) Fetch(ctx context.Context, ref string, caller Caller) (*Document, bool, error) {
	parsed, err := catalog.ParseRef(ref)
	if err != nil {
		return nil, false, nil //nolint:nilerr // not a catalog reference: decline so the Router tries the next provider
	}
	switch parsed.Kind {
	case catalog.KindDataset, catalog.KindColumn:
		if !caller.allowsConnection(p.boundName(parsed)) {
			return nil, true, ErrNotFound
		}
		doc, err := p.fetchDataset(ctx, parsed)
		return doc, true, err
	default:
		doc, err := p.fetchDefinition(ctx, parsed)
		return doc, true, err
	}
}

// boundName is the persona-rule name of the connection a reference names.
func (p *NativeCatalogProvider) boundName(r catalog.Ref) string {
	if p.bound == nil {
		return r.Connection
	}
	return p.bound(r.ConnectionKind, r.Connection)
}

// fetchDataset reads a dataset, or the one column a column reference names.
func (p *NativeCatalogProvider) fetchDataset(ctx context.Context, r catalog.Ref) (*Document, error) {
	d, err := p.store.GetDataset(ctx, r.Dataset())
	if err != nil {
		return nil, catalogError(err)
	}
	if r.Kind == catalog.KindDataset {
		content, err := p.withLineage(ctx, d)
		if err != nil {
			return nil, err
		}
		return &Document{
			Reference:  d.Reference,
			Source:     SourceCatalog,
			Title:      d.Name,
			Body:       d.Aspects.Description,
			Content:    content,
			EntityURNs: []string{d.Reference},
		}, nil
	}
	for _, c := range d.Columns {
		if c.Name == r.Column {
			return &Document{
				Reference:  r.String(),
				Source:     SourceCatalog,
				Title:      d.Name + "." + c.Name,
				Body:       c.Aspects.Description,
				Content:    columnDocument{Dataset: d.Reference, Column: c},
				EntityURNs: []string{d.Reference},
			}, nil
		}
	}
	return nil, ErrNotFound
}

// withLineage adds the dataset's lineage when the store can walk it.
func (p *NativeCatalogProvider) withLineage(ctx context.Context, d *catalog.Dataset) (datasetDocument, error) {
	doc := datasetDocument{Dataset: d, Upstream: []catalog.Hop{}, Downstream: []catalog.Hop{}}
	lr, ok := p.store.(LineageReader)
	if !ok {
		return doc, nil
	}
	up, err := lr.Lineage(ctx, d.Reference, catalog.Upstream, lineageDepth)
	if err != nil {
		return doc, fmt.Errorf("reading lineage: %w", err)
	}
	down, err := lr.Lineage(ctx, d.Reference, catalog.Downstream, lineageDepth)
	if err != nil {
		return doc, fmt.Errorf("reading lineage: %w", err)
	}
	doc.Upstream, doc.Downstream = up, down
	return doc, nil
}

// fetchDefinition reads a glossary term, glossary node, tag, domain or data
// product.
func (p *NativeCatalogProvider) fetchDefinition(ctx context.Context, r catalog.Ref) (*Document, error) {
	d, err := p.store.GetDefinition(ctx, r)
	if err != nil {
		return nil, catalogError(err)
	}
	return &Document{
		Reference: d.Reference,
		Source:    SourceGovernance,
		Title:     d.Name,
		Body:      d.Aspects.Description,
		Content:   d,
	}, nil
}

// catalogError maps the store's not-found onto the router's, which fetch
// renders as a structured not-found.
func catalogError(err error) error {
	if errors.Is(err, catalog.ErrNotFound) {
		return ErrNotFound
	}
	return fmt.Errorf("reading catalog: %w", err)
}
