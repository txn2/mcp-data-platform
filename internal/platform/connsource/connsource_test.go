package connsource

import (
	"strconv"
	"sync"
	"testing"
)

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

func TestOverlay(t *testing.T) {
	t.Run("a stored row that states nothing leaves the configured mapping standing", func(t *testing.T) {
		m := NewMap()
		m.Add(Source{
			Kind: "trino", Name: "warehouse", DataHubSourceName: "hive",
			CatalogMapping: map[string]string{"rdbms": "postgres"}, Description: "from file",
		})

		m.Overlay(Source{Kind: "trino", Name: "warehouse"})

		got := m.ForConnection("trino", "warehouse")
		if got == nil || got.DataHubSourceName != "hive" {
			t.Fatalf("DataHubSourceName = %+v, want hive", got)
		}
		if got.CatalogMapping["rdbms"] != "postgres" {
			t.Errorf("CatalogMapping = %v", got.CatalogMapping)
		}
		if got.Description != "from file" {
			t.Errorf("Description = %q", got.Description)
		}
	})

	t.Run("a stored row that states a mapping overrides field by field", func(t *testing.T) {
		m := NewMap()
		m.Add(Source{
			Kind: "trino", Name: "warehouse", DataHubSourceName: "hive",
			CatalogMapping: map[string]string{"rdbms": "postgres"}, Description: "from file",
		})

		m.Overlay(Source{
			Kind: "trino", Name: "warehouse", DataHubSourceName: "postgres",
			Description: "from admin",
		})

		got := m.ForConnection("trino", "warehouse")
		if got.DataHubSourceName != "postgres" || got.Description != "from admin" {
			t.Errorf("stated fields not applied: %+v", got)
		}
		if got.CatalogMapping["rdbms"] != "postgres" {
			t.Errorf("unstated CatalogMapping should survive: %v", got.CatalogMapping)
		}
	})

	t.Run("with no entry to overlay the kind default answers", func(t *testing.T) {
		m := NewMap()
		m.Overlay(Source{Kind: "s3", Name: "lake"})
		if got := m.ForConnection("s3", "lake"); got == nil || got.DataHubSourceName != "s3" {
			t.Errorf("ForConnection = %+v, want the s3 default", got)
		}

		m.Overlay(Source{Kind: "mystery", Name: "x"})
		if got := m.ForConnection("mystery", "x"); got == nil || got.DataHubSourceName != "" {
			t.Errorf("an unmapped kind names no platform: %+v", got)
		}
	})
}

func TestDefaultSourceNameFn(t *testing.T) {
	for kind, want := range map[string]string{"trino": "trino", "s3": "s3", "datahub": "datahub", "mcp": "", "": ""} {
		if got := DefaultSourceName(kind); got != want {
			t.Errorf("DefaultSourceName(%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestRegistryEntries(t *testing.T) {
	t.Run("trino carries the deployment urn_mapping across every connection", func(t *testing.T) {
		mapping := map[string]string{"rdbms": "postgres"}
		got := RegistryEntries("trino", []string{"warehouse", "staging"}, "hive", mapping)
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
		for _, src := range got {
			if src.DataHubSourceName != "hive" || src.CatalogMapping["rdbms"] != "postgres" {
				t.Errorf("entry = %+v", src)
			}
		}
		if got[0].Name != "warehouse" || got[1].Name != "staging" {
			t.Errorf("names = %q %q", got[0].Name, got[1].Name)
		}
	})

	t.Run("trino with no urn_mapping falls back to its own platform name", func(t *testing.T) {
		got := RegistryEntries("trino", []string{"warehouse"}, "", nil)
		if len(got) != 1 || got[0].DataHubSourceName != "trino" {
			t.Errorf("entries = %+v", got)
		}
	})

	t.Run("s3 and datahub name themselves and ignore the query mapping", func(t *testing.T) {
		for _, kind := range []string{"s3", "datahub"} {
			got := RegistryEntries(kind, []string{"one"}, "hive", map[string]string{"a": "b"})
			if len(got) != 1 || got[0].DataHubSourceName != kind || got[0].CatalogMapping != nil {
				t.Errorf("%s entries = %+v", kind, got)
			}
		}
	})

	t.Run("a kind with no DataHub platform contributes nothing", func(t *testing.T) {
		if got := RegistryEntries("mcp", []string{"vendor"}, "hive", nil); got != nil {
			t.Errorf("entries = %+v, want nil", got)
		}
	})
}

// TestMapConcurrentAccess is the regression for the shared map's locking: one
// map serves tool-call goroutines reading on every enrichment and URN build
// while the admin connection routes add, overlay and remove from HTTP
// goroutines. Concurrent read and write of a Go map is a fatal runtime error,
// so this must hold under -race and in a plain run.
func TestMapConcurrentAccess(t *testing.T) {
	m := NewMap()
	m.Seed(RegistryEntries("trino", []string{"warehouse"}, "hive", map[string]string{"rdbms": "postgres"}))

	const iterations = 200
	var wg sync.WaitGroup
	wg.Add(4)

	go func() {
		defer wg.Done()
		for i := range iterations {
			m.Overlay(Source{Kind: "trino", Name: "warehouse", Description: strconv.Itoa(i)})
		}
	}()
	go func() {
		defer wg.Done()
		for i := range iterations {
			m.Add(Source{Kind: "s3", Name: strconv.Itoa(i), DataHubSourceName: "s3"})
		}
	}()
	go func() {
		defer wg.Done()
		for i := range iterations {
			m.Remove("s3", strconv.Itoa(i))
		}
	}()
	go func() {
		defer wg.Done()
		for range iterations {
			_ = m.ForConnection("trino", "warehouse")
			_ = m.DataHubSourceName("trino", "warehouse")
			for _, src := range m.ConnectionsForURN("urn:li:dataset:(urn:li:dataPlatform:s3,b/k,PROD)") {
				_ = src.Name
			}
			_ = m.ConnectionsForSource("hive")
		}
	}()

	wg.Wait()

	if got := m.ForConnection("trino", "warehouse"); got == nil || got.DataHubSourceName != "hive" {
		t.Errorf("the seeded mapping survived the traffic: %+v", got)
	}
}

func TestSeed(t *testing.T) {
	m := NewMap()
	m.Seed(RegistryEntries("trino", []string{"warehouse"}, "hive", nil))
	m.Overlay(Source{Kind: "trino", Name: "warehouse", DataHubSourceName: "postgres"})

	// Re-seeding replaces the overlay, which is what a deleted stored override
	// falls back to.
	m.Seed(RegistryEntries("trino", []string{"warehouse"}, "hive", nil))
	if got := m.ForConnection("trino", "warehouse"); got == nil || got.DataHubSourceName != "hive" {
		t.Errorf("ForConnection = %+v, want hive", got)
	}

	// Seeding nothing is a no-op, not a panic.
	m.Seed(nil)
	m.Seed(RegistryEntries("mcp", []string{"vendor"}, "", nil))
	if got := m.ForConnection("mcp", "vendor"); got != nil {
		t.Errorf("an unmapped kind seeds nothing: %+v", got)
	}
}
