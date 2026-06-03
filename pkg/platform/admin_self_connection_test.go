package platform

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalogindex"
)

func TestSelfConnectionEnabled(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name    string
		enabled *bool
		prereqs bool
		want    bool
	}{
		{"nil + prereqs met = auto on", nil, true, true},
		{"nil + prereqs unmet = off", nil, false, false},
		{"explicit true + prereqs met = on", &yes, true, true},
		{"explicit true + prereqs unmet = off", &yes, false, false},
		{"explicit false + prereqs met = off", &no, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := APIGatewaySelfConnectionConfig{Enabled: tt.enabled}
			if got := c.SelfConnectionEnabled(tt.prereqs); got != tt.want {
				t.Errorf("SelfConnectionEnabled(%v) = %v; want %v", tt.prereqs, got, tt.want)
			}
		})
	}
}

func TestLoopbackBaseURL(t *testing.T) {
	tests := []struct {
		addr string
		want string
	}{
		{":8080", "http://127.0.0.1:8080"},
		{"0.0.0.0:9000", "http://127.0.0.1:9000"},
		{"localhost:3000", "http://127.0.0.1:3000"},
		{"garbage-no-port", "http://127.0.0.1:8080"},
		{"", "http://127.0.0.1:8080"},
	}
	for _, tt := range tests {
		if got := loopbackBaseURL(tt.addr); got != tt.want {
			t.Errorf("loopbackBaseURL(%q) = %q; want %q", tt.addr, got, tt.want)
		}
	}
}

func TestAdminSelfSpecContent_ParsesToOperations(t *testing.T) {
	content, err := adminSelfSpecContent()
	if err != nil {
		t.Fatalf("adminSelfSpecContent: %v", err)
	}
	items, err := apigatewaykit.BuildOperationItems(content, adminSelfSpecName)
	if err != nil {
		t.Fatalf("BuildOperationItems on converted spec: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("converted admin spec yielded zero operations")
	}
}

func TestEnsureAdminSelfCatalog_CreatesAndIdempotent(t *testing.T) {
	store := apicatalog.NewMemoryStore()
	ctx := context.Background()

	if err := ensureAdminSelfCatalog(ctx, store); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	if _, err := store.GetCatalog(ctx, adminSelfCatalogID); err != nil {
		t.Fatalf("catalog not created: %v", err)
	}
	// Second call must be a no-op, not a conflict error.
	if err := ensureAdminSelfCatalog(ctx, store); err != nil {
		t.Fatalf("second ensure should be idempotent: %v", err)
	}
}

func TestSeedAdminSelfConnection_RegistersAndRestricts(t *testing.T) {
	store := apicatalog.NewMemoryStore()
	tk := apigatewaykit.New("api")
	tk.SetCatalogStore(store)
	reg := registry.NewRegistry()
	if err := reg.Register(tk); err != nil {
		t.Fatalf("register toolkit: %v", err)
	}

	preg := persona.NewRegistry()
	if err := preg.Register(persona.AdminPersona()); err != nil {
		t.Fatalf("register admin persona: %v", err)
	}
	authz := persona.NewAuthorizer(preg, &persona.StaticRoleMapper{Registry: preg, DefaultPersonaName: "admin"})

	p := &Platform{toolkitRegistry: reg, authorizer: authz}

	if err := p.seedAdminSelfConnection(context.Background(), tk, store, nil, "http://127.0.0.1:8080"); err != nil {
		t.Fatalf("seedAdminSelfConnection: %v", err)
	}

	// Connection registered.
	if !tk.HasConnection(adminSelfConnectionName) {
		t.Fatal("platform-admin connection was not registered")
	}
	// The connection surfaces the built-in description (not the loopback
	// base URL) so the admin UI shows a legit explanation.
	var desc string
	for _, c := range tk.ListConnections() {
		if c.Name == adminSelfConnectionName {
			desc = c.Description
		}
	}
	if desc != adminSelfDescription {
		t.Errorf("platform-admin description = %q; want the built-in description", desc)
	}
	// Catalog + spec seeded.
	if specs, err := store.ListSpecs(context.Background(), adminSelfCatalogID); err != nil || len(specs) != 1 {
		t.Fatalf("expected 1 seeded spec, got %d (err=%v)", len(specs), err)
	}
	// Marked admin-only and pushed into the authorizer's restricted set.
	if got := tk.AdminOnlyConnections(); len(got) != 1 || got[0] != adminSelfConnectionName {
		t.Fatalf("AdminOnlyConnections = %v; want [%s]", got, adminSelfConnectionName)
	}
	analyst := &persona.Persona{Name: "analyst", Connections: persona.ConnectionRules{}}
	filter := persona.NewToolFilter(nil)
	filter.SetRestrictedConnections(tk.AdminOnlyConnections())
	if filter.IsConnectionAllowed(analyst, adminSelfConnectionName) {
		t.Error("non-admin persona should be denied the platform-admin connection by default")
	}

	// Re-seed (idempotent): second call reloads, does not error.
	if err := p.seedAdminSelfConnection(context.Background(), tk, store, nil, "http://127.0.0.1:8080"); err != nil {
		t.Fatalf("re-seed should be idempotent: %v", err)
	}
}

func TestWireAdminSelfConnection_NoToolkitNoop(_ *testing.T) {
	p := &Platform{
		toolkitRegistry: registry.NewRegistry(),
		lifecycle:       NewLifecycle(),
		config:          &Config{},
	}
	// No api-gateway toolkit -> prerequisites unmet -> no OnStart hook,
	// no panic.
	p.WireAdminSelfConnection(":8080")
}

func TestWireAdminSelfConnection_DisabledNoop(t *testing.T) {
	store := apicatalog.NewMemoryStore()
	tk := apigatewaykit.New("api")
	tk.SetCatalogStore(store)
	reg := registry.NewRegistry()
	if err := reg.Register(tk); err != nil {
		t.Fatalf("register: %v", err)
	}
	disabled := false
	p := &Platform{
		toolkitRegistry: reg,
		lifecycle:       NewLifecycle(),
		config:          &Config{APIGateway: APIGatewayConfig{SelfConnection: APIGatewaySelfConnectionConfig{Enabled: &disabled}}},
	}
	p.WireAdminSelfConnection(":8080")
	if err := p.lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("lifecycle start: %v", err)
	}
	if tk.HasConnection(adminSelfConnectionName) {
		t.Error("self-connection should not register when disabled")
	}
}

// TestWireAdminSelfConnection_EnabledRegistersOnStart exercises the full
// wiring path: prerequisites met, an OnStart hook is registered, and
// firing the lifecycle seeds and registers the connection.
func TestWireAdminSelfConnection_EnabledRegistersOnStart(t *testing.T) {
	store := apicatalog.NewMemoryStore()
	tk := apigatewaykit.New("api")
	tk.SetCatalogStore(store)
	reg := registry.NewRegistry()
	if err := reg.Register(tk); err != nil {
		t.Fatalf("register: %v", err)
	}
	preg := persona.NewRegistry()
	if err := preg.Register(persona.AdminPersona()); err != nil {
		t.Fatalf("register admin: %v", err)
	}
	p := &Platform{
		toolkitRegistry: reg,
		lifecycle:       NewLifecycle(),
		config:          &Config{},
		authorizer:      persona.NewAuthorizer(preg, &persona.StaticRoleMapper{Registry: preg, DefaultPersonaName: "admin"}),
	}
	p.WireAdminSelfConnection("0.0.0.0:8080")
	if err := p.lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("lifecycle start: %v", err)
	}
	if !tk.HasConnection(adminSelfConnectionName) {
		t.Fatal("self-connection not registered after OnStart fired")
	}
}

// TestRefreshRestrictedConnections_NonPersonaAuthorizerNoop confirms the
// refresh is a safe no-op when the authorizer is not the persona impl.
func TestRefreshRestrictedConnections_NonPersonaAuthorizerNoop(_ *testing.T) {
	tk := apigatewaykit.New("api")
	p := &Platform{authorizer: &middleware.NoopAuthorizer{}}
	p.refreshRestrictedConnections(tk) // must not panic
}

func TestConvertSwaggerToV3_InvalidInput(t *testing.T) {
	if _, err := convertSwaggerToV3("{not valid json"); err == nil {
		t.Error("expected error for malformed JSON")
	}
}

type stubEnqueuer struct {
	called bool
	key    catalogindex.SpecKey
}

func (s *stubEnqueuer) Enqueue(_ context.Context, key catalogindex.SpecKey, _ catalogindex.Kind) (bool, error) {
	s.called = true
	s.key = key
	return true, nil
}

// TestSeedAdminSelfConnection_RegisterErrorPropagates covers the
// connection-registration error path: an empty base URL fails the
// toolkit's config validation, so the seed returns a wrapped error.
func TestSeedAdminSelfConnection_RegisterErrorPropagates(t *testing.T) {
	store := apicatalog.NewMemoryStore()
	tk := apigatewaykit.New("api")
	tk.SetCatalogStore(store)
	reg := registry.NewRegistry()
	_ = reg.Register(tk)
	p := &Platform{toolkitRegistry: reg}

	err := p.seedAdminSelfConnection(context.Background(), tk, store, nil, "")
	if err == nil {
		t.Fatal("expected error from registering a connection with an empty base_url")
	}
	if !strings.Contains(err.Error(), "registering connection") {
		t.Errorf("unexpected error: %v", err)
	}
	if tk.HasConnection(adminSelfConnectionName) {
		t.Error("connection should not be registered when validation fails")
	}
}

// upsertFailStore is a catalog store whose UpsertSpec always fails,
// reusing MemoryStore for every other method.
type upsertFailStore struct{ *apicatalog.MemoryStore }

func (upsertFailStore) UpsertSpec(_ context.Context, _ string, _ apicatalog.SpecEntry) error {
	return errors.New("simulated upsert failure")
}

// TestSeedAdminSelfConnection_UpsertErrorPropagates covers the
// spec-upsert error path.
func TestSeedAdminSelfConnection_UpsertErrorPropagates(t *testing.T) {
	store := upsertFailStore{apicatalog.NewMemoryStore()}
	tk := apigatewaykit.New("api")
	tk.SetCatalogStore(store)
	reg := registry.NewRegistry()
	_ = reg.Register(tk)
	p := &Platform{toolkitRegistry: reg}

	err := p.seedAdminSelfConnection(context.Background(), tk, store, nil, "http://127.0.0.1:8080")
	if err == nil || !strings.Contains(err.Error(), "upserting spec") {
		t.Fatalf("expected upsert error, got %v", err)
	}
}

// TestSeedAdminSelfConnection_EnqueuesEmbedding covers the embedding-queue
// branch: when an enqueuer is wired, the seed enqueues an index job for
// the embedded admin spec.
func TestSeedAdminSelfConnection_EnqueuesEmbedding(t *testing.T) {
	store := apicatalog.NewMemoryStore()
	tk := apigatewaykit.New("api")
	tk.SetCatalogStore(store)
	reg := registry.NewRegistry()
	if err := reg.Register(tk); err != nil {
		t.Fatalf("register: %v", err)
	}
	preg := persona.NewRegistry()
	_ = preg.Register(persona.AdminPersona())
	p := &Platform{
		toolkitRegistry: reg,
		authorizer:      persona.NewAuthorizer(preg, &persona.StaticRoleMapper{Registry: preg, DefaultPersonaName: "admin"}),
	}

	enq := &stubEnqueuer{}
	if err := p.seedAdminSelfConnection(context.Background(), tk, store, enq, "http://127.0.0.1:8080"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if !enq.called {
		t.Fatal("expected embedding enqueue to be called")
	}
	if enq.key.CatalogID != adminSelfCatalogID || enq.key.SpecName != adminSelfSpecName {
		t.Errorf("enqueued key = %+v; want catalog=%s spec=%s", enq.key, adminSelfCatalogID, adminSelfSpecName)
	}
}
