package toolkitcfg

import (
	"reflect"
	"testing"
)

func TestAutoEnableKind(t *testing.T) {
	t.Run("absent kind is enabled", func(t *testing.T) {
		toolkits := map[string]any{}
		AutoEnableKind(toolkits, "s3")
		if !reflect.DeepEqual(toolkits["s3"], map[string]any{"enabled": true}) {
			t.Errorf("toolkits[s3] = %#v, want enabled=true", toolkits["s3"])
		}
	})
	t.Run("an explicit choice is never overridden", func(t *testing.T) {
		for _, declared := range []any{true, false} {
			toolkits := map[string]any{"s3": map[string]any{"enabled": declared, "instances": map[string]any{}}}
			AutoEnableKind(toolkits, "s3")
			kindMap, ok := toolkits["s3"].(map[string]any)
			if !ok {
				t.Fatalf("toolkits[s3] = %#v, want a map", toolkits["s3"])
			}
			if kindMap["enabled"] != declared {
				t.Errorf("enabled = %#v, want %#v", kindMap["enabled"], declared)
			}
			if _, kept := kindMap["instances"]; !kept {
				t.Error("AutoEnableKind replaced the declared kind block")
			}
		}
	})
}

func TestMergeInstance(t *testing.T) {
	cfg := map[string]any{"region": "us-east-1"}

	t.Run("merged into an enabled kind", func(t *testing.T) {
		toolkits := map[string]any{"s3": map[string]any{"enabled": true}}
		MergeInstance(toolkits, "s3", "lake", cfg)
		if got := InstanceConfig(toolkits, "s3", "lake"); !reflect.DeepEqual(got, cfg) {
			t.Errorf("InstanceConfig = %#v, want %#v", got, cfg)
		}
	})
	t.Run("file config wins over a stored connection", func(t *testing.T) {
		fileCfg := map[string]any{"region": "eu-west-1"}
		toolkits := map[string]any{"s3": map[string]any{
			"enabled":   true,
			"instances": map[string]any{"lake": fileCfg},
		}}
		MergeInstance(toolkits, "s3", "lake", cfg)
		if got := InstanceConfig(toolkits, "s3", "lake"); !reflect.DeepEqual(got, fileCfg) {
			t.Errorf("InstanceConfig = %#v, want the file config %#v", got, fileCfg)
		}
	})
	t.Run("no-op for an absent or disabled kind", func(t *testing.T) {
		for name, toolkits := range map[string]map[string]any{
			"absent":   {},
			"disabled": {"s3": map[string]any{"enabled": false}},
		} {
			MergeInstance(toolkits, "s3", "lake", cfg)
			if got := InstanceConfig(toolkits, "s3", "lake"); got != nil {
				t.Errorf("%s: InstanceConfig = %#v, want nil", name, got)
			}
		}
	})
	t.Run("several merged instances still resolve deterministically", func(t *testing.T) {
		// Stored connections merge after Config.Validate has run, so
		// MissingDefaults never sees them and the kind can hold several with
		// no "default". The resolution must still not move between restarts.
		const want = "archive"
		for range 100 {
			toolkits := map[string]any{"s3": map[string]any{"enabled": true}}
			for _, name := range []string{"lake", "archive", "warehouse"} {
				MergeInstance(toolkits, "s3", name, cfg)
			}
			kindMap, ok := toolkits["s3"].(map[string]any)
			if !ok {
				t.Fatalf("toolkits[s3] = %#v, want a map", toolkits["s3"])
			}
			instances, ok := kindMap["instances"].(map[string]any)
			if !ok {
				t.Fatalf("instances = %#v, want a map", kindMap["instances"])
			}
			if got := ResolveDefaultInstance(kindMap, instances); got != want {
				t.Fatalf("ResolveDefaultInstance = %q, want %q", got, want)
			}
		}
	})
}

func TestKindEnabled(t *testing.T) {
	tests := []struct {
		name    string
		kindMap map[string]any
		want    bool
	}{
		{name: "absent key", kindMap: map[string]any{}},
		{name: "bool true", kindMap: map[string]any{"enabled": true}, want: true},
		{name: "bool false", kindMap: map[string]any{"enabled": false}},
		{name: "expanded string true", kindMap: map[string]any{"enabled": "true"}, want: true},
		{name: "expanded string false", kindMap: map[string]any{"enabled": "false"}},
		{name: "unusable type", kindMap: map[string]any{"enabled": 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := KindEnabled(tt.kindMap); got != tt.want {
				t.Errorf("KindEnabled(%#v) = %v, want %v", tt.kindMap, got, tt.want)
			}
		})
	}
}

func TestPinDeclaredDefaults(t *testing.T) {
	t.Run("a declared instance keeps the lookup a merged one would take", func(t *testing.T) {
		// One declared instance needs no "default", so nothing recorded which
		// connection the file meant. An admin-UI connection sorting earlier
		// would answer the unqualified lookup at the next restart.
		toolkits := map[string]any{"s3": map[string]any{
			"enabled":   true,
			"instances": map[string]any{"lake": map[string]any{"region": "us-east-1"}},
		}}
		PinDeclaredDefaults(toolkits)
		MergeInstance(toolkits, "s3", "archive", map[string]any{"region": "us-west-2"})

		cfg := S3Config(toolkits, "")
		if cfg == nil {
			t.Fatal("S3Config returned nil")
		}
		if cfg.ConnectionName != "lake" {
			t.Errorf("ConnectionName = %q, want lake", cfg.ConnectionName)
		}
		if _, merged := InstanceConfig(toolkits, "s3", "archive")["region"]; !merged {
			t.Error("the stored connection was not merged")
		}
	})
	t.Run("a named default is never overwritten", func(t *testing.T) {
		toolkits := map[string]any{"s3": map[string]any{
			"default":   "archive",
			"instances": map[string]any{"archive": nil, "lake": nil},
		}}
		PinDeclaredDefaults(toolkits)
		kindMap, ok := toolkits["s3"].(map[string]any)
		if !ok {
			t.Fatalf("toolkits[s3] = %#v, want a map", toolkits["s3"])
		}
		if kindMap["default"] != "archive" {
			t.Errorf("default = %#v, want archive", kindMap["default"])
		}
	})
	t.Run("nothing to pin", func(t *testing.T) {
		// A kind whose instances all come from the database has no declared
		// instance to protect, so no default is invented for it.
		toolkits := map[string]any{
			"s3":  map[string]any{"enabled": true},
			"mcp": "not-a-map",
		}
		PinDeclaredDefaults(toolkits)
		kindMap, ok := toolkits["s3"].(map[string]any)
		if !ok {
			t.Fatalf("toolkits[s3] = %#v, want a map", toolkits["s3"])
		}
		if _, pinned := kindMap["default"]; pinned {
			t.Errorf("default = %#v, want none", kindMap["default"])
		}
	})
}

func TestDeclared(t *testing.T) {
	toolkits := map[string]any{
		// Every kind is captured, whether or not the admin connection API
		// manages it and whether or not the kind is enabled.
		"trino": map[string]any{
			"enabled":   true,
			"instances": map[string]any{"warehouse": nil, "staging": nil},
		},
		"datahub": map[string]any{
			"enabled":   false,
			"instances": map[string]any{"catalog": nil},
		},
		// A kind with no instances contributes nothing, and neither does one
		// whose config is not a map at all.
		"mcp": map[string]any{"enabled": true},
		"s3":  "not-a-map",
	}
	declared := Declared(toolkits)

	for _, tc := range []struct {
		kind, name string
		want       bool
	}{
		{"trino", "warehouse", true},
		{"trino", "staging", true},
		{"datahub", "catalog", true},
		{"trino", "catalog", false},
		{"mcp", "anything", false},
		{"s3", "lake", false},
		{"api", "acme", false},
	} {
		if got := declared.Has(tc.kind, tc.name); got != tc.want {
			t.Errorf("Has(%q, %q) = %v, want %v", tc.kind, tc.name, got, tc.want)
		}
	}
}

func TestDeclaredIsASnapshot(t *testing.T) {
	// The snapshot is what separates a file-declared connection from one
	// MergeInstance later puts into the same map; sharing the map would erase
	// the distinction the moment a stored connection merged.
	instances := map[string]any{"warehouse": nil}
	toolkits := map[string]any{"trino": map[string]any{"enabled": true, "instances": instances}}

	declared := Declared(toolkits)
	MergeInstance(toolkits, "trino", "adhoc", map[string]any{"host": "trino.local"})

	if _, merged := instances["adhoc"]; !merged {
		t.Fatal("precondition: MergeInstance should have added adhoc to the live map")
	}
	if !declared.Has("trino", "warehouse") {
		t.Error("the file's instance should still be declared after a merge")
	}
	if declared.Has("trino", "adhoc") {
		t.Error("a merged connection must not appear in a snapshot taken before it")
	}
}

func TestDeclaredZeroValue(t *testing.T) {
	var declared DeclaredConnections
	if declared.Has("trino", "warehouse") {
		t.Error("the zero value declares nothing")
	}
}
