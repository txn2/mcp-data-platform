package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/platform/utilconn"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform/personastore"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// listedConnStore is a persistent connection store whose List answers with a
// fixed set of rows, which is what a replica reads when it re-syncs.
type listedConnStore struct {
	fakeConnStore
	rows    []ConnectionInstance
	listErr error
}

func (s listedConnStore) List(context.Context) ([]ConnectionInstance, error) {
	return s.rows, s.listErr
}
func (listedConnStore) Persistent() bool { return true }

// resyncFixture is a replica serving four api connections: one the file
// declares, one the platform registers itself, one the store still holds with
// a changed base URL, and one the store no longer holds. The store also holds
// a fifth this replica never took on.
func resyncFixture(t *testing.T, listErr error) (*Platform, *apigatewaykit.Toolkit) {
	t.Helper()
	reg := registry.NewRegistry()
	apiTk := apigatewaykit.New("api")
	if err := reg.Register(apiTk); err != nil {
		t.Fatalf("register toolkit: %v", err)
	}
	cfg := &Config{Toolkits: map[string]any{
		"api": map[string]any{
			"enabled": true,
			"instances": map[string]any{
				"declared": map[string]any{"base_url": "https://declared.example.com"},
			},
		},
	}}
	cfg.SnapshotDeclaredConnections()
	for _, name := range []string{"declared", adminSelfConnectionName, "changed", "deleted"} {
		if err := apiTk.AddConnection(name, map[string]any{"base_url": "https://x.example.com", "description": "old"}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	return &Platform{
		config:          cfg,
		toolkitRegistry: reg,
		connectionStore: listedConnStore{listErr: listErr, rows: []ConnectionInstance{
			{Kind: "api", Name: "declared", Config: map[string]any{}},
			{Kind: "api", Name: "changed", Config: map[string]any{"base_url": "https://x.example.com", "description": "new"}},
			{Kind: "api", Name: "created", Config: map[string]any{"base_url": "https://x.example.com", "description": "created"}},
		}},
	}, apiTk
}

func descriptionOf(t *testing.T, tk *apigatewaykit.Toolkit, name string) string {
	t.Helper()
	for _, c := range tk.ListConnections() {
		if c.Name == name {
			return c.Description
		}
	}
	t.Fatalf("connection %s is not served", name)
	return ""
}

// TestResyncConnections_MatchesTheStore covers #1902: after the reload
// channel reconnects, a replica serves what the store holds, as a restart
// would, without disturbing what the file declares or the platform registered.
func TestResyncConnections_MatchesTheStore(t *testing.T) {
	p, apiTk := resyncFixture(t, nil)

	resyncFromStore(p)

	if apiTk.HasConnection("deleted") {
		t.Error("a connection the store no longer holds should be removed")
	}
	if got := descriptionOf(t, apiTk, "changed"); got != "new" {
		t.Errorf("changed connection description = %q, want the stored one", got)
	}
	if got := descriptionOf(t, apiTk, "created"); got != "created" {
		t.Errorf("created connection description = %q, want the stored one", got)
	}
	if got := descriptionOf(t, apiTk, "declared"); got != "old" {
		t.Errorf("file-declared connection description = %q, want it left as the file set it", got)
	}
	if !apiTk.HasConnection(adminSelfConnectionName) {
		t.Error("a connection the platform registers itself must not be removed")
	}
}

// A store that cannot be read changes nothing.
func TestResyncConnections_ListFailureKeepsLiveConfig(t *testing.T) {
	p, apiTk := resyncFixture(t, errors.New("db unavailable"))

	resyncFromStore(p)

	if !apiTk.HasConnection("deleted") {
		t.Error("a failed store read must not remove live connections")
	}
	if got := descriptionOf(t, apiTk, "changed"); got != "old" {
		t.Errorf("changed connection description = %q, want the live one kept", got)
	}
}

// A store with no durable backing has nothing to re-sync from.
func TestResyncConnections_NonPersistentStoreIsSkipped(t *testing.T) {
	p, apiTk := resyncFixture(t, nil)
	p.connectionStore = fakeConnStore{}

	resyncFromStore(p)

	if !apiTk.HasConnection("deleted") {
		t.Error("a non-persistent store must not drive removals")
	}
}

func TestPlatformRegisteredConnection(t *testing.T) {
	for _, tc := range []struct {
		kind, name string
		want       bool
	}{
		{apigatewaykit.Kind, utilconn.ConnectionName, true},
		{apigatewaykit.Kind, adminSelfConnectionName, true},
		{apigatewaykit.Kind, "stored", false},
		{"mcp", utilconn.ConnectionName, false},
	} {
		if got := platformRegisteredConnection(tc.kind, tc.name); got != tc.want {
			t.Errorf("platformRegisteredConnection(%q, %q) = %v, want %v", tc.kind, tc.name, got, tc.want)
		}
	}
}

// TestLoadDBPersonas_DropsDeletedPersonas covers the persona half of #1902. A
// persona deleted on another replica is gone from the store; this replica
// removes it, or reverts it to the file's definition when the file has one,
// as the replica that deleted it did. Personas the file alone defines stay.
func TestLoadDBPersonas_DropsDeletedPersonas(t *testing.T) {
	reg := persona.NewRegistry()
	for _, per := range []*persona.Persona{
		{Name: "db-only", DisplayName: "DB", Source: SourceDatabase},
		{Name: "overridden", DisplayName: "DB override", Source: SourceBoth},
		{Name: "file-only", DisplayName: "File", Source: SourceFile},
		{Name: "kept", DisplayName: "Old", Source: SourceDatabase},
	} {
		if err := reg.Register(per); err != nil {
			t.Fatalf("register %s: %v", per.Name, err)
		}
	}
	p := &Platform{
		config: &Config{Personas: PersonasConfig{Definitions: map[string]PersonaDef{
			"overridden": {DisplayName: "From file"},
			"file-only":  {DisplayName: "File"},
		}}},
		personaRegistry: reg,
		personaStore: &mockPersonaStoreForTest{defs: []personastore.Definition{
			{Name: "kept", DisplayName: "New"},
		}},
	}

	p.reloadPersonaLocal()

	if _, ok := reg.Get("db-only"); ok {
		t.Error("a database persona the store no longer holds should be removed")
	}
	if got, ok := reg.Get("overridden"); !ok || got.DisplayName != "From file" || got.Source != SourceFile {
		t.Errorf("overridden persona = %+v, want the file definition back", got)
	}
	if _, ok := reg.Get("file-only"); !ok {
		t.Error("a persona the file alone defines must stay")
	}
	if got, ok := reg.Get("kept"); !ok || got.DisplayName != "New" {
		t.Errorf("kept persona = %+v, want the stored definition", got)
	}
}

// TestResyncFromStore_RunsEveryPass pins that the reconnect handler re-reads
// connections, personas and API keys, not one of them.
func TestResyncFromStore_RunsEveryPass(t *testing.T) {
	p, apiTk := resyncFixture(t, nil)
	reg := persona.NewRegistry()
	if err := reg.Register(&persona.Persona{Name: "gone", Source: SourceDatabase}); err != nil {
		t.Fatalf("register: %v", err)
	}
	p.personaRegistry = reg
	p.personaStore = &mockPersonaStoreForTest{}

	resyncFromStore(p)

	if apiTk.HasConnection("deleted") {
		t.Error("resync did not run the connection pass")
	}
	if _, ok := reg.Get("gone"); ok {
		t.Error("resync did not run the persona pass")
	}
}
