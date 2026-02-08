package query

import (
	"context"
	"testing"
)

func TestNoopProvider_Name(t *testing.T) {
	provider := NewNoopProvider()
	if got := provider.Name(); got != "noop" {
		t.Errorf("Name() = %q, want %q", got, "noop")
	}
}

func TestNoopProvider_ResolveTable(t *testing.T) {
	provider := NewNoopProvider()
	ctx := context.Background()
	result, err := provider.ResolveTable(ctx, "urn:li:dataset:test")
	if err != nil {
		t.Errorf("ResolveTable() error = %v", err)
	}
	if result == nil {
		t.Error("ResolveTable() returned nil, expected empty identifier")
	}
}

func TestNoopProvider_GetTableAvailability(t *testing.T) {
	provider := NewNoopProvider()
	ctx := context.Background()
	result, err := provider.GetTableAvailability(ctx, "urn:li:dataset:test")
	if err != nil {
		t.Errorf("GetTableAvailability() error = %v", err)
	}
	if result.Available {
		t.Error("GetTableAvailability() expected unavailable for noop")
	}
}

func TestNoopProvider_GetQueryExamples(t *testing.T) {
	provider := NewNoopProvider()
	ctx := context.Background()
	result, err := provider.GetQueryExamples(ctx, "urn:li:dataset:test")
	if err != nil {
		t.Errorf("GetQueryExamples() error = %v", err)
	}
	if len(result) != 0 {
		t.Errorf("GetQueryExamples() returned %d examples, want 0", len(result))
	}
}

func TestNoopProvider_GetExecutionContext(t *testing.T) {
	provider := NewNoopProvider()
	ctx := context.Background()
	result, err := provider.GetExecutionContext(ctx, []string{"urn1", "urn2"})
	if err != nil {
		t.Errorf("GetExecutionContext() error = %v", err)
	}
	if result == nil {
		t.Error("GetExecutionContext() returned nil")
	}
}

func TestNoopProvider_GetTableSchema(t *testing.T) {
	provider := NewNoopProvider()
	ctx := context.Background()
	table := TableIdentifier{Schema: "test", Table: "table"}
	result, err := provider.GetTableSchema(ctx, table)
	if err != nil {
		t.Errorf("GetTableSchema() error = %v", err)
	}
	if result == nil {
		t.Error("GetTableSchema() returned nil")
	}
}

func TestNoopProvider_Close(t *testing.T) {
	provider := NewNoopProvider()
	if err := provider.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
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
