// Package trino provides a Trino implementation of the query provider.
package trino

import (
	"context"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/query"
)

// Config holds Trino adapter configuration.
type Config struct {
	Host           string
	Port           int
	User           string
	Password       string
	Catalog        string
	Schema         string
	SSL            bool
	DefaultLimit   int
	MaxLimit       int
	ReadOnly       bool
	ConnectionName string
}

// Adapter implements query.Provider using Trino.
type Adapter struct {
	cfg Config
}

// New creates a new Trino adapter.
func New(cfg Config) (*Adapter, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("trino host is required")
	}
	if cfg.Port == 0 {
		cfg.Port = 8080
	}
	if cfg.DefaultLimit == 0 {
		cfg.DefaultLimit = 1000
	}
	if cfg.MaxLimit == 0 {
		cfg.MaxLimit = 10000
	}
	return &Adapter{cfg: cfg}, nil
}

// Name returns the provider name.
func (a *Adapter) Name() string {
	return "trino"
}

// ResolveTable converts a URN to a table identifier.
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
	case 3:
		return &query.TableIdentifier{
			Catalog:    parts[0],
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
		return &query.TableAvailability{
			Available: false,
			Error:     err.Error(),
		}, nil
	}

	// In a real implementation, this would connect to Trino and verify the table exists.
	return &query.TableAvailability{
		Available:  true,
		QueryTable: table.String(),
		Connection: a.cfg.ConnectionName,
	}, nil
}

// GetQueryExamples returns sample queries for a table.
func (a *Adapter) GetQueryExamples(ctx context.Context, urn string) ([]query.QueryExample, error) {
	table, err := a.ResolveTable(ctx, urn)
	if err != nil {
		return nil, err
	}

	tableName := table.String()
	return []query.QueryExample{
		{
			Description: "Select first 10 rows",
			SQL:         fmt.Sprintf("SELECT * FROM %s LIMIT 10", tableName),
		},
		{
			Description: "Count all rows",
			SQL:         fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName),
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

		tables = append(tables, query.TableInfo{
			URN:        urn,
			QueryTable: table.String(),
			Connection: table.Connection,
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
func (a *Adapter) GetTableSchema(_ context.Context, table query.TableIdentifier) (*query.TableSchema, error) {
	// In a real implementation, this would execute DESCRIBE against Trino.
	return &query.TableSchema{
		Columns: []query.Column{},
	}, nil
}

// Close releases resources.
func (a *Adapter) Close() error {
	return nil
}

// Verify interface compliance.
var _ query.Provider = (*Adapter)(nil)
