package platform

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestScriptDestinations_NormalizesAndDefaultsTheKind proves the accessor the
// wiring reads hands the engine addresses in one canonical form: fields
// trimmed, the prefix without its slashes, and the kind defaulted to s3 —
// configuration declares only bucket destinations.
func TestScriptDestinations_NormalizesAndDefaultsTheKind(t *testing.T) {
	cfg := ScriptsConfig{Destinations: []script.Destination{
		{Name: " acme-drop ", Connection: " acme-s3", Bucket: "acme-exports ", Prefix: "/weekly/"},
	}}

	got := cfg.ScriptDestinations()

	require.Len(t, got, 1)
	assert.Equal(t, script.Destination{
		Name: "acme-drop", Kind: script.DestinationKindS3,
		Connection: "acme-s3", Bucket: "acme-exports", Prefix: "weekly",
	}, got[0])
}

// TestConfigValidate_ScriptDestinations proves the startup validation refuses
// the declarations a run could not honor: an incomplete address, a duplicated
// name, and a redeclaration of the built-in portal.
func TestConfigValidate_ScriptDestinations(t *testing.T) {
	base := func(destinations ...script.Destination) *Config {
		return &Config{Scripts: ScriptsConfig{Destinations: destinations}}
	}

	tests := []struct {
		name    string
		cfg     *Config
		wantErr string
	}{
		{
			name: "a complete declaration is accepted",
			cfg: base(script.Destination{
				Name: "acme-drop", Connection: "acme-s3", Bucket: "acme-exports", Prefix: "weekly",
			}),
		},
		{
			name: "no declarations at all is the ordinary state",
			cfg:  base(),
		},
		{
			name:    "a destination without a connection is refused",
			cfg:     base(script.Destination{Name: "acme-drop", Bucket: "acme-exports"}),
			wantErr: "must name the platform connection",
		},
		{
			name:    "a destination without a bucket is refused",
			cfg:     base(script.Destination{Name: "acme-drop", Connection: "acme-s3"}),
			wantErr: "must name a bucket",
		},
		{
			name: "one name declared twice is refused",
			cfg: base(
				script.Destination{Name: "acme-drop", Connection: "acme-s3", Bucket: "a"},
				script.Destination{Name: "acme-drop", Connection: "other-s3", Bucket: "b"},
			),
			wantErr: "declared twice",
		},
		{
			name:    "the reserved portal name is refused as a bucket",
			cfg:     base(script.Destination{Name: "portal", Connection: "acme-s3", Bucket: "a"}),
			wantErr: "reserved",
		},
		{
			name: "the portal kind cannot be declared: it is built in",
			cfg: base(script.Destination{
				Name: "portal", Kind: script.DestinationKindPortal,
			}),
			wantErr: "built in",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Contains(t, err.Error(), "scripts.destinations")
		})
	}
}
