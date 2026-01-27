package datahub

import "time"

// LineageConfig controls lineage-aware semantic enrichment.
type LineageConfig struct {
	// Enabled activates lineage traversal for missing documentation.
	Enabled bool `yaml:"enabled"`

	// MaxHops limits upstream traversal depth. Range: 1-5. Default: 2.
	MaxHops int `yaml:"max_hops"`

	// Inherit specifies which metadata types to inherit.
	// Valid: "glossary_terms", "descriptions", "tags"
	Inherit []string `yaml:"inherit"`

	// ConflictResolution determines behavior when multiple upstreams
	// define metadata for the same column.
	// Values: "nearest" (closest upstream wins), "all" (merge), "skip" (no inheritance on conflict)
	ConflictResolution string `yaml:"conflict_resolution"`

	// PreferColumnLineage uses DataHub's column-level lineage edges when available.
	PreferColumnLineage bool `yaml:"prefer_column_lineage"`

	// ColumnTransforms defines path normalization rules.
	ColumnTransforms []ColumnTransformConfig `yaml:"column_transforms"`

	// Aliases defines explicit source-target mappings that bypass lineage lookup.
	Aliases []AliasConfig `yaml:"aliases"`

	// CacheTTL for lineage graphs.
	CacheTTL time.Duration `yaml:"cache_ttl"`

	// Timeout for the entire inheritance operation.
	Timeout time.Duration `yaml:"timeout"`
}

// ColumnTransformConfig defines a path normalization rule.
type ColumnTransformConfig struct {
	// TargetPattern is a glob pattern matching target dataset names.
	TargetPattern string `yaml:"target_pattern"`

	// StripPrefix removes this prefix from target column names.
	StripPrefix string `yaml:"strip_prefix,omitempty"`

	// StripSuffix removes this suffix from target column names.
	StripSuffix string `yaml:"strip_suffix,omitempty"`
}

// AliasConfig defines an explicit source-target relationship.
type AliasConfig struct {
	// Source is the fully-qualified source table name.
	Source string `yaml:"source"`

	// Targets are glob patterns matching target table names.
	Targets []string `yaml:"targets"`

	// ColumnMapping provides explicit column name mappings.
	// Key: target column, Value: source column
	ColumnMapping map[string]string `yaml:"column_mapping,omitempty"`
}

// DefaultLineageConfig returns sensible defaults.
func DefaultLineageConfig() LineageConfig {
	return LineageConfig{
		Enabled:             false,
		MaxHops:             2,
		Inherit:             []string{"glossary_terms", "descriptions"},
		ConflictResolution:  "nearest",
		PreferColumnLineage: true,
		CacheTTL:            10 * time.Minute,
		Timeout:             5 * time.Second,
	}
}

// shouldInherit checks if a metadata type should be inherited.
func (c *LineageConfig) shouldInherit(metadataType string) bool {
	for _, t := range c.Inherit {
		if t == metadataType {
			return true
		}
	}
	return false
}
