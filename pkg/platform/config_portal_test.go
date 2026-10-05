package platform

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
)

// TestConfigValidate_PortalMaxVersions proves the startup validation refuses a
// negative deployment retention default. Every per-asset entry point refuses a
// negative value and the column carries a non-negative CHECK, but the
// deployment default reaches the prune with no gate in front of it (#1421).
func TestConfigValidate_PortalMaxVersions(t *testing.T) {
	maxVersions := func(n int) *Config {
		return &Config{Portal: PortalConfig{MaxVersions: &n}}
	}

	tests := []struct {
		name    string
		cfg     *Config
		wantErr string
	}{
		{name: "unset is the ordinary state", cfg: &Config{}},
		{name: "zero keeps every version", cfg: maxVersions(0)},
		{name: "a positive cap is accepted", cfg: maxVersions(100)},
		{
			name:    "a negative cap is refused",
			cfg:     maxVersions(-1),
			wantErr: "portal.max_versions must be 0",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestConfig_PortalAssetRefsMax is #2021's configuration criterion: the key is
// parsed from YAML, an absent key resolves to the default, and a value outside
// 1..100 is refused at startup with the key named.
func TestConfig_PortalAssetRefsMax(t *testing.T) {
	cfg, err := LoadConfigFromBytes([]byte("portal:\n  asset_refs:\n    max: 40\n"))
	require.NoError(t, err)
	assert.Equal(t, 40, cfg.Portal.AssetRefs.Max)
	assert.Equal(t, 40, cfg.Portal.AssetRefs.Resolved())

	unset, err := LoadConfigFromBytes([]byte("portal:\n  title: x\n"))
	require.NoError(t, err)
	assert.Equal(t, 20, unset.Portal.AssetRefs.Resolved(), "an absent key keeps the default")

	for _, bad := range []int{-1, 101} {
		c := &Config{Portal: PortalConfig{AssetRefs: assetrefs.Config{Max: bad}}}
		err := c.Validate()
		require.Error(t, err, "%d", bad)
		assert.Contains(t, err.Error(), "portal.asset_refs.max must be between 1 and 100")
		assert.Equal(t, 20, c.Portal.AssetRefs.Resolved(), "an invalid value never reaches a consumer as itself")
	}
	require.NoError(t, (&Config{Portal: PortalConfig{AssetRefs: assetrefs.Config{Max: 100}}}).Validate())
}

// TestConfig_PortalAssetRefsStrict proves strict parsing refuses a misspelled key
// under asset_refs rather than leaving the cap silently at its default.
func TestConfig_PortalAssetRefsStrict(t *testing.T) {
	_, err := LoadConfigFromBytes([]byte("config:\n  strict: true\nportal:\n  asset_refs:\n    maximum: 40\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maximum")
}
