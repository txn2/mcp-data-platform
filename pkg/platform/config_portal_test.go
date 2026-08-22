package platform

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
