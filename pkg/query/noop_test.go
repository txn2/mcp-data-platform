package query

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

	t.Run("ResolveTable", func(t *testing.T) {
		ctx := context.Background()
		result, err := provider.ResolveTable(ctx, "urn:li:dataset:test")
		if err != nil {
			t.Errorf("ResolveTable() error = %v", err)
		}
		if result != nil {
			t.Error("ResolveTable() expected nil for noop")
		}
	})

	t.Run("GetTableAvailability", func(t *testing.T) {
		ctx := context.Background()
		result, err := provider.GetTableAvailability(ctx, "urn:li:dataset:test")
		if err != nil {
			t.Errorf("GetTableAvailability() error = %v", err)
		}
		if result.Available {
			t.Error("GetTableAvailability() expected unavailable for noop")
		}
	})

	t.Run("GetQueryExamples", func(t *testing.T) {
		ctx := context.Background()
		result, err := provider.GetQueryExamples(ctx, "urn:li:dataset:test")
		if err != nil {
			t.Errorf("GetQueryExamples() error = %v", err)
		}
		if len(result) != 0 {
			t.Errorf("GetQueryExamples() returned %d examples, want 0", len(result))
		}
	})

	t.Run("GetExecutionContext", func(t *testing.T) {
		ctx := context.Background()
		result, err := provider.GetExecutionContext(ctx, []string{"urn1", "urn2"})
		if err != nil {
			t.Errorf("GetExecutionContext() error = %v", err)
		}
		if result == nil {
			t.Error("GetExecutionContext() returned nil")
		}
	})

	t.Run("GetTableSchema", func(t *testing.T) {
		ctx := context.Background()
		table := TableIdentifier{Schema: "test", Table: "table"}
		result, err := provider.GetTableSchema(ctx, table)
		if err != nil {
			t.Errorf("GetTableSchema() error = %v", err)
		}
		if result == nil {
			t.Error("GetTableSchema() returned nil")
		}
	})

	t.Run("Close", func(t *testing.T) {
		if err := provider.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
}

func TestTableIdentifier_String(t *testing.T) {
	tests := []struct {
		name  string
		table TableIdentifier
		want  string
	}{
		{
			name:  "with catalog",
			table: TableIdentifier{Catalog: "iceberg", Schema: "sales", Table: "orders"},
			want:  "iceberg.sales.orders",
		},
		{
			name:  "without catalog",
			table: TableIdentifier{Schema: "sales", Table: "orders"},
			want:  "sales.orders",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.table.String()
			if got != tt.want {
				t.Errorf("TableIdentifier.String() = %q, want %q", got, tt.want)
			}
		})
	}
}
