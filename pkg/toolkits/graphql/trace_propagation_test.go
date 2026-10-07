package graphql

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseConfig_TracePropagationRoundTrips: the flag Parse reads reaches
// the shared transport config the client is built from (#1895).
func TestParseConfig_TracePropagationRoundTrips(t *testing.T) {
	on, err := ParseConfig(map[string]any{"endpoint_url": "https://gql.example.com/graphql"})
	require.NoError(t, err)
	require.True(t, on.TracePropagation)
	require.True(t, on.upstream().TracePropagation)

	off, err := ParseConfig(map[string]any{"endpoint_url": "https://gql.example.com/graphql", "trace_propagation": false})
	require.NoError(t, err)
	require.False(t, off.TracePropagation)
	require.False(t, off.upstream().TracePropagation)
}
