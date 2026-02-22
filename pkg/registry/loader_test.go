package registry

import (
	"fmt"
	"testing"
)

func TestNewLoader(t *testing.T) {
	registry := NewRegistry()
	loader := NewLoader(registry)

	if loader == nil {
		t.Fatal("NewLoader() returned nil")
	}
	if loader.registry != registry {
		t.Error("registry not set correctly")
	}
}

func TestLoader_Load(t *testing.T) {
	t.Run("empty config", func(t *testing.T) {
		registry := NewRegistry()
		loader := NewLoader(registry)

		err := loader.Load(LoaderConfig{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("disabled toolkit", func(t *testing.T) {
		registry := NewRegistry()
		loader := NewLoader(registry)

		cfg := LoaderConfig{
			Toolkits: map[string]ToolkitKindConfig{
				"test": {
					Enabled: false,
					Instances: map[string]map[string]any{
						"instance1": {"key": "value"},
					},
				},
			},
		}

		err := loader.Load(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should not have any toolkits
		if len(registry.All()) != 0 {
			t.Error("expected no toolkits for disabled config")
		}
	})

	t.Run("enabled toolkit with factory", func(t *testing.T) {
		registry := NewRegistry()

		// Register a test factory
		registry.RegisterFactory("test", func(name string, _ map[string]any) (Toolkit, error) {
			return &mockToolkit{
				kind: "test",
				name: name,
			}, nil
		})

		loader := NewLoader(registry)

		cfg := LoaderConfig{
			Toolkits: map[string]ToolkitKindConfig{
				"test": {
					Enabled: true,
					Config: map[string]any{
						"shared": "value",
					},
					Instances: map[string]map[string]any{
						"instance1": {"specific": "value1"},
					},
					Default: "instance1",
				},
			},
		}

		err := loader.Load(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should have one toolkit
		if len(registry.All()) != 1 {
			t.Errorf("expected 1 toolkit, got %d", len(registry.All()))
		}
	})

	t.Run("missing factory", func(t *testing.T) {
		registry := NewRegistry()
		loader := NewLoader(registry)

		cfg := LoaderConfig{
			Toolkits: map[string]ToolkitKindConfig{
				"unknown": {
					Enabled: true,
					Instances: map[string]map[string]any{
						"instance1": {},
					},
				},
			},
		}

		err := loader.Load(cfg)
		if err == nil {
			t.Error("expected error for missing factory")
		}
	})

	t.Run("aggregate factory called instead of per-instance", func(t *testing.T) {
		reg := NewRegistry()

		perInstanceCalled := false
		reg.RegisterFactory("agg-kind", func(_ string, _ map[string]any) (Toolkit, error) {
			perInstanceCalled = true
			return &mockToolkit{kind: "agg-kind", name: "should-not-be-used"}, nil
		})

		aggCalled := false
		reg.RegisterAggregateFactory("agg-kind", func(defaultName string, instances map[string]map[string]any) (Toolkit, error) {
			aggCalled = true
			if defaultName != "inst1" {
				t.Errorf("defaultName = %q, want 'inst1'", defaultName)
			}
			if len(instances) != 2 {
				t.Errorf("expected 2 instances, got %d", len(instances))
			}
			// Verify kind-level config is merged
			if instances["inst1"]["shared"] != "yes" {
				t.Error("expected shared config to be merged")
			}
			return &mockToolkit{kind: "agg-kind", name: defaultName, tools: []string{"agg_tool"}}, nil
		})

		loader := NewLoader(reg)

		cfg := LoaderConfig{
			Toolkits: map[string]ToolkitKindConfig{
				"agg-kind": {
					Enabled: true,
					Default: "inst1",
					Config:  map[string]any{"shared": "yes"},
					Instances: map[string]map[string]any{
						"inst1": {"host": "a"},
						"inst2": {"host": "b"},
					},
				},
			},
		}

		err := loader.Load(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !aggCalled {
			t.Error("aggregate factory was not called")
		}
		if perInstanceCalled {
			t.Error("per-instance factory should not be called when aggregate is registered")
		}

		// Should register one toolkit, not two.
		if len(reg.All()) != 1 {
			t.Errorf("expected 1 toolkit, got %d", len(reg.All()))
		}
	})

	t.Run("aggregate factory error propagated", func(t *testing.T) {
		reg := NewRegistry()
		reg.RegisterAggregateFactory("failing", func(_ string, _ map[string]map[string]any) (Toolkit, error) {
			return nil, fmt.Errorf("aggregate creation failed")
		})

		loader := NewLoader(reg)
		cfg := LoaderConfig{
			Toolkits: map[string]ToolkitKindConfig{
				"failing": {
					Enabled:   true,
					Instances: map[string]map[string]any{"inst1": {}},
				},
			},
		}

		err := loader.Load(cfg)
		if err == nil {
			t.Error("expected error from aggregate factory")
		}
	})
}

func TestLoader_LoadFromMap(t *testing.T) {
	t.Run("empty map", func(t *testing.T) {
		registry := NewRegistry()
		loader := NewLoader(registry)

		err := loader.LoadFromMap(map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("disabled toolkit", func(t *testing.T) {
		registry := NewRegistry()
		loader := NewLoader(registry)

		toolkits := map[string]any{
			"test": map[string]any{
				"enabled": false,
				"instances": map[string]any{
					"inst1": map[string]any{},
				},
			},
		}

		err := loader.LoadFromMap(toolkits)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(registry.All()) != 0 {
			t.Error("expected no toolkits for disabled config")
		}
	})

	t.Run("enabled toolkit", func(t *testing.T) {
		registry := NewRegistry()

		registry.RegisterFactory("test", func(name string, _ map[string]any) (Toolkit, error) {
			return &mockToolkit{
				kind: "test",
				name: name,
			}, nil
		})

		loader := NewLoader(registry)

		toolkits := map[string]any{
			"test": map[string]any{
				"enabled": true,
				"default": "inst1",
				"config": map[string]any{
					"shared": "value",
				},
				"instances": map[string]any{
					"inst1": map[string]any{
						"specific": "value1",
					},
				},
			},
		}

		err := loader.LoadFromMap(toolkits)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(registry.All()) != 1 {
			t.Errorf("expected 1 toolkit, got %d", len(registry.All()))
		}
	})

	t.Run("invalid kind value", func(t *testing.T) {
		registry := NewRegistry()
		loader := NewLoader(registry)

		toolkits := map[string]any{
			"test": "not a map",
		}

		err := loader.LoadFromMap(toolkits)
		if err != nil {
			t.Fatalf("unexpected error (should skip invalid): %v", err)
		}
	})

	t.Run("missing factory", func(t *testing.T) {
		registry := NewRegistry()
		loader := NewLoader(registry)

		toolkits := map[string]any{
			"unknown": map[string]any{
				"enabled": true,
				"instances": map[string]any{
					"inst1": map[string]any{},
				},
			},
		}

		err := loader.LoadFromMap(toolkits)
		if err == nil {
			t.Error("expected error for missing factory")
		}
	})

	t.Run("aggregate factory from map", func(t *testing.T) {
		reg := NewRegistry()

		aggCalled := false
		reg.RegisterAggregateFactory("agg-map", func(defaultName string, instances map[string]map[string]any) (Toolkit, error) {
			aggCalled = true
			if defaultName != "main" {
				t.Errorf("defaultName = %q, want 'main'", defaultName)
			}
			if len(instances) != 2 {
				t.Errorf("expected 2 instances, got %d", len(instances))
			}
			// Verify kind-level config is merged into instances.
			if instances["main"]["shared"] != "value" {
				t.Error("expected shared config to be merged into main")
			}
			if instances["secondary"]["shared"] != "value" {
				t.Error("expected shared config to be merged into secondary")
			}
			return &mockToolkit{kind: "agg-map", name: defaultName}, nil
		})

		loader := NewLoader(reg)

		toolkits := map[string]any{
			"agg-map": map[string]any{
				"enabled": true,
				"default": "main",
				"config":  map[string]any{"shared": "value"},
				"instances": map[string]any{
					"main":      map[string]any{"host": "a"},
					"secondary": map[string]any{"host": "b"},
				},
			},
		}

		err := loader.LoadFromMap(toolkits)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !aggCalled {
			t.Error("aggregate factory was not called")
		}
		if len(reg.All()) != 1 {
			t.Errorf("expected 1 toolkit, got %d", len(reg.All()))
		}
	})

	t.Run("aggregate factory error from map", func(t *testing.T) {
		reg := NewRegistry()
		reg.RegisterAggregateFactory("fail-map", func(_ string, _ map[string]map[string]any) (Toolkit, error) {
			return nil, fmt.Errorf("map aggregate failed")
		})

		loader := NewLoader(reg)
		toolkits := map[string]any{
			"fail-map": map[string]any{
				"enabled":   true,
				"instances": map[string]any{"inst1": map[string]any{}},
			},
		}

		err := loader.LoadFromMap(toolkits)
		if err == nil {
			t.Error("expected error from aggregate factory")
		}
	})
}
