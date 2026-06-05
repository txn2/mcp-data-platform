package platform

import (
	"context"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// fakePromptAuth is a minimal Authenticator returning a fixed caller identity.
type fakePromptAuth struct {
	email string
	roles []string
}

func (f *fakePromptAuth) Authenticate(_ context.Context) (*middleware.UserInfo, error) {
	return &middleware.UserInfo{UserID: f.email, Email: f.email, Roles: f.roles}, nil
}

// TestPromptVisibility_EndToEnd wires the real prompts/list middleware to the
// real isPromptVisible rule and the per-prompt scope map populated at
// registration, proving another user's personal prompt is dropped from the
// list a caller receives. This is the assembled-system check for the fix: the
// SDK hands every registered prompt to every client, and the middleware must
// remove the ones the caller is not entitled to see.
func TestPromptVisibility_EndToEnd(t *testing.T) {
	p := &Platform{}
	// Stands in for registerDatabasePrompt populating promptScopes at boot/runtime.
	p.setPromptScope(&prompt.Prompt{Name: "global-help", Scope: prompt.ScopeGlobal})
	p.setPromptScope(&prompt.Prompt{Name: "personal-sarah", Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com"})
	p.setPromptScope(&prompt.Prompt{Name: "personal-bob", Scope: prompt.ScopePersonal, OwnerEmail: "bob@example.com"})
	p.setPromptScope(&prompt.Prompt{Name: "analyst-report", Scope: prompt.ScopePersona, Personas: []string{"analyst"}})
	p.setPromptScope(&prompt.Prompt{Name: "engineer-report", Scope: prompt.ScopePersona, Personas: []string{"engineer"}})

	// Wire exactly as addPromptVisibilityMiddleware does, with Sarah (an
	// analyst) as the authenticated caller.
	cfg := middleware.PromptVisibilityConfig{
		Authenticator: &fakePromptAuth{email: "sarah@example.com", roles: []string{"r-analyst"}},
		PersonasForRoles: func(roles []string) []string {
			if slices.Contains(roles, "r-analyst") {
				return []string{"analyst"}
			}
			return nil
		},
		IsVisible: p.isPromptVisible,
	}

	// The SDK returns ALL registered prompts (plus a built-in) to every client.
	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.ListPromptsResult{Prompts: []*mcp.Prompt{
			{Name: "builtin-overview"},
			{Name: "global-help"},
			{Name: "personal-sarah"},
			{Name: "personal-bob"},
			{Name: "analyst-report"},
			{Name: "engineer-report"},
		}}, nil
	}

	handler := middleware.MCPPromptVisibilityMiddleware(cfg)(base)
	got, err := handler(context.Background(), "prompts/list", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lr, ok := got.(*mcp.ListPromptsResult)
	if !ok {
		t.Fatalf("want *mcp.ListPromptsResult, got %T", got)
	}

	visible := make(map[string]bool, len(lr.Prompts))
	for _, pr := range lr.Prompts {
		visible[pr.Name] = true
	}

	// Sarah may see: the built-in, the global prompt, her own personal prompt,
	// and the analyst persona prompt.
	for _, name := range []string{"builtin-overview", "global-help", "personal-sarah", "analyst-report"} {
		if !visible[name] {
			t.Errorf("expected %q visible to Sarah", name)
		}
	}
	// She must NOT see Bob's personal prompt or the engineer persona prompt.
	for _, name := range []string{"personal-bob", "engineer-report"} {
		if visible[name] {
			t.Errorf("%q must NOT be visible to Sarah", name)
		}
	}
}

func TestIsPromptVisible(t *testing.T) {
	p := &Platform{}
	p.setPromptScope(&prompt.Prompt{Name: "g", Scope: prompt.ScopeGlobal})
	p.setPromptScope(&prompt.Prompt{Name: "mine", Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com"})
	p.setPromptScope(&prompt.Prompt{Name: "team", Scope: prompt.ScopePersona, Personas: []string{"analyst", "lead"}})
	p.setPromptScope(&prompt.Prompt{Name: "weird", Scope: "bogus"})

	cases := []struct {
		name     string
		email    string
		personas []string
		isAdmin  bool
		prompt   string
		want     bool
	}{
		{"built-in always visible", "", nil, false, "not-registered", true},
		{"global to anonymous", "", nil, false, "g", true},
		{"personal to owner", "sarah@example.com", nil, false, "mine", true},
		{"personal hidden from other user", "bob@example.com", nil, false, "mine", false},
		{"personal hidden from anonymous", "", nil, false, "mine", false},
		{"persona to member", "x", []string{"analyst"}, false, "team", true},
		{"persona to member (second persona)", "x", []string{"lead"}, false, "team", true},
		{"persona hidden from non-member", "x", []string{"engineer"}, false, "team", false},
		{"persona hidden when no personas", "x", nil, false, "team", false},
		{"admin sees another user's personal", "bob@example.com", nil, true, "mine", true},
		{"admin sees non-member persona", "x", []string{"engineer"}, true, "team", true},
		{"unknown scope hidden (fail closed)", "sarah@example.com", nil, false, "weird", false},
		{"admin sees unknown scope", "sarah@example.com", nil, true, "weird", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := p.isPromptVisible(c.email, c.personas, c.isAdmin, c.prompt); got != c.want {
				t.Errorf("isPromptVisible(%q, %v, admin=%v, %q) = %v; want %v",
					c.email, c.personas, c.isAdmin, c.prompt, got, c.want)
			}
		})
	}
}

func TestPromptScopeLifecycle(t *testing.T) {
	p := &Platform{}
	p.setPromptScope(&prompt.Prompt{Name: "mine", Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com"})
	if p.isPromptVisible("bob@example.com", nil, false, "mine") {
		t.Fatal("personal prompt must be hidden from a non-owner")
	}

	// Unregister: the name is no longer a database prompt, so it falls back to
	// the built-in default (visible). Guards against a stale hide after delete.
	p.deletePromptScope("mine")
	if !p.isPromptVisible("bob@example.com", nil, false, "mine") {
		t.Error("after deletePromptScope the name should be treated as built-in (visible)")
	}

	// Re-register with a new scope (the update path) overwrites the entry.
	p.setPromptScope(&prompt.Prompt{Name: "mine", Scope: prompt.ScopeGlobal})
	if !p.isPromptVisible("bob@example.com", nil, false, "mine") {
		t.Error("re-registered global prompt should be visible to everyone")
	}
}
