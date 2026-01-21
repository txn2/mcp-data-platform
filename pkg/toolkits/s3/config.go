package s3

import (
	"time"
)

// ParseConfig parses an S3 toolkit configuration from a map.
func ParseConfig(cfg map[string]any) (Config, error) {
	c := Config{
		Region:     "us-east-1",
		Timeout:    30 * time.Second,
		MaxGetSize: 10 * 1024 * 1024,
		MaxPutSize: 100 * 1024 * 1024,
	}

	// String fields
	c.Region = getStringDefault(cfg, "region", c.Region)
	c.Endpoint = getString(cfg, "endpoint")
	c.AccessKeyID = getString(cfg, "access_key_id")
	c.SecretAccessKey = getString(cfg, "secret_access_key")
	c.SessionToken = getString(cfg, "session_token")
	c.Profile = getString(cfg, "profile")
	c.ConnectionName = getString(cfg, "connection_name")
	c.BucketPrefix = getString(cfg, "bucket_prefix")

	// Bool fields
	c.UsePathStyle = getBool(cfg, "use_path_style")
	c.DisableSSL = getBool(cfg, "disable_ssl")
	c.ReadOnly = getBool(cfg, "read_only")

	// Timeout
	c.Timeout = getDuration(cfg, "timeout", c.Timeout)

	// Size limits
	c.MaxGetSize = getInt64(cfg, "max_get_size", c.MaxGetSize)
	c.MaxPutSize = getInt64(cfg, "max_put_size", c.MaxPutSize)

	return c, nil
}

// getString extracts a string value from a config map.
func getString(cfg map[string]any, key string) string {
	if v, ok := cfg[key].(string); ok {
		return v
	}
	return ""
}

// getStringDefault extracts a string value from a config map with a default.
func getStringDefault(cfg map[string]any, key, defaultVal string) string {
	if v, ok := cfg[key].(string); ok {
		return v
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

// getDuration extracts a duration value from a config map.
func getDuration(cfg map[string]any, key string, defaultVal time.Duration) time.Duration {
	if v, ok := cfg[key].(string); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	if v, ok := cfg[key].(int); ok {
		return time.Duration(v) * time.Second
	}
	if v, ok := cfg[key].(float64); ok {
		return time.Duration(v) * time.Second
	}
	return defaultVal
}

// getInt64 extracts an int64 value from a config map with a default.
func getInt64(cfg map[string]any, key string, defaultVal int64) int64 {
	if v, ok := cfg[key].(int); ok {
		return int64(v)
	}
	if v, ok := cfg[key].(float64); ok {
		return int64(v)
	}
	return defaultVal
}
