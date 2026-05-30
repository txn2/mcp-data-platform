package trino

import (
	"context"
	"fmt"
	"strings"
	"time"

	trinoclient "github.com/txn2/mcp-trino/pkg/client"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// query_kind labels for Trino metadata operations (issue #461). SQL queries use
// the SQL verb (see queryKind); the catalog/metadata calls the provider makes
// during cross-injection use these fixed kinds so trino_queries reflects all
// Trino traffic the query provider generates, not just COUNT estimates.
const (
	kindListCatalogs = "list_catalogs"
	kindListSchemas  = "list_schemas"
	kindListTables   = "list_tables"
	kindDescribe     = "describe_table"
	kindOther        = "other"
)

// SetMetrics enables per-call Trino query metrics by wrapping the adapter's
// client in an instrumenting decorator. No-op when metrics are disabled.
func (a *Adapter) SetMetrics(m *observability.Metrics) {
	if !m.Enabled() {
		return
	}
	a.client = &instrumentedClient{Client: a.client, metrics: m}
}

// instrumentedClient records observability.RecordTrinoQuery for every upstream
// Trino call. It embeds Client so Ping/Close fall through unchanged.
type instrumentedClient struct {
	Client
	metrics *observability.Metrics
}

// rec records one observation for the given query_kind and returns the
// (wrapped) error so callers can `return value, c.rec(...)` in one line.
// Wrapping matches the codebase's decorator convention and preserves the error
// chain for errors.Is/As.
func (c *instrumentedClient) rec(ctx context.Context, kind string, start time.Time, err error) error {
	c.metrics.RecordTrinoQuery(ctx, observability.UpstreamStatus(err), kind, time.Since(start))
	if err != nil {
		return fmt.Errorf("trino %s: %w", kind, err)
	}
	return nil
}

// Query records a query observation (query_kind from the SQL verb) and delegates.
func (c *instrumentedClient) Query(ctx context.Context, sql string, opts trinoclient.QueryOptions) (*trinoclient.QueryResult, error) {
	start := time.Now()
	r, err := c.Client.Query(ctx, sql, opts)
	return r, c.rec(ctx, queryKind(sql), start, err)
}

// ListCatalogs records a list_catalogs observation and delegates.
func (c *instrumentedClient) ListCatalogs(ctx context.Context) ([]string, error) {
	start := time.Now()
	r, err := c.Client.ListCatalogs(ctx)
	return r, c.rec(ctx, kindListCatalogs, start, err)
}

// ListSchemas records a list_schemas observation and delegates.
func (c *instrumentedClient) ListSchemas(ctx context.Context, catalog string) ([]string, error) {
	start := time.Now()
	r, err := c.Client.ListSchemas(ctx, catalog)
	return r, c.rec(ctx, kindListSchemas, start, err)
}

// ListTables records a list_tables observation and delegates.
func (c *instrumentedClient) ListTables(ctx context.Context, catalog, schema string) ([]trinoclient.TableInfo, error) {
	start := time.Now()
	r, err := c.Client.ListTables(ctx, catalog, schema)
	return r, c.rec(ctx, kindListTables, start, err)
}

// DescribeTable records a describe_table observation and delegates.
func (c *instrumentedClient) DescribeTable(ctx context.Context, catalog, schema, table string) (*trinoclient.TableInfo, error) {
	start := time.Now()
	r, err := c.Client.DescribeTable(ctx, catalog, schema, table)
	return r, c.rec(ctx, kindDescribe, start, err)
}

// queryKind extracts a bounded query_kind label from a SQL statement by taking
// its leading keyword. Unknown or empty statements map to "other" so the label
// can never grow unbounded from arbitrary SQL.
func queryKind(sql string) string {
	fields := strings.Fields(strings.TrimSpace(sql))
	if len(fields) == 0 {
		return kindOther
	}
	switch verb := strings.ToLower(fields[0]); verb {
	case "select", "insert", "update", "delete", "merge",
		"show", "describe", "desc", "explain", "create", "drop", "alter", "call", "with":
		return verb
	default:
		return kindOther
	}
}
