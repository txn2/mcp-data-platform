package graphql

import (
	"context"
	"errors"
	"slices"
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

// listServerTools returns the tool names an MCP client sees on a server,
// which is the only statement about registration that binds: a toolkit's own
// Tools() is what it intends to register, not what the server holds (#1675).
func listServerTools(t *testing.T, server *mcp.Server) []string {
	t.Helper()
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer func() { _ = serverSession.Close() }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "tk-test", Version: "v0"}, nil).Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = session.Close() }()
	res, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

func TestRegisterToolsAddsTheKindsSurface(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	// The platform wires the export dependencies before it registers any
	// toolkit's tools, so this is the startup order.
	tk.SetExportDeps(ExportDeps{AssetStore: &fakeAssets{}})
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	tk.RegisterTools(server)

	want := []string{ToolDiscover, ToolExport, ToolQuery}
	if got := listServerTools(t, server); !slices.Equal(got, want) {
		t.Errorf("tools/list = %v; want %v", got, want)
	}
	if got := tk.Tools(); len(got) != 3 {
		t.Errorf("tools = %v", got)
	}
}

// TestExportDepsWiredAfterRegistrationLeaveTheToolUnknown is the shape of
// #1675: the toolkit names three tools and the server holds two, because the
// export dependencies arrived after RegisterTools ran. The platform wires them
// before registration (Platform.wireGraphQLExport) and refuses to start on a
// mismatch, so this order cannot reach a deployment; the test holds the
// toolkit's half of that contract in place.
func TestExportDepsWiredAfterRegistrationLeaveTheToolUnknown(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	tk.RegisterTools(server)
	tk.SetExportDeps(ExportDeps{AssetStore: &fakeAssets{}})

	want := []string{ToolDiscover, ToolQuery}
	if got := listServerTools(t, server); !slices.Equal(got, want) {
		t.Errorf("tools/list = %v; want %v", got, want)
	}
	if got := tk.Tools(); len(got) != 3 {
		t.Errorf("tools = %v; the toolkit still names the tool the server lacks", got)
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
