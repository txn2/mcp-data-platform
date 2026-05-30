package datahub

import (
	"context"
	"fmt"
	"time"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"
	"github.com/txn2/mcp-datahub/pkg/types"

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
)

// SetMetrics enables per-operation DataHub request metrics by wrapping the
// adapter's client in an instrumenting decorator. No-op when metrics are
// disabled, so the public Provider interface is unchanged either way.
func (a *Adapter) SetMetrics(m *observability.Metrics) {
	if !m.Enabled() {
		return
	}
	a.client = &instrumentedClient{Client: a.client, metrics: m}
}

// instrumentedClient records observability.RecordDataHubRequest for every
// upstream call. It embeds Client so non-instrumented methods (Ping, Close)
// fall through unchanged.
type instrumentedClient struct {
	Client
	metrics *observability.Metrics
}

// rec records one observation for op and returns the (wrapped) error so the
// caller can `return value, c.rec(...)` in a single line. Wrapping matches the
// codebase's decorator convention (see semantic.CachedProvider) and keeps the
// error chain intact for errors.Is/As.
func (c *instrumentedClient) rec(ctx context.Context, op string, start time.Time, err error) error {
	c.metrics.RecordDataHubRequest(ctx, op, observability.UpstreamStatus(err), time.Since(start))
	if err != nil {
		return fmt.Errorf("datahub %s: %w", op, err)
	}
	return nil
}

// GetEntity records a get_entity observation and delegates to the wrapped client.
func (c *instrumentedClient) GetEntity(ctx context.Context, urn string) (*types.Entity, error) {
	start := time.Now()
	e, err := c.Client.GetEntity(ctx, urn)
	return e, c.rec(ctx, opGetEntity, start, err)
}

// GetSchema records a get_schema observation and delegates to the wrapped client.
func (c *instrumentedClient) GetSchema(ctx context.Context, urn string) (*types.SchemaMetadata, error) {
	start := time.Now()
	s, err := c.Client.GetSchema(ctx, urn)
	return s, c.rec(ctx, opGetSchema, start, err)
}

// GetSchemas records a get_schemas observation and delegates to the wrapped client.
func (c *instrumentedClient) GetSchemas(ctx context.Context, urns []string) (map[string]*types.SchemaMetadata, error) {
	start := time.Now()
	s, err := c.Client.GetSchemas(ctx, urns)
	return s, c.rec(ctx, opGetSchemas, start, err)
}

// GetLineage records a get_lineage observation and delegates to the wrapped client.
func (c *instrumentedClient) GetLineage(ctx context.Context, urn string, opts ...dhclient.LineageOption) (*types.LineageResult, error) {
	start := time.Now()
	l, err := c.Client.GetLineage(ctx, urn, opts...)
	return l, c.rec(ctx, opGetLineage, start, err)
}

// GetColumnLineage records a get_column_lineage observation and delegates to the wrapped client.
func (c *instrumentedClient) GetColumnLineage(ctx context.Context, urn string) (*types.ColumnLineage, error) {
	start := time.Now()
	l, err := c.Client.GetColumnLineage(ctx, urn)
	return l, c.rec(ctx, opGetColumnLineage, start, err)
}

// GetGlossaryTerm records a get_glossary_term observation and delegates to the wrapped client.
func (c *instrumentedClient) GetGlossaryTerm(ctx context.Context, urn string) (*types.GlossaryTerm, error) {
	start := time.Now()
	g, err := c.Client.GetGlossaryTerm(ctx, urn)
	return g, c.rec(ctx, opGetGlossaryTerm, start, err)
}

// GetQueries records a get_queries observation and delegates to the wrapped client.
func (c *instrumentedClient) GetQueries(ctx context.Context, urn string) (*types.QueryList, error) {
	start := time.Now()
	q, err := c.Client.GetQueries(ctx, urn)
	return q, c.rec(ctx, opGetQueries, start, err)
}
