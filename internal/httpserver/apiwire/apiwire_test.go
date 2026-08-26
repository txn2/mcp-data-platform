package apiwire_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/httpserver/apiwire"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// What the wiring decides (#1478): which connections one caller reaches, and
// that the surface answers at all in a deployment with none.

const (
	analystRole = "dp_analyst"
	adminRole   = "dp_admin"
)

// otherKindToolkit is a non-api connection the caller also reaches, which is
// what makes the kind filter observable rather than assumed.
type otherKindToolkit struct{ name string }

func (*otherKindToolkit) Kind() string                            { return "trino" }
func (c *otherKindToolkit) Name() string                          { return c.name }
func (c *otherKindToolkit) Connection() string                    { return c.name }
func (*otherKindToolkit) RegisterTools(_ *mcp.Server)             {}
func (*otherKindToolkit) Tools() []string                         { return nil }
func (*otherKindToolkit) SetSemanticProvider(_ semantic.Provider) {}
func (*otherKindToolkit) SetQueryProvider(_ query.Provider)       {}
func (*otherKindToolkit) Close() error                            { return nil }

// fixture wires two api connections and one warehouse, with a persona that
// reaches one api connection and an admin persona that reaches everything.
func fixture(t *testing.T) apiwire.Deps {
	t.Helper()
	toolkits := registry.NewRegistry()

	api := apigatewaykit.New("api")
	for _, name := range []string{"billing", "internal"} {
		if err := api.AddConnection(name, map[string]any{
			"base_url": "https://" + name + ".example.com", "auth_mode": "none",
		}); err != nil {
			t.Fatalf("AddConnection %s: %v", name, err)
		}
	}
	if err := toolkits.Register(api); err != nil {
		t.Fatalf("Register api: %v", err)
	}
	if err := toolkits.Register(&otherKindToolkit{name: "warehouse"}); err != nil {
		t.Fatalf("Register trino: %v", err)
	}

	personas := persona.NewRegistry()
	for _, p := range []*persona.Persona{
		{
			Name: "analyst", Roles: []string{analystRole},
			Connections: persona.ConnectionRules{Allow: []string{"billing", "warehouse"}},
		},
		{
			Name: "admin", Roles: []string{adminRole},
			Connections: persona.ConnectionRules{Allow: []string{"*"}},
		},
	} {
		if err := personas.Register(p); err != nil {
			t.Fatalf("Register persona %s: %v", p.Name, err)
		}
	}

	return apiwire.Deps{
		Toolkits: toolkits, Personas: personas, AdminRoles: []string{adminRole},
		Resolver: func(roles []string) *portal.PersonaInfo {
			per, ok := personas.GetForRoles(roles)
			if !ok || per == nil {
				return nil
			}
			return &portal.PersonaInfo{Name: per.Name}
		},
	}
}

// mountAs mounts the routes behind a wrapper that puts one user on the context,
// standing in for the portal auth chain the composition root supplies.
func mountAs(t *testing.T, deps apiwire.Deps, user *portal.User) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	apiwire.Mount(mux, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if user != nil {
				r = r.WithContext(portal.ContextWithUser(r.Context(), user))
			}
			next.ServeHTTP(w, r)
		})
	}, deps)
	return mux
}

func names(t *testing.T, mux *http.ServeMux) []string {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/v1/apis", http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Connections []struct {
			Name string `json:"name"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := make([]string, 0, len(out.Connections))
	for _, c := range out.Connections {
		got = append(got, c.Name)
	}
	return got
}

// TestMount_NarrowsToThePersonaAndToTheAPIKind is the acceptance criterion: a
// non-administrator sees only the api connections their persona allows.
func TestMount_NarrowsToThePersonaAndToTheAPIKind(t *testing.T) {
	mux := mountAs(t, fixture(t), &portal.User{
		UserID: "u1", Email: "analyst@example.com", Roles: []string{analystRole},
	})

	// The persona also reaches "warehouse", which is not an api connection.
	if got := names(t, mux); len(got) != 1 || got[0] != "billing" {
		t.Fatalf("connections = %v, want only the api connection the persona allows", got)
	}
}

func TestMount_AdministratorIsUnrestricted(t *testing.T) {
	mux := mountAs(t, fixture(t), &portal.User{
		UserID: "u2", Email: "admin@example.com", Roles: []string{adminRole},
	})

	if got := names(t, mux); len(got) != 2 {
		t.Fatalf("connections = %v, want both api connections", got)
	}
}

// TestMount_UnclaimedRoleReachesNothing is the fail-closed default the
// authorizer applies to a tool call, applied here.
func TestMount_UnclaimedRoleReachesNothing(t *testing.T) {
	mux := mountAs(t, fixture(t), &portal.User{
		UserID: "u3", Email: "nobody@example.com", Roles: []string{"dp_nothing"},
	})

	if got := names(t, mux); len(got) != 0 {
		t.Errorf("a caller no persona claims reached %v", got)
	}
}

func TestMount_RequiresACaller(t *testing.T) {
	mux := mountAs(t, fixture(t), nil)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/v1/apis", http.NoBody))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// TestMount_ServedWithoutAnAPIToolkit is the deployment with no api
// connections at all, which is most of them. The surface still answers "you
// reach none": an absent route under /api/v1 falls through to the MCP root
// handler, which answers 401 to a browser fetch carrying only a session
// cookie, and the portal client turns a 401 into a redirect to the login
// screen. A reader who opens the APIs page must see the empty state, not be
// thrown out of the portal.
func TestMount_ServedWithoutAnAPIToolkit(t *testing.T) {
	personas := persona.NewRegistry()
	if err := personas.Register(&persona.Persona{
		Name: "analyst", Roles: []string{analystRole},
		Connections: persona.ConnectionRules{Allow: []string{"*"}},
	}); err != nil {
		t.Fatalf("Register persona: %v", err)
	}
	deps := apiwire.Deps{
		Toolkits: registry.NewRegistry(), Personas: personas,
		Resolver: func([]string) *portal.PersonaInfo { return &portal.PersonaInfo{Name: "analyst"} },
	}
	mux := mountAs(t, deps, &portal.User{
		UserID: "u1", Email: "analyst@example.com", Roles: []string{analystRole},
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/v1/apis", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	// [] rather than null, so the page renders an empty state rather than
	// failing to iterate.
	if body := rec.Body.String(); body != `{"connections":[]}`+"\n" && body != `{"connections":[]}` {
		t.Errorf("body = %q, want an empty connection list", body)
	}
}

// TestMount_UnmountedWithoutAToolkitRegistry keeps a deployment that cannot
// enumerate its connections from serving a set at all, which is the same
// reading connreach gives a nil registry.
func TestMount_UnmountedWithoutAToolkitRegistry(t *testing.T) {
	mux := http.NewServeMux()
	apiwire.Mount(mux, nil, apiwire.Deps{})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/v1/apis", http.NoBody))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want the routes unmounted", rec.Code)
	}
}

// TestMount_ElevatesTheCallerForTheRoutePolicy is what makes the page and the
// tool agree: the route policy resolves a caller's roles from the
// pre-authenticated user on the context, so this surface has to install one or
// every operation would be judged against an anonymous caller.
//
// The policy here admits an operation only when the caller's own role reached
// it, so the operations that come back are proof the identity crossed.
func TestMount_ElevatesTheCallerForTheRoutePolicy(t *testing.T) {
	deps := fixtureWithSpec(t)
	api, ok := deps.Toolkits.GetByKind(apigatewaykit.Kind)[0].(*apigatewaykit.Toolkit)
	if !ok {
		t.Fatal("the fixture's api toolkit is not an *apigateway.Toolkit")
	}
	api.SetRoutePolicy(callerRolePolicy{want: analystRole})

	mux := mountAs(t, deps, &portal.User{
		UserID: "u1", Email: "analyst@example.com", Roles: []string{analystRole},
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/v1/apis/billing/operations", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Operations []struct {
			OperationID string `json:"operation_id"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Operations) != 1 || out.Operations[0].OperationID != "listPets" {
		t.Fatalf("operations = %+v; the caller's roles did not reach the route policy", out.Operations)
	}
}

// callerRolePolicy admits an operation only when the caller carrying want is
// on the context the toolkit passes it.
type callerRolePolicy struct{ want string }

func (p callerRolePolicy) Allow(ctx context.Context, _, _, _, _ string) (allowed bool, reason string) {
	info := middleware.GetPreAuthenticatedUser(ctx)
	if info == nil {
		return false, "no caller on the context"
	}
	return slices.Contains(info.Roles, p.want), "role not held"
}

// petstoreSpec gives the billing connection one operation to filter.
const petstoreSpec = `
openapi: 3.0.3
info:
  title: Petstore
  version: "1.0"
paths:
  /pets:
    get:
      operationId: listPets
      summary: List pets
      responses:
        '200':
          description: OK
`

// fixtureWithSpec is the fixture with a catalog behind the billing connection,
// so there is an operation for the route policy to be consulted about.
func fixtureWithSpec(t *testing.T) apiwire.Deps {
	t.Helper()
	deps := fixture(t)
	api, ok := deps.Toolkits.GetByKind(apigatewaykit.Kind)[0].(*apigatewaykit.Toolkit)
	if !ok {
		t.Fatal("the fixture's api toolkit is not an *apigateway.Toolkit")
	}
	store := apicatalog.NewMemoryStore()
	if err := store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "petstore", Name: "petstore", DisplayName: "Petstore",
	}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	if err := store.UpsertSpec(context.Background(), "petstore", apicatalog.SpecEntry{
		SpecName: "default", Content: petstoreSpec, SourceKind: apicatalog.SourceInline,
	}); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
	api.SetCatalogStore(store)
	// Re-register billing against the catalog: the connection's specs are
	// resolved once, at registration, from whatever store was wired then.
	if err := api.RemoveConnection("billing"); err != nil {
		t.Fatalf("RemoveConnection: %v", err)
	}
	if err := api.AddConnection("billing", map[string]any{
		"base_url": "https://billing.example.com", "auth_mode": "none", "catalog_id": "petstore",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	return deps
}
