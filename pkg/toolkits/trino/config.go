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

	return c, nil
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
