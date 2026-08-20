package connsource

import "testing"

func TestPlatformFromURN(t *testing.T) {
	tests := []struct {
		name string
		urn  string
		want string
	}{
		{"standard dataset URN", "urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.table,PROD)", "trino"},
		{"postgres platform", "urn:li:dataset:(urn:li:dataPlatform:postgres,db.schema.table,PROD)", "postgres"},
		{"platform only", "urn:li:dataPlatform:s3", "s3"},
		{"no platform prefix", "urn:li:dataset:(urn:li:dataFlow:airflow,flow1,PROD)", ""},
		{"empty string", "", ""},
		{"platform with closing paren", "urn:li:dataset:(urn:li:dataPlatform:hive)", "hive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PlatformFromURN(tt.urn); got != tt.want {
				t.Errorf("PlatformFromURN(%q) = %q, want %q", tt.urn, got, tt.want)
			}
		})
	}
}

func TestMap_AddForConnectionRemove(t *testing.T) {
	m := NewMap()
	m.Add(Source{Kind: "trino", Name: "prod", DataHubSourceName: "trino", CatalogMapping: map[string]string{"raw": "warehouse"}})

	if s := m.ForConnection("trino", "prod"); s == nil || s.DataHubSourceName != "trino" {
		t.Fatalf("ForConnection = %+v", s)
	}
	if s := m.ForConnection("trino", "prod"); s == nil || s.CatalogMapping["raw"] != "warehouse" {
		t.Fatalf("ForConnection catalog mapping = %+v", s)
	}
	if got := m.DataHubSourceName("trino", "prod"); got != "trino" {
		t.Errorf("DataHubSourceName = %q", got)
	}
	if got := m.DataHubSourceName("trino", "unknown"); got != "" {
		t.Errorf("DataHubSourceName(unknown) = %q, want empty", got)
	}
	if conns := m.ConnectionsForSource("trino"); len(conns) != 1 {
		t.Errorf("ConnectionsForSource = %d", len(conns))
	}
	if conns := m.ConnectionsForURN("urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)"); len(conns) != 1 {
		t.Errorf("ConnectionsForURN = %d", len(conns))
	}

	m.Remove("trino", "prod")
	if s := m.ForConnection("trino", "prod"); s != nil {
		t.Errorf("expected removal, got %+v", s)
	}
}

func TestMap_Nil(t *testing.T) {
	var m *Map
	if m.ForConnection("k", "n") != nil ||
		m.ConnectionsForSource("s") != nil || m.ConnectionsForURN("urn") != nil {
		t.Error("nil map lookups should return nil")
	}
}

// TestMap_SharedNameResolvesByKind proves the fix for #1384: three connections
// carrying one name, one per kind, each resolve to their own source, and the
// answer is the same on every call. Before the fix the only lookup that took a
// bare name ranged the backing map, so a repeated call returned a different
// kind; a single-call assertion passed roughly one time in three by luck.
func TestMap_SharedNameResolvesByKind(t *testing.T) {
	m := NewMap()
	for _, kind := range []string{"trino", "datahub", "s3"} {
		m.Add(Source{Kind: kind, Name: "acme", DataHubSourceName: kind})
	}

	for range 20 {
		for _, kind := range []string{"trino", "datahub", "s3"} {
			src := m.ForConnection(kind, "acme")
			if src == nil {
				t.Fatalf("ForConnection(%q, acme) = nil", kind)
			}
			if src.DataHubSourceName != kind {
				t.Fatalf("ForConnection(%q, acme).DataHubSourceName = %q, want %q",
					kind, src.DataHubSourceName, kind)
			}
		}
	}
}
