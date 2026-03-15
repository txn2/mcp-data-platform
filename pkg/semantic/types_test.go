package semantic

import (
	"encoding/json"
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

func TestStructuredProperty_JSON(t *testing.T) {
	sp := StructuredProperty{
		QualifiedName: "io.acryl.privacy.retentionTime",
		DisplayName:   "Retention Time",
		Values:        []any{float64(90)},
	}
	data, err := json.Marshal(sp)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var got StructuredProperty
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if got.QualifiedName != sp.QualifiedName {
		t.Errorf("QualifiedName = %q, want %q", got.QualifiedName, sp.QualifiedName)
	}
	if got.DisplayName != sp.DisplayName {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, sp.DisplayName)
	}
	if len(got.Values) != 1 {
		t.Errorf("Values len = %d, want 1", len(got.Values))
	}
}

func TestIncident_JSON(t *testing.T) {
	inc := Incident{
		URN:   "urn:li:incident:abc",
		Type:  "OPERATIONAL",
		Title: "Pipeline down",
		State: "ACTIVE",
	}
	data, err := json.Marshal(inc)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var got Incident
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if got.URN != inc.URN || got.Type != inc.Type || got.Title != inc.Title || got.State != inc.State {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", got, inc)
	}
}

func TestDataContractStatus_JSON(t *testing.T) {
	dc := DataContractStatus{
		Status: "FAILING",
		AssertionResults: []AssertionResult{
			{Type: "FRESHNESS", ResultType: "FAILURE"},
			{Type: "SCHEMA", ResultType: "SUCCESS"},
		},
	}
	data, err := json.Marshal(dc)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var got DataContractStatus
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if got.Status != dc.Status {
		t.Errorf("Status = %q, want %q", got.Status, dc.Status)
	}
	if len(got.AssertionResults) != 2 {
		t.Fatalf("AssertionResults len = %d, want 2", len(got.AssertionResults))
	}
	if got.AssertionResults[0].Type != "FRESHNESS" || got.AssertionResults[0].ResultType != "FAILURE" {
		t.Errorf("AssertionResults[0] mismatch: %+v", got.AssertionResults[0])
	}
}

func TestTableContext_V14FieldsOmitEmpty(t *testing.T) {
	// V1.3.x compat: empty v1.4 fields should be omitted from JSON
	tc := TableContext{Description: "test"}
	data, err := json.Marshal(tc)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	s := string(data)
	for _, field := range []string{"structured_properties", "active_incidents", "incidents", "data_contract"} {
		if contains(s, field) {
			t.Errorf("expected %q to be omitted from JSON, got: %s", field, s)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && jsonContains(s, sub)
}

func jsonContains(s, key string) bool {
	return s != "" && key != "" && stringContains(s, `"`+key+`"`)
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
