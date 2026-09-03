package datahub

import (
	"context"
	"errors"
	"fmt"
	"time"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"
	"github.com/txn2/mcp-datahub/pkg/types"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// DataHub operation labels (issue #461). Bounded set, one per instrumented
// upstream client call.
const (
	opGetEntity        = "get_entity"
	opGetSchema        = "get_schema"
	opGetSchemas       = "get_schemas"
	opGetLineage       = "get_lineage"
	opGetColumnLineage = "get_column_lineage"
	opGetGlossaryTerm  = "get_glossary_term"
	opGetQueries       = "get_queries"

	opSearchAcross        = "search_across_entities"
	opSemanticSearch      = "semantic_search"
	opSearchDocuments     = "search_documents"
	opGetRelatedDocuments = "get_related_documents"
	opGetDocument         = "get_document"
	opListTags            = "list_tags"
	opListDomains         = "list_domains"

	opGetRootGlossaryNodes    = "get_root_glossary_nodes"
	opGetRootGlossaryTerms    = "get_root_glossary_terms"
	opGetGlossaryNodeChildren = "get_glossary_node_children"
	opGetGlossaryParentChain  = "get_glossary_parent_chain"
)

// SetMetrics wraps the adapter's client in an instrumenting decorator
// that records DataHub metrics AND opens a per-operation span. The caller
// installs it only when metrics OR tracing is enabled; the metric record
// is nil-safe and the span is a no-op outside an active trace, so the
// public Provider interface is unchanged and the off subsystem costs
// effectively nothing.
func (a *Adapter) SetMetrics(m *observability.Metrics) {
	a.client = &instrumentedClient{Client: a.client, metrics: m}
}

// instrumentedClient records observability.RecordDataHubRequest and a
// child span for every upstream call. It embeds Client so non-instrumented
// methods (Ping, Close) fall through unchanged.
type instrumentedClient struct {
	Client
	metrics *observability.Metrics
}

// startSpan opens the per-operation child span for a DataHub call. Paired
// with finish, which records the metric, sets span status, and wraps the
// error.
func (*instrumentedClient) startSpan(ctx context.Context, op string) (context.Context, trace.Span) {
	return observability.ChildSpan(ctx, "datahub."+op,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("datahub.operation", op)))
}

// upstreamStatus is observability.UpstreamStatus with the one exception the
// catalog's own answers force: a URN DataHub holds no entity for is reported as
// ErrNotFound (mcp-datahub v1.15.1, #1610), and that is an answer rather than a
// failed request. Counting it as an upstream error would put every enrichment
// read of a table the catalog does not hold into the DataHub error rate and
// mark its span failed, and most tables a deployment queries are not in its
// catalog.
func upstreamStatus(err error) string {
	if errors.Is(err, dhclient.ErrNotFound) {
		return observability.StatusOK
	}
	return observability.UpstreamStatus(err)
}

// finish records one observation for op, ends the span, and returns the
// (wrapped) error so the caller can `return value, c.finish(...)` in a
// single line. Wrapping matches the codebase's decorator convention (see
// semantic.CachedProvider) and keeps the error chain intact for errors.Is/As.
func (c *instrumentedClient) finish(ctx context.Context, span trace.Span, op string, start time.Time, err error) error {
	status := upstreamStatus(err)
	c.metrics.RecordDataHubRequest(ctx, op, status, time.Since(start))
	// The span carries the error only when the call actually failed:
	// SetSpanStatus records an exception for any non-nil error, and a URN the
	// catalog holds no entity for is an answer (#1610), not a failure to attach
	// one to.
	spanErr := err
	if status == observability.StatusOK {
		spanErr = nil
	}
	observability.SetSpanStatus(span, status, spanErr)
	span.End()
	if err != nil {
		return fmt.Errorf("datahub %s: %w", op, err)
	}
	return nil
}

// GetEntity records a get_entity observation and delegates to the wrapped client.
func (c *instrumentedClient) GetEntity(ctx context.Context, urn string) (*types.Entity, error) {
	ctx, span := c.startSpan(ctx, opGetEntity)
	start := time.Now()
	e, err := c.Client.GetEntity(ctx, urn)
	return e, c.finish(ctx, span, opGetEntity, start, err)
}

// GetSchema records a get_schema observation and delegates to the wrapped client.
func (c *instrumentedClient) GetSchema(ctx context.Context, urn string) (*types.SchemaMetadata, error) {
	ctx, span := c.startSpan(ctx, opGetSchema)
	start := time.Now()
	s, err := c.Client.GetSchema(ctx, urn)
	return s, c.finish(ctx, span, opGetSchema, start, err)
}

// GetSchemas records a get_schemas observation and delegates to the wrapped client.
func (c *instrumentedClient) GetSchemas(ctx context.Context, urns []string) (map[string]*types.SchemaMetadata, error) {
	ctx, span := c.startSpan(ctx, opGetSchemas)
	start := time.Now()
	s, err := c.Client.GetSchemas(ctx, urns)
	return s, c.finish(ctx, span, opGetSchemas, start, err)
}

// GetLineage records a get_lineage observation and delegates to the wrapped client.
func (c *instrumentedClient) GetLineage(ctx context.Context, urn string, opts ...dhclient.LineageOption) (*types.LineageResult, error) {
	ctx, span := c.startSpan(ctx, opGetLineage)
	start := time.Now()
	l, err := c.Client.GetLineage(ctx, urn, opts...)
	return l, c.finish(ctx, span, opGetLineage, start, err)
}

// GetColumnLineage records a get_column_lineage observation and delegates to the wrapped client.
func (c *instrumentedClient) GetColumnLineage(ctx context.Context, urn string) (*types.ColumnLineage, error) {
	ctx, span := c.startSpan(ctx, opGetColumnLineage)
	start := time.Now()
	l, err := c.Client.GetColumnLineage(ctx, urn)
	return l, c.finish(ctx, span, opGetColumnLineage, start, err)
}

// GetGlossaryTerm records a get_glossary_term observation and delegates to the wrapped client.
func (c *instrumentedClient) GetGlossaryTerm(ctx context.Context, urn string) (*types.GlossaryTerm, error) {
	ctx, span := c.startSpan(ctx, opGetGlossaryTerm)
	start := time.Now()
	g, err := c.Client.GetGlossaryTerm(ctx, urn)
	return g, c.finish(ctx, span, opGetGlossaryTerm, start, err)
}

// GetQueries records a get_queries observation and delegates to the wrapped client.
func (c *instrumentedClient) GetQueries(ctx context.Context, urn string) (*types.QueryList, error) {
	ctx, span := c.startSpan(ctx, opGetQueries)
	start := time.Now()
	q, err := c.Client.GetQueries(ctx, urn)
	return q, c.finish(ctx, span, opGetQueries, start, err)
}

// SearchAcrossEntities records a search_across_entities observation and delegates.
func (c *instrumentedClient) SearchAcrossEntities(ctx context.Context, query string, opts ...dhclient.SearchOption) (*types.SearchResult, error) {
	ctx, span := c.startSpan(ctx, opSearchAcross)
	start := time.Now()
	r, err := c.Client.SearchAcrossEntities(ctx, query, opts...)
	return r, c.finish(ctx, span, opSearchAcross, start, err)
}

// SemanticSearch records a semantic_search observation and delegates.
func (c *instrumentedClient) SemanticSearch(ctx context.Context, query string, opts ...dhclient.SearchOption) (*types.SearchResult, error) {
	ctx, span := c.startSpan(ctx, opSemanticSearch)
	start := time.Now()
	r, err := c.Client.SemanticSearch(ctx, query, opts...)
	return r, c.finish(ctx, span, opSemanticSearch, start, err)
}

// SearchDocuments records a search_documents observation and delegates (#692).
func (c *instrumentedClient) SearchDocuments(ctx context.Context, query string, opts ...dhclient.SearchOption) ([]types.Document, error) {
	ctx, span := c.startSpan(ctx, opSearchDocuments)
	start := time.Now()
	d, err := c.Client.SearchDocuments(ctx, query, opts...)
	return d, c.finish(ctx, span, opSearchDocuments, start, err)
}

// GetDocument records a get_document observation and delegates (#694).
func (c *instrumentedClient) GetDocument(ctx context.Context, urn string) (*types.Document, error) {
	ctx, span := c.startSpan(ctx, opGetDocument)
	start := time.Now()
	d, err := c.Client.GetDocument(ctx, urn)
	return d, c.finish(ctx, span, opGetDocument, start, err)
}

// GetRelatedDocuments records a get_related_documents observation and delegates (#692).
func (c *instrumentedClient) GetRelatedDocuments(ctx context.Context, urn string) ([]types.Document, error) {
	ctx, span := c.startSpan(ctx, opGetRelatedDocuments)
	start := time.Now()
	d, err := c.Client.GetRelatedDocuments(ctx, urn)
	return d, c.finish(ctx, span, opGetRelatedDocuments, start, err)
}

// ListTags records a list_tags observation and delegates (#785 catalog picker).
func (c *instrumentedClient) ListTags(ctx context.Context, filter string) ([]types.Tag, error) {
	ctx, span := c.startSpan(ctx, opListTags)
	start := time.Now()
	t, err := c.Client.ListTags(ctx, filter)
	return t, c.finish(ctx, span, opListTags, start, err)
}

// ListDomains records a list_domains observation and delegates (#785 catalog picker).
func (c *instrumentedClient) ListDomains(ctx context.Context) ([]types.Domain, error) {
	ctx, span := c.startSpan(ctx, opListDomains)
	start := time.Now()
	d, err := c.Client.ListDomains(ctx)
	return d, c.finish(ctx, span, opListDomains, start, err)
}

// GetRootGlossaryNodes records a get_root_glossary_nodes observation and
// delegates (#1155 glossary hierarchy).
func (c *instrumentedClient) GetRootGlossaryNodes(ctx context.Context, start, count int) ([]types.GlossaryNode, int, error) {
	ctx, span := c.startSpan(ctx, opGetRootGlossaryNodes)
	began := time.Now()
	nodes, total, err := c.Client.GetRootGlossaryNodes(ctx, start, count)
	return nodes, total, c.finish(ctx, span, opGetRootGlossaryNodes, began, err)
}

// GetRootGlossaryTerms records a get_root_glossary_terms observation and
// delegates (#1155 glossary hierarchy).
func (c *instrumentedClient) GetRootGlossaryTerms(ctx context.Context, start, count int) ([]types.GlossaryTerm, int, error) {
	ctx, span := c.startSpan(ctx, opGetRootGlossaryTerms)
	began := time.Now()
	terms, total, err := c.Client.GetRootGlossaryTerms(ctx, start, count)
	return terms, total, c.finish(ctx, span, opGetRootGlossaryTerms, began, err)
}

// GetGlossaryNodeChildren records a get_glossary_node_children observation and
// delegates (#1155 glossary hierarchy).
func (c *instrumentedClient) GetGlossaryNodeChildren(ctx context.Context, nodeURN string, start, count int) (*types.GlossaryChildren, error) {
	ctx, span := c.startSpan(ctx, opGetGlossaryNodeChildren)
	began := time.Now()
	children, err := c.Client.GetGlossaryNodeChildren(ctx, nodeURN, start, count)
	return children, c.finish(ctx, span, opGetGlossaryNodeChildren, began, err)
}

// GetGlossaryParentChain records a get_glossary_parent_chain observation and
// delegates (#1155 glossary hierarchy).
func (c *instrumentedClient) GetGlossaryParentChain(ctx context.Context, urn string) ([]types.GlossaryNode, error) {
	ctx, span := c.startSpan(ctx, opGetGlossaryParentChain)
	began := time.Now()
	chain, err := c.Client.GetGlossaryParentChain(ctx, urn)
	return chain, c.finish(ctx, span, opGetGlossaryParentChain, began, err)
}
