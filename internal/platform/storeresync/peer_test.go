package storeresync

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/connstate"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

func peerRig(t *testing.T, names ...string) (*registry.Registry, *apigatewaykit.Toolkit) {
	t.Helper()
	reg := registry.NewRegistry()
	tk := apigatewaykit.New("api")
	require.NoError(t, reg.Register(tk))
	for _, n := range names {
		require.NoError(t, tk.AddConnection(n, map[string]any{"base_url": "https://x.example.com", "description": "old"}))
	}
	return reg, tk
}

// A peer's delete removes a stored connection and keeps one the file declares.
func TestRemoved(t *testing.T) {
	reg, tk := peerRig(t, "stored", "declared")
	Removed(reg, declares{"declared": true}, "api", "stored", "removing")
	Removed(reg, declares{"declared": true}, "api", "declared", "removing")
	assert.False(t, tk.HasConnection("stored"))
	assert.True(t, tk.HasConnection("declared"))
	Removed(reg, nil, "api", "absent", "removing") // nothing to remove
}

// A peer's save is adopted when the store has it, removed when the store does
// not, and left alone when the store cannot be read (#885).
func TestUpserted(t *testing.T) {
	reg, tk := peerRig(t, "changed", "gone", "kept")
	Upserted(reg, nil, "api", "changed", Read{Found: true, Config: map[string]any{"base_url": "https://x.example.com", "description": "new"}})
	Upserted(reg, nil, "api", "gone", Read{})
	Upserted(reg, nil, "api", "kept", Read{Err: errors.New("db down")})
	Upserted(reg, nil, "api", "broken", Read{Found: true, Config: map[string]any{"base_url": "::not a url::"}})

	assert.Equal(t, "new", description(t, tk, "changed"))
	assert.False(t, tk.HasConnection("gone"))
	assert.Equal(t, "old", description(t, tk, "kept"))
}

// A catalog change rebuilds the connections that mount it.
func TestCatalog(t *testing.T) {
	reg, tk := peerRig(t, "plain")
	require.NoError(t, reg.Register(graphqlkit.NewMulti(graphqlkit.MultiConfig{})))
	Catalog(reg.All(), "")             // no catalog named: nothing to rebuild
	Catalog(reg.All(), "some-catalog") // no connection mounts it
	assert.True(t, tk.HasConnection("plain"))
}

// A peer's save drops what calls found the connection in, and a read failure,
// which keeps the live config, keeps its state too (#2444).
func TestUpsertedForgetsTheSavedConnectionsState(t *testing.T) {
	reg, _ := peerRig(t, "saved-2444", "unread-2444")
	connstate.Observe("api", "saved-2444", connstate.AuthFailed)
	connstate.Observe("api", "unread-2444", connstate.AuthFailed)
	Upserted(reg, nil, "api", "saved-2444", Read{Found: true, Config: map[string]any{"base_url": "https://x.example.com"}})
	Upserted(reg, nil, "api", "unread-2444", Read{Err: errors.New("db down")})
	if s, seen := connstate.State("api", "saved-2444"); seen {
		t.Errorf("a peer's save kept the old state %q", s)
	}
	if s, _ := connstate.State("api", "unread-2444"); s != connstate.AuthFailed {
		t.Errorf("a failed read changed the state to %q", s)
	}
}
