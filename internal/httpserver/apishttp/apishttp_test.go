package apishttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/httpserver/apishttp"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// fakeBrowser answers for one connection, recording the context it was called
// with so a test can prove the caller was put on it.
type fakeBrowser struct {
	detail  *apigatewaykit.BrowseConnection
	ops     []apigatewaykit.OperationSummary
	op      *apigatewaykit.EndpointSchemaOutput
	opErr   error
	sawUser string
}

// userKey is the context key the fake elevation writes, standing in for the
// pre-authenticated user the real one installs.
type userKey struct{}

func (f *fakeBrowser) record(ctx context.Context) {
	if v, ok := ctx.Value(userKey{}).(string); ok {
		f.sawUser = v
	}
}

func (f *fakeBrowser) BrowseConnection(ctx context.Context, name string) (*apigatewaykit.BrowseConnection, error) {
	f.record(ctx)
	if f.detail == nil {
		return nil, apigatewaykit.ErrConnectionNotFound
	}
	out := *f.detail
	out.Name = name
	return &out, nil
}

func (f *fakeBrowser) BrowseOperations(ctx context.Context, _ string) ([]apigatewaykit.OperationSummary, error) {
	f.record(ctx)
	return f.ops, nil
}

func (f *fakeBrowser) BrowseOperation(ctx context.Context, _, _, _ string) (*apigatewaykit.EndpointSchemaOutput, error) {
	f.record(ctx)
	if f.opErr != nil {
		return nil, f.opErr
	}
	return f.op, nil
}

// fixture mounts the routes over one api connection the caller reaches and one
// trino connection they also reach, which is what proves the kind filter.
func fixture(t *testing.T, caller *apishttp.Caller, browser *fakeBrowser) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	apishttp.New(apishttp.Deps{
		Caller: func(*http.Request) *apishttp.Caller { return caller },
		Connections: func(_ context.Context, caller *apishttp.Caller) []apishttp.Connection {
			if caller.Persona == "" && !caller.IsAdmin {
				return nil
			}
			conns := []apishttp.Connection{
				{Name: "warehouse", Kind: "trino", Description: "the warehouse"},
				{Name: "billing", Kind: "api", Description: "the billing API"},
			}
			if caller.IsAdmin {
				conns = append(conns, apishttp.Connection{Name: "internal", Kind: "api"})
			}
			return conns
		},
		Locate: func(connection string) apishttp.OperationBrowser {
			if connection == "billing" {
				return browser
			}
			return nil
		},
		Elevate: func(ctx context.Context, c *apishttp.Caller) context.Context {
			return context.WithValue(ctx, userKey{}, c.Email)
		},
	}).Register(mux, nil)
	return mux
}

func get(t *testing.T, mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
	return rec
}

func analyst() *apishttp.Caller {
	return &apishttp.Caller{UserID: "u1", Email: "analyst@example.com", Roles: []string{"analyst"}, Persona: "analyst"}
}

func billingBrowser() *fakeBrowser {
	return &fakeBrowser{
		detail: &apigatewaykit.BrowseConnection{
			Description: "the billing API", BaseURL: "https://billing.example.com",
			AuthMode: "bearer", CatalogID: "billing-catalog", OperationCount: 2,
			Specs: []apigatewaykit.SpecSummary{{Name: "default", Title: "Billing", OperationCount: 2}},
		},
		ops: []apigatewaykit.OperationSummary{
			{OperationID: "listInvoices", Method: "GET", Path: "/v1/invoices", Summary: "List invoices", Spec: "default"},
			{OperationID: "createInvoice", Method: "POST", Path: "/v1/invoices", Spec: "default"},
		},
		op: &apigatewaykit.EndpointSchemaOutput{
			OperationID: "listInvoices", Method: "GET", Path: "/v1/invoices", Spec: "default",
		},
	}
}

// TestListConnections_OnlyTheAPIKind is the surface's subject: the caller
// reaches connections of several kinds and this one is about one of them.
func TestListConnections_OnlyTheAPIKind(t *testing.T) {
	res := get(t, fixture(t, analyst(), billingBrowser()), "/api/v1/apis")
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	var out struct {
		Connections []struct {
			Name           string `json:"name"`
			BaseURL        string `json:"base_url"`
			AuthMode       string `json:"auth_mode"`
			OperationCount int    `json:"operation_count"`
			Specs          []struct {
				Name string `json:"name"`
			} `json:"specs"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Connections) != 1 || out.Connections[0].Name != "billing" {
		t.Fatalf("connections = %+v, want only the api-kind one", out.Connections)
	}
	c := out.Connections[0]
	if c.BaseURL != "https://billing.example.com" || c.AuthMode != "bearer" {
		t.Errorf("upstream root and auth mode: %+v", c)
	}
	if c.OperationCount != 2 || len(c.Specs) != 1 {
		t.Errorf("counts and specs: %+v", c)
	}
}

// TestListConnections_NoPersonaReachesNothing is the fail-closed default the
// authorizer applies to a tool call, applied here.
func TestListConnections_NoPersonaReachesNothing(t *testing.T) {
	caller := &apishttp.Caller{UserID: "u2", Email: "nobody@example.com"}
	res := get(t, fixture(t, caller, billingBrowser()), "/api/v1/apis")
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d", res.Code)
	}
	var out struct {
		Connections []json.RawMessage `json:"connections"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if out.Connections == nil {
		t.Fatal("connections must be [] rather than null")
	}
	if len(out.Connections) != 0 {
		t.Errorf("a caller no persona claims reached %d connections", len(out.Connections))
	}
}

// TestListConnections_AdministratorIsUnrestricted keeps the admin surface what
// it is everywhere else. The second connection has no live toolkit, which is
// also the skip path this exercises.
func TestListConnections_AdministratorIsUnrestricted(t *testing.T) {
	caller := &apishttp.Caller{UserID: "u3", Email: "admin@example.com", Persona: "admin", IsAdmin: true}
	res := get(t, fixture(t, caller, billingBrowser()), "/api/v1/apis")
	var out struct {
		Connections []struct {
			Name string `json:"name"`
		} `json:"connections"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	// "internal" is enumerated for an administrator but no toolkit serves it,
	// so the listing reports what is readable right now.
	if len(out.Connections) != 1 || out.Connections[0].Name != "billing" {
		t.Fatalf("connections = %+v", out.Connections)
	}
}

func TestRoutes_RequireACaller(t *testing.T) {
	mux := fixture(t, nil, billingBrowser())
	for _, path := range []string{
		"/api/v1/apis",
		"/api/v1/apis/billing/operations",
		"/api/v1/apis/billing/operations/listInvoices",
	} {
		if res := get(t, mux, path); res.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", path, res.Code)
		}
	}
}

// TestListOperations_CarriesTheConnectionWithIt saves the page a second request
// for the upstream root a snippet is built from.
func TestListOperations_CarriesTheConnectionWithIt(t *testing.T) {
	browser := billingBrowser()
	res := get(t, fixture(t, analyst(), browser), "/api/v1/apis/billing/operations")
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	var out struct {
		Connection struct {
			Name     string `json:"name"`
			BaseURL  string `json:"base_url"`
			AuthMode string `json:"auth_mode"`
		} `json:"connection"`
		Operations []struct {
			OperationID string `json:"operation_id"`
			Method      string `json:"method"`
			Path        string `json:"path"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Connection.BaseURL != "https://billing.example.com" || out.Connection.AuthMode != "bearer" {
		t.Errorf("connection: %+v", out.Connection)
	}
	if len(out.Operations) != 2 || out.Operations[0].OperationID != "listInvoices" {
		t.Fatalf("operations: %+v", out.Operations)
	}
	if browser.sawUser != "analyst@example.com" {
		t.Errorf("the caller reached the toolkit as %q; the route policy would resolve the wrong roles", browser.sawUser)
	}
}

// TestOperationRoutes_ConnectionOutsideTheCallersReachIsNotFound is why the
// persona boundary is the surface's shape: a connection outside it does not
// exist here, rather than existing and being refused.
func TestOperationRoutes_ConnectionOutsideTheCallersReachIsNotFound(t *testing.T) {
	mux := fixture(t, analyst(), billingBrowser())
	for _, path := range []string{
		"/api/v1/apis/internal/operations",
		"/api/v1/apis/internal/operations/listInvoices",
		"/api/v1/apis/warehouse/operations",
	} {
		if res := get(t, mux, path); res.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, res.Code)
		}
	}
}

func TestGetOperation_ReturnsTheResolvedSchema(t *testing.T) {
	res := get(t, fixture(t, analyst(), billingBrowser()), "/api/v1/apis/billing/operations/listInvoices")
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	var out apigatewaykit.EndpointSchemaOutput
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.OperationID != "listInvoices" || out.Method != "GET" {
		t.Errorf("operation: %+v", out)
	}
}

// TestGetOperation_SynthesizedIDSurvivesTheURL covers an id carrying a method
// and a path, which is what an operation with no declared operationId gets.
func TestGetOperation_SynthesizedIDSurvivesTheURL(t *testing.T) {
	browser := billingBrowser()
	browser.op = &apigatewaykit.EndpointSchemaOutput{
		OperationID: "GET /v1/invoices/{id}", Method: "GET", Path: "/v1/invoices/{id}",
	}
	res := get(t, fixture(t, analyst(), browser),
		"/api/v1/apis/billing/operations/"+url.PathEscape("GET /v1/invoices/{id}"))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
}

// TestGetOperation_DeniedIsNotFound keeps the detail route consistent with the
// listing an operation was absent from.
func TestGetOperation_DeniedIsNotFound(t *testing.T) {
	browser := billingBrowser()
	browser.opErr = apigatewaykit.ErrOperationNotFound
	res := get(t, fixture(t, analyst(), browser), "/api/v1/apis/billing/operations/createInvoice")
	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", res.Code, res.Body.String())
	}
}

// TestGetOperation_AmbiguousIDIsActionable gives the reader the specs to retry
// against rather than a bare failure.
func TestGetOperation_AmbiguousIDIsActionable(t *testing.T) {
	browser := billingBrowser()
	browser.opErr = fmt.Errorf("apigateway: %w: %q is defined in multiple specs (mirror, primary)",
		apigatewaykit.ErrAmbiguousOperation, "listInvoices")
	res := get(t, fixture(t, analyst(), browser), "/api/v1/apis/billing/operations/listInvoices")
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "mirror") {
		t.Errorf("the refusal must name the specs to retry against: %s", res.Body.String())
	}
}

// TestRegister_UnmountedWithoutItsDependencies keeps a deployment that cannot
// answer who is asking from serving a surface narrowed to nobody.
func TestRegister_UnmountedWithoutItsDependencies(t *testing.T) {
	for name, deps := range map[string]apishttp.Deps{
		"no caller":      {Connections: func(context.Context, *apishttp.Caller) []apishttp.Connection { return nil }, Locate: func(string) apishttp.OperationBrowser { return nil }},
		"no enumeration": {Caller: func(*http.Request) *apishttp.Caller { return analyst() }, Locate: func(string) apishttp.OperationBrowser { return nil }},
		"no toolkits":    {Caller: func(*http.Request) *apishttp.Caller { return analyst() }, Connections: func(context.Context, *apishttp.Caller) []apishttp.Connection { return nil }},
	} {
		mux := http.NewServeMux()
		apishttp.New(deps).Register(mux, nil)
		if res := get(t, mux, "/api/v1/apis"); res.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want the route unmounted", name, res.Code)
		}
	}
}

// TestOperationRoutes_ReachedButUnservedIsNotFound covers the window between a
// connection being configured and the reload that materializes it: the caller
// reaches the name, and nothing answers for it yet.
func TestOperationRoutes_ReachedButUnservedIsNotFound(t *testing.T) {
	admin := &apishttp.Caller{UserID: "u3", Email: "admin@example.com", Persona: "admin", IsAdmin: true}
	mux := fixture(t, admin, billingBrowser())

	// "internal" is enumerated for an administrator, but the fixture's locator
	// serves only "billing".
	for _, path := range []string{
		"/api/v1/apis/internal/operations",
		"/api/v1/apis/internal/operations/listInvoices",
	} {
		if res := get(t, mux, path); res.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, res.Code)
		}
	}
}

// failingBrowser refuses everything, which is what a connection removed
// mid-request looks like from here.
type failingBrowser struct{ err error }

func (f failingBrowser) BrowseConnection(context.Context, string) (*apigatewaykit.BrowseConnection, error) {
	return nil, f.err
}

func (f failingBrowser) BrowseOperations(context.Context, string) ([]apigatewaykit.OperationSummary, error) {
	return nil, f.err
}

func (f failingBrowser) BrowseOperation(context.Context, string, string, string) (*apigatewaykit.EndpointSchemaOutput, error) {
	return nil, f.err
}

func TestListOperations_ToolkitRefusalIsNotFound(t *testing.T) {
	mux := http.NewServeMux()
	apishttp.New(apishttp.Deps{
		Caller: func(*http.Request) *apishttp.Caller { return analyst() },
		Connections: func(context.Context, *apishttp.Caller) []apishttp.Connection {
			return []apishttp.Connection{{Name: "billing", Kind: "api"}}
		},
		Locate: func(string) apishttp.OperationBrowser {
			return failingBrowser{err: apigatewaykit.ErrConnectionNotFound}
		},
	}).Register(mux, nil)

	if res := get(t, mux, "/api/v1/apis/billing/operations"); res.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404: %s", res.Code, res.Body.String())
	}
}

// TestRoutes_ServeWithoutElevation keeps a deployment with no identity to pass
// down serving the surface rather than failing per request. The route policy
// then reads an anonymous caller, which is the correct reading of "we could not
// say who this is".
func TestRoutes_ServeWithoutElevation(t *testing.T) {
	mux := http.NewServeMux()
	apishttp.New(apishttp.Deps{
		Caller: func(*http.Request) *apishttp.Caller { return analyst() },
		Connections: func(context.Context, *apishttp.Caller) []apishttp.Connection {
			return []apishttp.Connection{{Name: "billing", Kind: "api"}}
		},
		Locate: func(string) apishttp.OperationBrowser { return billingBrowser() },
	}).Register(mux, nil)

	if res := get(t, mux, "/api/v1/apis/billing/operations"); res.Code != http.StatusOK {
		t.Errorf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
}

// TestRegister_WrapsEveryRoute proves the supplied middleware actually reaches
// the handlers, which is what makes the mount's auth chain load-bearing.
func TestRegister_WrapsEveryRoute(t *testing.T) {
	mux := http.NewServeMux()
	apishttp.New(apishttp.Deps{
		Caller:      func(*http.Request) *apishttp.Caller { return analyst() },
		Connections: func(context.Context, *apishttp.Caller) []apishttp.Connection { return nil },
		Locate:      func(string) apishttp.OperationBrowser { return nil },
	}).Register(mux, func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		})
	})

	for _, path := range []string{
		"/api/v1/apis",
		"/api/v1/apis/billing/operations",
		"/api/v1/apis/billing/operations/listInvoices",
	} {
		if res := get(t, mux, path); res.Code != http.StatusTeapot {
			t.Errorf("%s bypassed the middleware: status = %d", path, res.Code)
		}
	}
}
