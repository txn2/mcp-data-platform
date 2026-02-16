package datahub

import (
	"fmt"
	"time"
)

// ParseConfig parses a DataHub toolkit configuration from a map.
func ParseConfig(cfg map[string]any) (Config, error) {
	c := Config{
		Timeout:         defaultTimeout,
		DefaultLimit:    defaultDataHubLimit,
		MaxLimit:        defaultMaxLimit,
		MaxLineageDepth: defaultMaxLineageDepth,
	}

	// Required URL field (supports both "url" and "endpoint" keys)
	c.URL = getString(cfg, "url")
	if c.URL == "" {
		c.URL = getString(cfg, "endpoint")
	}
	if c.URL == "" {
		return c, fmt.Errorf("url is required")
	}

	// Optional string fields
	c.Token = getString(cfg, "token")
	c.ConnectionName = getString(cfg, "connection_name")

	// Optional int fields
	c.DefaultLimit = getInt(cfg, "default_limit", c.DefaultLimit)
	c.MaxLimit = getInt(cfg, "max_limit", c.MaxLimit)
	c.MaxLineageDepth = getInt(cfg, "max_lineage_depth", c.MaxLineageDepth)

	// Timeout with duration parsing
	if timeout, err := getDuration(cfg, "timeout"); err != nil {
		return c, fmt.Errorf("invalid timeout: %w", err)
	} else if timeout > 0 {
		c.Timeout = timeout
	}

	// Optional bool fields
	c.Debug = getBool(cfg, "debug", false)

	// Optional description overrides
	c.Descriptions = getStringMap(cfg, "descriptions")

	// Optional annotation overrides
	c.Annotations = getAnnotationsMap(cfg, "annotations")

	return c, nil
}

// AnnotationConfig holds tool annotation overrides from configuration.
type AnnotationConfig struct {
	ReadOnlyHint    *bool `yaml:"read_only_hint"`
	DestructiveHint *bool `yaml:"destructive_hint"`
	IdempotentHint  *bool `yaml:"idempotent_hint"`
	OpenWorldHint   *bool `yaml:"open_world_hint"`
}

// getAnnotationsMap extracts annotation overrides from a config map.
func getAnnotationsMap(cfg map[string]any, key string) map[string]AnnotationConfig { //nolint:unparam // consistent with getStringMap
	raw, ok := cfg[key].(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]AnnotationConfig, len(raw))
	for k, v := range raw {
		toolCfg, ok := v.(map[string]any)
		if !ok {
			continue
		}
		ann := AnnotationConfig{}
		if b, ok := toolCfg["read_only_hint"].(bool); ok {
			ann.ReadOnlyHint = &b
		}
		if b, ok := toolCfg["destructive_hint"].(bool); ok {
			ann.DestructiveHint = &b
		}
		if b, ok := toolCfg["idempotent_hint"].(bool); ok {
			ann.IdempotentHint = &b
		}
		if b, ok := toolCfg["open_world_hint"].(bool); ok {
			ann.OpenWorldHint = &b
		}
		result[k] = ann
	}
	return result
}

// getString extracts a string value from a config map.
func getString(cfg map[string]any, key string) string {
	if v, ok := cfg[key].(string); ok {
		return v
	}
	return ""
}

// getInt extracts an int value from a config map with a default.
func getInt(cfg map[string]any, key string, defaultVal int) int {
	if v, ok := cfg[key].(int); ok {
		return v
	}
	if v, ok := cfg[key].(float64); ok {
		return int(v)
	}
	return defaultVal
}

// getStringMap extracts a map[string]string value from a config map.
func getStringMap(cfg map[string]any, key string) map[string]string { //nolint:unparam // consistent with getString/getInt helpers
	raw, ok := cfg[key].(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	return result
}

// getDuration extracts a duration value from a config map.
func getDuration(cfg map[string]any, key string) (time.Duration, error) {
	if v, ok := cfg[key].(string); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return 0, fmt.Errorf("parsing duration %q: %w", v, err)
		}
		return d, nil
	}
	if v, ok := cfg[key].(int); ok {
		return time.Duration(v) * time.Second, nil
	}
	if v, ok := cfg[key].(float64); ok {
		return time.Duration(v) * time.Second, nil
	}
	return 0, nil
}

// getBool extracts a bool value from a config map with a default.
func getBool(cfg map[string]any, key string, defaultVal bool) bool {
	if v, ok := cfg[key].(bool); ok {
		return v
	}
	return defaultVal
}
