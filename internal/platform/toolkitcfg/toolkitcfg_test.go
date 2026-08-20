package toolkitcfg

import (
	"slices"
	"testing"
	"time"
)

func TestInstanceConfig(t *testing.T) {
	toolkits := map[string]any{
		"trino": map[string]any{
			"default": "primary",
			"instances": map[string]any{
				"primary": map[string]any{"host": "localhost"},
				"other":   map[string]any{"host": "other"},
			},
		},
		"broken_kind":      "not-a-map",
		"broken_instances": map[string]any{"instances": "not-a-map"},
	}

	t.Run("named instance", func(t *testing.T) {
		cfg := InstanceConfig(toolkits, "trino", "other")
		if cfg == nil || cfg["host"] != "other" {
			t.Fatalf("InstanceConfig(trino, other) = %v", cfg)
		}
	})
	t.Run("empty instance resolves default", func(t *testing.T) {
		cfg := InstanceConfig(toolkits, "trino", "")
		if cfg == nil || cfg["host"] != "localhost" {
			t.Fatalf("InstanceConfig(trino, '') = %v", cfg)
		}
	})
	t.Run("missing kind", func(t *testing.T) {
		if cfg := InstanceConfig(toolkits, "unknown", "x"); cfg != nil {
			t.Errorf("InstanceConfig(unknown) = %v, want nil", cfg)
		}
	})
	t.Run("kind not a map", func(t *testing.T) {
		if cfg := InstanceConfig(toolkits, "broken_kind", "x"); cfg != nil {
			t.Errorf("InstanceConfig(broken_kind) = %v, want nil", cfg)
		}
	})
	t.Run("instances not a map", func(t *testing.T) {
		if cfg := InstanceConfig(toolkits, "broken_instances", "x"); cfg != nil {
			t.Errorf("InstanceConfig(broken_instances) = %v, want nil", cfg)
		}
	})
	t.Run("named instance absent", func(t *testing.T) {
		if cfg := InstanceConfig(toolkits, "trino", "nonexistent"); cfg != nil {
			t.Errorf("InstanceConfig(trino, nonexistent) = %v, want nil", cfg)
		}
	})
}

func TestResolveDefaultInstance(t *testing.T) {
	t.Run("explicit default key", func(t *testing.T) {
		kindCfg := map[string]any{"default": "chosen"}
		if got := ResolveDefaultInstance(kindCfg, map[string]any{"chosen": nil}); got != "chosen" {
			t.Errorf("ResolveDefaultInstance = %q, want chosen", got)
		}
	})
	t.Run("first instance when no default", func(t *testing.T) {
		instances := map[string]any{"only": nil}
		if got := ResolveDefaultInstance(map[string]any{}, instances); got != "only" {
			t.Errorf("ResolveDefaultInstance = %q, want only", got)
		}
	})
	t.Run("empty when no default and no instances", func(t *testing.T) {
		if got := ResolveDefaultInstance(map[string]any{}, map[string]any{}); got != "" {
			t.Errorf("ResolveDefaultInstance = %q, want empty", got)
		}
	})
}

func TestDataHubConfig(t *testing.T) {
	t.Run("not configured", func(t *testing.T) {
		if cfg := DataHubConfig(map[string]any{}, "x"); cfg != nil {
			t.Errorf("DataHubConfig(empty) = %v, want nil", cfg)
		}
	})
	t.Run("from url", func(t *testing.T) {
		toolkits := map[string]any{"datahub": map[string]any{
			"instances": map[string]any{"primary": map[string]any{
				"url": "https://datahub.example.com", "token": "tok", "timeout": "45s", "debug": true,
			}},
		}}
		cfg := DataHubConfig(toolkits, "primary")
		if cfg == nil {
			t.Fatal("DataHubConfig returned nil")
		}
		if cfg.URL != "https://datahub.example.com" || cfg.Token != "tok" || cfg.Timeout != 45*time.Second || !cfg.Debug {
			t.Errorf("DataHubConfig = %+v", cfg)
		}
	})
	t.Run("url falls back to endpoint, default timeout", func(t *testing.T) {
		toolkits := map[string]any{"datahub": map[string]any{
			"instances": map[string]any{"primary": map[string]any{"endpoint": "https://ep.example.com"}},
		}}
		cfg := DataHubConfig(toolkits, "primary")
		if cfg == nil || cfg.URL != "https://ep.example.com" || cfg.Timeout != 30*time.Second {
			t.Errorf("DataHubConfig = %+v", cfg)
		}
	})
}

func TestTrinoConfig(t *testing.T) {
	t.Run("not configured", func(t *testing.T) {
		if cfg := TrinoConfig(map[string]any{}, "x"); cfg != nil {
			t.Errorf("TrinoConfig(empty) = %v, want nil", cfg)
		}
	})
	t.Run("defaults applied", func(t *testing.T) {
		toolkits := map[string]any{"trino": map[string]any{
			"instances": map[string]any{"primary": map[string]any{"host": "localhost", "user": "u"}},
		}}
		cfg := TrinoConfig(toolkits, "primary")
		if cfg == nil {
			t.Fatal("TrinoConfig returned nil")
		}
		if cfg.Host != "localhost" || cfg.User != "u" {
			t.Errorf("TrinoConfig host/user = %+v", cfg)
		}
		if cfg.Port != DefaultTrinoPort || cfg.DefaultLimit != DefaultTrinoQueryLimit || cfg.MaxLimit != DefaultTrinoMaxLimit {
			t.Errorf("TrinoConfig defaults = %+v", cfg)
		}
		if !cfg.SSLVerify {
			t.Error("TrinoConfig SSLVerify should default true")
		}
	})
	t.Run("explicit values", func(t *testing.T) {
		toolkits := map[string]any{"trino": map[string]any{
			"instances": map[string]any{"primary": map[string]any{
				"host": "h", "port": 9090, "catalog": "c", "schema": "s",
				"ssl": true, "ssl_verify": false, "read_only": true,
				"default_limit": 50, "max_limit": 500, "connection_name": "cn",
			}},
		}}
		cfg := TrinoConfig(toolkits, "primary")
		if cfg == nil || cfg.Port != 9090 || cfg.Catalog != "c" || cfg.Schema != "s" ||
			!cfg.SSL || cfg.SSLVerify || !cfg.ReadOnly ||
			cfg.DefaultLimit != 50 || cfg.MaxLimit != 500 {
			t.Errorf("TrinoConfig = %+v", cfg)
		}
		// The query provider stamps ConnectionName onto every availability
		// answer as the name to pass as `connection`. Trino routes by instance,
		// so the instance key is that name and the instance's connection_name
		// ("cn" here) is not (#1396).
		if cfg.ConnectionName != "primary" {
			t.Errorf("ConnectionName = %q, want the routed instance 'primary'", cfg.ConnectionName)
		}
	})
	t.Run("an unnamed instance resolves the default rather than an empty label", func(t *testing.T) {
		toolkits := map[string]any{"trino": map[string]any{
			"default": "warehouse",
			"instances": map[string]any{
				"warehouse": map[string]any{"host": "h", "user": "u"},
				"staging":   map[string]any{"host": "s", "user": "u"},
			},
		}}
		cfg := TrinoConfig(toolkits, "")
		if cfg == nil {
			t.Fatal("TrinoConfig returned nil")
		}
		if cfg.ConnectionName != "warehouse" {
			t.Errorf("ConnectionName = %q, want 'warehouse'", cfg.ConnectionName)
		}
	})
}

func TestS3Config(t *testing.T) {
	t.Run("not configured", func(t *testing.T) {
		if cfg := S3Config(map[string]any{}, "x"); cfg != nil {
			t.Errorf("S3Config(empty) = %v, want nil", cfg)
		}
	})
	t.Run("explicit connection_name", func(t *testing.T) {
		toolkits := map[string]any{"s3": map[string]any{
			"instances": map[string]any{"primary": map[string]any{
				"region": "us-east-1", "endpoint": "https://s3.example.com",
				"access_key_id": "ak", "connection_name": "explicit", "use_path_style": true,
			}},
		}}
		cfg := S3Config(toolkits, "primary")
		if cfg == nil || cfg.Region != "us-east-1" || cfg.Endpoint != "https://s3.example.com" ||
			cfg.AccessKeyID != "ak" || cfg.ConnectionName != "explicit" || !cfg.UsePathStyle {
			t.Errorf("S3Config = %+v", cfg)
		}
	})
	t.Run("connection_name defaults to instance", func(t *testing.T) {
		toolkits := map[string]any{"s3": map[string]any{
			"instances": map[string]any{"myinstance": map[string]any{"region": "us-west-2"}},
		}}
		cfg := S3Config(toolkits, "myinstance")
		if cfg == nil || cfg.ConnectionName != "myinstance" {
			t.Errorf("S3Config connection_name = %+v, want myinstance", cfg)
		}
	})
}

func TestResolveDefaultInstanceIsStableAcrossRestarts(t *testing.T) {
	// Go randomizes map iteration order, so a fallback that ranged the
	// instances map resolved a different instance on each process start: two
	// replicas built from one config disagreed about which connection an
	// unqualified lookup meant. Building the map fresh each round models a
	// restart; the answer must not move. A single resolution would pass by
	// luck.
	newInstances := func() map[string]any {
		return map[string]any{"zeta": nil, "delta": nil, "alpha": nil, "omega": nil, "beta": nil}
	}
	const want = "alpha"
	for i := range 200 {
		if got := ResolveDefaultInstance(map[string]any{}, newInstances()); got != want {
			t.Fatalf("round %d: ResolveDefaultInstance = %q, want %q", i, got, want)
		}
	}
}

func TestResolveDefaultInstanceUnusableDefaultKey(t *testing.T) {
	// An empty or non-string "default" has named nothing, so it falls through
	// to the deterministic pick rather than resolving to "".
	instances := map[string]any{"beta": nil, "alpha": nil}
	for name, kindCfg := range map[string]map[string]any{
		"empty string": {"default": ""},
		"not a string": {"default": 7},
	} {
		if got := ResolveDefaultInstance(kindCfg, instances); got != "alpha" {
			t.Errorf("%s: ResolveDefaultInstance = %q, want alpha", name, got)
		}
	}
}

func TestMissingDefaults(t *testing.T) {
	tests := []struct {
		name     string
		toolkits map[string]any
		want     []string
	}{
		{name: "nil config"},
		{
			name: "single instance needs no default",
			toolkits: map[string]any{"s3": map[string]any{
				"instances": map[string]any{"only": nil},
			}},
		},
		{
			name: "several instances with a default",
			toolkits: map[string]any{"s3": map[string]any{
				"default":   "archive",
				"instances": map[string]any{"archive": nil, "lake": nil},
			}},
		},
		{
			name: "several instances without a default",
			toolkits: map[string]any{"s3": map[string]any{
				"instances": map[string]any{"lake": nil, "archive": nil},
			}},
			want: []string{"toolkits.s3.default is required when more than one instance is configured (instances: archive, lake)"},
		},
		{
			name: "empty default does not count as named",
			toolkits: map[string]any{"datahub": map[string]any{
				"default":   "",
				"instances": map[string]any{"primary": nil, "secondary": nil},
			}},
			want: []string{"toolkits.datahub.default is required when more than one instance is configured (instances: primary, secondary)"},
		},
		{
			name: "every offending kind is reported, sorted",
			toolkits: map[string]any{
				"trino": map[string]any{
					"instances": map[string]any{"staging": nil, "production": nil},
				},
				"datahub": map[string]any{
					"instances": map[string]any{"b": nil, "a": nil},
				},
				"s3": map[string]any{
					"default":   "lake",
					"instances": map[string]any{"lake": nil, "archive": nil},
				},
			},
			want: []string{
				"toolkits.datahub.default is required when more than one instance is configured (instances: a, b)",
				"toolkits.trino.default is required when more than one instance is configured (instances: production, staging)",
			},
		},
		{
			// The gateway kinds namespace every proxied tool by its
			// connection, so nothing resolves a default for them and
			// requiring one would refuse a config over an inert key.
			name: "gateway kinds are not asked for a default",
			toolkits: map[string]any{
				"mcp": map[string]any{
					"enabled":   true,
					"instances": map[string]any{"upstream_a": nil, "upstream_b": nil},
				},
				"api": map[string]any{
					"enabled":   true,
					"instances": map[string]any{"billing": nil, "crm": nil},
				},
			},
		},
		{
			// The providers read an instance through InstanceConfig without
			// consulting the enable flag, so a disabled kind is still asked
			// which of its instances an unqualified lookup means.
			name: "a disabled kind is still ambiguous",
			toolkits: map[string]any{"datahub": map[string]any{
				"enabled":   false,
				"instances": map[string]any{"primary": nil, "legacy": nil},
			}},
			want: []string{"toolkits.datahub.default is required when more than one instance is configured (instances: legacy, primary)"},
		},
		{
			name:     "malformed kind block is not a default problem",
			toolkits: map[string]any{"s3": "not-a-map"},
		},
		{
			name: "malformed instances block is not a default problem",
			toolkits: map[string]any{"s3": map[string]any{
				"instances": []string{"lake", "archive"},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MissingDefaults(tt.toolkits)
			if !slices.Equal(got, tt.want) {
				t.Errorf("MissingDefaults() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestS3ConfigNamesTheResolvedConnection(t *testing.T) {
	// An instance that omits connection_name is labeled by the instance name.
	// Resolving through the default used to hand back the caller's empty
	// string instead, leaving every downstream connection label blank.
	toolkits := map[string]any{"s3": map[string]any{
		"default": "archive",
		"instances": map[string]any{
			"archive": map[string]any{"region": "us-west-2"},
			"lake":    map[string]any{"region": "us-east-1"},
		},
	}}
	cfg := S3Config(toolkits, "")
	if cfg == nil {
		t.Fatal("S3Config returned nil")
	}
	if cfg.ConnectionName != "archive" {
		t.Errorf("ConnectionName = %q, want archive", cfg.ConnectionName)
	}
	if cfg.Region != "us-west-2" {
		t.Errorf("Region = %q, want us-west-2", cfg.Region)
	}
	if named := S3Config(toolkits, "lake"); named == nil || named.ConnectionName != "lake" {
		t.Errorf("S3Config(lake) = %+v, want ConnectionName lake", named)
	}
}
