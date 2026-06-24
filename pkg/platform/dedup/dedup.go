// Package dedup holds the session-level metadata deduplication config, split out
// of pkg/platform to keep that package under its size budget (#594).
package dedup

import "time"

// Config configures session-level metadata deduplication: it avoids repeating
// large semantic metadata blocks for previously-enriched tables within the same
// client session, saving LLM context tokens.
type Config struct {
	// Enabled controls whether session dedup is active. Defaults to true.
	Enabled *bool `yaml:"enabled"`

	// Mode controls what is sent for previously-enriched tables.
	// Values: "reference" (default), "summary", "none".
	Mode string `yaml:"mode"`

	// EntryTTL is how long a table's enrichment is considered fresh.
	// Defaults to the semantic cache TTL (typically 5m).
	EntryTTL time.Duration `yaml:"entry_ttl"`

	// SessionTimeout is how long an idle session persists before cleanup.
	// Defaults to the server's streamable session timeout (typically 30m).
	SessionTimeout time.Duration `yaml:"session_timeout"`
}

// IsEnabled returns whether session dedup is enabled, defaulting to true.
func (c *Config) IsEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// EffectiveMode returns the dedup mode, defaulting to "reference".
func (c *Config) EffectiveMode() string {
	if c.Mode == "" {
		return "reference"
	}
	return c.Mode
}
