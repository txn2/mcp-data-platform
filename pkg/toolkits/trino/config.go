package trino

import (
	"fmt"
	"time"
)

// ParseConfig parses a Trino toolkit configuration from a map.
func ParseConfig(cfg map[string]any) (Config, error) {
	c := Config{
		Port:         8080,
		DefaultLimit: 1000,
		MaxLimit:     10000,
		Timeout:      120 * time.Second,
		SSLVerify:    true,
	}

	// Required fields
	if v, ok := cfg["host"].(string); ok {
		c.Host = v
	} else {
		return c, fmt.Errorf("host is required")
	}

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

// getDuration extracts a duration value from a config map.
func getDuration(cfg map[string]any, key string) (time.Duration, error) {
	if v, ok := cfg[key].(string); ok {
		return time.ParseDuration(v)
	}
	if v, ok := cfg[key].(int); ok {
		return time.Duration(v) * time.Second, nil
	}
	if v, ok := cfg[key].(float64); ok {
		return time.Duration(v) * time.Second, nil
	}
	return 0, nil
}
