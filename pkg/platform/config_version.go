package platform

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// currentConfigVersion is the current config API version.
const currentConfigVersion = "v1"

// versionStatus represents the lifecycle state of a config version.
type versionStatus int

const (
	// versionCurrent is an actively supported version.
	versionCurrent versionStatus = iota
	// versionDeprecated is a version that still works but emits warnings.
	versionDeprecated
	// versionRemoved is a version that is no longer supported.
	versionRemoved
)

// String returns a human-readable representation of the version status.
func (s versionStatus) String() string {
	switch s {
	case versionCurrent:
		return "current"
	case versionDeprecated:
		return "deprecated"
	case versionRemoved:
		return "removed"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// versionConverter converts raw YAML bytes directly to the latest Config.
// A nil converter means the version uses standard YAML unmarshalling.
type versionConverter func(data []byte) (*Config, error)

// versionInfo describes a config API version.
type versionInfo struct {
	// Version is the version string (e.g., "v1").
	Version string

	// Status is the lifecycle state of this version.
	Status versionStatus

	// DeprecationMessage is shown when a deprecated version is loaded.
	DeprecationMessage string

	// MigrationGuide is shown when a removed version is loaded.
	MigrationGuide string

	// Converter transforms raw YAML bytes into a Config. Nil means
	// standard YAML unmarshalling is used (i.e., the version matches
	// the current schema).
	Converter versionConverter
}

// versionRegistry holds known config API versions.
type versionRegistry struct {
	versions map[string]*versionInfo
	current  string
}

// newVersionRegistry creates an empty version registry.
func newVersionRegistry() *versionRegistry {
	return &versionRegistry{
		versions: make(map[string]*versionInfo),
	}
}

// Register adds a version to the registry. If current is empty and this is
// the first versionCurrent entry, it becomes the current version.
func (r *versionRegistry) Register(info *versionInfo) {
	r.versions[info.Version] = info
	if info.Status == versionCurrent && r.current == "" {
		r.current = info.Version
	}
}

// Get returns the version info for the given version string.
func (r *versionRegistry) Get(version string) (*versionInfo, bool) {
	info, ok := r.versions[version]
	return info, ok
}

// Current returns the current version string.
func (r *versionRegistry) Current() string {
	return r.current
}

// ListSupported returns all non-removed version strings, sorted.
func (r *versionRegistry) ListSupported() []string {
	var supported []string
	for v, info := range r.versions {
		if info.Status != versionRemoved {
			supported = append(supported, v)
		}
	}
	sort.Strings(supported)
	return supported
}

// IsDeprecated returns true if the version exists and is deprecated.
func (r *versionRegistry) IsDeprecated(version string) bool {
	info, ok := r.versions[version]
	return ok && info.Status == versionDeprecated
}

// configEnvelope is a minimal struct for peeking at the apiVersion field
// without parsing the full config.
type configEnvelope struct {
	APIVersion string `yaml:"apiVersion"`
}

// peekVersion extracts the apiVersion from raw YAML bytes.
// Returns "v1" if the field is missing or empty (backward compatibility).
func peekVersion(data []byte) string {
	var envelope configEnvelope
	if err := yaml.Unmarshal(data, &envelope); err != nil {
		return currentConfigVersion
	}
	if envelope.APIVersion == "" {
		return currentConfigVersion
	}
	return envelope.APIVersion
}

// defaultRegistry returns the standard version registry with v1 registered.
func defaultRegistry() *versionRegistry {
	r := newVersionRegistry()
	r.Register(&versionInfo{
		Version:   "v1",
		Status:    versionCurrent,
		Converter: nil, // v1 uses standard YAML unmarshalling
	})
	return r
}

// resolveVersion validates the config version against the registry and returns
// the version info. It returns an error for unknown or removed versions and
// logs a warning for deprecated versions.
func resolveVersion(reg *versionRegistry, version string) (*versionInfo, error) {
	info, ok := reg.Get(version)
	if !ok {
		supported := reg.ListSupported()
		return nil, fmt.Errorf(
			"unsupported config apiVersion %q; supported versions: %s",
			version, strings.Join(supported, ", "),
		)
	}
	if info.Status == versionRemoved {
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
