package datahub

import (
	"fmt"
	"time"
)

// ParseConfig parses a DataHub toolkit configuration from a map.
func ParseConfig(cfg map[string]any) (Config, error) {
	c := Config{
		Timeout:         30 * time.Second,
		DefaultLimit:    10,
		MaxLimit:        100,
		MaxLineageDepth: 5,
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

// getBool extracts a bool value from a config map with a default.
func getBool(cfg map[string]any, key string, defaultVal bool) bool {
	if v, ok := cfg[key].(bool); ok {
		return v
	}
	return defaultVal
}
