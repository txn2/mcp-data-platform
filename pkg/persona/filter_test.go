package persona

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

const (
	filterTestAnalyst       = "analyst"
	filterTestAdmin         = "admin"
	filterTestDatahubSearch = "datahub_search"
	filterTestTrinoQuery    = "trino_query"
	filterTestTrinoWild     = "trino_*"
	filterTestFilterCount   = 3
	filterTestWildcard      = "*"
)

func TestToolFilter_IsAllowed(t *testing.T) {
	reg := NewRegistry()
	filter := NewToolFilter(reg)

	tests := []struct {
		name     string
		persona  *Persona
		toolName string
		want     bool
	}{
		{
			name:     "nil persona denies all",
			persona:  nil,
			toolName: "any_tool",
			want:     false, // SECURITY: fail closed - nil persona denies access
		},
		{
			name: "wildcard allow",
			persona: &Persona{
				Name:  filterTestAdmin,
				Tools: ToolRules{Allow: []string{filterTestWildcard}},
			},
			toolName: "any_tool",
			want:     true,
		},
		{
			name: "prefix allow",
			persona: &Persona{
				Name:  filterTestAnalyst,
				Tools: ToolRules{Allow: []string{filterTestTrinoWild}},
			},
			toolName: filterTestTrinoQuery,
			want:     true,
		},
		{
			name: "prefix deny",
			persona: &Persona{
				Name:  filterTestAnalyst,
				Tools: ToolRules{Allow: []string{filterTestWildcard}, Deny: []string{"s3_delete_*"}},
			},
			toolName: "s3_delete_object",
			want:     false,
		},
		{
			name: "exact match allow",
			persona: &Persona{
				Name:  "exec",
				Tools: ToolRules{Allow: []string{filterTestDatahubSearch}},
			},
			toolName: filterTestDatahubSearch,
			want:     true,
		},
		{
			name: "no match deny",
			persona: &Persona{
				Name:  "exec",
				Tools: ToolRules{Allow: []string{filterTestDatahubSearch}},
			},
			toolName: filterTestTrinoQuery,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filter.IsAllowed(tt.persona, tt.toolName)
			if got != tt.want {
				t.Errorf("IsAllowed(%q) = %v, want %v", tt.toolName, got, tt.want)
			}
		})
	}
}

// TestToolFilter_WhyAllowed verifies that the diagnostic variant
// surfaces which pattern produced the decision (allow / deny /
// default) so the admin UI can explain access in the Tools detail.
func TestToolFilter_WhyAllowed(t *testing.T) {
	reg := NewRegistry()
	filter := NewToolFilter(reg)

	persona := &Persona{
		Name: "analyst",
		Tools: ToolRules{
			Allow: []string{"trino_*", "datahub_search"},
			Deny:  []string{"trino_execute"},
		},
	}

	cases := []struct {
		tool        string
		wantAllowed bool
		wantPattern string
		wantSource  AccessSource
		expectMatch bool
	}{
		{"trino_query", true, "trino_*", AccessSourceAllow, true},
		{"datahub_search", true, "datahub_search", AccessSourceAllow, true},
		{"trino_execute", false, "trino_execute", AccessSourceDeny, true},
		{"s3_list_buckets", false, "", AccessSourceDefault, true},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			got := filter.WhyAllowed(persona, c.tool)
			if got.Allowed != c.wantAllowed {
				t.Errorf("Allowed = %v, want %v", got.Allowed, c.wantAllowed)
			}
			if got.MatchedPattern != c.wantPattern {
				t.Errorf("MatchedPattern = %q, want %q", got.MatchedPattern, c.wantPattern)
			}
			if got.Source != c.wantSource {
				t.Errorf("Source = %q, want %q", got.Source, c.wantSource)
			}
		})
	}
}

// Nil persona must fail closed with default-deny semantics.
func TestToolFilter_WhyAllowed_NilPersona(t *testing.T) {
	reg := NewRegistry()
	filter := NewToolFilter(reg)
	got := filter.WhyAllowed(nil, "anything")
	if got.Allowed {
		t.Error("nil persona should default-deny")
	}
	if got.Source != AccessSourceDefault {
		t.Errorf("Source = %q, want %q", got.Source, AccessSourceDefault)
	}
}

func TestToolFilter_FilterTools(t *testing.T) {
	reg := NewRegistry()
	filter := NewToolFilter(reg)

	persona := &Persona{
		Name: "analyst",
		Tools: ToolRules{
			Allow: []string{filterTestTrinoWild, "datahub_*"},
			Deny:  []string{"trino_admin*"},
		},
	}

	tools := []string{
		filterTestTrinoQuery,
		"trino_describe",
		"trino_admin_users",
		filterTestDatahubSearch,
		"s3_list_buckets",
	}

	allowed := filter.FilterTools(persona, tools)

	if len(allowed) != filterTestFilterCount {
		t.Errorf("FilterTools() returned %d tools, want %d", len(allowed), filterTestFilterCount)
	}

	// Check specific tools
	expected := map[string]bool{
		filterTestTrinoQuery:    true,
		"trino_describe":        true,
		filterTestDatahubSearch: true,
	}

	for _, tool := range allowed {
		if !expected[tool] {
			t.Errorf("unexpected tool in result: %s", tool)
		}
	}
}

func TestToolFilter_FilterTools_NilPersona(t *testing.T) {
	reg := NewRegistry()
	filter := NewToolFilter(reg)

	tools := []string{"tool1", "tool2", "tool3"}
	allowed := filter.FilterTools(nil, tools)

	// SECURITY: nil persona denies all tools (fail closed)
	if len(allowed) != 0 {
		t.Errorf("FilterTools(nil) should return no tools (fail closed), got %d", len(allowed))
	}
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"*", "anything", true},
		{filterTestTrinoWild, filterTestTrinoQuery, true},
		{filterTestTrinoWild, filterTestDatahubSearch, false},
		{"exact_match", "exact_match", true},
		{"exact_match", "other", false},
		{"prefix_*_suffix", "prefix_middle_suffix", true},
		{"[invalid", "test", false}, // Invalid pattern
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.name, func(t *testing.T) {
			got := matchPattern(tt.pattern, tt.name)
			if got != tt.want {
				t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
			}
		})
	}
}

// mockRoleMapper implements RoleMapper for testing.
type mockRoleMapper struct {
	mapToPersonaFunc func(ctx context.Context, roles []string) (*Persona, error)
	mapToRolesFunc   func(claims map[string]any) ([]string, error)
}

func (m *mockRoleMapper) MapToPersona(ctx context.Context, roles []string) (*Persona, error) {
	if m.mapToPersonaFunc != nil {
		return m.mapToPersonaFunc(ctx, roles)
	}
	return nil, nil //nolint:nilnil // test mock: nil means no persona found
}

func (m *mockRoleMapper) MapToRoles(claims map[string]any) ([]string, error) {
	if m.mapToRolesFunc != nil {
		return m.mapToRolesFunc(claims)
	}
	return nil, nil //nolint:nilnil // test mock: nil means no roles found
}

func TestAuthorizer_IsAuthorized_MapperError(t *testing.T) {
	reg := NewRegistry()
	mapper := &mockRoleMapper{
		mapToPersonaFunc: func(_ context.Context, _ []string) (*Persona, error) {
			return nil, errors.New("mapper error")
		},
	}
	auth := NewAuthorizer(reg, mapper)

	authorized, personaName, reason := auth.IsAuthorized(context.Background(), "user1", []string{"role1"}, "tool1", "")
	if authorized {
		t.Error("expected not authorized on mapper error")
	}
	if personaName != "" {
		t.Errorf("expected empty persona name on mapper error, got %q", personaName)
	}
	if reason != "failed to determine persona" {
		t.Errorf("unexpected reason: %s", reason)
	}
}

func TestAuthorizer_IsAuthorized_ToolNotAllowed(t *testing.T) {
	reg := NewRegistry()
	persona := &Persona{Name: filterTestAnalyst, Tools: ToolRules{Allow: []string{filterTestTrinoWild}}}
	mapper := &mockRoleMapper{
		mapToPersonaFunc: func(_ context.Context, _ []string) (*Persona, error) {
			return persona, nil
		},
	}
	auth := NewAuthorizer(reg, mapper)

	authorized, personaName, reason := auth.IsAuthorized(context.Background(), "user1", []string{filterTestAnalyst}, "s3_list_buckets", "")
	if authorized {
		t.Error("expected not authorized for disallowed tool")
	}
	if personaName != filterTestAnalyst {
		t.Errorf("expected persona name 'analyst', got %q", personaName)
	}
	if reason != "tool not allowed for persona: "+filterTestAnalyst {
		t.Errorf("unexpected reason: %s", reason)
	}
}

func TestAuthorizer_IsAuthorized_ToolAllowed(t *testing.T) {
	reg := NewRegistry()
	persona := &Persona{Name: filterTestAdmin, Tools: ToolRules{Allow: []string{filterTestWildcard}}}
	mapper := &mockRoleMapper{
		mapToPersonaFunc: func(_ context.Context, _ []string) (*Persona, error) {
			return persona, nil
		},
	}
	auth := NewAuthorizer(reg, mapper)

	authorized, personaName, reason := auth.IsAuthorized(context.Background(), "user1", []string{filterTestAdmin}, "any_tool", "")
	if !authorized {
		t.Error("expected authorized for admin persona")
	}
	if personaName != filterTestAdmin {
		t.Errorf("expected persona name 'admin', got %q", personaName)
	}
	if reason != "" {
		t.Errorf("unexpected reason: %s", reason)
	}
}

func TestToolFilter_IsConnectionAllowed(t *testing.T) {
	reg := NewRegistry()
	filter := NewToolFilter(reg)

	tests := []struct {
		name           string
		persona        *Persona
		connectionName string
		want           bool
	}{
		{
			name:           "nil persona returns false",
			persona:        nil,
			connectionName: "any",
			want:           false,
		},
		{
			name: "empty connection name returns true",
			persona: &Persona{
				Name:        "analyst",
				Connections: ConnectionRules{Deny: []string{"*"}},
			},
			connectionName: "",
			want:           true,
		},
		{
			name: "empty allow list denies (deny-by-default)",
			persona: &Persona{
				Name:        "analyst",
				Connections: ConnectionRules{},
			},
			connectionName: "prod-trino",
			want:           false,
		},
		{
			name: "wildcard allow permits any connection",
			persona: &Persona{
				Name:        "admin",
				Connections: ConnectionRules{Allow: []string{"*"}},
			},
			connectionName: "prod-trino",
			want:           true,
		},
		{
			name: "deny pattern blocks",
			persona: &Persona{
				Name:        "analyst",
				Connections: ConnectionRules{Deny: []string{"prod-*"}},
			},
			connectionName: "prod-trino",
			want:           false,
		},
		{
			name: "allow pattern permits",
			persona: &Persona{
				Name:        "analyst",
				Connections: ConnectionRules{Allow: []string{"dev-*"}},
			},
			connectionName: "dev-trino",
			want:           true,
		},
		{
			name: "deny overrides allow",
			persona: &Persona{
				Name: "analyst",
				Connections: ConnectionRules{
					Allow: []string{"*"},
					Deny:  []string{"prod-*"},
				},
			},
			connectionName: "prod-trino",
			want:           false,
		},
		{
			name: "no matching allow denies",
			persona: &Persona{
				Name:        "analyst",
				Connections: ConnectionRules{Allow: []string{"dev-*"}},
			},
			connectionName: "staging-trino",
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filter.IsConnectionAllowed(tt.persona, tt.connectionName)
			if got != tt.want {
				t.Errorf("IsConnectionAllowed(%q) = %v, want %v", tt.connectionName, got, tt.want)
			}
		})
	}
}

func TestToolFilter_IsAPIRouteAllowed(t *testing.T) {
	filter := NewToolFilter(NewRegistry())

	type tc struct {
		name       string
		persona    *Persona
		connection string
		method     string
		path       string
		// template is the catalog path the call's path resolved from.
		// Empty for the cases written before rules could name one.
		template string
		want     bool
	}

	cases := []tc{
		{
			name:       "nil persona denies",
			persona:    nil,
			connection: "crm-prod",
			method:     "GET",
			path:       "/v1/users",
			want:       false,
		},
		{
			name:       "no APIRoutes configured — passthrough (true)",
			persona:    &Persona{Name: "p"},
			connection: "crm-prod",
			method:     "GET",
			path:       "/v1/users",
			want:       true,
		},
		{
			name: "rules for a different connection do not gate this one",
			persona: &Persona{Name: "p", APIRoutes: []APIRouteRule{
				{Connection: "billing-*", Methods: []string{"GET"}},
			}},
			connection: "crm-prod",
			method:     "POST",
			path:       "/v1/users",
			want:       true, // no rule touches crm-prod → passthrough
		},
		{
			name: "matching allow rule with explicit method",
			persona: &Persona{Name: "p", APIRoutes: []APIRouteRule{
				{Connection: "crm-*", Methods: []string{"GET"}, Paths: []string{"/v1/users/*"}},
			}},
			connection: "crm-prod",
			method:     "GET",
			path:       "/v1/users/123",
			want:       true,
		},
		{
			name: "method mismatch denies even if path allowed",
			persona: &Persona{Name: "p", APIRoutes: []APIRouteRule{
				{Connection: "crm-*", Methods: []string{"GET"}, Paths: []string{"/v1/users/*"}},
			}},
			connection: "crm-prod",
			method:     "DELETE",
			path:       "/v1/users/123",
			want:       false,
		},
		{
			name: "path mismatch denies even if method allowed",
			persona: &Persona{Name: "p", APIRoutes: []APIRouteRule{
				{Connection: "crm-*", Methods: []string{"GET"}, Paths: []string{"/v1/users/*"}},
			}},
			connection: "crm-prod",
			method:     "GET",
			path:       "/v1/orders",
			want:       false,
		},
		{
			name: "empty Methods means any method is allowed",
			persona: &Persona{Name: "p", APIRoutes: []APIRouteRule{
				{Connection: "crm-*", Paths: []string{"/v1/users/*"}},
			}},
			connection: "crm-prod",
			method:     "DELETE",
			path:       "/v1/users/abc",
			want:       true,
		},
		{
			name: "empty Paths means any path is allowed",
			persona: &Persona{Name: "p", APIRoutes: []APIRouteRule{
				{Connection: "crm-*", Methods: []string{"POST"}},
			}},
			connection: "crm-prod",
			method:     "POST",
			path:       "/anything/here",
			want:       true,
		},
		{
			name: "deny takes precedence over allow",
			persona: &Persona{Name: "p", APIRoutes: []APIRouteRule{
				{Connection: "crm-*", Methods: []string{"DELETE"}, Action: ActionDeny},
				{Connection: "crm-*"}, // allow-all
			}},
			connection: "crm-prod",
			method:     "DELETE",
			path:       "/v1/anything",
			want:       false,
		},
		{
			name: "deny on different method does not block allow",
			persona: &Persona{Name: "p", APIRoutes: []APIRouteRule{
				{Connection: "crm-*", Methods: []string{"DELETE"}, Action: ActionDeny},
				{Connection: "crm-*", Methods: []string{"GET"}},
			}},
			connection: "crm-prod",
			method:     "GET",
			path:       "/v1/users",
			want:       true,
		},
		{
			name: "rules touch connection but no matching allow → deny",
			persona: &Persona{Name: "p", APIRoutes: []APIRouteRule{
				{Connection: "crm-*", Methods: []string{"GET"}},
			}},
			connection: "crm-prod",
			method:     "POST",
			path:       "/v1/users",
			want:       false,
		},
		{
			// The defect the portal's operation selection would otherwise
			// write: a rule naming the operation as its catalog declares it
			// hid the operation from api_list_endpoints and let the call it
			// hides through, because an invoke reaches the policy with its
			// path parameters already substituted.
			name: "deny on the templated path refuses the concrete call it serves",
			persona: &Persona{Name: "p", APIRoutes: []APIRouteRule{
				{Connection: "crm-*"},
				{Connection: "crm-*", Methods: []string{"DELETE"}, Paths: []string{"/v1/orders/{id}"}, Action: ActionDeny},
			}},
			connection: "crm-prod",
			method:     "DELETE",
			path:       "/v1/orders/42",
			template:   "/v1/orders/{id}",
			want:       false,
		},
		{
			name: "allow on the templated path admits the concrete call it serves",
			persona: &Persona{Name: "p", APIRoutes: []APIRouteRule{
				{Connection: "crm-*", Methods: []string{"GET"}, Paths: []string{"/v1/orders/{id}"}},
			}},
			connection: "crm-prod",
			method:     "GET",
			path:       "/v1/orders/42",
			template:   "/v1/orders/{id}",
			want:       true,
		},
		{
			name: "a sibling operation under the same prefix is untouched by the rule",
			persona: &Persona{Name: "p", APIRoutes: []APIRouteRule{
				{Connection: "crm-*"},
				{Connection: "crm-*", Methods: []string{"GET"}, Paths: []string{"/v1/orders/{id}"}, Action: ActionDeny},
			}},
			connection: "crm-prod",
			method:     "GET",
			path:       "/v1/orders/summary",
			template:   "/v1/orders/summary",
			want:       true,
		},
		{
			// filepath.Match's wildcards stop at a separator, which is what
			// makes a path glob govern one segment. A rule meant to cover a
			// subtree has to say so per depth, or name no path at all.
			name: "a path glob does not reach past a separator",
			persona: &Persona{Name: "p", APIRoutes: []APIRouteRule{
				{Connection: "crm-*", Paths: []string{"/v1/orders/*"}},
			}},
			connection: "crm-prod",
			method:     "GET",
			path:       "/v1/orders/42/items",
			want:       false,
		},
		{
			name: "a doubled star is not a recursive form",
			persona: &Persona{Name: "p", APIRoutes: []APIRouteRule{
				{Connection: "crm-*", Paths: []string{"/v1/orders/**"}},
			}},
			connection: "crm-prod",
			method:     "GET",
			path:       "/v1/orders/42/items",
			want:       false,
		},
		{
			name: "a hand-written glob still matches the concrete path when a template is reported",
			persona: &Persona{Name: "p", APIRoutes: []APIRouteRule{
				{Connection: "crm-*", Methods: []string{"GET"}, Paths: []string{"/v1/orders/*"}},
			}},
			connection: "crm-prod",
			method:     "GET",
			path:       "/v1/orders/42",
			template:   "/v1/orders/{id}",
			want:       true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := RouteQuery{Connection: c.connection, Method: c.method, Path: c.path, Template: c.template}
			got := filter.IsAPIRouteAllowed(c.persona, q)
			if got != c.want {
				t.Errorf("IsAPIRouteAllowed(%s, %s %s) = %v, want %v", c.connection, c.method, c.path, got, c.want)
			}
		})
	}
}

// The Authorizer is what the api-gateway's route policy calls: it resolves the
// caller's roles to a persona first, then asks the same question the filter
// answers. It is exercised here rather than only through
// internal/platform/routepolicy so the resolution failure and the denial
// message are covered where they are written.
func TestAuthorizer_IsAPIRouteAllowed(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&Persona{
		Name:  "analyst",
		Roles: []string{"analyst"},
		APIRoutes: []APIRouteRule{
			{Connection: "crm-*", Methods: []string{"GET"}},
			{Connection: "crm-*", Methods: []string{"DELETE"}, Action: ActionDeny},
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	authzr := NewAuthorizer(reg, &OIDCRoleMapper{Registry: reg})
	q := func(method string) RouteQuery {
		return RouteQuery{Connection: "crm-prod", Method: method, Path: "/v1/orders/{id}"}
	}

	t.Run("an allow rule authorizes and names the persona", func(t *testing.T) {
		allowed, name, reason := authzr.IsAPIRouteAllowed(context.Background(),
			[]string{"analyst"}, q("GET"))
		if !allowed {
			t.Fatalf("GET was refused: %s", reason)
		}
		if name != "analyst" {
			t.Errorf("persona = %q, want analyst", name)
		}
	})

	t.Run("a deny rule refuses with a reason naming the call", func(t *testing.T) {
		allowed, name, reason := authzr.IsAPIRouteAllowed(context.Background(),
			[]string{"analyst"}, q("DELETE"))
		if allowed {
			t.Fatal("DELETE was allowed despite the deny rule")
		}
		if name != "analyst" {
			t.Errorf("persona = %q, want analyst", name)
		}
		for _, want := range []string{"analyst", "DELETE", "/v1/orders/{id}", "crm-prod"} {
			if !strings.Contains(reason, want) {
				t.Errorf("reason %q does not name %q", reason, want)
			}
		}
	})

	t.Run("roles matching no persona reach the no-op, not a refusal", func(t *testing.T) {
		// The deny-all persona an unmapped caller gets carries no APIRoutes, and
		// a persona no rule names passes the route check by design. This layer
		// is a narrowing, not a gate: such a caller is refused by the tool and
		// connection checks the middleware ran before the toolkit was reached,
		// and by the connection boundary the browse surfaces draw from.
		allowed, name, _ := authzr.IsAPIRouteAllowed(context.Background(),
			[]string{"stranger"}, q("DELETE"))
		if !allowed {
			t.Error("an unruled persona was refused by the route check")
		}
		if name == "analyst" {
			t.Errorf("persona = %q; an unmapped caller must not resolve to analyst", name)
		}
	})
}

// Verify interface compliance.
var (
	_ RoleMapper            = (*mockRoleMapper)(nil)
	_ middleware.Authorizer = (*Authorizer)(nil)
)
