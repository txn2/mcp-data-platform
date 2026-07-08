package toolkitcfg

import (
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
			cfg.DefaultLimit != 50 || cfg.MaxLimit != 500 || cfg.ConnectionName != "cn" {
			t.Errorf("TrinoConfig = %+v", cfg)
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
