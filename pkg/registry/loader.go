package registry

import (
	"fmt"
)

// LoaderConfig holds configuration for loading toolkits.
type LoaderConfig struct {
	Toolkits map[string]ToolkitKindConfig `yaml:"toolkits"`
}

// ToolkitKindConfig holds configuration for a toolkit kind.
type ToolkitKindConfig struct {
	Enabled   bool                      `yaml:"enabled"`
	Instances map[string]map[string]any `yaml:"instances"`
	Default   string                    `yaml:"default"`
	Config    map[string]any            `yaml:"config"`
}

// Loader loads toolkits from configuration.
type Loader struct {
	registry *Registry
}

// NewLoader creates a new toolkit loader.
func NewLoader(registry *Registry) *Loader {
	return &Loader{registry: registry}
}

// Load loads toolkits from configuration.
func (l *Loader) Load(cfg LoaderConfig) error {
	for kind, kindCfg := range cfg.Toolkits {
		if !kindCfg.Enabled {
			continue
		}

		for name, instanceCfg := range kindCfg.Instances {
			// Merge kind-level config with instance config
			mergedCfg := make(map[string]any)
			for k, v := range kindCfg.Config {
				mergedCfg[k] = v
			}
			for k, v := range instanceCfg {
				mergedCfg[k] = v
			}

			toolkitCfg := ToolkitConfig{
				Kind:    kind,
				Name:    name,
				Enabled: true,
				Config:  mergedCfg,
				Default: name == kindCfg.Default,
			}

			if err := l.registry.CreateAndRegister(toolkitCfg); err != nil {
				return fmt.Errorf("loading toolkit %s/%s: %w", kind, name, err)
			}
		}
	}

	return nil
}

// LoadFromMap loads toolkits from a map configuration.
func (l *Loader) LoadFromMap(toolkits map[string]any) error {
	for kind, v := range toolkits {
		kindMap, ok := v.(map[string]any)
		if !ok {
			continue
		}

		enabled, _ := kindMap["enabled"].(bool)
		if !enabled {
			continue
		}

		instances, _ := kindMap["instances"].(map[string]any)
		defaultName, _ := kindMap["default"].(string)
		kindConfig, _ := kindMap["config"].(map[string]any)

		for name, instanceV := range instances {
			instanceCfg, _ := instanceV.(map[string]any)

			// Merge configs
			mergedCfg := make(map[string]any)
			for k, val := range kindConfig {
				mergedCfg[k] = val
			}
			for k, val := range instanceCfg {
				mergedCfg[k] = val
			}

			toolkitCfg := ToolkitConfig{
				Kind:    kind,
				Name:    name,
				Enabled: true,
				Config:  mergedCfg,
				Default: name == defaultName,
			}

			if err := l.registry.CreateAndRegister(toolkitCfg); err != nil {
				return fmt.Errorf("loading toolkit %s/%s: %w", kind, name, err)
			}
		}
	}

	return nil
}
