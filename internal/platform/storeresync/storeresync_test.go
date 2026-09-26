package storeresync

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/personacfg"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// declares is a configuration file that declares a fixed set of api
// connections.
type declares map[string]bool

func (d declares) DeclaresConnection(kind, name string) bool { return kind == "api" && d[name] }

func description(t *testing.T, tk *apigatewaykit.Toolkit, name string) string {
	t.Helper()
	for _, c := range tk.ListConnections() {
		if c.Name == name {
			return c.Description
		}
	}
	t.Fatalf("connection %s is not served", name)
	return ""
}

// TestConnections covers #1902: after a resync a replica serves what the
// store holds, as a restart would, and leaves what the file declares and what
// the platform registers itself.
func TestConnections(t *testing.T) {
	reg := registry.NewRegistry()
	tk := apigatewaykit.New("api")
	require.NoError(t, reg.Register(tk))
	for _, name := range []string{"declared", "builtin", "changed", "deleted"} {
		require.NoError(t, tk.AddConnection(name, map[string]any{"base_url": "https://x.example.com", "description": "old"}))
	}
	stored := []StoredConnection{
		{Kind: "api", Name: "declared", Config: map[string]any{}},
		{Kind: "api", Name: "changed", Config: map[string]any{"base_url": "https://x.example.com", "description": "new"}},
		{Kind: "api", Name: "created", Config: map[string]any{"base_url": "https://x.example.com", "description": "created"}},
		{Kind: "api", Name: "broken", Config: map[string]any{"base_url": "::not a url::"}},
	}
	builtin := func(kind, name string) bool { return kind == "api" && name == "builtin" }

	Connections(stored, reg, declares{"declared": true}, builtin)

	assert.False(t, tk.HasConnection("deleted"), "a connection the store no longer holds is removed")
	assert.Equal(t, "new", description(t, tk, "changed"))
	assert.Equal(t, "created", description(t, tk, "created"))
	assert.Equal(t, "old", description(t, tk, "declared"), "the file's connection is left as it is")
	assert.True(t, tk.HasConnection("builtin"), "a connection the platform registers itself is kept")

	Connections(nil, nil, nil, builtin) // no toolkits: nothing to do
}

// def is a stored persona definition, as the persona store's is.
type def struct{ Name, DisplayName string }

func (d *def) ToPersona() *persona.Persona {
	return &persona.Persona{Name: d.Name, DisplayName: d.DisplayName}
}

type fakePersonaStore struct {
	defs []def
	err  error
}

func (f fakePersonaStore) List(context.Context) ([]def, error) { return f.defs, f.err }

var sources = Sources{File: "file", Database: "database", Both: "both"}

// TestPersonas covers the persona half of #1902: stored definitions are
// registered, and a database persona no longer stored is removed or reverted
// to the file's definition; file-only personas stay.
func TestPersonas(t *testing.T) {
	reg := persona.NewRegistry()
	for _, per := range []*persona.Persona{
		{Name: "db-only", Source: "database"},
		{Name: "overridden", DisplayName: "DB override", Source: "both"},
		{Name: "file-only", Source: "file"},
	} {
		require.NoError(t, reg.Register(per))
	}
	file := map[string]personacfg.PersonaDef{"overridden": {DisplayName: "From file"}, "file-only": {}, "stored-over-file": {}}
	store := fakePersonaStore{defs: []def{
		{Name: "stored", DisplayName: "Stored"},
		{Name: "stored-over-file", DisplayName: "Stored over file"},
	}}

	Personas[def](context.Background(), store, reg, file, sources)

	_, ok := reg.Get("db-only")
	assert.False(t, ok, "a database persona no longer stored is removed")
	got, _ := reg.Get("overridden")
	assert.Equal(t, "From file", got.DisplayName, "reverted to the file's definition")
	assert.Equal(t, "file", got.Source)
	_, ok = reg.Get("file-only")
	assert.True(t, ok)
	got, _ = reg.Get("stored")
	assert.Equal(t, "database", got.Source)
	got, _ = reg.Get("stored-over-file")
	assert.Equal(t, "both", got.Source)
}

func TestPersonasUnreadableStoreChangesNothing(t *testing.T) {
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{Name: "db-only", Source: "database"}))
	Personas[def](context.Background(), fakePersonaStore{err: errors.New("down")}, reg, nil, sources)
	_, ok := reg.Get("db-only")
	assert.True(t, ok)
	Personas[def](context.Background(), nil, reg, nil, sources)                // no store
	Personas[def](context.Background(), fakePersonaStore{}, nil, nil, sources) // no registry
}
