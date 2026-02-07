package registry

import (
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
		registry.RegisterFactory("test", func(name string, config map[string]any) (Toolkit, error) {
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

		registry.RegisterFactory("test", func(name string, config map[string]any) (Toolkit, error) {
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
}
