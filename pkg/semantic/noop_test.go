package semantic

import (
	"context"
	"testing"
)

func TestNoopProvider(t *testing.T) {
	provider := NewNoopProvider()

	t.Run("Name", func(t *testing.T) {
		if got := provider.Name(); got != "noop" {
			t.Errorf("Name() = %q, want %q", got, "noop")
		}
	})

	t.Run("GetTableContext", func(t *testing.T) {
		ctx := context.Background()
		table := TableIdentifier{Schema: "test", Table: "table"}
		result, err := provider.GetTableContext(ctx, table)
		if err != nil {
			t.Errorf("GetTableContext() error = %v", err)
		}
		if result == nil {
			t.Error("GetTableContext() returned nil")
		}
	})

	t.Run("GetColumnContext", func(t *testing.T) {
		ctx := context.Background()
		column := ColumnIdentifier{
			TableIdentifier: TableIdentifier{Schema: "test", Table: "table"},
			Column:          "col",
		}
		result, err := provider.GetColumnContext(ctx, column)
		if err != nil {
			t.Errorf("GetColumnContext() error = %v", err)
		}
		if result == nil {
			t.Error("GetColumnContext() returned nil")
		}
	})

	t.Run("GetColumnsContext", func(t *testing.T) {
		ctx := context.Background()
		table := TableIdentifier{Schema: "test", Table: "table"}
		result, err := provider.GetColumnsContext(ctx, table)
		if err != nil {
			t.Errorf("GetColumnsContext() error = %v", err)
		}
		if result == nil {
			t.Error("GetColumnsContext() returned nil")
		}
	})

	t.Run("GetLineage", func(t *testing.T) {
		ctx := context.Background()
		table := TableIdentifier{Schema: "test", Table: "table"}
		result, err := provider.GetLineage(ctx, table, LineageUpstream, 3)
		if err != nil {
			t.Errorf("GetLineage() error = %v", err)
		}
		if result == nil {
			t.Error("GetLineage() returned nil")
		}
		if result.Direction != LineageUpstream {
			t.Errorf("GetLineage() Direction = %v, want %v", result.Direction, LineageUpstream)
		}
	})

	t.Run("GetGlossaryTerm", func(t *testing.T) {
		ctx := context.Background()
		result, err := provider.GetGlossaryTerm(ctx, "urn:li:glossaryTerm:test")
		if err != nil {
			t.Errorf("GetGlossaryTerm() error = %v", err)
		}
		if result != nil {
			t.Error("GetGlossaryTerm() expected nil result for noop")
		}
	})

	t.Run("SearchTables", func(t *testing.T) {
		ctx := context.Background()
		filter := SearchFilter{Query: "test"}
		result, err := provider.SearchTables(ctx, filter)
		if err != nil {
			t.Errorf("SearchTables() error = %v", err)
		}
		if len(result) != 0 {
			t.Errorf("SearchTables() returned %d results, want 0", len(result))
		}
	})

	t.Run("Close", func(t *testing.T) {
		if err := provider.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
}
