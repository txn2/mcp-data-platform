package platform

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// CurrentConfigVersion is the current config API version.
const CurrentConfigVersion = "v1"

// VersionStatus represents the lifecycle state of a config version.
type VersionStatus int

const (
	// VersionCurrent is an actively supported version.
	VersionCurrent VersionStatus = iota
	// VersionDeprecated is a version that still works but emits warnings.
	VersionDeprecated
	// VersionRemoved is a version that is no longer supported.
	VersionRemoved
)

// String returns a human-readable representation of the version status.
func (s VersionStatus) String() string {
	switch s {
	case VersionCurrent:
		return "current"
	case VersionDeprecated:
		return "deprecated"
	case VersionRemoved:
		return "removed"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// VersionConverter converts raw YAML bytes directly to the latest Config.
// A nil converter means the version uses standard YAML unmarshalling.
type VersionConverter func(data []byte) (*Config, error)

// VersionInfo describes a config API version.
type VersionInfo struct {
	// Version is the version string (e.g., "v1").
	Version string

	// Status is the lifecycle state of this version.
	Status VersionStatus

	// DeprecationMessage is shown when a deprecated version is loaded.
	DeprecationMessage string

	// MigrationGuide is shown when a removed version is loaded.
	MigrationGuide string

	// Converter transforms raw YAML bytes into a Config. Nil means
	// standard YAML unmarshalling is used (i.e., the version matches
	// the current schema).
	Converter VersionConverter
}

// VersionRegistry holds known config API versions.
type VersionRegistry struct {
	versions map[string]*VersionInfo
	current  string
}

// NewVersionRegistry creates an empty version registry.
func NewVersionRegistry() *VersionRegistry {
	return &VersionRegistry{
		versions: make(map[string]*VersionInfo),
	}
}

// Register adds a version to the registry. If current is empty and this is
// the first VersionCurrent entry, it becomes the current version.
func (r *VersionRegistry) Register(info *VersionInfo) {
	r.versions[info.Version] = info
	if info.Status == VersionCurrent && r.current == "" {
		r.current = info.Version
	}
}

// Get returns the version info for the given version string.
func (r *VersionRegistry) Get(version string) (*VersionInfo, bool) {
	info, ok := r.versions[version]
	return info, ok
}

// Current returns the current version string.
func (r *VersionRegistry) Current() string {
	return r.current
}

// ListSupported returns all non-removed version strings, sorted.
func (r *VersionRegistry) ListSupported() []string {
	var supported []string
	for v, info := range r.versions {
		if info.Status != VersionRemoved {
			supported = append(supported, v)
		}
	}
	sort.Strings(supported)
	return supported
}

// IsDeprecated returns true if the version exists and is deprecated.
func (r *VersionRegistry) IsDeprecated(version string) bool {
	info, ok := r.versions[version]
	return ok && info.Status == VersionDeprecated
}

// ConfigEnvelope is a minimal struct for peeking at the apiVersion field
// without parsing the full config.
type ConfigEnvelope struct {
	APIVersion string `yaml:"apiVersion"`
}

// PeekVersion extracts the apiVersion from raw YAML bytes.
// Returns "v1" if the field is missing or empty (backward compatibility).
func PeekVersion(data []byte) string {
	var envelope ConfigEnvelope
	if err := yaml.Unmarshal(data, &envelope); err != nil {
		return CurrentConfigVersion
	}
	if envelope.APIVersion == "" {
		return CurrentConfigVersion
	}
	return envelope.APIVersion
}

// DefaultRegistry returns the standard version registry with v1 registered.
func DefaultRegistry() *VersionRegistry {
	r := NewVersionRegistry()
	r.Register(&VersionInfo{
		Version:   "v1",
		Status:    VersionCurrent,
		Converter: nil, // v1 uses standard YAML unmarshalling
	})
	return r
}

// resolveVersion validates the config version against the registry and returns
// the version info. It returns an error for unknown or removed versions and
// logs a warning for deprecated versions.
func resolveVersion(reg *VersionRegistry, version string) (*VersionInfo, error) {
	info, ok := reg.Get(version)
	if !ok {
		supported := reg.ListSupported()
		return nil, fmt.Errorf(
			"unsupported config apiVersion %q; supported versions: %s",
			version, strings.Join(supported, ", "),
		)
	}
	if info.Status == VersionRemoved {
		if info.MigrationGuide != "" {
			return nil, fmt.Errorf(
				"config apiVersion %q has been removed; %s",
				version, info.MigrationGuide,
			)
		}
		return nil, fmt.Errorf("config apiVersion %q has been removed", version)
	}
	return info, nil
}
