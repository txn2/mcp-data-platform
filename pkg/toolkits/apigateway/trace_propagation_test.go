package apigateway

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseConfig_TracePropagationRoundTrips: the flag Parse reads reaches
// the shared transport config the client is built from, in both the default
// and the turned-off form (#1895). The two copies, configFromUpstream and
// upstream(), are what a dropped field hides in.
func TestParseConfig_TracePropagationRoundTrips(t *testing.T) {
	on, err := ParseConfig(map[string]any{"base_url": "https://api.example.com"})
	require.NoError(t, err)
	require.True(t, on.TracePropagation)
	require.True(t, on.upstream().TracePropagation)

	off, err := ParseConfig(map[string]any{"base_url": "https://api.example.com", "trace_propagation": false})
	require.NoError(t, err)
	require.False(t, off.TracePropagation)
	require.False(t, off.upstream().TracePropagation)
}
