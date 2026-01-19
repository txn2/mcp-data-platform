// Package datahub provides a DataHub implementation of the semantic provider.
package datahub

import (
	"context"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// Config holds DataHub adapter configuration.
type Config struct {
	Endpoint string
	Token    string
	Platform string // Default platform for URN building (e.g., "trino")
}

// Adapter implements semantic.Provider using DataHub.
type Adapter struct {
	cfg Config
}

// New creates a new DataHub adapter.
func New(cfg Config) (*Adapter, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("datahub endpoint is required")
	}
	if cfg.Platform == "" {
		cfg.Platform = "trino"
	}
	return &Adapter{cfg: cfg}, nil
}

// Name returns the provider name.
func (a *Adapter) Name() string {
	return "datahub"
}

// GetTableContext retrieves table context from DataHub.
func (a *Adapter) GetTableContext(ctx context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error) {
	urn := a.buildDatasetURN(table)

	// In a real implementation, this would make GraphQL calls to DataHub.
	// For now, return a placeholder that shows the structure.
	return &semantic.TableContext{
		URN:         urn,
		Description: fmt.Sprintf("Table %s (metadata from DataHub)", table.String()),
	}, nil
}

// GetColumnContext retrieves column context from DataHub.
func (a *Adapter) GetColumnContext(ctx context.Context, column semantic.ColumnIdentifier) (*semantic.ColumnContext, error) {
	return &semantic.ColumnContext{
		Name:        column.Column,
		Description: fmt.Sprintf("Column %s (metadata from DataHub)", column.Column),
	}, nil
}

// GetColumnsContext retrieves all columns context from DataHub.
func (a *Adapter) GetColumnsContext(ctx context.Context, table semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error) {
	// In a real implementation, this would query DataHub schema fields.
	return make(map[string]*semantic.ColumnContext), nil
}

// GetLineage retrieves lineage from DataHub.
func (a *Adapter) GetLineage(ctx context.Context, table semantic.TableIdentifier, direction semantic.LineageDirection, maxDepth int) (*semantic.LineageInfo, error) {
	return &semantic.LineageInfo{
		Direction: direction,
		Entities:  []semantic.LineageEntity{},
		MaxDepth:  maxDepth,
	}, nil
}

// GetGlossaryTerm retrieves a glossary term from DataHub.
func (a *Adapter) GetGlossaryTerm(ctx context.Context, urn string) (*semantic.GlossaryTerm, error) {
	// In a real implementation, this would query DataHub.
	return nil, nil
}

// SearchTables searches for tables in DataHub.
func (a *Adapter) SearchTables(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	// In a real implementation, this would use DataHub's search API.
	return []semantic.TableSearchResult{}, nil
}

// Close releases resources.
func (a *Adapter) Close() error {
	return nil
}

// buildDatasetURN creates a DataHub URN for a table.
func (a *Adapter) buildDatasetURN(table semantic.TableIdentifier) string {
	// DataHub URN format: urn:li:dataset:(urn:li:dataPlatform:platform,database.schema.table,PROD)
	name := table.String()
	return fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:%s,%s,PROD)", a.cfg.Platform, name)
}

// ResolveURN converts a DataHub URN to a table identifier.
func (a *Adapter) ResolveURN(_ context.Context, urn string) (*semantic.TableIdentifier, error) {
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
		return &semantic.TableIdentifier{
			Schema: parts[0],
			Table:  parts[1],
		}, nil
	case 3:
		return &semantic.TableIdentifier{
			Catalog: parts[0],
			Schema:  parts[1],
			Table:   parts[2],
		}, nil
	default:
		return nil, fmt.Errorf("invalid table name in URN: %s", name)
	}
}

// BuildURN creates a URN from a table identifier.
func (a *Adapter) BuildURN(_ context.Context, table semantic.TableIdentifier) (string, error) {
	return a.buildDatasetURN(table), nil
}

// Verify interface compliance.
var (
	_ semantic.Provider    = (*Adapter)(nil)
	_ semantic.URNResolver = (*Adapter)(nil)
)
