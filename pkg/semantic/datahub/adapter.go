// Package datahub provides a DataHub implementation of the semantic provider.
package datahub

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"
	"github.com/txn2/mcp-datahub/pkg/types"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/urnbuild"
)

const (
	// defaultPlatform is the default data platform for URN building.
	defaultPlatform = "trino"

	// defaultTimeout is the default HTTP client timeout.
	defaultTimeout = 30 * time.Second

	// urnPartsMinCount is the minimum number of dot-separated parts in a URN name
	// (e.g., "schema.table" = 2, "catalog.schema.table" = 3).
	urnPartsMinCount = 3

	// documentSnippetRunes bounds a context-document body excerpt in a search hit
	// so the snippet stays readable without carrying the whole document (#692).
	documentSnippetRunes = 280

	// datahubPlatform is the provider name for this adapter.
	datahubPlatform = "datahub"

	// glossaryTermEntityType is the DataHub entity-type token used to scope a
	// searchAcrossEntities query to glossary terms for the catalog picker (#785).
	glossaryTermEntityType = "GLOSSARY_TERM"

	// defaultRefLimit and maxRefLimit bound a picker lookup page.
	defaultRefLimit = 25
	maxRefLimit     = 100

	// DataHub search-filter field names. Defined as constants because
	// each appears in multiple places (filter mapping, lineage inheritance,
	// query construction).
	filterFieldPlatform = "platform"
	filterFieldTags     = "tags"
	filterFieldOwners   = "owners"
)

// Config holds DataHub adapter configuration.
type Config struct {
	URL      string
	Token    string
	Platform string // Default platform for URN building (e.g., "trino", "postgres")
	Timeout  time.Duration
	Debug    bool // Enable debug logging

	// CatalogMapping maps query engine catalog names to metadata catalog names.
	// For example: {"rdbms": "warehouse"} means the Trino "rdbms" catalog
	// corresponds to the "warehouse" catalog in DataHub URNs.
	CatalogMapping map[string]string

	// Lineage configuration for inheritance-aware column resolution.
	Lineage LineageConfig
}

// Client defines the interface for DataHub operations.
// This allows for mocking in tests.
type Client interface {
	SearchAcrossEntities(ctx context.Context, query string, opts ...dhclient.SearchOption) (*types.SearchResult, error)
	SemanticSearch(ctx context.Context, query string, opts ...dhclient.SearchOption) (*types.SearchResult, error)
	SearchDocuments(ctx context.Context, query string, opts ...dhclient.SearchOption) ([]types.Document, error)
	GetRelatedDocuments(ctx context.Context, urn string) ([]types.Document, error)
	GetDocument(ctx context.Context, urn string) (*types.Document, error)
	GetEntity(ctx context.Context, urn string) (*types.Entity, error)
	GetSchema(ctx context.Context, urn string) (*types.SchemaMetadata, error)
	GetSchemas(ctx context.Context, urns []string) (map[string]*types.SchemaMetadata, error)
	GetLineage(ctx context.Context, urn string, opts ...dhclient.LineageOption) (*types.LineageResult, error)
	GetColumnLineage(ctx context.Context, urn string) (*types.ColumnLineage, error)
	GetGlossaryTerm(ctx context.Context, urn string) (*types.GlossaryTerm, error)
	GetRootGlossaryNodes(ctx context.Context, start, count int) ([]types.GlossaryNode, int, error)
	GetRootGlossaryTerms(ctx context.Context, start, count int) ([]types.GlossaryTerm, int, error)
	GetGlossaryNodeChildren(ctx context.Context, nodeURN string, start, count int) (*types.GlossaryChildren, error)
	GetGlossaryParentChain(ctx context.Context, urn string) ([]types.GlossaryNode, error)
	GetQueries(ctx context.Context, urn string) (*types.QueryList, error)
	ListTags(ctx context.Context, filter string) ([]types.Tag, error)
	ListDomains(ctx context.Context) ([]types.Domain, error)
	GetDataProduct(ctx context.Context, urn string) (*types.DataProduct, error)
	Ping(ctx context.Context) error
	Close() error
}

// Adapter implements semantic.Provider using DataHub.
type Adapter struct {
	cfg       Config
	client    Client
	sanitizer *semantic.Sanitizer
}

// New creates a new DataHub adapter with a real client.
func New(cfg Config) (*Adapter, error) {
	if cfg.URL == "" {
		return nil, errors.New("datahub URL is required")
	}
	if cfg.Platform == "" {
		cfg.Platform = defaultPlatform
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}

	clientCfg := dhclient.DefaultConfig()
	clientCfg.URL = cfg.URL
	clientCfg.Token = cfg.Token
	clientCfg.Timeout = cfg.Timeout
	clientCfg.Debug = cfg.Debug

	client, err := dhclient.New(clientCfg)
	if err != nil {
		return nil, fmt.Errorf("creating datahub client: %w", err)
	}

	return &Adapter{
		cfg:       cfg,
		client:    client,
		sanitizer: semantic.NewSanitizer(semantic.DefaultSanitizeConfig()),
	}, nil
}

// NewWithClient creates a new DataHub adapter with a provided client (for testing).
func NewWithClient(cfg Config, client Client) (*Adapter, error) {
	if client == nil {
		return nil, errors.New("datahub client is required")
	}
	if cfg.Platform == "" {
		cfg.Platform = defaultPlatform
	}
	return &Adapter{
		cfg:       cfg,
		client:    client,
		sanitizer: semantic.NewSanitizer(semantic.DefaultSanitizeConfig()),
	}, nil
}

// Name returns the provider name.
func (*Adapter) Name() string {
	return datahubPlatform
}

// LineageConfig returns the lineage configuration.
// This allows verifying that configuration was wired correctly.
func (a *Adapter) LineageConfig() LineageConfig {
	return a.cfg.Lineage
}

// GetTableContext retrieves table context from DataHub.
func (a *Adapter) GetTableContext(ctx context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error) {
	urn := a.buildDatasetURN(table)

	entity, err := a.client.GetEntity(ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("getting entity from datahub: %w", err)
	}

	tc := a.entityToTableContext(entity)
	return a.sanitizer.SanitizeTableContext(tc), nil
}

// GetColumnContext retrieves column context from DataHub.
func (a *Adapter) GetColumnContext(ctx context.Context, column semantic.ColumnIdentifier) (*semantic.ColumnContext, error) {
	urn := a.buildDatasetURN(column.TableIdentifier)

	schema, err := a.client.GetSchema(ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("getting schema from datahub: %w", err)
	}

	// Find the column in the schema by FieldPath
	for _, field := range schema.Fields {
		fieldName := extractFieldName(field.FieldPath)
		if fieldName == column.Column {
			cc := a.fieldToColumnContext(field)
			return a.sanitizer.SanitizeColumnContext(cc), nil
		}
	}

	return nil, fmt.Errorf("column %s not found in schema", column.Column)
}

// GetColumnsContext retrieves all columns context from DataHub.
// When lineage is enabled, it inherits metadata from upstream datasets for undocumented columns.
func (a *Adapter) GetColumnsContext(ctx context.Context, table semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error) {
	urn := a.buildDatasetURN(table)

	// Use lineage-aware resolution if enabled
	if a.cfg.Lineage.Enabled {
		resolver := newLineageResolver(a.client, a.cfg.Lineage, a.sanitizer)
		columns, err := resolver.resolveColumnsWithLineage(ctx, urn, table.String())
		if err != nil {
			return nil, fmt.Errorf("getting columns with lineage from datahub: %w", err)
		}
		return columns, nil
	}

	// Standard resolution without lineage
	schema, err := a.client.GetSchema(ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("getting schema from datahub: %w", err)
	}

	columns := make(map[string]*semantic.ColumnContext, len(schema.Fields))
	for _, field := range schema.Fields {
		fieldName := extractFieldName(field.FieldPath)
		cc := a.fieldToColumnContext(field)
		columns[fieldName] = a.sanitizer.SanitizeColumnContext(cc)
	}

	return columns, nil
}

// GetLineage retrieves lineage from DataHub.
func (a *Adapter) GetLineage(ctx context.Context, table semantic.TableIdentifier, direction semantic.LineageDirection, maxDepth int) (*semantic.LineageInfo, error) {
	urn := a.buildDatasetURN(table)

	dhDirection := dhclient.LineageDirectionUpstream
	if direction == semantic.LineageDownstream {
		dhDirection = dhclient.LineageDirectionDownstream
	}

	result, err := a.client.GetLineage(ctx, urn,
		dhclient.WithDirection(dhDirection),
		dhclient.WithDepth(maxDepth),
	)
	if err != nil {
		return nil, fmt.Errorf("getting lineage from datahub: %w", err)
	}

	return a.lineageResultToInfo(result, direction, maxDepth), nil
}

// GetGlossaryTerm retrieves a glossary term from DataHub.
func (a *Adapter) GetGlossaryTerm(ctx context.Context, urn string) (*semantic.GlossaryTerm, error) {
	term, err := a.client.GetGlossaryTerm(ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("getting glossary term from datahub: %w", err)
	}

	if term == nil {
		return nil, fmt.Errorf("glossary term not found: %s", urn)
	}

	return a.sanitizer.SanitizeGlossaryTerm(&semantic.GlossaryTerm{
		URN:              term.URN,
		Name:             term.Name,
		Description:      term.Description,
		ParentNode:       term.ParentNode,
		Owners:           convertOwners(term.Owners),
		CustomProperties: term.Properties,
	}), nil
}

// SearchTables searches for tables in DataHub using searchAcrossEntities.
// Supports advanced field-level filtering (column names, tags, etc.) via SearchFilter.Filters.
func (a *Adapter) SearchTables(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	results, _, err := a.SearchTablesCounted(ctx, filter)
	return results, err
}

// SearchTablesCounted is SearchTables plus DataHub's total match count, which the
// same GraphQL response already carries (searchAcrossEntities returns total
// alongside the page). It implements semantic.TableMatchCounter, the seam that lets a
// caller tell "these are all the matches" from "this is one clamped page of them":
// the upstream client caps every search at its MaxLimit, so the page length alone
// cannot distinguish the two (#1238).
func (a *Adapter) SearchTablesCounted(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, int, error) {
	opts := buildSearchOptions(filter)

	var (
		result *types.SearchResult
		err    error
	)
	if strings.EqualFold(filter.Mode, "semantic") {
		result, err = a.client.SemanticSearch(ctx, filter.Query, opts...)
	} else {
		result, err = a.client.SearchAcrossEntities(ctx, filter.Query, opts...)
	}
	if err != nil {
		return nil, semantic.TotalUnknown, fmt.Errorf("searching datahub: %w", err)
	}

	results := make([]semantic.TableSearchResult, 0, len(result.Entities))
	for _, entity := range result.Entities {
		results = append(results, a.toSearchResult(entity))
	}

	return results, searchTotal(result), nil
}

// SearchTags searches DataHub tags by display name for the catalog tag picker
// (#785), returning URN + name refs the picker resolves under the hood. It wraps
// the upstream client's ListTags, which name-searches the TAG entity type; an
// empty query lists tags. The returned name/URN come straight from DataHub so a
// selected value is always a real tag URN. ListTags has no server-side limit, so
// the page is truncated before sanitizing to avoid sanitizing discarded entries.
func (a *Adapter) SearchTags(ctx context.Context, query string, limit int) ([]semantic.EntityRef, error) {
	tags, err := a.client.ListTags(ctx, strings.TrimSpace(query))
	if err != nil {
		return nil, fmt.Errorf("listing datahub tags: %w", err)
	}
	if n := clampRefLimit(limit); len(tags) > n {
		tags = tags[:n]
	}
	refs := make([]semantic.EntityRef, 0, len(tags))
	for _, t := range tags {
		refs = append(refs, a.entityRef(t.URN, t.Name, t.Description))
	}
	return refs, nil
}

// SearchGlossaryTerms searches DataHub glossary terms by display name for the
// catalog glossary picker (#785) and lists them on an empty query. Glossary terms
// have no dedicated list method, so this uses searchAcrossEntities scoped to the
// GLOSSARY_TERM entity type, whose Name is resolved from the term's properties by
// the upstream client.
//
// An empty query becomes the "*" list query rather than reaching DataHub as "".
// The query string is forwarded verbatim into searchAcrossEntities (mcp-datahub
// buildBaseSearchInput), where "" matches nothing, so the documented "empty query
// lists" contract only holds with the wildcard substituted here — the same
// substitution ListTags makes upstream for tags, and the same query BrowseDocuments
// already uses to enumerate documents through this client.
func (a *Adapter) SearchGlossaryTerms(ctx context.Context, query string, limit int) ([]semantic.EntityRef, error) {
	refs, _, err := a.SearchGlossaryTermsCounted(ctx, query, limit)
	return refs, err
}

// SearchGlossaryTermsCounted is SearchGlossaryTerms plus DataHub's total match
// count, completing semantic.GlossaryMatchCounter. The glossary page is bounded twice —
// by clampRefLimit here and by the client's MaxLimit upstream — so its length is
// even less able to report whether more terms matched than the dataset page's is.
func (a *Adapter) SearchGlossaryTermsCounted(ctx context.Context, query string, limit int) ([]semantic.EntityRef, int, error) {
	result, err := a.client.SearchAcrossEntities(ctx, listAllQuery(query),
		dhclient.WithTypes([]string{glossaryTermEntityType}), dhclient.WithLimit(clampRefLimit(limit)))
	if err != nil {
		return nil, semantic.TotalUnknown, fmt.Errorf("searching datahub glossary terms: %w", err)
	}
	refs := make([]semantic.EntityRef, 0, len(result.Entities))
	for _, e := range result.Entities {
		refs = append(refs, a.entityRef(e.URN, e.Name, e.Description))
	}
	return refs, searchTotal(result), nil
}

// searchTotal reads the backend's total match count off a search response,
// reporting semantic.TotalUnknown when the response contradicts itself by
// counting fewer matches than the page it returned. A total below the page length
// cannot be the number of matches, and passing it on would let a caller declare a
// clamped page complete — the exact reading the count exists to prevent.
func searchTotal(result *types.SearchResult) int {
	if result.Total < len(result.Entities) {
		return semantic.TotalUnknown
	}
	return result.Total
}

// ListDomains lists DataHub domains for the catalog domain picker (#785). Domains
// are not returned by name from searchAcrossEntities (no inline fragment upstream),
// so this wraps the client's dedicated ListDomains, which returns every domain with
// its display name; the picker filters client-side.
func (a *Adapter) ListDomains(ctx context.Context) ([]semantic.EntityRef, error) {
	domains, err := a.client.ListDomains(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing datahub domains: %w", err)
	}
	refs := make([]semantic.EntityRef, 0, len(domains))
	for _, d := range domains {
		refs = append(refs, a.entityRef(d.URN, d.Name, d.Description))
	}
	return refs, nil
}

// --- glossary hierarchy (#1155) ---

// ListRootGlossaryNodes returns the top of the glossary tree: the nodes with no
// parent. It returns the requested page plus the total number of root nodes so a
// browser can page. Backed by mcp-datahub's GetRootGlossaryNodes (v1.15.0+).
func (a *Adapter) ListRootGlossaryNodes(ctx context.Context, offset, limit int) ([]semantic.GlossaryNode, int, error) {
	nodes, total, err := a.client.GetRootGlossaryNodes(ctx, clampGlossaryOffset(offset), clampRefLimit(limit))
	if err != nil {
		return nil, 0, fmt.Errorf("listing datahub root glossary nodes: %w", err)
	}
	return a.glossaryNodes(nodes), total, nil
}

// ListRootGlossaryTerms returns the glossary terms with no parent node — terms
// that sit at the top of the tree rather than inside a node — plus the total so a
// browser can page. Root terms are a distinct read from root nodes because
// DataHub pages the two separately.
func (a *Adapter) ListRootGlossaryTerms(ctx context.Context, offset, limit int) ([]semantic.GlossaryTerm, int, error) {
	terms, total, err := a.client.GetRootGlossaryTerms(ctx, clampGlossaryOffset(offset), clampRefLimit(limit))
	if err != nil {
		return nil, 0, fmt.Errorf("listing datahub root glossary terms: %w", err)
	}
	return a.glossaryTerms(terms), total, nil
}

// ListGlossaryNodeChildren returns one page of the nodes and terms directly under
// nodeURN. DataHub pages a node's children as one mixed collection, so the
// returned Start/Count/Total describe the combined page rather than either slice.
//
// The children come from DataHub's graph index, which is populated
// asynchronously: an entity created moments earlier may not appear yet. Confirm a
// just-written parent with GetGlossaryParentChain, which reads the entity itself
// and is immediately consistent.
func (a *Adapter) ListGlossaryNodeChildren(ctx context.Context, nodeURN string, offset, limit int) (*semantic.GlossaryChildren, error) {
	children, err := a.client.GetGlossaryNodeChildren(ctx, strings.TrimSpace(nodeURN),
		clampGlossaryOffset(offset), clampRefLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("listing datahub glossary node children: %w", err)
	}
	if children == nil {
		return &semantic.GlossaryChildren{Nodes: []semantic.GlossaryNode{}, Terms: []semantic.GlossaryTerm{}}, nil
	}
	return &semantic.GlossaryChildren{
		Nodes: a.glossaryNodes(children.Nodes),
		Terms: a.glossaryTerms(children.Terms),
		Start: children.Start,
		Count: children.Count,
		Total: children.Total,
	}, nil
}

// GetGlossaryParentChain returns the ancestor nodes of a glossary term or node,
// ordered from the direct parent up to the root, so a browser can show where an
// entity sits without walking the tree. A root entity has an empty chain.
func (a *Adapter) GetGlossaryParentChain(ctx context.Context, urn string) ([]semantic.GlossaryNode, error) {
	chain, err := a.client.GetGlossaryParentChain(ctx, strings.TrimSpace(urn))
	if err != nil {
		return nil, fmt.Errorf("reading datahub glossary parent chain: %w", err)
	}
	return a.glossaryNodes(chain), nil
}

// glossaryNodes maps upstream nodes to the sanitized semantic shape, preserving
// the parent URN and the backend's own child tallies.
func (a *Adapter) glossaryNodes(nodes []types.GlossaryNode) []semantic.GlossaryNode {
	out := make([]semantic.GlossaryNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, semantic.GlossaryNode{
			URN:         n.URN,
			Name:        a.sanitizer.SanitizeString(n.Name),
			Description: a.sanitizer.SanitizeDescription(n.Description),
			ParentNode:  n.ParentNode,
			TermsCount:  n.TermsCount,
			NodesCount:  n.NodesCount,
		})
	}
	return out
}

// glossaryTerms maps upstream terms to the sanitized semantic shape.
func (a *Adapter) glossaryTerms(terms []types.GlossaryTerm) []semantic.GlossaryTerm {
	out := make([]semantic.GlossaryTerm, 0, len(terms))
	for _, t := range terms {
		out = append(out, semantic.GlossaryTerm{
			URN:         t.URN,
			Name:        a.sanitizer.SanitizeString(t.Name),
			Description: a.sanitizer.SanitizeDescription(t.Description),
		})
	}
	return out
}

// clampGlossaryOffset floors a page offset at zero so a negative offset does not
// reach DataHub as a negative start.
func clampGlossaryOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

// convertTagRefs maps an entity's tags to URN + display-name refs for the catalog
// editor's tag chips, preserving the URN that convertTags drops. A tag read from
// the raw-aspect fallback has an empty Name (only the URN is present); the UI falls
// back to the URN's short form for the label in that case.
func (a *Adapter) convertTagRefs(tags []types.Tag) []semantic.EntityRef {
	if len(tags) == 0 {
		return nil
	}
	refs := make([]semantic.EntityRef, len(tags))
	for i, t := range tags {
		refs[i] = a.entityRef(t.URN, t.Name, t.Description)
	}
	return refs
}

// entityRef builds a sanitized URN/name/description ref, centralizing the sanitize
// step shared by every picker lookup and the tag-ref conversion.
func (a *Adapter) entityRef(urn, name, description string) semantic.EntityRef {
	return semantic.EntityRef{
		URN:         urn,
		Name:        a.sanitizer.SanitizeString(name),
		Description: a.sanitizer.SanitizeDescription(description),
	}
}

// listAllQuery normalizes a picker query, substituting DataHub's "*" list query
// for an empty one. It is defined once because the substitution is a property of
// the backend, not of any one picker: searchAcrossEntities takes the query
// verbatim, and "" there matches nothing rather than everything.
func listAllQuery(query string) string {
	if q := strings.TrimSpace(query); q != "" {
		return q
	}
	return listAll
}

// listAll is DataHub's match-everything query.
const listAll = "*"

// clampRefLimit bounds a picker limit to a sane positive default so an unset or
// negative limit does not request an unbounded page.
func clampRefLimit(limit int) int {
	if limit <= 0 {
		return defaultRefLimit
	}
	if limit > maxRefLimit {
		return maxRefLimit
	}
	return limit
}

// SearchDocuments searches DataHub context documents by relevance and maps them to
// the neutral semantic.DocumentResult for the unified search corpus (#692). It wraps
// the upstream client's SearchDocuments (mcp-datahub v1.10.0+), which searches the
// DOCUMENT entity type via searchAcrossEntities and returns ALL matching documents
// regardless of visibility or publication state: filtering to globally-visible,
// published documents is the caller's job (each result carries ShowInGlobalContext
// and Status for that). A query of "*" lists; the query is passed through verbatim.
func (a *Adapter) SearchDocuments(ctx context.Context, query string, limit int) ([]semantic.DocumentResult, error) {
	var opts []dhclient.SearchOption
	if limit > 0 {
		opts = append(opts, dhclient.WithLimit(limit))
	}
	docs, err := a.client.SearchDocuments(ctx, query, opts...)
	if err != nil {
		return nil, fmt.Errorf("searching datahub documents: %w", err)
	}
	results := make([]semantic.DocumentResult, 0, len(docs))
	for i := range docs {
		results = append(results, toDocumentResult(&docs[i]))
	}
	return results, nil
}

// GetRelatedDocuments returns the context documents linked to an entity URN (the
// reverse of a document's related assets) and maps them to semantic.DocumentResult,
// for entity-keyed document discovery (#692). It wraps the upstream client's
// GetRelatedDocuments.
func (a *Adapter) GetRelatedDocuments(ctx context.Context, urn string) ([]semantic.DocumentResult, error) {
	docs, err := a.client.GetRelatedDocuments(ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("getting related datahub documents: %w", err)
	}
	results := make([]semantic.DocumentResult, 0, len(docs))
	for i := range docs {
		results = append(results, toDocumentResult(&docs[i]))
	}
	return results, nil
}

// GetDocument reads one context document by its URN and maps it to
// semantic.DocumentResult with the FULL body (not the truncated search snippet),
// so the fetch surface can dereference a urn:li:document:<id> reference to the
// complete content (#694). The upstream ErrNotFound (a URN that resolves to no
// document) is translated to semantic.ErrDocumentNotFound so the caller can render
// a structured not-found instead of a transport error.
func (a *Adapter) GetDocument(ctx context.Context, urn string) (*semantic.DocumentResult, error) {
	doc, err := a.client.GetDocument(ctx, urn)
	if err != nil {
		if errors.Is(err, dhclient.ErrNotFound) {
			return nil, fmt.Errorf("document %s: %w", urn, semantic.ErrDocumentNotFound)
		}
		return nil, fmt.Errorf("getting datahub document %s: %w", urn, err)
	}
	r := toDocumentResult(doc)
	// Single-document read returns the whole body; the snippet truncation that
	// keeps search hits bounded would defeat the purpose of a fetch.
	r.Body = doc.Content
	r.Snippet = ""
	return &r, nil
}

// BrowseDocuments enumerates context documents for the browse surface (#695): the
// offset/limit page of the complete corpus plus the total count. The list query
// "*" returns every document with no relevance threshold; WithOffset/WithLimit
// page it. The total comes from a separate count-only searchAcrossEntities scoped
// to the DOCUMENT entity type (the document-scoped SearchDocuments query fetches
// the total in GraphQL but the upstream client does not surface it, so the total is
// read from the typed SearchResult instead, with count=1 to avoid transferring a
// second full page). No visibility/status filter is applied, so the page and total
// describe the same complete set (drafts and hidden documents included).
func (a *Adapter) BrowseDocuments(ctx context.Context, offset, limit int) ([]semantic.DocumentResult, int, error) {
	// The count and the page are independent DataHub round trips with no data
	// dependency, so they run concurrently: a corpus sweep pays max(count, page)
	// latency per page rather than their sum.
	var (
		wg       sync.WaitGroup
		total    int
		countErr error
		docs     []types.Document
		listErr  error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		countRes, err := a.client.SearchAcrossEntities(ctx, listAll,
			dhclient.WithTypes([]string{dhclient.EntityTypeDocument}), dhclient.WithLimit(1))
		if err != nil {
			countErr = fmt.Errorf("counting datahub documents: %w", err)
			return
		}
		total = countRes.Total
	}()
	go func() {
		defer wg.Done()
		opts := []dhclient.SearchOption{}
		if limit > 0 {
			opts = append(opts, dhclient.WithLimit(limit))
		}
		if offset > 0 {
			opts = append(opts, dhclient.WithOffset(offset))
		}
		d, err := a.client.SearchDocuments(ctx, listAll, opts...)
		if err != nil {
			listErr = fmt.Errorf("listing datahub documents: %w", err)
			return
		}
		docs = d
	}()
	wg.Wait()
	if countErr != nil {
		return nil, 0, countErr
	}
	if listErr != nil {
		return nil, 0, listErr
	}

	results := make([]semantic.DocumentResult, 0, len(docs))
	for i := range docs {
		results = append(results, toDocumentResult(&docs[i]))
	}
	return results, total, nil
}

// toDocumentResult maps a DataHub document to the neutral semantic result.
func toDocumentResult(d *types.Document) semantic.DocumentResult {
	r := semantic.DocumentResult{
		URN:     d.URN,
		Title:   d.Title,
		SubType: d.SubType,
		Snippet: truncateRunes(d.Content, documentSnippetRunes),
		Status:  d.Status,
		// mcp-datahub documents global_context as "default: true" (pkg/tools/write_create.go):
		// a document is globally visible unless a settings aspect explicitly hides it.
		// The upstream client leaves Settings nil when the aspect is absent, so default
		// to visible here and let an explicit setting override, rather than dropping
		// default-visible documents.
		ShowInGlobalContext: true,
	}
	if d.Settings != nil {
		r.ShowInGlobalContext = d.Settings.ShowInGlobalContext
	}
	for i := range d.RelatedAssets {
		if d.RelatedAssets[i].URN != "" {
			r.RelatedAssetURNs = append(r.RelatedAssetURNs, d.RelatedAssets[i].URN)
		}
	}
	return r
}

// truncateRunes returns s clipped to at most n runes, appending an ellipsis when
// clipped, so a document snippet stays bounded in a search hit. Byte length bounds
// rune count, so a short string returns immediately; otherwise it walks runes and
// slices at the n-th rune's byte offset, avoiding a full []rune allocation over a
// possibly multi-KB body.
func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i] + "..."
		}
		count++
	}
	return s
}

// buildSearchOptions converts a semantic.SearchFilter to datahub client search options.
func buildSearchOptions(filter semantic.SearchFilter) []dhclient.SearchOption {
	var opts []dhclient.SearchOption
	if filter.Limit > 0 {
		opts = append(opts, dhclient.WithLimit(filter.Limit))
	}
	if filter.Offset > 0 {
		opts = append(opts, dhclient.WithOffset(filter.Offset))
	}

	// Entity types: explicit types, or default to DATASET.
	if len(filter.EntityTypes) > 0 {
		opts = append(opts, dhclient.WithTypes(filter.EntityTypes))
	} else {
		opts = append(opts, dhclient.WithEntityType(dhclient.DefaultEntityType))
	}

	// Build advanced filters from both legacy fields and new Filters slice.
	dhFilters := buildDHFilters(filter)
	if len(dhFilters) > 0 {
		opts = append(opts, dhclient.WithSearchFilters(dhFilters))
	}

	return opts
}

// buildDHFilters converts semantic filter fields to datahub client SearchFilter entries.
// Legacy fields (Platform, Tags, Domain, Owner) are mapped to their DataHub equivalents,
// then any explicit Filters are appended.
func buildDHFilters(filter semantic.SearchFilter) []dhclient.SearchFilter {
	var out []dhclient.SearchFilter

	if filter.Platform != "" {
		out = append(out, dhclient.SearchFilter{
			Field:  filterFieldPlatform,
			Values: []string{filter.Platform},
		})
	}
	if len(filter.Tags) > 0 {
		out = append(out, dhclient.SearchFilter{
			Field:  filterFieldTags,
			Values: filter.Tags,
		})
	}
	if filter.Domain != "" {
		out = append(out, dhclient.SearchFilter{
			Field:  "domains",
			Values: []string{filter.Domain},
		})
	}
	if filter.Owner != "" {
		out = append(out, dhclient.SearchFilter{
			Field:  filterFieldOwners,
			Values: []string{filter.Owner},
		})
	}

	// Append explicit advanced filters (fieldPaths, fieldTags, etc.)
	for _, f := range filter.Filters {
		out = append(out, dhclient.SearchFilter{
			Field:     f.Field,
			Values:    f.Values,
			Condition: f.Condition,
			Negated:   f.Negated,
		})
	}

	return out
}

// toSearchResult converts a datahub search entity to a semantic TableSearchResult.
func (a *Adapter) toSearchResult(entity types.SearchEntity) semantic.TableSearchResult {
	matchedField := ""
	if len(entity.MatchedFields) > 0 {
		matchedField = entity.MatchedFields[0].Name
	}

	domainName := ""
	if entity.Domain != nil {
		domainName = a.sanitizer.SanitizeString(entity.Domain.Name)
	}

	rawTags := make([]string, len(entity.Tags))
	for i, tag := range entity.Tags {
		rawTags[i] = tag.Name
	}

	return semantic.TableSearchResult{
		URN:          entity.URN,
		Name:         entity.Name,
		Platform:     entity.Platform,
		Description:  a.sanitizer.SanitizeDescription(entity.Description),
		Tags:         a.sanitizer.SanitizeTags(rawTags),
		Domain:       domainName,
		MatchedField: matchedField,
	}
}

// GetCuratedQueryCount returns the number of curated/saved queries for a dataset.
func (a *Adapter) GetCuratedQueryCount(ctx context.Context, urn string) (int, error) {
	queries, err := a.client.GetQueries(ctx, urn)
	if err != nil {
		return 0, fmt.Errorf("getting curated query count: %w", err)
	}
	return queries.Total, nil
}

// Close releases resources.
func (a *Adapter) Close() error {
	if a.client != nil {
		if err := a.client.Close(); err != nil {
			return fmt.Errorf("closing datahub client: %w", err)
		}
	}
	return nil
}

// buildDatasetURN creates a DataHub URN for a table.
// It applies catalog mapping to translate query engine catalogs to metadata catalogs.
func (a *Adapter) buildDatasetURN(table semantic.TableIdentifier) string {
	catalog := table.Catalog

	// Apply catalog mapping if configured
	if mapped, ok := a.cfg.CatalogMapping[catalog]; ok {
		catalog = mapped
	}

	// Build name from available parts
	var parts []string
	if catalog != "" {
		parts = append(parts, catalog)
	}
	if table.Schema != "" {
		parts = append(parts, table.Schema)
	}
	parts = append(parts, table.Table)

	return urnbuild.DatasetURNFromName(a.cfg.Platform, strings.Join(parts, "."))
}

// ResolveURN converts a DataHub URN to a table identifier.
func (*Adapter) ResolveURN(_ context.Context, urn string) (*semantic.TableIdentifier, error) {
	parsed, err := urnbuild.ParseDatasetURN(urn)
	if err != nil {
		return nil, fmt.Errorf("parsing dataset URN: %w", err)
	}

	parts := strings.Split(parsed.Name, ".")

	switch len(parts) {
	case 2:
		return &semantic.TableIdentifier{
			Schema: parts[0],
			Table:  parts[1],
		}, nil
	case urnPartsMinCount:
		return &semantic.TableIdentifier{
			Catalog: parts[0],
			Schema:  parts[1],
			Table:   parts[2],
		}, nil
	default:
		return nil, fmt.Errorf("invalid table name in URN: %s", parsed.Name)
	}
}

// BuildURN creates a URN from a table identifier.
func (a *Adapter) BuildURN(_ context.Context, table semantic.TableIdentifier) (string, error) {
	return a.buildDatasetURN(table), nil
}

// entityToTableContext converts a DataHub entity to semantic table context.
func (a *Adapter) entityToTableContext(entity *types.Entity) *semantic.TableContext {
	// Log any injection attempts in user-provided content
	a.logInjectionAttempts(entity)

	tc := &semantic.TableContext{
		URN:                  entity.URN,
		Description:          entity.Description,
		Owners:               convertOwners(entity.Owners),
		Tags:                 convertTags(entity.Tags),
		TagRefs:              a.convertTagRefs(entity.Tags),
		GlossaryTerms:        convertGlossaryTerms(entity.GlossaryTerms),
		Domain:               convertDomain(entity.Domain),
		Deprecation:          convertDeprecation(entity.Deprecation),
		CustomProperties:     convertProperties(entity.Properties),
		LastModified:         convertTimestamp(entity.LastModified),
		StructuredProperties: convertStructuredProperties(entity.StructuredProperties),
		DataContract:         convertDataContract(entity.DataContract),
	}

	// Populate incidents from enriched entity response (DataHub 1.4.x)
	tc.ActiveIncidents, tc.Incidents = convertIncidents(entity.ActiveIncidents)

	return tc
}

// logInjectionAttempts checks for and logs any prompt injection attempts in entity fields.
func (a *Adapter) logInjectionAttempts(entity *types.Entity) {
	// Check description
	semantic.DetectAndLogInjection(a.sanitizer, entity.URN, "description", entity.Description)

	// Check owner names
	for i, owner := range entity.Owners {
		semantic.DetectAndLogInjection(a.sanitizer, entity.URN, fmt.Sprintf("owners[%d].name", i), owner.Name)
	}

	// Check glossary term descriptions
	for i, term := range entity.GlossaryTerms {
		semantic.DetectAndLogInjection(a.sanitizer, entity.URN, fmt.Sprintf("glossaryTerms[%d].description", i), term.Description)
	}

	// Check domain description
	if entity.Domain != nil {
		semantic.DetectAndLogInjection(a.sanitizer, entity.URN, "domain.description", entity.Domain.Description)
	}

	// Check deprecation note
	if entity.Deprecation != nil {
		semantic.DetectAndLogInjection(a.sanitizer, entity.URN, "deprecation.note", entity.Deprecation.Note)
	}

	// Check custom properties
	for key, value := range entity.Properties {
		if str, ok := value.(string); ok {
			semantic.DetectAndLogInjection(a.sanitizer, entity.URN, fmt.Sprintf("properties[%s]", key), str)
		}
	}

	a.logInjectionAttemptsV14(entity)
}

// logInjectionAttemptsV14 checks DataHub 1.4.x fields for prompt injection attempts.
func (a *Adapter) logInjectionAttemptsV14(entity *types.Entity) {
	// Check structured property display names and string values
	for i, sp := range entity.StructuredProperties {
		if sp.Definition != nil {
			semantic.DetectAndLogInjection(a.sanitizer, entity.URN, fmt.Sprintf("structuredProperties[%d].displayName", i), sp.Definition.DisplayName)
		}
		for j, v := range sp.Values {
			if str, ok := v.(string); ok {
				semantic.DetectAndLogInjection(a.sanitizer, entity.URN, fmt.Sprintf("structuredProperties[%d].values[%d]", i, j), str)
			}
		}
	}

	// Check incident titles and descriptions
	if entity.ActiveIncidents != nil {
		for i, inc := range entity.ActiveIncidents.Incidents {
			semantic.DetectAndLogInjection(a.sanitizer, entity.URN, fmt.Sprintf("incidents[%d].title", i), inc.Title)
			semantic.DetectAndLogInjection(a.sanitizer, entity.URN, fmt.Sprintf("incidents[%d].description", i), inc.Description)
		}
	}
}

// convertOwners converts DataHub owners to semantic owners.
func convertOwners(owners []types.Owner) []semantic.Owner {
	result := make([]semantic.Owner, len(owners))
	for i, owner := range owners {
		result[i] = semantic.Owner{
			URN:   owner.URN,
			Type:  ownerTypeToSemantic(owner.Type),
			Name:  owner.Name,
			Email: owner.Email,
		}
	}
	return result
}

// convertTags converts DataHub tags to string slice.
func convertTags(tags []types.Tag) []string {
	result := make([]string, len(tags))
	for i, tag := range tags {
		result[i] = tag.Name
	}
	return result
}

// convertGlossaryTerms converts DataHub glossary terms to semantic terms.
func convertGlossaryTerms(terms []types.GlossaryTerm) []semantic.GlossaryTerm {
	result := make([]semantic.GlossaryTerm, len(terms))
	for i, term := range terms {
		result[i] = semantic.GlossaryTerm{
			URN:         term.URN,
			Name:        term.Name,
			Description: term.Description,
		}
	}
	return result
}

// convertDomain converts DataHub domain to semantic domain.
func convertDomain(domain *types.Domain) *semantic.Domain {
	if domain == nil {
		return nil
	}
	return &semantic.Domain{
		URN:         domain.URN,
		Name:        domain.Name,
		Description: domain.Description,
	}
}

// convertDeprecation converts DataHub deprecation to semantic deprecation.
func convertDeprecation(dep *types.Deprecation) *semantic.Deprecation {
	if dep == nil {
		return nil
	}
	result := &semantic.Deprecation{
		Deprecated: dep.Deprecated, //nolint:staticcheck // Using deprecated field as it's the only option
		Note:       dep.Note,
		Actor:      dep.Actor,
	}
	if dep.DecommissionTime > 0 {
		t := time.Unix(dep.DecommissionTime/1000, 0)
		result.DecommDate = &t
	}
	return result
}

// convertProperties converts DataHub properties to string map.
func convertProperties(props map[string]any) map[string]string {
	if len(props) == 0 {
		return nil
	}
	result := make(map[string]string, len(props))
	for k, v := range props {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	return result
}

// convertTimestamp converts a millisecond timestamp to a time pointer.
func convertTimestamp(ms int64) *time.Time {
	if ms == 0 {
		return nil
	}
	t := time.Unix(ms/1000, 0)
	return &t
}

// fieldToColumnContext converts a DataHub schema field to semantic column context.
func (a *Adapter) fieldToColumnContext(field types.SchemaField) *semantic.ColumnContext {
	fieldName := extractFieldName(field.FieldPath)

	// Log any injection attempts in user-provided content
	semantic.DetectAndLogInjection(a.sanitizer, "column:"+fieldName, "description", field.Description)
	for i, term := range field.GlossaryTerms {
		semantic.DetectAndLogInjection(a.sanitizer, "column:"+fieldName, fmt.Sprintf("glossaryTerms[%d].description", i), term.Description)
	}

	cc := &semantic.ColumnContext{
		Name:        fieldName,
		Description: field.Description,
	}

	// Check for PII and sensitivity tags
	for _, tag := range field.Tags {
		tagLower := strings.ToLower(tag.Name)
		if strings.Contains(tagLower, "pii") {
			cc.IsPII = true
		}
		if strings.Contains(tagLower, "sensitive") || strings.Contains(tagLower, "confidential") {
			cc.IsSensitive = true
		}
		cc.Tags = append(cc.Tags, tag.Name)
	}

	// Convert glossary terms
	cc.GlossaryTerms = make([]semantic.GlossaryTerm, len(field.GlossaryTerms))
	for i, term := range field.GlossaryTerms {
		cc.GlossaryTerms[i] = semantic.GlossaryTerm{
			URN:         term.URN,
			Name:        term.Name,
			Description: term.Description,
		}
	}

	return cc
}

// lineageResultToInfo converts a DataHub lineage result to semantic lineage info.
func (*Adapter) lineageResultToInfo(result *types.LineageResult, direction semantic.LineageDirection, maxDepth int) *semantic.LineageInfo {
	info := &semantic.LineageInfo{
		Direction: direction,
		MaxDepth:  maxDepth,
		Entities:  make([]semantic.LineageEntity, len(result.Nodes)),
	}

	// Build edge map for quick lookup
	edgeMap := make(map[string][]string)
	for _, edge := range result.Edges {
		edgeMap[edge.Target] = append(edgeMap[edge.Target], edge.Source)
	}

	for i, node := range result.Nodes {
		entity := semantic.LineageEntity{
			URN:      node.URN,
			Type:     node.Type,
			Name:     node.Name,
			Platform: node.Platform,
			Depth:    node.Level,
		}

		// Add parent edges
		if parents, ok := edgeMap[node.URN]; ok {
			entity.Parents = make([]semantic.LineageEdge, len(parents))
			for j, parent := range parents {
				entity.Parents[j] = semantic.LineageEdge{URN: parent}
			}
		}

		info.Entities[i] = entity
	}

	return info
}

// ownerTypeToSemantic converts DataHub ownership type to semantic type.
// Currently all DataHub ownership types map to OwnerTypeUser; extend this
// mapping when additional semantic owner types are introduced.
func ownerTypeToSemantic(_ types.OwnershipType) semantic.OwnerType {
	return semantic.OwnerTypeUser
}

// extractFieldName extracts the simple field name from a FieldPath.
// FieldPath can be "user.address.city" - we want "city".
func extractFieldName(fieldPath string) string {
	parts := strings.Split(fieldPath, ".")
	if len(parts) == 0 {
		return fieldPath
	}
	return parts[len(parts)-1]
}

// convertStructuredProperties converts DataHub structured properties to semantic types.
func convertStructuredProperties(props []types.StructuredPropertyValue) []semantic.StructuredProperty {
	if len(props) == 0 {
		return nil
	}
	result := make([]semantic.StructuredProperty, len(props))
	for i, p := range props {
		sp := semantic.StructuredProperty{
			Values: p.Values,
		}
		if p.Definition != nil {
			sp.QualifiedName = p.Definition.QualifiedName
			sp.DisplayName = p.Definition.DisplayName
		}
		result[i] = sp
	}
	return result
}

// convertIncidents converts DataHub incident result to semantic types.
// Returns (total, incidents) where total comes from ir.Total and may exceed
// len(incidents) if DataHub paginates or truncates the incident list.
func convertIncidents(ir *types.IncidentResult) (int, []semantic.Incident) {
	if ir == nil || ir.Total == 0 {
		return 0, nil
	}
	incidents := make([]semantic.Incident, len(ir.Incidents))
	for i, inc := range ir.Incidents {
		incidents[i] = semantic.Incident{
			URN:         inc.URN,
			Type:        inc.Type,
			Title:       inc.Title,
			Description: inc.Description,
			State:       inc.State,
			Created:     inc.Created,
		}
	}
	return ir.Total, incidents
}

// convertDataContract converts DataHub data contract to semantic types.
func convertDataContract(dc *types.DataContract) *semantic.DataContractStatus {
	if dc == nil {
		return nil
	}
	result := &semantic.DataContractStatus{
		Status: dc.Status,
	}
	if len(dc.AssertionResults) > 0 {
		result.AssertionResults = make([]semantic.AssertionResult, len(dc.AssertionResults))
		for i, ar := range dc.AssertionResults {
			result.AssertionResults[i] = semantic.AssertionResult{
				AssertionURN: ar.AssertionURN,
				Type:         ar.Type,
			}
		}
	}
	return result
}

// Verify interface compliance.
var (
	_ semantic.Provider             = (*Adapter)(nil)
	_ semantic.URNResolver          = (*Adapter)(nil)
	_ semantic.CatalogPicker        = (*Adapter)(nil)
	_ semantic.GovernanceReader     = (*Adapter)(nil)
	_ semantic.TableMatchCounter    = (*Adapter)(nil)
	_ semantic.GlossaryMatchCounter = (*Adapter)(nil)
)
