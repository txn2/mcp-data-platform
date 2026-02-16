package trino

import (
	"fmt"
	"time"
)

// ParseConfig parses a Trino toolkit configuration from a map.
func ParseConfig(cfg map[string]any) (Config, error) {
	c := Config{
		Port:         defaultPlainPort,
		DefaultLimit: defaultQueryLimit,
		MaxLimit:     defaultMaxLimit,
		Timeout:      defaultTrinoTimeout,
		SSLVerify:    true,
	}

	// Required fields
	v, ok := cfg["host"].(string)
	if !ok {
		return c, fmt.Errorf("host is required")
	}
	c.Host = v

	// Optional string fields
	c.User = getString(cfg, "user")
	c.Password = getString(cfg, "password")
	c.Catalog = getString(cfg, "catalog")
	c.Schema = getString(cfg, "schema")
	c.ConnectionName = getString(cfg, "connection_name")

	// Optional int fields
	c.Port = getInt(cfg, "port", c.Port)
	c.DefaultLimit = getInt(cfg, "default_limit", c.DefaultLimit)
	c.MaxLimit = getInt(cfg, "max_limit", c.MaxLimit)

	// Optional bool fields
	c.SSL = getBool(cfg, "ssl")
	c.SSLVerify = getBoolDefault(cfg, "ssl_verify", true)
	c.ReadOnly = getBool(cfg, "read_only")

	// Timeout with duration parsing
	if timeout, err := getDuration(cfg, "timeout"); err != nil {
		return c, fmt.Errorf("invalid timeout: %w", err)
	} else if timeout > 0 {
		c.Timeout = timeout
	}

	// Optional description overrides
	c.Descriptions = getStringMap(cfg, "descriptions")

	// Optional annotation overrides
	c.Annotations = getAnnotationsMap(cfg, "annotations")

	// Platform-injected flags
	c.ProgressEnabled = getBool(cfg, "progress_enabled")
	c.Elicitation = getElicitationConfig(cfg)

	return c, nil
}

// getElicitationConfig extracts elicitation configuration from a config map.
func getElicitationConfig(cfg map[string]any) ElicitationConfig {
	raw, ok := cfg["elicitation"].(map[string]any)
	if !ok {
		return ElicitationConfig{}
	}

	ec := ElicitationConfig{
		Enabled: getBool(raw, "enabled"),
	}

	if costRaw, ok := raw["cost_estimation"].(map[string]any); ok {
		ec.CostEstimation = CostEstimationConfig{
			Enabled:      getBool(costRaw, "enabled"),
			RowThreshold: getInt64(costRaw, "row_threshold", 0),
		}
	}

	if piiRaw, ok := raw["pii_consent"].(map[string]any); ok {
		ec.PIIConsent = PIIConsentConfig{
			Enabled: getBool(piiRaw, "enabled"),
		}
	}

	return ec
}

// getInt64 extracts an int64 value from a config map with a default.
func getInt64(cfg map[string]any, key string, defaultVal int64) int64 {
	if v, ok := cfg[key].(int64); ok {
		return v
	}
	if v, ok := cfg[key].(int); ok {
		return int64(v)
	}
	if v, ok := cfg[key].(float64); ok {
		return int64(v)
	}
	return defaultVal
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

// getBool extracts a bool value from a config map.
func getBool(cfg map[string]any, key string) bool {
	if v, ok := cfg[key].(bool); ok {
		return v
	}
	return false
}

// getBoolDefault extracts a bool value from a config map with a default.
func getBoolDefault(cfg map[string]any, key string, defaultVal bool) bool {
	if v, ok := cfg[key].(bool); ok {
		return v
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
