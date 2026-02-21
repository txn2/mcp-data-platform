package semantic

import (
	"testing"
)

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

func TestColumnContext_HasContent(t *testing.T) {
	tests := []struct {
		name string
		col  ColumnContext
		want bool
	}{
		{
			name: "empty column",
			col:  ColumnContext{Name: "id"},
			want: false,
		},
		{
			name: "with description",
			col:  ColumnContext{Name: "id", Description: "Primary key"},
			want: true,
		},
		{
			name: "with tags",
			col:  ColumnContext{Name: "id", Tags: []string{"important"}},
			want: true,
		},
		{
			name: "with glossary terms",
			col:  ColumnContext{Name: "id", GlossaryTerms: []GlossaryTerm{{URN: "urn:term", Name: "ID"}}},
			want: true,
		},
		{
			name: "with is_pii",
			col:  ColumnContext{Name: "ssn", IsPII: true},
			want: true,
		},
		{
			name: "with is_sensitive",
			col:  ColumnContext{Name: "salary", IsSensitive: true},
			want: true,
		},
		{
			name: "with business name",
			col:  ColumnContext{Name: "loc_id", BusinessName: "Location ID"},
			want: true,
		},
		{
			name: "with inherited from",
			col: ColumnContext{
				Name: "user_id",
				InheritedFrom: &InheritedMetadata{
					SourceURN:    "urn:li:dataset:upstream",
					SourceColumn: "id",
					Hops:         1,
					MatchMethod:  "name_exact",
				},
			},
			want: true,
		},
		{
			name: "name only is not content",
			col:  ColumnContext{Name: "some_column"},
			want: false,
		},
		{
			name: "empty tags slice is not content",
			col:  ColumnContext{Name: "id", Tags: []string{}},
			want: false,
		},
		{
			name: "empty glossary terms slice is not content",
			col:  ColumnContext{Name: "id", GlossaryTerms: []GlossaryTerm{}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.col.HasContent()
			if got != tt.want {
				t.Errorf("ColumnContext.HasContent() = %v, want %v", got, tt.want)
			}
		})
	}
}
