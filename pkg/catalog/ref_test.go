package catalog

import (
	"errors"
	"reflect"
	"testing"
)

func TestRefRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		ref  Ref
		want string
	}{
		{
			name: "trino dataset",
			ref:  DatasetRef("trino", "warehouse", "hive", "sales", "orders"),
			want: "mcp:dataset:(trino,warehouse,hive.sales.orders)",
		},
		{
			name: "api operation",
			ref:  DatasetRef("api", "stripe", "listCharges"),
			want: "mcp:dataset:(api,stripe,listCharges)",
		},
		{
			name: "graphql operation",
			ref:  DatasetRef("graphql", "github", "Query", "repository", "issues"),
			want: "mcp:dataset:(graphql,github,Query.repository.issues)",
		},
		{
			name: "column",
			ref:  ColumnRef(DatasetRef("trino", "warehouse", "hive", "sales", "orders"), "amount"),
			want: "mcp:column:(trino,warehouse,hive.sales.orders,amount)",
		},
		{
			name: "segment with a dot is quoted",
			ref:  DatasetRef("trino", "warehouse", "hive", "sales", "orders.v2"),
			want: `mcp:dataset:(trino,warehouse,hive.sales."orders.v2")`,
		},
		{
			name: "quote inside a quoted segment is doubled",
			ref:  DatasetRef("trino", "warehouse", "hive", "sales", `a"b,c`),
			want: `mcp:dataset:(trino,warehouse,hive.sales."a""b,c")`,
		},
		{
			name: "connection with parentheses is quoted",
			ref:  DatasetRef("api", "crm (eu)", "listContacts"),
			want: `mcp:dataset:(api,"crm (eu)",listContacts)`,
		},
		{
			name: "column with a comma is quoted",
			ref:  ColumnRef(DatasetRef("trino", "w", "c", "s", "t"), "a,b"),
			want: `mcp:column:(trino,w,c.s.t,"a,b")`,
		},
		{
			name: "surrounding whitespace is quoted",
			ref:  DatasetRef("trino", "w", "c", "s", " padded "),
			want: `mcp:dataset:(trino,w,c.s." padded ")`,
		},
		{name: "glossary term", ref: KeyRef(KindGlossaryTerm, "7d0c"), want: "mcp:glossary_term:7d0c"},
		{name: "tag", ref: KeyRef(KindTag, "pii"), want: "mcp:tag:pii"},
		{name: "tag with a colon", ref: KeyRef(KindTag, "tier:gold"), want: "mcp:tag:tier:gold"},
		{name: "data product", ref: KeyRef(KindDataProduct, "x"), want: "mcp:data_product:x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ref.String()
			if got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
			parsed, err := ParseRef(got)
			if err != nil {
				t.Fatalf("ParseRef(%q): %v", got, err)
			}
			if !reflect.DeepEqual(parsed, tt.ref) {
				t.Fatalf("ParseRef(%q) = %#v, want %#v", got, parsed, tt.ref)
			}
		})
	}
}

func TestParseRefRefusesMalformed(t *testing.T) {
	for _, s := range []string{
		"",
		"urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)",
		"mcp:asset:123",
		"mcp:dataset",
		"mcp:dataset:trino,w,a.b",
		"mcp:dataset:(trino,w,a.b",
		"mcp:dataset:(trino,w)",
		"mcp:dataset:(trino,w,a.b,extra)",
		"mcp:dataset:(,w,a.b)",
		"mcp:dataset:(trino,,a.b)",
		"mcp:dataset:(trino,w,a..b)",
		`mcp:dataset:(trino,w,"a.b)`,
		`mcp:dataset:(trino,w,"a"b")`,
		`mcp:dataset:(trino,w,a"b)`,
		`mcp:dataset:(trino,w,")`,
		"mcp:column:(trino,w,a.b)",
		"mcp:column:(trino,w,a.b,)",
		`mcp:dataset:(trino,w,"a"x)`,
		`mcp:dataset:(trino,w,"a"b"")`,
		`mcp:dataset:(trino,w,x.a"b")`,
		`mcp:dataset:(trino,"w"x,a)`,
		`mcp:dataset:("k"x,w,a)`,
		`mcp:column:(trino,w,a,"c"x)`,
		"mcp:glossary_term:",
		"mcp:tag:   ",
	} {
		if _, err := ParseRef(s); !errors.Is(err, ErrInvalidRef) {
			t.Errorf("ParseRef(%q) error = %v, want ErrInvalidRef", s, err)
		}
	}
}

func TestParseRefTrimsSurroundingSpace(t *testing.T) {
	r, err := ParseRef("  mcp:dataset:(trino,w,a.b.c)\n")
	if err != nil {
		t.Fatal(err)
	}
	if r.String() != "mcp:dataset:(trino,w,a.b.c)" {
		t.Fatalf("got %s", r)
	}
}

func TestColumnRefDataset(t *testing.T) {
	d := DatasetRef("trino", "w", "a", "b", "c")
	if got := ColumnRef(d, "x").Dataset(); !reflect.DeepEqual(got, d) {
		t.Fatalf("Dataset() = %#v, want %#v", got, d)
	}
}

func TestValidateUnknownKind(t *testing.T) {
	if err := (Ref{Kind: "bogus", Key: "x"}).Validate(); !errors.Is(err, ErrInvalidRef) {
		t.Fatalf("got %v", err)
	}
}

func TestSlug(t *testing.T) {
	tests := map[string]string{
		"Revenue":                 "revenue",
		"Net Revenue (USD)":       "net-revenue-usd",
		"  leading and trailing ": "leading-and-trailing",
		"Ümlaut Straße":           "ümlaut-straße",
		"---":                     "",
	}
	for in, want := range tests {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDatasetTypeValid(t *testing.T) {
	for _, typ := range []DatasetType{
		DatasetTable, DatasetView, DatasetRegisteredTable, DatasetWebhookTable,
		DatasetAPIOperation, DatasetGraphQLOperation,
	} {
		if !typ.Valid() {
			t.Errorf("%s should be valid", typ)
		}
	}
	if DatasetType("dashboard").Valid() {
		t.Error("dashboard should not be valid")
	}
}
