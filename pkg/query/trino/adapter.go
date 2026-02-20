// Package trino provides a Trino implementation of the query provider.
package trino

import (
	"context"
	"fmt"
	"strings"
	"time"

	trinoclient "github.com/txn2/mcp-trino/pkg/client"

	"github.com/txn2/mcp-data-platform/pkg/query"
)

const (
	defaultSSLPort      = 443
	defaultPlainPort    = 8080
	defaultQueryLimit   = 1000
	defaultMaxLimit     = 10000
	defaultQueryTimeout = 120 * time.Second
	tablePartsMinCount  = 3
)

// Config holds Trino adapter configuration.
type Config struct {
	Host           string
	Port           int
	User           string
	Password       string // #nosec G117 -- Trino connection credential from admin config
	Catalog        string
	Schema         string
	SSL            bool
	SSLVerify      bool
	Timeout        time.Duration
	DefaultLimit   int
	MaxLimit       int
	ReadOnly       bool
	ConnectionName string

	// CatalogMapping maps DataHub catalog names to Trino catalog names.
	// This is the reverse of the semantic layer's catalog mapping.
	// For example: {"warehouse": "rdbms"} means DataHub "warehouse" → Trino "rdbms"
	CatalogMapping map[string]string

	// EstimateRowCounts controls whether GetTableAvailability runs
	// SELECT COUNT(*) to estimate row counts. Disabled by default because
	// COUNT(*) can cause full table scans on large tables, making DataHub
	// search enrichment very slow.
	EstimateRowCounts bool
}

// Client defines the interface for Trino operations.
// This allows for mocking in tests.
type Client interface {
	Query(ctx context.Context, sql string, opts trinoclient.QueryOptions) (*trinoclient.QueryResult, error)
	ListCatalogs(ctx context.Context) ([]string, error)
	ListSchemas(ctx context.Context, catalog string) ([]string, error)
	ListTables(ctx context.Context, catalog, schema string) ([]trinoclient.TableInfo, error)
	DescribeTable(ctx context.Context, catalog, schema, table string) (*trinoclient.TableInfo, error)
	Ping(ctx context.Context) error
	Close() error
}

// Adapter implements query.Provider using Trino.
type Adapter struct {
	cfg    Config
	client Client
}

// New creates a new Trino adapter with a real client.
func New(cfg Config) (*Adapter, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("trino host is required")
	}
	if cfg.User == "" {
		return nil, fmt.Errorf("trino user is required")
	}
	if cfg.Port == 0 {
		if cfg.SSL {
			cfg.Port = defaultSSLPort
		} else {
			cfg.Port = defaultPlainPort
		}
	}
	if cfg.DefaultLimit == 0 {
		cfg.DefaultLimit = defaultQueryLimit
	}
	if cfg.MaxLimit == 0 {
		cfg.MaxLimit = defaultMaxLimit
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultQueryTimeout
	}

	clientCfg := trinoclient.Config{
		Host:      cfg.Host,
		Port:      cfg.Port,
		User:      cfg.User,
		Password:  cfg.Password,
		Catalog:   cfg.Catalog,
		Schema:    cfg.Schema,
		SSL:       cfg.SSL,
		SSLVerify: cfg.SSLVerify,
		Timeout:   cfg.Timeout,
		Source:    "mcp-data-platform",
	}

	client, err := trinoclient.New(clientCfg)
	if err != nil {
		return nil, fmt.Errorf("creating trino client: %w", err)
	}

	return &Adapter{
		cfg:    cfg,
		client: client,
	}, nil
}

// NewWithClient creates a new Trino adapter with a provided client (for testing).
func NewWithClient(cfg Config, client Client) (*Adapter, error) {
	if client == nil {
		return nil, fmt.Errorf("trino client is required")
	}
	if cfg.DefaultLimit == 0 {
		cfg.DefaultLimit = defaultQueryLimit
	}
	if cfg.MaxLimit == 0 {
		cfg.MaxLimit = defaultMaxLimit
	}
	return &Adapter{
		cfg:    cfg,
		client: client,
	}, nil
}

// Name returns the provider name.
func (*Adapter) Name() string {
	return "trino"
}

// ResolveTable converts a URN to a table identifier.
// Applies reverse catalog mapping if configured.
func (a *Adapter) ResolveTable(_ context.Context, urn string) (*query.TableIdentifier, error) {
	// Parse URN format: urn:li:dataset:(urn:li:dataPlatform:platform,name,env)
	if !strings.HasPrefix(urn, "urn:li:dataset:") {
		return nil, fmt.Errorf("invalid dataset URN: %s", urn)
	}

	// Extract the name part
	start := strings.Index(urn, ",")
	end := strings.LastIndex(urn, ",")
	if start == -1 || end == -1 || start == end {
		return nil, fmt.Errorf("invalid URN format: %s", urn)
	}

	name := urn[start+1 : end]
	parts := strings.Split(name, ".")

	switch len(parts) {
	case 2:
		return &query.TableIdentifier{
			Catalog:    a.cfg.Catalog,
			Schema:     parts[0],
			Table:      parts[1],
			Connection: a.cfg.ConnectionName,
		}, nil
	case tablePartsMinCount:
		catalog := parts[0]
		// Apply reverse catalog mapping if configured
		if mapped, ok := a.cfg.CatalogMapping[catalog]; ok {
			catalog = mapped
		}
		return &query.TableIdentifier{
			Catalog:    catalog,
			Schema:     parts[1],
			Table:      parts[2],
			Connection: a.cfg.ConnectionName,
		}, nil
	default:
		return nil, fmt.Errorf("invalid table name in URN: %s", name)
	}
}

// GetTableAvailability checks if a table is queryable.
func (a *Adapter) GetTableAvailability(ctx context.Context, urn string) (*query.TableAvailability, error) {
	table, err := a.ResolveTable(ctx, urn)
	if err != nil {
		return &query.TableAvailability{ //nolint:nilerr // availability check: resolve errors mean "not available", not a system failure
			Available: false,
			Error:     err.Error(),
		}, nil
	}

	// Actually verify the table exists by describing it
	_, err = a.client.DescribeTable(ctx, table.Catalog, table.Schema, table.Table)
	if err != nil {
		return &query.TableAvailability{ //nolint:nilerr // availability check: describe errors mean "not available", not a system failure
			Available: false,
			Error:     err.Error(),
		}, nil
	}

	// Optionally estimate row count via COUNT(*). Disabled by default
	// because COUNT(*) can trigger full table scans on large tables.
	var estimatedRows *int64
	if a.cfg.EstimateRowCounts {
		estimatedRows = a.estimateRowCount(ctx, table)
	}

	return &query.TableAvailability{
		Available:     true,
		QueryTable:    table.String(),
		Connection:    a.cfg.ConnectionName,
		EstimatedRows: estimatedRows,
	}, nil
}

// estimateRowCount runs SELECT COUNT(*) and returns the result, or nil on error.
func (a *Adapter) estimateRowCount(ctx context.Context, table *query.TableIdentifier) *int64 {
	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM %s.%s.%s",
		trinoclient.QuoteIdentifier(table.Catalog),
		trinoclient.QuoteIdentifier(table.Schema),
		trinoclient.QuoteIdentifier(table.Table),
	)

	result, err := a.client.Query(ctx, countSQL, trinoclient.QueryOptions{Limit: 1})
	if err != nil || len(result.Rows) == 0 {
		return nil
	}

	for _, v := range result.Rows[0] {
		if count, ok := v.(int64); ok {
			return &count
		}
		if count, ok := v.(float64); ok {
			c := int64(count)
			return &c
		}
	}
	return nil
}

// GetQueryExamples returns sample queries for a table.
func (a *Adapter) GetQueryExamples(ctx context.Context, urn string) ([]query.Example, error) {
	table, err := a.ResolveTable(ctx, urn)
	if err != nil {
		return nil, err
	}

	tableName := fmt.Sprintf("%s.%s.%s",
		trinoclient.QuoteIdentifier(table.Catalog),
		trinoclient.QuoteIdentifier(table.Schema),
		trinoclient.QuoteIdentifier(table.Table),
	)

	return []query.Example{
		{
			Description: "Preview first 10 rows",
			SQL:         fmt.Sprintf("SELECT * FROM %s LIMIT 10", tableName),
		},
		{
			Description: "Count all rows",
			SQL:         fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName),
		},
		{
			Description: "Sample 1%% of data",
			SQL:         fmt.Sprintf("SELECT * FROM %s TABLESAMPLE SYSTEM (1)", tableName),
		},
	}, nil
}

// GetExecutionContext returns context for querying multiple tables.
func (a *Adapter) GetExecutionContext(ctx context.Context, urns []string) (*query.ExecutionContext, error) {
	tables := make([]query.TableInfo, 0, len(urns))
	connections := make(map[string]bool)

	for _, urn := range urns {
		table, err := a.ResolveTable(ctx, urn)
		if err != nil {
			continue
		}

		// Check availability to get row estimates
		availability, _ := a.GetTableAvailability(ctx, urn)

		tables = append(tables, query.TableInfo{
			URN:           urn,
			QueryTable:    table.String(),
			Connection:    table.Connection,
			EstimatedRows: availability.EstimatedRows,
		})
		connections[table.Connection] = true
	}

	connList := make([]string, 0, len(connections))
	for conn := range connections {
		connList = append(connList, conn)
	}

	return &query.ExecutionContext{
		Tables:      tables,
		Connections: connList,
	}, nil
}

// GetTableSchema returns the schema of a table.
func (a *Adapter) GetTableSchema(ctx context.Context, table query.TableIdentifier) (*query.TableSchema, error) {
	catalog := table.Catalog
	if catalog == "" {
		catalog = a.cfg.Catalog
	}
	schema := table.Schema
	if schema == "" {
		schema = a.cfg.Schema
	}

	info, err := a.client.DescribeTable(ctx, catalog, schema, table.Table)
	if err != nil {
		return nil, fmt.Errorf("describing table: %w", err)
	}

	columns := make([]query.Column, len(info.Columns))
	for i, col := range info.Columns {
		columns[i] = query.Column{
			Name:     col.Name,
			Type:     col.Type,
			Nullable: col.Nullable != "NOT NULL",
			Comment:  col.Comment,
		}
	}

	return &query.TableSchema{
		Columns: columns,
	}, nil
}

// Close releases resources.
func (a *Adapter) Close() error {
	if a.client != nil {
		if err := a.client.Close(); err != nil {
			return fmt.Errorf("closing trino client: %w", err)
		}
	}
	return nil
}

// Ping tests the connection to Trino.
func (a *Adapter) Ping(ctx context.Context) error {
	if err := a.client.Ping(ctx); err != nil {
		return fmt.Errorf("pinging trino: %w", err)
	}
	return nil
}

// Verify interface compliance.
var _ query.Provider = (*Adapter)(nil)
