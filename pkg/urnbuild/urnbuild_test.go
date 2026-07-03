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

func TestDatasetURNFromName(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		dsName   string
		want     string
	}{
		{
			name:   "default platform",
			dsName: "rdbms.public.users",
			want:   "urn:li:dataset:(urn:li:dataPlatform:trino,rdbms.public.users,PROD)",
		},
		{
			name:     "s3 object path name",
			platform: "s3",
			dsName:   "landing/raw/events",
			want:     "urn:li:dataset:(urn:li:dataPlatform:s3,landing/raw/events,PROD)",
		},
		{
			name:     "partial name without catalog",
			platform: "postgres",
			dsName:   "public.users",
			want:     "urn:li:dataset:(urn:li:dataPlatform:postgres,public.users,PROD)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DatasetURNFromName(tt.platform, tt.dsName)
			if got != tt.want {
				t.Errorf("DatasetURNFromName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseDatasetURN(t *testing.T) {
	tests := []struct {
		name    string
		urn     string
		want    ParsedDataset
		wantErr bool
	}{
		{
			name: "standard table URN",
			urn:  "urn:li:dataset:(urn:li:dataPlatform:trino,rdbms.public.users,PROD)",
			want: ParsedDataset{Platform: "trino", Name: "rdbms.public.users", Env: "PROD"},
		},
		{
			name: "non-PROD environment",
			urn:  "urn:li:dataset:(urn:li:dataPlatform:trino,rdbms.public.users,DEV)",
			want: ParsedDataset{Platform: "trino", Name: "rdbms.public.users", Env: "DEV"},
		},
		{
			name: "comma in dataset name survives",
			urn:  "urn:li:dataset:(urn:li:dataPlatform:s3,landing/exports/report,v2.csv,PROD)",
			want: ParsedDataset{Platform: "s3", Name: "landing/exports/report,v2.csv", Env: "PROD"},
		},
		{
			name: "s3 bucket-prefix name",
			urn:  "urn:li:dataset:(urn:li:dataPlatform:s3,my-bucket/raw/events,PROD)",
			want: ParsedDataset{Platform: "s3", Name: "my-bucket/raw/events", Env: "PROD"},
		},
		{
			name: "parens in dataset name survive",
			urn:  "urn:li:dataset:(urn:li:dataPlatform:trino,rdbms.public.orders(archive),PROD)",
			want: ParsedDataset{Platform: "trino", Name: "rdbms.public.orders(archive)", Env: "PROD"},
		},
		{
			name:    "not a dataset URN",
			urn:     "urn:li:glossaryTerm:revenue",
			wantErr: true,
		},
		{
			name:    "missing dataPlatform prefix",
			urn:     "urn:li:dataset:(trino,rdbms.public.users,PROD)",
			wantErr: true,
		},
		{
			name:    "missing closing paren",
			urn:     "urn:li:dataset:(urn:li:dataPlatform:trino,rdbms.public.users,PROD",
			wantErr: true,
		},
		{
			name:    "missing env segment",
			urn:     "urn:li:dataset:(urn:li:dataPlatform:trino,rdbms.public.users)",
			wantErr: true,
		},
		{
			name:    "empty body",
			urn:     "urn:li:dataset:(urn:li:dataPlatform:)",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDatasetURN(tt.urn)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseDatasetURN(%q) expected error, got %+v", tt.urn, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDatasetURN(%q) error: %v", tt.urn, err)
			}
			if *got != tt.want {
				t.Errorf("ParseDatasetURN(%q) = %+v, want %+v", tt.urn, *got, tt.want)
			}
		})
	}
}

func TestDatasetURNRoundTrip(t *testing.T) {
	urn := DatasetURN("trino", nil, "rdbms", "public", "users")
	parsed, err := ParseDatasetURN(urn)
	if err != nil {
		t.Fatalf("round trip parse error: %v", err)
	}
	if parsed.Platform != "trino" || parsed.Name != "rdbms.public.users" || parsed.Env != "PROD" {
		t.Errorf("round trip mismatch: %+v", parsed)
	}
}
