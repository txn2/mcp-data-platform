package graphql

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolkitIdentity(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	if tk.Kind() != Kind || tk.Kind() != "graphql" {
		t.Errorf("kind = %q", tk.Kind())
	}
	if tk.Name() != "gql" || tk.Connection() != "gql" {
		t.Errorf("name/connection = %q/%q", tk.Name(), tk.Connection())
	}
	// The provider setters exist to satisfy the registry contract; a
	// GraphQL endpoint carries its own schema and is not a table catalog.
	tk.SetSemanticProvider(nil)
	tk.SetQueryProvider(nil)
	if err := tk.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

func TestToolsListsExportOnlyWhenItCanRun(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	if got := tk.Tools(); len(got) != 2 || got[0] != ToolDiscover || got[1] != ToolQuery {
		t.Fatalf("tools = %v; export is absent until the platform wires it", got)
	}
	tk.SetExportDeps(ExportDeps{AssetStore: &fakeAssets{}})
	if got := tk.Tools(); len(got) != 3 || got[2] != ToolExport {
		t.Errorf("tools = %v; want export once wired", got)
	}
}

func TestNewMultiSkipsAConnectionThatCannotMaterialize(t *testing.T) {
	// An auth mode ParseConfig would have refused can still reach the
	// constructor through a hand-built Config, and one connection whose
	// authenticator cannot be built must not stop the others registering.
	tk := NewMulti(MultiConfig{DefaultName: "good", Instances: map[string]Config{
		"good": {EndpointURL: "https://x", AuthMode: AuthModeNone},
		"bad":  {EndpointURL: "https://x", AuthMode: "nonsense"},
	}})
	if !tk.HasConnection("good") {
		t.Error("the valid connection did not register")
	}
	if tk.HasConnection("bad") {
		t.Error("a connection with an unknown auth mode registered")
	}
}

func TestAddAndRemoveConnection(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)

	if err := tk.AddConnection("second", map[string]any{"endpoint_url": u.server.URL}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if !tk.HasConnection("second") {
		t.Error("the added connection is not registered")
	}
	if err := tk.AddConnection("second", map[string]any{"endpoint_url": u.server.URL}); !errors.Is(err, ErrConnectionExists) {
		t.Errorf("re-adding gave %v; want ErrConnectionExists", err)
	}
	if err := tk.AddConnection("bad", map[string]any{}); err == nil {
		t.Error("a connection with no endpoint was added")
	}
	if err := tk.RemoveConnection("second"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := tk.RemoveConnection("second"); !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("removing twice gave %v; want ErrConnectionNotFound", err)
	}
}

func TestRemoveConnectionDropsTheStoredSchema(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	store := newMemorySchemaStore()
	tk.SetSchemaStore(store)
	if err := tk.SetSchema(context.Background(), "gql", fixtureSDL(t, "flat")); err != nil {
		t.Fatalf("set schema: %v", err)
	}
	if _, err := store.GetSchema(context.Background(), "gql"); err != nil {
		t.Fatalf("the schema was not stored: %v", err)
	}
	if err := tk.RemoveConnection("gql"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	// A connection that is gone must not leave an index behind for a
	// later connection of the same name to inherit.
	if _, err := store.GetSchema(context.Background(), "gql"); !errors.Is(err, ErrSchemaNotFound) {
		t.Errorf("the stored schema outlived its connection: %v", err)
	}
}

func TestListConnectionsReportsWhatAnOperatorNeeds(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", map[string]any{"description": "Metadata service."})
	details := tk.ListConnections()
	if len(details) != 1 {
		t.Fatalf("connections = %+v", details)
	}
	d := details[0]
	if d.Name != "gql" || !d.IsDefault {
		t.Errorf("detail = %+v", d)
	}
	if d.Description != "Metadata service." {
		t.Errorf("description = %q", d.Description)
	}
	if d.OperationCount == 0 {
		t.Error("operation count is zero; the fixture schema exposes operations")
	}

	// With no description of their own a connection is named by the
	// endpoint it reaches, rather than by nothing.
	bare := newToolkit(t, u, "", nil)
	if got := bare.ListConnections()[0].Description; got != u.server.URL {
		t.Errorf("description = %q; want the endpoint", got)
	}
}

func TestReloadConnectionRebuildsAndRereadsTheSchema(t *testing.T) {
	u := newUpstream(t)
	u.introspection = flatIntrospectionResult
	tk := newToolkit(t, u, "", nil)
	if err := tk.ReloadConnection("gql"); err != nil {
		t.Fatalf("reload: %v", err)
	}
	info, err := tk.SchemaInfo("gql")
	if err != nil {
		t.Fatalf("schema info: %v", err)
	}
	if info.OperationCount == 0 {
		t.Error("the reload did not read the schema back")
	}
	if err := tk.ReloadConnection("missing"); !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("reloading an unknown connection gave %v", err)
	}
}

func TestRegisterToolsAddsTheKindsSurface(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	tk.SetExportDeps(ExportDeps{AssetStore: &fakeAssets{}})
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	tk.RegisterTools(server)
	// Registration is the SDK's; asserting it did not panic and that the
	// toolkit still reports its three tools is what this level can prove.
	// The tools are exercised end to end through their handlers below and
	// through a real MCP client in the acceptance suite.
	if got := tk.Tools(); len(got) != 3 {
		t.Errorf("tools = %v", got)
	}
}

func TestSettersAreIdempotentAndNilSafe(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	tk.SetRoutePolicy(nil)
	tk.SetConnOAuthStore(nil)
	tk.SetAuthEvents(nil)
	tk.SetSchemaStore(nil)
	tk.SetVectorReader(nil)
	tk.SetEmbeddingProvider(nil)
	tk.SetMetrics(nil)
	tk.SetMemBudget(nil)
	if tk.ConnOAuthStore() != nil {
		t.Error("a nil store was recorded as a store")
	}
	// A connection must still serve after every optional dependency was
	// cleared: a deployment with no database is the normal case.
	if _, err := tk.SchemaInfo("gql"); err != nil {
		t.Errorf("schema info after clearing everything: %v", err)
	}
}
