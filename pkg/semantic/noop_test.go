package semantic

import (
	"context"
	"testing"
)

const (
	noopTestSchema       = "test"
	noopTestTable        = "table"
	noopTestLineageDepth = 3
)

func TestNoopProvider_Name(t *testing.T) {
	provider := NewNoopProvider()
	if got := provider.Name(); got != "noop" {
		t.Errorf("Name() = %q, want %q", got, "noop")
	}
}

func TestNoopProvider_GetTableContext(t *testing.T) {
	provider := NewNoopProvider()
	ctx := context.Background()
	table := TableIdentifier{Schema: noopTestSchema, Table: noopTestTable}
	result, err := provider.GetTableContext(ctx, table)
	if err != nil {
		t.Errorf("GetTableContext() error = %v", err)
	}
	if result == nil {
		t.Error("GetTableContext() returned nil")
	}
}

func TestNoopProvider_GetColumnContext(t *testing.T) {
	provider := NewNoopProvider()
	ctx := context.Background()
	column := ColumnIdentifier{
		TableIdentifier: TableIdentifier{Schema: noopTestSchema, Table: noopTestTable},
		Column:          "col",
	}
	result, err := provider.GetColumnContext(ctx, column)
	if err != nil {
		t.Errorf("GetColumnContext() error = %v", err)
	}
	if result == nil {
		t.Error("GetColumnContext() returned nil")
	}
}

func TestNoopProvider_GetColumnsContext(t *testing.T) {
	provider := NewNoopProvider()
	ctx := context.Background()
	table := TableIdentifier{Schema: noopTestSchema, Table: noopTestTable}
	result, err := provider.GetColumnsContext(ctx, table)
	if err != nil {
		t.Errorf("GetColumnsContext() error = %v", err)
	}
	if result == nil {
		t.Error("GetColumnsContext() returned nil")
	}
}

func TestNoopProvider_GetLineage(t *testing.T) {
	provider := NewNoopProvider()
	ctx := context.Background()
	table := TableIdentifier{Schema: noopTestSchema, Table: noopTestTable}
	result, err := provider.GetLineage(ctx, table, LineageUpstream, noopTestLineageDepth)
	if err != nil {
		t.Errorf("GetLineage() error = %v", err)
	}
	if result == nil {
		t.Fatal("GetLineage() returned nil")
	}
	if result.Direction != LineageUpstream {
		t.Errorf("GetLineage() Direction = %v, want %v", result.Direction, LineageUpstream)
	}
}

func TestNoopProvider_GetGlossaryTerm(t *testing.T) {
	provider := NewNoopProvider()
	ctx := context.Background()
	result, err := provider.GetGlossaryTerm(ctx, "urn:li:glossaryTerm:test")
	if err != nil {
		t.Errorf("GetGlossaryTerm() error = %v", err)
	}
	if result == nil {
		t.Error("GetGlossaryTerm() returned nil, expected empty term")
	}
}

func TestNoopProvider_SearchTables(t *testing.T) {
	provider := NewNoopProvider()
	ctx := context.Background()
	filter := SearchFilter{Query: "test"}
	result, err := provider.SearchTables(ctx, filter)
	if err != nil {
		t.Errorf("SearchTables() error = %v", err)
	}
	if len(result) != 0 {
		t.Errorf("SearchTables() returned %d results, want 0", len(result))
	}
}

func TestNoopProvider_Close(t *testing.T) {
	provider := NewNoopProvider()
	if err := provider.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}
