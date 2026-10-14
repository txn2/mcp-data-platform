package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// CatalogReader is what the native catalog provider reads from the platform's
// catalog store (#1917).
type CatalogReader interface {
	GetDataset(ctx context.Context, ref catalog.Ref) (*catalog.Dataset, error)
}

// CatalogBrowser lists one level of a connection's datasets. The catalog store
// satisfies it; a reader without it answers a reference to a path that is not
// a dataset with not-found.
type CatalogBrowser interface {
	Browse(ctx context.Context, q catalog.BrowseQuery) ([]catalog.BrowseEntry, error)
}

// DocumentReader reads a connection's schema document. The catalog store
// satisfies it; a reader without it declines schema document references.
type DocumentReader interface {
	GetDocument(ctx context.Context, ref catalog.Ref) (*catalog.SchemaDocument, error)
}

// LineageReader walks the platform catalog's lineage edges. The catalog store
// satisfies it; a reader without it fetches datasets without their lineage.
type LineageReader interface {
	Lineage(ctx context.Context, ref string, direction catalog.Direction, maxDepth int) ([]catalog.Hop, error)
}

// AvailabilityResolver answers whether and where one catalog dataset is
// queryable. It is the query provider's own read, declared here so the
// provider depends on the capability alone. It is deliberately not the
// search-time availability cache: that cache exists to mark positive answers
// on a page of hits and drops a negative one, whereas a fetched dataset must
// say "not queryable, and why" as plainly as "queryable, here".
type AvailabilityResolver interface {
	GetTableAvailability(ctx context.Context, urn string) (*query.TableAvailability, error)
}

// availabilityTimeout bounds the query-side lookup a dataset fetch makes. Row
// estimation can run a COUNT(*) against the warehouse; the record must not
// wait on it indefinitely, and a lookup that runs out of time leaves the
// record without an availability rather than failing the fetch.
const availabilityTimeout = 5 * time.Second

// lineageDepth is how far a fetched dataset's lineage is walked each way.
const lineageDepth = 3

// datasetDocument is what fetching a dataset returns: the dataset, and the
// lineage edges a walk reaches upstream and downstream of it.
type datasetDocument struct {
	*catalog.Dataset
	Upstream   []catalog.Hop `json:"upstream"`
	Downstream []catalog.Hop `json:"downstream"`
	// QueryAvailability is the query engine's answer for a table: the table to
	// query, the connection it is reachable on, and the estimated row count.
	// Nil for a dataset no query engine serves or when the lookup did not
	// resolve.
	QueryAvailability *query.TableAvailability `json:"query_availability,omitempty"`
}

// BoundName returns the name a persona's connection rules match for the
// connection a catalog reference names by kind and instance. The platform
// supplies it from connid.Resolver, the one place those two names are crossed.
type BoundName func(connectionKind, instance string) string

// NativeCatalogProvider dereferences the platform catalog's references to what
// a connection holds: mcp:dataset:(...), mcp:column:(...) and
// mcp:schema_document:(...). A dataset reference whose path stops above a
// dataset, a catalog or a schema, lists what is under it, which is how a
// connection's catalog is browsed. Definitions are the governance provider's.
//
// Each of these belongs to its connection, so it is dereferenced only for a
// caller whose persona may reach that connection, the same boundary search and
// every tool call apply.
type NativeCatalogProvider struct {
	store        CatalogReader
	bound        BoundName
	availability AvailabilityResolver
}

// NewNativeCatalogProvider builds the provider over a catalog store. A nil
// bound treats the instance name as the name persona rules match.
func NewNativeCatalogProvider(store CatalogReader, bound BoundName) *NativeCatalogProvider {
	return &NativeCatalogProvider{store: store, bound: bound}
}

// SetAvailabilityResolver attaches the query-side answer to "can this dataset
// be queried, and where" (#1590), so a fetched table says so without a second
// call. Nil (the default) leaves the record without one. Call once at wiring
// time.
func (p *NativeCatalogProvider) SetAvailabilityResolver(r AvailabilityResolver) {
	p.availability = r
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
		doc, err := p.fetchDataset(ctx, parsed, caller)
		return doc, true, err
	case catalog.KindSchemaDocument:
		if !caller.allowsConnection(p.boundName(parsed)) {
			return nil, true, ErrNotFound
		}
		doc, err := p.fetchSchemaDocument(ctx, parsed)
		return doc, true, err
	default:
		return nil, false, nil
	}
}

// boundName is the persona-rule name of the connection a reference names.
func (p *NativeCatalogProvider) boundName(r catalog.Ref) string {
	if p.bound == nil {
		return r.Connection
	}
	return p.bound(r.ConnectionKind, r.Connection)
}

// sourceRefs are the documents a dataset was read from, as references the
// caller can fetch in turn: the connection's schema document a scan read it
// from (#1918), and the managed resource a registered table was created from.
func sourceRefs(d *catalog.Dataset) []DocumentRef {
	out := make([]DocumentRef, 0, 2)
	if d.SchemaDocument != "" {
		out = append(out, DocumentRef{Reference: d.SchemaDocument, Type: knowledgepage.RefTargetCatalog})
	}
	if d.SchemaResourceID != "" {
		out = append(out, DocumentRef{Reference: "mcp:resource:" + d.SchemaResourceID, Type: knowledgepage.RefTargetResource})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// fetchSchemaDocument reads a connection's schema document. Its text is the
// body, up to the inline limit every fetched file is held to; a longer one is
// cut at a character boundary and says so.
func (p *NativeCatalogProvider) fetchSchemaDocument(ctx context.Context, r catalog.Ref) (*Document, error) {
	dr, ok := p.store.(DocumentReader)
	if !ok {
		return nil, ErrNotFound
	}
	d, err := dr.GetDocument(ctx, r)
	if err != nil {
		return nil, catalogError(err)
	}
	body, note := d.Content, ""
	if len(body) > resource.MaxInlineContentBytes {
		body = body[:runeCut(body, resource.MaxInlineContentBytes)]
		note = fmt.Sprintf("Only the first %d of this document's %d bytes are shown here.", len(body), d.SizeBytes)
	}
	return &Document{
		Reference: d.Reference,
		Source:    SourceCatalog,
		Title:     d.Connection + " " + d.Filename,
		Body:      string(body),
		Content:   d,
		Note:      note,
	}, nil
}

// fetchDataset reads a dataset, or the one column a column reference names. A
// dataset reference the store holds no dataset for is browsed instead.
func (p *NativeCatalogProvider) fetchDataset(ctx context.Context, r catalog.Ref, caller Caller) (*Document, error) {
	d, err := p.store.GetDataset(ctx, r.Dataset())
	if errors.Is(err, catalog.ErrNotFound) && r.Kind == catalog.KindDataset {
		return p.browse(ctx, r)
	}
	if err != nil {
		return nil, catalogError(err)
	}
	d = scrubDataset(d)
	if r.Kind == catalog.KindDataset {
		content, err := p.withLineage(ctx, d, caller)
		if err != nil {
			return nil, err
		}
		content.QueryAvailability = p.resolveAvailability(ctx, d)
		doc := &Document{
			Reference:  d.Reference,
			Source:     SourceCatalog,
			Title:      d.Name,
			Body:       d.Aspects.Description,
			Content:    content,
			EntityURNs: []string{d.Reference},
			References: sourceRefs(d),
		}
		if a := content.QueryAvailability; a != nil && a.Available {
			doc.Verifiable = &query.Verifiable{URN: d.Reference, QueryTable: a.QueryTable, Connection: a.Connection}
		}
		return doc, nil
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

// resolveAvailability asks the query side whether and where a table is
// queryable; nil for a dataset that is not a table or view, when no resolver
// is wired, or when the lookup failed or ran out of time. A negative answer
// (Available false, with the provider's reason) is kept: it is what the reader
// needs to know before writing a query.
func (p *NativeCatalogProvider) resolveAvailability(ctx context.Context, d *catalog.Dataset) *query.TableAvailability {
	if p.availability == nil || !queryable(d.Type) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, availabilityTimeout)
	defer cancel()
	avail, err := p.availability.GetTableAvailability(ctx, d.Reference)
	if err != nil || ctx.Err() != nil {
		slog.Debug("dataset availability lookup skipped", logKeyURN, logsan.SanitizeForLog(d.Reference), logKeyError, logsan.SanitizeForLog(fmt.Sprint(err)))
		return nil
	}
	return avail
}

// queryable reports whether a dataset of this type is a table a query engine
// reads, rather than an API or GraphQL operation.
func queryable(t catalog.DatasetType) bool {
	switch t {
	case catalog.DatasetTable, catalog.DatasetView, catalog.DatasetRegisteredTable, catalog.DatasetWebhookTable:
		return true
	default:
		return false
	}
}

// withLineage adds the dataset's lineage when the store can walk it: the hops
// whose two ends the caller may both see, so a fetch never names a dataset on
// a connection the persona does not reach.
func (p *NativeCatalogProvider) withLineage(ctx context.Context, d *catalog.Dataset, caller Caller) (datasetDocument, error) {
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
	doc.Upstream, doc.Downstream = p.visibleHops(up, caller), p.visibleHops(down, caller)
	return doc, nil
}

// visibleHops keeps the hops whose source and target the caller may both see.
func (p *NativeCatalogProvider) visibleHops(hops []catalog.Hop, caller Caller) []catalog.Hop {
	out := make([]catalog.Hop, 0, len(hops))
	for _, h := range hops {
		if p.reaches(h.Source, caller) && p.reaches(h.Target, caller) {
			out = append(out, h)
		}
	}
	return out
}

// reaches reports whether the caller may see a reference: a dataset, column
// or schema document when the persona reaches its connection, and anything
// that names no connection (a file a script wrote) always.
func (p *NativeCatalogProvider) reaches(ref string, caller Caller) bool {
	r, err := catalog.ParseRef(ref)
	if err != nil || r.Connection == "" {
		return true
	}
	return caller.allowsConnection(p.boundName(r))
}

// browseDocument is what fetching a path above a dataset returns: the
// connection and path browsed, and the datasets and path groups directly under
// it, each with the reference that fetches it.
type browseDocument struct {
	ConnectionKind string                `json:"connection_kind"`
	Connection     string                `json:"connection"`
	Path           []string              `json:"path"`
	Entries        []catalog.BrowseEntry `json:"entries"`
}

// browse lists what is under a dataset reference's path. A path with nothing
// under it is not-found, as the dataset it might have named is.
func (p *NativeCatalogProvider) browse(ctx context.Context, r catalog.Ref) (*Document, error) {
	b, ok := p.store.(CatalogBrowser)
	if !ok {
		return nil, ErrNotFound
	}
	entries, err := b.Browse(ctx, catalog.BrowseQuery{ConnectionKind: r.ConnectionKind, Connection: r.Connection, Prefix: r.Path})
	if err != nil {
		return nil, fmt.Errorf("browsing %s: %w", r, err)
	}
	if len(entries) == 0 {
		return nil, ErrNotFound
	}
	return &Document{
		Reference: r.String(),
		Source:    SourceCatalog,
		Title:     r.Connection + " " + strings.Join(r.Path, "."),
		Content:   browseDocument{ConnectionKind: r.ConnectionKind, Connection: r.Connection, Path: r.Path, Entries: entries},
	}, nil
}

// runeCut is the largest cut at or below limit that does not split a UTF-8
// character, looking back no further than one character's length. limit must
// be below len(b).
func runeCut(b []byte, limit int) int {
	for cut := limit; cut > limit-utf8.UTFMax && cut > 0; cut-- {
		if utf8.RuneStart(b[cut]) {
			return cut
		}
	}
	return limit
}

// catalogError maps the store's not-found onto the router's, which fetch
// renders as a structured not-found.
func catalogError(err error) error {
	if errors.Is(err, catalog.ErrNotFound) {
		return ErrNotFound
	}
	return fmt.Errorf("reading catalog: %w", err)
}
