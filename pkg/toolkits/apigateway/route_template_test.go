package apigateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The route policy is consulted from two places holding the path in two forms.
// A listing surface holds the operation as the catalog declares it
// ("/v1/users/{id}"); an invoke holds the path the call reaches
// ("/v1/users/42"), because the parameters are substituted before any gating
// runs. A rule naming the declared path hid the operation from
// api_list_endpoints and let the call it hides through, so the toolkit now
// resolves the concrete path back to its template and reports both (#1479).

// templatePolicy records what the toolkit passed and refuses one declared path.
type templatePolicy struct {
	denyMethod, denyTemplate string
	gotPath, gotTemplate     string
}

func (p *templatePolicy) Allow(_ context.Context, _, method, path, template string) (allowed bool, reason string) {
	p.gotPath, p.gotTemplate = path, template
	if method == p.denyMethod && template == p.denyTemplate {
		return false, "denied by the operation's declared path"
	}
	return true, ""
}

// invokeToolkit builds a toolkit whose "c1" connection serves validMinimalSpec
// against a live upstream that answers every request.
func invokeToolkit(t *testing.T) *Toolkit {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	tk := New("test")
	setupCatalogWithSpec(t, tk, "petstore", "default", validMinimalSpec)
	if err := tk.AddConnection("c1", map[string]any{
		"base_url":   srv.URL,
		"catalog_id": "petstore",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	return tk
}

func TestHandleInvoke_ReportsTheOperationTemplateToThePolicy(t *testing.T) {
	tk := invokeToolkit(t)
	policy := &templatePolicy{denyMethod: http.MethodGet, denyTemplate: "/v1/users/{id}"}
	tk.SetRoutePolicy(policy)

	res, _, err := tk.handleInvoke(t.Context(), &mcp.CallToolRequest{}, InvokeInput{
		Connection: "c1", Method: http.MethodGet, Path: "/v1/users/42",
	})
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	if !res.IsError {
		t.Fatal("a deny rule on the operation's declared path did not refuse the call it serves")
	}
	if policy.gotPath != "/v1/users/42" {
		t.Errorf("policy saw path %q, want the concrete path", policy.gotPath)
	}
	if policy.gotTemplate != "/v1/users/{id}" {
		t.Errorf("policy saw template %q, want the catalog's declared path", policy.gotTemplate)
	}
}

func TestHandleInvoke_LeavesASiblingOperationCallable(t *testing.T) {
	tk := invokeToolkit(t)
	// The literal collection path shares the prefix a "/v1/users/*" glob would
	// have swept up. Denying one operation must not reach it.
	tk.SetRoutePolicy(&templatePolicy{denyMethod: http.MethodGet, denyTemplate: "/v1/users/{id}"})

	res, _, err := tk.handleInvoke(t.Context(), &mcp.CallToolRequest{}, InvokeInput{
		Connection: "c1", Method: http.MethodGet, Path: "/v1/users",
	})
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	if res.IsError {
		t.Fatalf("a sibling operation was refused: %s", resultText(t, res))
	}
}

func TestHandleInvoke_ReportsNoTemplateForAnUncatalogedPath(t *testing.T) {
	tk := invokeToolkit(t)
	policy := &templatePolicy{}
	tk.SetRoutePolicy(policy)

	if _, _, err := tk.handleInvoke(t.Context(), &mcp.CallToolRequest{}, InvokeInput{
		Connection: "c1", Method: http.MethodGet, Path: "/v1/nothing/here",
	}); err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	if policy.gotTemplate != "" {
		t.Errorf("template = %q for a path no operation declares, want empty", policy.gotTemplate)
	}
}

func TestOperationTemplate(t *testing.T) {
	tk := invokeToolkit(t)
	tk.mu.RLock()
	c := tk.connections["c1"]
	tk.mu.RUnlock()

	cases := []struct {
		name, method, path, want string
	}{
		{"a templated path resolves to its declaration", http.MethodGet, "/v1/users/42", "/v1/users/{id}"},
		{"a literal path resolves to itself", http.MethodGet, "/v1/users", "/v1/users"},
		{"a method the catalog does not declare has no template", http.MethodPatch, "/v1/users/42", ""},
		{"a path no operation serves has no template", http.MethodGet, "/v1/nothing", ""},
	}
	for _, c2 := range cases {
		t.Run(c2.name, func(t *testing.T) {
			if got := operationTemplate(c, c2.method, c2.path); got != c2.want {
				t.Errorf("operationTemplate(%s %s) = %q, want %q", c2.method, c2.path, got, c2.want)
			}
		})
	}
	t.Run("a nil connection has no template", func(t *testing.T) {
		if got := operationTemplate(nil, http.MethodGet, "/v1/users"); got != "" {
			t.Errorf("operationTemplate(nil) = %q, want empty", got)
		}
	})
}

// CatalogConnections is the authoring view the persona editor is filled from:
// what a connection exposes, narrowed by nothing. Every other browse method on
// the toolkit answers what one caller reaches and is route-policy filtered,
// which is the wrong set to draw a rule against (#1479).
func TestCatalogConnections(t *testing.T) {
	tk := invokeToolkit(t)

	conns := tk.CatalogConnections()
	if len(conns) != 1 {
		t.Fatalf("connections = %d, want 1", len(conns))
	}
	c := conns[0]
	if c.Name != "c1" {
		t.Errorf("name = %q, want c1", c.Name)
	}
	if c.CatalogID != "petstore" {
		t.Errorf("catalog_id = %q, want petstore", c.CatalogID)
	}
	total := len(c.Operations)
	if total == 0 {
		t.Fatal("the connection reported no operations")
	}
	// The declared path, placeholders and all: that is the form a rule names.
	var sawTemplate bool
	for _, op := range c.Operations {
		if op.Path == "/v1/users/{id}" {
			sawTemplate = true
		}
	}
	if !sawTemplate {
		t.Error("the templated path is absent from the index")
	}

	t.Run("a deny-all policy does not narrow it", func(t *testing.T) {
		tk.SetRoutePolicy(&templatePolicy{denyMethod: "*", denyTemplate: "*"})
		again := tk.CatalogConnections()
		if len(again) != 1 || len(again[0].Operations) != total {
			t.Errorf("operations = %d under a deny-all policy, want the catalog's %d",
				len(again[0].Operations), total)
		}
	})

	t.Run("a connection with no catalog reports no operation", func(t *testing.T) {
		if err := tk.AddConnection("bare", map[string]any{
			"base_url": "https://example.com",
		}); err != nil {
			t.Fatalf("AddConnection: %v", err)
		}
		for _, conn := range tk.CatalogConnections() {
			if conn.Name == "bare" && len(conn.Operations) != 0 {
				t.Errorf("operations = %d for an uncataloged connection, want 0",
					len(conn.Operations))
			}
		}
	})
}
