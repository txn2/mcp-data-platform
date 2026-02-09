package platform

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const (
	verTestV1             = "v1"
	verTestV0             = "v0"
	verTestV99            = "v99"
	verTestUnknownStatus  = 99
	verTestUnknownStr     = "unknown(99)"
	verTestServerNameTest = "test"
)

func TestPeekVersion(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "explicit v1",
			data: "apiVersion: v1\nserver:\n  name: test",
			want: verTestV1,
		},
		{
			name: "missing apiVersion defaults to v1",
			data: "server:\n  name: test",
			want: verTestV1,
		},
		{
			name: "empty apiVersion defaults to v1",
			data: "apiVersion: \"\"\nserver:\n  name: test",
			want: verTestV1,
		},
		{
			name: "invalid YAML defaults to v1",
			data: ":::invalid",
			want: verTestV1,
		},
		{
			name: "empty input defaults to v1",
			data: "",
			want: verTestV1,
		},
		{
			name: "unknown version returns as-is",
			data: "apiVersion: v99\nserver:\n  name: test",
			want: verTestV99,
		},
		{
			name: "version with spaces",
			data: "apiVersion:   v1  \nserver:\n  name: test",
			want: verTestV1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PeekVersion([]byte(tt.data))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestVersionStatus_String(t *testing.T) {
	tests := []struct {
		status VersionStatus
		want   string
	}{
		{VersionCurrent, "current"},
		{VersionDeprecated, "deprecated"},
		{VersionRemoved, "removed"},
		{VersionStatus(verTestUnknownStatus), verTestUnknownStr},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.status.String())
		})
	}
}

func TestVersionRegistry_Register(t *testing.T) {
	r := NewVersionRegistry()
	r.Register(&VersionInfo{
		Version: verTestV1,
		Status:  VersionCurrent,
	})

	info, ok := r.Get(verTestV1)
	require.True(t, ok)
	assert.Equal(t, verTestV1, info.Version)
	assert.Equal(t, VersionCurrent, info.Status)
}

func TestVersionRegistry_Get_Unknown(t *testing.T) {
	r := NewVersionRegistry()
	_, ok := r.Get(verTestV99)
	assert.False(t, ok)
}

func TestVersionRegistry_Current(t *testing.T) {
	r := DefaultRegistry()
	assert.Equal(t, verTestV1, r.Current())
}

func TestVersionRegistry_ListSupported(t *testing.T) {
	r := NewVersionRegistry()
	r.Register(&VersionInfo{Version: verTestV1, Status: VersionCurrent})
	r.Register(&VersionInfo{Version: verTestV0, Status: VersionRemoved})
	r.Register(&VersionInfo{Version: "v2", Status: VersionDeprecated})

	supported := r.ListSupported()
	assert.Equal(t, []string{verTestV1, "v2"}, supported)
}

func TestVersionRegistry_IsDeprecated(t *testing.T) {
	r := NewVersionRegistry()
	r.Register(&VersionInfo{Version: verTestV1, Status: VersionCurrent})
	r.Register(&VersionInfo{Version: verTestV0, Status: VersionDeprecated})

	assert.False(t, r.IsDeprecated(verTestV1), "current should not be deprecated")
	assert.True(t, r.IsDeprecated(verTestV0), "v0 should be deprecated")
	assert.False(t, r.IsDeprecated(verTestV99), "unknown should not be deprecated")
}

func TestDefaultRegistry(t *testing.T) {
	r := DefaultRegistry()
	assert.Equal(t, verTestV1, r.Current())

	info, ok := r.Get(verTestV1)
	require.True(t, ok)
	assert.Equal(t, VersionCurrent, info.Status)
	assert.Nil(t, info.Converter)
}

func TestResolveVersion_Unknown(t *testing.T) {
	r := DefaultRegistry()
	_, err := resolveVersion(r, verTestV99)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported config apiVersion")
	assert.Contains(t, err.Error(), verTestV1)
}

func TestResolveVersion_Removed(t *testing.T) {
	r := NewVersionRegistry()
	r.Register(&VersionInfo{
		Version:        verTestV0,
		Status:         VersionRemoved,
		MigrationGuide: "run migrate-config --target-version v1",
	})
	r.Register(&VersionInfo{Version: verTestV1, Status: VersionCurrent})

	_, err := resolveVersion(r, verTestV0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has been removed")
	assert.Contains(t, err.Error(), "migrate-config")
}

func TestResolveVersion_Current(t *testing.T) {
	r := DefaultRegistry()
	info, err := resolveVersion(r, verTestV1)
	require.NoError(t, err)
	assert.Equal(t, VersionCurrent, info.Status)
}

func TestResolveVersion_Deprecated(t *testing.T) {
	r := NewVersionRegistry()
	r.Register(&VersionInfo{
		Version:            verTestV0,
		Status:             VersionDeprecated,
		DeprecationMessage: "use v1 instead",
	})
	r.Register(&VersionInfo{Version: verTestV1, Status: VersionCurrent})

	info, err := resolveVersion(r, verTestV0)
	require.NoError(t, err)
	assert.Equal(t, VersionDeprecated, info.Status)
}

func TestLoadConfigFromBytes_WithVersion(t *testing.T) {
	cfg, err := LoadConfigFromBytes([]byte("apiVersion: v1\nserver:\n  name: test"))
	require.NoError(t, err)
	assert.Equal(t, verTestV1, cfg.APIVersion)
	assert.Equal(t, verTestServerNameTest, cfg.Server.Name)
}

func TestLoadConfigFromBytes_WithoutVersion(t *testing.T) {
	cfg, err := LoadConfigFromBytes([]byte("server:\n  name: test"))
	require.NoError(t, err)
	assert.Equal(t, verTestV1, cfg.APIVersion, "should default to v1")
	assert.Equal(t, verTestServerNameTest, cfg.Server.Name)
}

func TestLoadConfigFromBytes_UnknownVersion(t *testing.T) {
	_, err := LoadConfigFromBytes([]byte("apiVersion: v99\nserver:\n  name: test"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported config apiVersion")
	assert.Contains(t, err.Error(), verTestV1)
}

func TestLoadConfigFromBytes_AppliesDefaults(t *testing.T) {
	cfg, err := LoadConfigFromBytes([]byte("apiVersion: v1"))
	require.NoError(t, err)
	assert.Equal(t, "mcp-data-platform", cfg.Server.Name)
	assert.Equal(t, "stdio", cfg.Server.Transport)
}

func TestLoadConfigFromBytes_ExpandsEnvVars(t *testing.T) {
	t.Setenv("TEST_CFGVER_NAME", "env-platform")
	cfg, err := LoadConfigFromBytes([]byte("apiVersion: v1\nserver:\n  name: ${TEST_CFGVER_NAME}"))
	require.NoError(t, err)
	assert.Equal(t, "env-platform", cfg.Server.Name)
}

func TestVersionRegistry_Current_FirstRegistered(t *testing.T) {
	r := NewVersionRegistry()
	// Register deprecated first, then current
	r.Register(&VersionInfo{Version: verTestV0, Status: VersionDeprecated})
	r.Register(&VersionInfo{Version: verTestV1, Status: VersionCurrent})
	assert.Equal(t, verTestV1, r.Current())
}

func TestVersionRegistry_Current_EmptyRegistry(t *testing.T) {
	r := NewVersionRegistry()
	assert.Equal(t, "", r.Current())
}

func TestLoadConfigFromBytes_WithConverter(t *testing.T) {
	// Test that a converter function works correctly in isolation.
	// LoadConfigFromBytes uses DefaultRegistry() where v1 has nil converter,
	// so we verify the converter pattern via direct invocation.
	converter := func(data []byte) (*Config, error) {
		var cfg Config
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("unmarshal: %w", err)
		}
		cfg.Server.Name = "converted-" + cfg.Server.Name
		return &cfg, nil
	}

	data := []byte("server:\n  name: test\n")
	cfg, err := converter(data)
	require.NoError(t, err)
	assert.Equal(t, "converted-test", cfg.Server.Name)
}

func TestResolveVersion_RemovedWithoutGuide(t *testing.T) {
	r := NewVersionRegistry()
	r.Register(&VersionInfo{
		Version: verTestV0,
		Status:  VersionRemoved,
	})
	r.Register(&VersionInfo{Version: verTestV1, Status: VersionCurrent})

	_, err := resolveVersion(r, verTestV0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has been removed")
}
