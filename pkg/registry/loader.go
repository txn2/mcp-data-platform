package registry

import (
	"fmt"
	"maps"
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

		// Build merged instance configs for aggregate factory detection.
		mergedInstances := mergeInstanceConfigs(kindCfg.Instances, kindCfg.Config)

		// Check for aggregate factory first (multi-connection → single toolkit).
		if aggFactory, ok := l.registry.GetAggregateFactory(kind); ok {
			if err := l.loadAggregate(kind, kindCfg.Default, mergedInstances, aggFactory); err != nil {
				return err
			}
			continue
		}

		// Fall through to per-instance factory loop.
		for name, mergedCfg := range mergedInstances {
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

		if err := l.loadKindFromMap(kind, kindMap); err != nil {
			return err
		}
	}

	return nil
}

// loadKindFromMap loads all instances of a toolkit kind from a map config.
func (l *Loader) loadKindFromMap(kind string, kindMap map[string]any) error {
	instances, _ := kindMap["instances"].(map[string]any)
	defaultName, _ := kindMap["default"].(string)
	kindConfig, _ := kindMap["config"].(map[string]any)

	mergedInstances := mergeMapInstances(instances, kindConfig)

	// Check for aggregate factory first.
	if aggFactory, ok := l.registry.GetAggregateFactory(kind); ok {
		return l.loadAggregate(kind, defaultName, mergedInstances, aggFactory)
	}

	// Fall through to per-instance factory loop.
	for name, mergedCfg := range mergedInstances {
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
	return nil
}

// mergeMapInstances builds typed instance configs from untyped map, merging kind-level config.
func mergeMapInstances(instances, kindConfig map[string]any) map[string]map[string]any {
	merged := make(map[string]map[string]any, len(instances))
	for name, instanceV := range instances {
		instanceCfg, _ := instanceV.(map[string]any)
		mergedCfg := make(map[string]any)
		maps.Copy(mergedCfg, kindConfig)
		maps.Copy(mergedCfg, instanceCfg)
		merged[name] = mergedCfg
	}
	return merged
}

// loadAggregate invokes an aggregate factory and registers the resulting toolkit.
func (l *Loader) loadAggregate(
	kind, defaultName string,
	instances map[string]map[string]any,
	factory AggregateToolkitFactory,
) error {
	toolkit, err := factory(defaultName, instances)
	if err != nil {
		return fmt.Errorf("loading aggregate toolkit %s: %w", kind, err)
	}
	return l.registry.Register(toolkit)
}

// mergeInstanceConfigs merges kind-level config into each instance config.
func mergeInstanceConfigs(instances map[string]map[string]any, kindConfig map[string]any) map[string]map[string]any {
	merged := make(map[string]map[string]any, len(instances))
	for name, instanceCfg := range instances {
		mergedCfg := make(map[string]any)
		maps.Copy(mergedCfg, kindConfig)
		maps.Copy(mergedCfg, instanceCfg)
		merged[name] = mergedCfg
	}
	return merged
}
