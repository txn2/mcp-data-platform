package urnbuild

import "testing"

func TestDatasetURN(t *testing.T) {
	tests := []struct {
		name           string
		platform       string
		catalogMapping map[string]string
		catalog        string
		schema         string
		table          string
		want           string
	}{
		{
			name:    "default platform",
			catalog: "rdbms", schema: "public", table: "users",
			want: "urn:li:dataset:(urn:li:dataPlatform:trino,rdbms.public.users,PROD)",
		},
		{
			name:     "custom platform",
			platform: "postgres",
			catalog:  "rdbms", schema: "public", table: "users",
			want: "urn:li:dataset:(urn:li:dataPlatform:postgres,rdbms.public.users,PROD)",
		},
		{
			name:           "catalog mapping applied",
			platform:       "postgres",
			catalogMapping: map[string]string{"rdbms": "warehouse"},
			catalog:        "rdbms", schema: "public", table: "users",
			want: "urn:li:dataset:(urn:li:dataPlatform:postgres,warehouse.public.users,PROD)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DatasetURN(tt.platform, tt.catalogMapping, tt.catalog, tt.schema, tt.table)
			if got != tt.want {
				t.Errorf("DatasetURN() = %q, want %q", got, tt.want)
			}
		})
	}
}
