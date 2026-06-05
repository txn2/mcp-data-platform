package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var errTest = errors.New("upstream failure")

func promptListResult(names ...string) *mcp.ListPromptsResult {
	prompts := make([]*mcp.Prompt, len(names))
	for i, n := range names {
		prompts[i] = &mcp.Prompt{Name: n}
	}
	return &mcp.ListPromptsResult{Prompts: prompts}
}

func promptResultNames(r *mcp.ListPromptsResult) []string {
	out := make([]string, len(r.Prompts))
	for i, p := range r.Prompts {
		out[i] = p.Name
	}
	return out
}

func TestMCPPromptVisibilityMiddleware_FiltersList(t *testing.T) {
	base := func(_ context.Context, method string, _ mcp.Request) (mcp.Result, error) {
		if method == methodPromptsList {
			return promptListResult("global", "secret", "mine"), nil
		}
		return &mcp.CallToolResult{}, nil
	}
	cfg := PromptVisibilityConfig{
		IsVisible: func(_ string, _ []string, _ bool, name string) bool {
			return name != "secret"
		},
	}
	handler := MCPPromptVisibilityMiddleware(cfg)(base)

	got, err := handler(context.Background(), methodPromptsList, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lr, ok := got.(*mcp.ListPromptsResult)
	if !ok {
		t.Fatalf("want *mcp.ListPromptsResult, got %T", got)
	}
	names := promptResultNames(lr)
	if len(names) != 2 || names[0] != "global" || names[1] != "mine" {
		t.Errorf("expected [global mine], got %v", names)
	}
}

func TestMCPPromptVisibilityMiddleware_InjectsScopePrefixedDatabasePrompts(t *testing.T) {
	// The static list holds only built-ins; the caller's database prompts are
	// injected under their scope-prefixed names. Prefixes keep every name
	// distinct, so there is no collision even if a personal prompt's bare name
	// matches a global one.
	base := func(_ context.Context, method string, _ mcp.Request) (mcp.Result, error) {
		return promptListResult("builtin-overview"), nil
	}
	var gotEmail string
	var gotPersonas []string
	cfg := PromptVisibilityConfig{
		IsVisible: func(_ string, _ []string, _ bool, _ string) bool { return true },
		ListVisible: func(_ context.Context, email string, personas []string) []*mcp.Prompt {
			gotEmail, gotPersonas = email, personas
			return []*mcp.Prompt{{Name: "global-report"}, {Name: "personal-report"}, {Name: "analyst-runbook"}}
		},
		PersonasForRoles: func(_ []string) []string { return []string{"analyst"} },
		Authenticator: &mockAuthenticator{
			authenticateFunc: func(_ context.Context) (*UserInfo, error) {
				return &UserInfo{Email: "sarah@example.com", Roles: []string{"r-analyst"}}, nil
			},
		},
	}
	handler := MCPPromptVisibilityMiddleware(cfg)(base)
	got, err := handler(context.Background(), methodPromptsList, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lr, ok := got.(*mcp.ListPromptsResult)
	if !ok {
		t.Fatalf("want *mcp.ListPromptsResult, got %T", got)
	}
	names := promptResultNames(lr)
	// built-in + the three prefixed database prompts, in order.
	want := []string{"builtin-overview", "global-report", "personal-report", "analyst-runbook"}
	if len(names) != len(want) {
		t.Fatalf("want %v, got %v", want, names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("position %d: want %q, got %q", i, want[i], names[i])
		}
	}
	// ListVisible received the resolved caller identity (email + personas).
	if gotEmail != "sarah@example.com" || len(gotPersonas) != 1 || gotPersonas[0] != "analyst" {
		t.Errorf("ListVisible got email=%q personas=%v", gotEmail, gotPersonas)
	}
}

func TestMCPPromptVisibilityMiddleware_PassThroughNonList(t *testing.T) {
	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{}, nil
	}
	called := false
	cfg := PromptVisibilityConfig{IsVisible: func(_ string, _ []string, _ bool, _ string) bool {
		called = true
		return true
	}}
	handler := MCPPromptVisibilityMiddleware(cfg)(base)
	if _, err := handler(context.Background(), "tools/call", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("IsVisible must not be consulted for non-prompts/list methods")
	}
}

func TestMCPPromptVisibilityMiddleware_NilIsVisibleNoOp(t *testing.T) {
	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return promptListResult("a", "b"), nil
	}
	handler := MCPPromptVisibilityMiddleware(PromptVisibilityConfig{})(base)
	got, _ := handler(context.Background(), methodPromptsList, nil)
	lr, ok := got.(*mcp.ListPromptsResult)
	if !ok {
		t.Fatalf("want *mcp.ListPromptsResult, got %T", got)
	}
	if len(lr.Prompts) != 2 {
		t.Errorf("nil IsVisible must not filter; got %d", len(lr.Prompts))
	}
}

func TestMCPPromptVisibilityMiddleware_ResolvesCallerIdentity(t *testing.T) {
	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return promptListResult("mine"), nil
	}
	var gotEmail string
	var gotPersonas []string
	cfg := PromptVisibilityConfig{
		Authenticator: &mockAuthenticator{
			authenticateFunc: func(_ context.Context) (*UserInfo, error) {
				return &UserInfo{UserID: "u", Email: "sarah@example.com", Roles: []string{"r-analyst"}}, nil
			},
		},
		PersonasForRoles: func(roles []string) []string {
			if len(roles) > 0 && roles[0] == "r-analyst" {
				return []string{"analyst"}
			}
			return nil
		},
		IsVisible: func(email string, personas []string, _ bool, _ string) bool {
			gotEmail = email
			gotPersonas = personas
			return true
		},
	}
	handler := MCPPromptVisibilityMiddleware(cfg)(base)
	if _, err := handler(context.Background(), methodPromptsList, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotEmail != "sarah@example.com" {
		t.Errorf("email = %q; want sarah@example.com", gotEmail)
	}
	if len(gotPersonas) != 1 || gotPersonas[0] != "analyst" {
		t.Errorf("personas = %v; want [analyst]", gotPersonas)
	}
}

func TestMCPPromptVisibilityMiddleware_AllowsVisibleGet(t *testing.T) {
	nextCalled := false
	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		nextCalled = true
		return &mcp.GetPromptResult{}, nil
	}
	cfg := PromptVisibilityConfig{
		IsVisible: func(_ string, _ []string, _ bool, name string) bool { return name == "ok" },
	}
	handler := MCPPromptVisibilityMiddleware(cfg)(base)
	req := &mcp.ServerRequest[*mcp.GetPromptParams]{Params: &mcp.GetPromptParams{Name: "ok"}}

	_, err := handler(context.Background(), methodPromptsGet, req)
	if err != nil {
		t.Fatalf("visible prompt get should not error: %v", err)
	}
	if !nextCalled {
		t.Error("next handler must run for a visible prompt")
	}
}

func TestMCPPromptVisibilityMiddleware_DeniesHiddenGet(t *testing.T) {
	nextCalled := false
	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		nextCalled = true
		return &mcp.GetPromptResult{}, nil
	}
	cfg := PromptVisibilityConfig{
		IsVisible: func(_ string, _ []string, _ bool, name string) bool { return name != "secret" },
	}
	handler := MCPPromptVisibilityMiddleware(cfg)(base)
	req := &mcp.ServerRequest[*mcp.GetPromptParams]{Params: &mcp.GetPromptParams{Name: "secret"}}

	_, err := handler(context.Background(), methodPromptsGet, req)
	if err == nil {
		t.Fatal("hidden prompt get must be denied")
	}
	if nextCalled {
		t.Error("next handler must NOT run for a hidden prompt (no content fetched)")
	}
}

func TestMCPPromptVisibilityMiddleware_GetEdgeCasesAllow(t *testing.T) {
	cases := []struct {
		name string
		cfg  PromptVisibilityConfig
		req  mcp.Request
	}{
		{
			name: "nil IsVisible is a no-op",
			cfg:  PromptVisibilityConfig{},
			req:  &mcp.ServerRequest[*mcp.GetPromptParams]{Params: &mcp.GetPromptParams{Name: "x"}},
		},
		{
			name: "missing name defers to the SDK",
			cfg:  PromptVisibilityConfig{IsVisible: func(_ string, _ []string, _ bool, _ string) bool { return false }},
			req:  &mcp.ServerRequest[*mcp.GetPromptParams]{Params: &mcp.GetPromptParams{Name: ""}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nextCalled := false
			base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
				nextCalled = true
				return &mcp.GetPromptResult{}, nil
			}
			handler := MCPPromptVisibilityMiddleware(tc.cfg)(base)
			if _, err := handler(context.Background(), methodPromptsGet, tc.req); err != nil {
				t.Fatalf("expected allow, got error: %v", err)
			}
			if !nextCalled {
				t.Error("next must run when the get is allowed")
			}
		})
	}
}

func TestMCPPromptVisibilityMiddleware_GetNilRequestAllows(t *testing.T) {
	nextCalled := false
	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		nextCalled = true
		return &mcp.GetPromptResult{}, nil
	}
	cfg := PromptVisibilityConfig{IsVisible: func(_ string, _ []string, _ bool, _ string) bool { return false }}
	handler := MCPPromptVisibilityMiddleware(cfg)(base)
	if _, err := handler(context.Background(), methodPromptsGet, nil); err != nil {
		t.Fatalf("nil request should defer to the SDK, got error: %v", err)
	}
	if !nextCalled {
		t.Error("next must run when the name cannot be determined")
	}
}

func TestMCPPromptVisibilityMiddleware_ListErrorPropagates(t *testing.T) {
	wantErr := errTest
	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return nil, wantErr
	}
	cfg := PromptVisibilityConfig{IsVisible: func(_ string, _ []string, _ bool, _ string) bool { return true }}
	handler := MCPPromptVisibilityMiddleware(cfg)(base)
	if _, err := handler(context.Background(), methodPromptsList, nil); !errors.Is(err, wantErr) {
		t.Errorf("expected the upstream error to propagate, got %v", err)
	}
}

func TestResolvePromptCaller_PersonaNameFallback(t *testing.T) {
	// When PersonasForRoles is nil, a single PersonaName from an existing
	// PlatformContext is used as the caller's persona set.
	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return promptListResult("p"), nil
	}
	var gotPersonas []string
	cfg := PromptVisibilityConfig{
		IsVisible: func(_ string, personas []string, _ bool, _ string) bool {
			gotPersonas = personas
			return true
		},
	}
	ctx := WithPlatformContext(context.Background(), &PlatformContext{UserEmail: "x@example.com", PersonaName: "analyst"})
	handler := MCPPromptVisibilityMiddleware(cfg)(base)
	if _, err := handler(ctx, methodPromptsList, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gotPersonas) != 1 || gotPersonas[0] != "analyst" {
		t.Errorf("personas = %v; want [analyst]", gotPersonas)
	}
}

func TestMCPPromptVisibilityMiddleware_NoAuthYieldsEmptyIdentity(t *testing.T) {
	// No authenticator → nil PlatformContext → empty identity, so an
	// IsVisible that hides non-global-on-empty-email drops the personal prompt.
	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return promptListResult("mine"), nil
	}
	gotEmail := "unset"
	cfg := PromptVisibilityConfig{
		IsVisible: func(email string, _ []string, _ bool, _ string) bool {
			gotEmail = email
			return email != ""
		},
	}
	handler := MCPPromptVisibilityMiddleware(cfg)(base)
	got, _ := handler(context.Background(), methodPromptsList, nil)
	lr, ok := got.(*mcp.ListPromptsResult)
	if !ok {
		t.Fatalf("want *mcp.ListPromptsResult, got %T", got)
	}
	if gotEmail != "" {
		t.Errorf("expected empty identity with no authenticator, got %q", gotEmail)
	}
	if len(lr.Prompts) != 0 {
		t.Errorf("anonymous caller should see no personal prompts; got %d", len(lr.Prompts))
	}
}
