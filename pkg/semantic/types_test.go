package semantic

import "testing"

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
		{
			name:  "table only",
			table: TableIdentifier{Table: "orders"},
			want:  ".orders",
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
