package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var errPromptUpstream = errors.New("upstream failure")

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

func TestMCPPromptVisibilityMiddleware_InjectsDatabasePrompts(t *testing.T) {
	// The static list holds only built-ins; the caller's database prompts are
	// appended under their scope-prefixed names, and ListVisible receives the
	// resolved caller identity (email + personas).
	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return promptListResult("builtin-overview"), nil
	}
	var gotEmail string
	var gotPersonas []string
	cfg := PromptVisibilityConfig{
		PersonasForRoles: func(_ []string) []string { return []string{"analyst"} },
		Authenticator: &mockAuthenticator{authenticateFunc: func(_ context.Context) (*UserInfo, error) {
			return &UserInfo{Email: "sarah@example.com", Roles: []string{"r-analyst"}}, nil
		}},
		ListVisible: func(_ context.Context, email string, personas []string) []*mcp.Prompt {
			gotEmail, gotPersonas = email, personas
			return []*mcp.Prompt{{Name: "global-report"}, {Name: "personal-report"}, {Name: "analyst-runbook"}}
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
	want := []string{"builtin-overview", "global-report", "personal-report", "analyst-runbook"}
	names := promptResultNames(lr)
	if len(names) != len(want) {
		t.Fatalf("want %v, got %v", want, names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("position %d: want %q, got %q", i, want[i], names[i])
		}
	}
	if gotEmail != "sarah@example.com" || len(gotPersonas) != 1 || gotPersonas[0] != "analyst" {
		t.Errorf("ListVisible got email=%q personas=%v", gotEmail, gotPersonas)
	}
}

func TestMCPPromptVisibilityMiddleware_NilListVisibleIsNoOp(t *testing.T) {
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
		t.Errorf("nil ListVisible must not change the list; got %d", len(lr.Prompts))
	}
}

func TestMCPPromptVisibilityMiddleware_ListErrorPropagates(t *testing.T) {
	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return nil, errPromptUpstream
	}
	cfg := PromptVisibilityConfig{ListVisible: func(_ context.Context, _ string, _ []string) []*mcp.Prompt { return nil }}
	handler := MCPPromptVisibilityMiddleware(cfg)(base)
	if _, err := handler(context.Background(), methodPromptsList, nil); !errors.Is(err, errPromptUpstream) {
		t.Errorf("expected upstream error to propagate, got %v", err)
	}
}

func TestMCPPromptVisibilityMiddleware_PassThroughNonPromptMethods(t *testing.T) {
	called := false
	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{}, nil
	}
	cfg := PromptVisibilityConfig{
		ListVisible: func(_ context.Context, _ string, _ []string) []*mcp.Prompt { called = true; return nil },
		GetByName: func(_ context.Context, _ string, _ []string, _ string, _ map[string]string) (*mcp.GetPromptResult, bool) {
			called = true
			return nil, false
		},
	}
	handler := MCPPromptVisibilityMiddleware(cfg)(base)
	if _, err := handler(context.Background(), "tools/call", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("prompt callbacks must not run for non-prompt methods")
	}
}

func TestMCPPromptVisibilityMiddleware_ServesDatabaseGet(t *testing.T) {
	nextCalled := false
	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		nextCalled = true
		return &mcp.GetPromptResult{}, nil
	}
	cfg := PromptVisibilityConfig{
		GetByName: func(_ context.Context, _ string, _ []string, name string, _ map[string]string) (*mcp.GetPromptResult, bool) {
			if name == "personal-report" {
				return &mcp.GetPromptResult{}, true
			}
			return nil, false
		},
	}
	handler := MCPPromptVisibilityMiddleware(cfg)(base)
	req := &mcp.ServerRequest[*mcp.GetPromptParams]{Params: &mcp.GetPromptParams{Name: "personal-report"}}
	if _, err := handler(context.Background(), methodPromptsGet, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nextCalled {
		t.Error("a database prompt should be served by the middleware, not passed to next")
	}
}

func TestMCPPromptVisibilityMiddleware_GetFallsThroughForBuiltins(t *testing.T) {
	cases := []struct {
		name string
		cfg  PromptVisibilityConfig
		req  mcp.Request
	}{
		{"unresolved database name", PromptVisibilityConfig{
			GetByName: func(_ context.Context, _ string, _ []string, _ string, _ map[string]string) (*mcp.GetPromptResult, bool) {
				return nil, false
			},
		}, &mcp.ServerRequest[*mcp.GetPromptParams]{Params: &mcp.GetPromptParams{Name: "platform-overview"}}},
		{"nil GetByName", PromptVisibilityConfig{}, &mcp.ServerRequest[*mcp.GetPromptParams]{Params: &mcp.GetPromptParams{Name: "x"}}},
		{"nil request", PromptVisibilityConfig{
			GetByName: func(_ context.Context, _ string, _ []string, _ string, _ map[string]string) (*mcp.GetPromptResult, bool) {
				return nil, false
			},
		}, nil},
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
				t.Fatalf("unexpected error: %v", err)
			}
			if !nextCalled {
				t.Error("a name the middleware does not serve must fall through to the static registry")
			}
		})
	}
}

func TestResolvePromptCaller_PersonaNameFallback(t *testing.T) {
	// With no PersonasForRoles, a single PersonaName from an existing
	// PlatformContext is used as the caller's persona set.
	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return promptListResult(), nil
	}
	var gotEmail string
	var gotPersonas []string
	cfg := PromptVisibilityConfig{
		ListVisible: func(_ context.Context, email string, personas []string) []*mcp.Prompt {
			gotEmail, gotPersonas = email, personas
			return nil
		},
	}
	ctx := WithPlatformContext(context.Background(), &PlatformContext{UserEmail: "x@example.com", PersonaName: "analyst"})
	handler := MCPPromptVisibilityMiddleware(cfg)(base)
	if _, err := handler(ctx, methodPromptsList, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotEmail != "x@example.com" || len(gotPersonas) != 1 || gotPersonas[0] != "analyst" {
		t.Errorf("got email=%q personas=%v", gotEmail, gotPersonas)
	}
}

func TestResolvePromptCaller_NoAuthEmptyIdentity(t *testing.T) {
	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return promptListResult(), nil
	}
	gotEmail := "unset"
	cfg := PromptVisibilityConfig{
		ListVisible: func(_ context.Context, email string, _ []string) []*mcp.Prompt { gotEmail = email; return nil },
	}
	handler := MCPPromptVisibilityMiddleware(cfg)(base)
	if _, err := handler(context.Background(), methodPromptsList, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotEmail != "" {
		t.Errorf("no authenticator should yield empty identity, got %q", gotEmail)
	}
}
