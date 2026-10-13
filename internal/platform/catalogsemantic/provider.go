// Package catalogsemantic is the platform's semantic.Provider over its own
// catalog (#1919): table and column context, glossary terms, dataset search,
// curated query counts and lineage, all read from the catalog store. It is the
// only provider on a deployment with a database; the consumers of
// semantic.Provider are unchanged.
//
// A dataset belongs to the connection that sees it, and a
// semantic.TableIdentifier names no connection. The provider takes the
// connection from the call being enriched when there is one, and otherwise
// reads the first connection that catalogs a table at that path. It accepts a
// dataset named by an mcp:dataset reference and by the urn:li:dataset form the
// platform built before the catalog moved, which it resolves by the same path.
package catalogsemantic

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/urnbuild"
)

// Name is the provider's name.
const Name = "catalog"

// trinoKind is the connection kind a table identifier names.
const trinoKind = "trino"

// Reader is what the provider reads from the catalog store.
type Reader interface {
	GetDataset(ctx context.Context, ref catalog.Ref) (*catalog.Dataset, error)
	GetDefinition(ctx context.Context, ref catalog.Ref) (*catalog.Definition, error)
	DatasetsByPath(ctx context.Context, connectionKind, path string) ([]string, error)
	SearchDatasets(ctx context.Context, q catalog.DatasetQuery) ([]catalog.DatasetSummary, error)
	Lineage(ctx context.Context, ref string, direction catalog.Direction, maxDepth int) ([]catalog.Hop, error)
}

// InstanceOf returns the instance name of the Trino connection a call bound
// by name, and false when no live connection has that name.
type InstanceOf func(bound string) (string, bool)

// Provider is the native semantic provider.
type Provider struct {
	store    Reader
	instance InstanceOf
}

// New builds the provider. A nil instance lookup uses the bound name as the
// instance.
func New(store Reader, instance InstanceOf) *Provider {
	if instance == nil {
		instance = func(bound string) (string, bool) { return bound, bound != "" }
	}
	return &Provider{store: store, instance: instance}
}

var _ semantic.Provider = (*Provider)(nil)

// Name returns "catalog".
func (*Provider) Name() string { return Name }

// Close releases nothing; the store's pool belongs to the platform.
func (*Provider) Close() error { return nil }

// notFound wraps the semantic sentinel so a caller can tell a table the
// catalog has never seen from a failure.
func notFound(what string) error {
	return fmt.Errorf("the catalog holds no %s: %w", what, semantic.ErrNotFound)
}

// dataset reads the dataset a table identifier names.
func (p *Provider) dataset(ctx context.Context, table semantic.TableIdentifier) (*catalog.Dataset, error) {
	if table.Catalog == "" || table.Schema == "" || table.Table == "" {
		return nil, notFound("table " + table.String())
	}
	if d, onConnection, err := p.onCallConnection(ctx, table); onConnection {
		return d, err
	}
	path := catalog.DatasetRef(trinoKind, "_", table.Catalog, table.Schema, table.Table).PathString()
	return p.byPath(ctx, path, table.String())
}

// onCallConnection reads the table on the Trino connection the call being
// enriched ran on. onConnection is false only when the call names no such
// connection; when it does, that connection's answer is the answer, found or
// not, so a table is never described with the curation of a connection the
// caller did not use and may not be allowed to reach.
func (p *Provider) onCallConnection(
	ctx context.Context, table semantic.TableIdentifier,
) (d *catalog.Dataset, onConnection bool, err error) {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil || pc.ToolkitKind != trinoKind || pc.Connection == "" {
		return nil, false, nil
	}
	inst, ok := p.instance(pc.Connection)
	if !ok {
		return nil, true, notFound("table " + table.String() + " on connection " + pc.Connection)
	}
	d, err = p.store.GetDataset(ctx, catalog.DatasetRef(trinoKind, inst, table.Catalog, table.Schema, table.Table))
	if err != nil {
		return nil, true, datasetError(err, table.String())
	}
	return d, true, nil
}

// byPath reads the first dataset that a Trino connection catalogs at path.
func (p *Provider) byPath(ctx context.Context, path, what string) (*catalog.Dataset, error) {
	refs, err := p.store.DatasetsByPath(ctx, trinoKind, path)
	if err != nil {
		return nil, fmt.Errorf("finding %s: %w", what, err)
	}
	if len(refs) == 0 {
		return nil, notFound("table " + what)
	}
	ref, err := catalog.ParseRef(refs[0])
	if err != nil {
		return nil, fmt.Errorf("stored reference %s: %w", refs[0], err)
	}
	d, err := p.store.GetDataset(ctx, ref)
	if err != nil {
		return nil, datasetError(err, what)
	}
	return d, nil
}

// datasetByURN reads the dataset an mcp:dataset reference or a urn:li:dataset
// URN names.
func (p *Provider) datasetByURN(ctx context.Context, urn string) (*catalog.Dataset, error) {
	if ref, err := catalog.ParseRef(urn); err == nil && ref.Kind == catalog.KindDataset {
		d, err := p.store.GetDataset(ctx, ref)
		if err != nil {
			return nil, datasetError(err, urn)
		}
		return d, nil
	}
	parsed, err := urnbuild.ParseDatasetURN(urn)
	if err != nil {
		return nil, notFound("dataset " + urn)
	}
	return p.byPath(ctx, parsed.Name, urn)
}

// datasetError maps the store's not-found onto the semantic sentinel.
func datasetError(err error, what string) error {
	if errors.Is(err, catalog.ErrNotFound) {
		return notFound("dataset " + what)
	}
	return fmt.Errorf("reading %s: %w", what, err)
}

// GetTableContext returns a table's curation.
func (p *Provider) GetTableContext(ctx context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error) {
	d, err := p.dataset(ctx, table)
	if err != nil {
		return nil, err
	}
	return p.tableContext(ctx, d), nil
}

// tableContext renders a dataset as the context enrichment carries.
func (p *Provider) tableContext(ctx context.Context, d *catalog.Dataset) *semantic.TableContext {
	a := d.Aspects
	tc := &semantic.TableContext{
		URN:                  d.Reference,
		Description:          a.Description,
		Owners:               owners(a.Owners),
		Tags:                 a.Tags,
		GlossaryTerms:        p.terms(ctx, a.GlossaryTerms),
		Domain:               p.domain(ctx, a.Domain),
		TagRefs:              tagRefs(a.Tags),
		CustomProperties:     a.CustomProperties,
		StructuredProperties: structured(a.StructuredProperties),
		Incidents:            incidents(a.Incidents),
	}
	updated := d.UpdatedAt
	tc.LastModified = &updated
	for _, in := range a.Incidents {
		if in.State == catalog.IncidentActive {
			tc.ActiveIncidents++
		}
	}
	return tc
}

// GetColumnContext returns one column's curation.
func (p *Provider) GetColumnContext(ctx context.Context, column semantic.ColumnIdentifier) (*semantic.ColumnContext, error) {
	cols, err := p.GetColumnsContext(ctx, column.TableIdentifier)
	if err != nil {
		return nil, err
	}
	c, ok := cols[column.Column]
	if !ok {
		return nil, notFound("column " + column.Column)
	}
	return c, nil
}

// GetColumnsContext returns every column's curation, by column name.
func (p *Provider) GetColumnsContext(ctx context.Context, table semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error) {
	d, err := p.dataset(ctx, table)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*semantic.ColumnContext, len(d.Columns))
	for _, c := range d.Columns {
		out[c.Name] = &semantic.ColumnContext{
			Name:          c.Name,
			Description:   c.Aspects.Description,
			Tags:          c.Aspects.Tags,
			GlossaryTerms: p.terms(ctx, c.Aspects.GlossaryTerms),
			IsPII:         hasTag(c.Aspects.Tags, "pii"),
			IsSensitive:   hasTag(c.Aspects.Tags, "sensitive") || hasTag(c.Aspects.Tags, "pii"),
		}
	}
	return out, nil
}

// GetGlossaryTerm reads a glossary term by its reference.
func (p *Provider) GetGlossaryTerm(ctx context.Context, urn string) (*semantic.GlossaryTerm, error) {
	ref, err := catalog.ParseRef(urn)
	if err != nil || ref.Kind != catalog.KindGlossaryTerm {
		return nil, notFound("glossary term " + urn)
	}
	def, err := p.store.GetDefinition(ctx, ref)
	if errors.Is(err, catalog.ErrNotFound) {
		return nil, notFound("glossary term " + urn)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", urn, err)
	}
	return &semantic.GlossaryTerm{
		URN: def.Reference, Name: def.Name, Description: def.Aspects.Description,
		ParentNode: def.Parent, Owners: owners(def.Aspects.Owners), CustomProperties: def.Aspects.CustomProperties,
	}, nil
}

// SearchTables finds datasets. Query "*" lists them.
func (p *Provider) SearchTables(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	found, err := p.store.SearchDatasets(ctx, catalog.DatasetQuery{
		Text: filter.Query, ConnectionKind: filter.Platform, Tags: filter.Tags,
		Limit: filter.Limit, Offset: filter.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("searching the catalog: %w", err)
	}
	domains := make(map[string]string)
	out := make([]semantic.TableSearchResult, 0, len(found))
	for _, d := range found {
		out = append(out, semantic.TableSearchResult{
			URN: d.Reference, Name: d.Path, Platform: d.ConnectionKind,
			Description: d.Description, Tags: d.Tags, Domain: p.domainName(ctx, d.Domain, domains),
		})
	}
	return out, nil
}

// GetCuratedQueryCount counts a dataset's curated queries. A dataset the
// catalog does not hold has none.
func (p *Provider) GetCuratedQueryCount(ctx context.Context, urn string) (int, error) {
	d, err := p.datasetByURN(ctx, urn)
	if errors.Is(err, semantic.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return len(d.Aspects.CuratedQueries), nil
}

// GetLineage walks a table's lineage edges.
func (p *Provider) GetLineage(
	ctx context.Context, table semantic.TableIdentifier, direction semantic.LineageDirection, maxDepth int,
) (*semantic.LineageInfo, error) {
	info := &semantic.LineageInfo{Direction: direction, Entities: []semantic.LineageEntity{}, MaxDepth: maxDepth}
	d, err := p.dataset(ctx, table)
	if errors.Is(err, semantic.ErrNotFound) {
		return info, nil
	}
	if err != nil {
		return nil, err
	}
	dir := catalog.Upstream
	if direction == semantic.LineageDownstream {
		dir = catalog.Downstream
	}
	hops, err := p.store.Lineage(ctx, d.Reference, dir, maxDepth)
	if err != nil {
		return nil, fmt.Errorf("reading lineage of %s: %w", d.Reference, err)
	}
	info.Entities = lineageEntities(hops, dir)
	return info, nil
}

// lineageEntities renders each dataset a walk reached once, at its shortest
// distance, with the edges that connect it toward the start.
func lineageEntities(hops []catalog.Hop, dir catalog.Direction) []semantic.LineageEntity {
	byURN := make(map[string]*semantic.LineageEntity)
	order := make([]string, 0)
	for _, h := range hops {
		far, near := h.Source, h.Target
		if dir == catalog.Downstream {
			far, near = h.Target, h.Source
		}
		e, ok := byURN[far]
		if !ok {
			e = &semantic.LineageEntity{URN: far, Type: entityType(far), Name: entityName(far), Depth: h.Depth}
			byURN[far] = e
			order = append(order, far)
		}
		e.Depth = min(e.Depth, h.Depth)
		edge := semantic.LineageEdge{URN: near, Type: string(h.WriterKind)}
		if dir == catalog.Upstream {
			e.Children = append(e.Children, edge)
		} else {
			e.Parents = append(e.Parents, edge)
		}
	}
	out := make([]semantic.LineageEntity, 0, len(order))
	for _, urn := range order {
		out = append(out, *byURN[urn])
	}
	return out
}

// entityType names what a lineage reference is: a dataset, or the kind of
// platform record a file edge names (resource, asset).
func entityType(ref string) string {
	rest, ok := strings.CutPrefix(ref, "mcp:")
	if !ok {
		return "dataset"
	}
	kind, _, _ := strings.Cut(rest, ":")
	return kind
}

// entityName is a short label for a lineage reference: a dataset's path, or
// the reference itself.
func entityName(ref string) string {
	if r, err := catalog.ParseRef(ref); err == nil && r.Kind == catalog.KindDataset {
		return r.PathString()
	}
	return ref
}

// terms resolves glossary term references to their names. A reference the
// glossary does not hold is kept, named by its key.
func (p *Provider) terms(ctx context.Context, refs []string) []semantic.GlossaryTerm {
	if len(refs) == 0 {
		return nil
	}
	out := make([]semantic.GlossaryTerm, 0, len(refs))
	for _, r := range refs {
		term := semantic.GlossaryTerm{URN: r, Name: r}
		if ref, err := catalog.ParseRef(r); err == nil {
			term.Name = ref.Key
			if def, err := p.store.GetDefinition(ctx, ref); err == nil {
				term.URN, term.Name, term.Description = def.Reference, def.Name, def.Aspects.Description
			}
		}
		out = append(out, term)
	}
	return out
}

// domain resolves a domain reference.
func (p *Provider) domain(ctx context.Context, ref string) *semantic.Domain {
	if ref == "" {
		return nil
	}
	d := &semantic.Domain{URN: ref, Name: ref}
	if r, err := catalog.ParseRef(ref); err == nil {
		d.Name = r.Key
		if def, err := p.store.GetDefinition(ctx, r); err == nil {
			d.URN, d.Name, d.Description = def.Reference, def.Name, def.Aspects.Description
		}
	}
	return d
}

// domainName resolves a domain reference to its name, once per search.
func (p *Provider) domainName(ctx context.Context, ref string, seen map[string]string) string {
	if ref == "" {
		return ""
	}
	if name, ok := seen[ref]; ok {
		return name
	}
	name := p.domain(ctx, ref).Name
	seen[ref] = name
	return name
}

// owners renders the catalog's owners. An owner with an address is a person;
// any other is a group.
func owners(in []catalog.Owner) []semantic.Owner {
	if len(in) == 0 {
		return nil
	}
	out := make([]semantic.Owner, 0, len(in))
	for _, o := range in {
		so := semantic.Owner{URN: o.Owner, Name: o.Owner, Type: semantic.OwnerTypeGroup}
		if strings.Contains(o.Owner, "@") {
			so.Type, so.Email = semantic.OwnerTypeUser, o.Owner
		}
		out = append(out, so)
	}
	return out
}

// tagRefs renders tags as the references a caller can fetch.
func tagRefs(tags []string) []semantic.EntityRef {
	if len(tags) == 0 {
		return nil
	}
	out := make([]semantic.EntityRef, 0, len(tags))
	for _, t := range tags {
		out = append(out, semantic.EntityRef{URN: catalog.KeyRef(catalog.KindTag, t).String(), Name: t})
	}
	return out
}

// structured renders structured properties in name order.
func structured(props map[string][]any) []semantic.StructuredProperty {
	if len(props) == 0 {
		return nil
	}
	names := make([]string, 0, len(props))
	for n := range props {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]semantic.StructuredProperty, 0, len(names))
	for _, n := range names {
		out = append(out, semantic.StructuredProperty{QualifiedName: n, DisplayName: n, Values: props[n]})
	}
	return out
}

// incidents renders incidents with the upper-case state enrichment reports.
func incidents(in []catalog.Incident) []semantic.Incident {
	if len(in) == 0 {
		return nil
	}
	out := make([]semantic.Incident, 0, len(in))
	for _, i := range in {
		out = append(out, semantic.Incident{
			URN: i.ID, Type: i.Type, Title: i.Title, Description: i.Description, State: strings.ToUpper(i.State),
		})
	}
	return out
}

// hasTag reports a tag, ignoring case.
func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}
