package promptlayer

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// staticAuth authenticates every request as one fixed caller, standing in for
// the platform authenticator in the assembled-server tests.
type staticAuth struct{ email string }

func (a *staticAuth) Authenticate(_ context.Context) (*middleware.UserInfo, error) {
	return &middleware.UserInfo{UserID: a.email, Email: a.email}, nil
}

// connectServingClient assembles the real serving stack for one caller: an
// mcp.Server carrying the handle's registered static prompts plus the
// prompt-visibility middleware wired to the handle's serving callbacks, with an
// in-memory client session on top.
func connectServingClient(t *testing.T, h *Handle, email string) (session *mcp.ClientSession, cleanup func()) {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	h.RegisterPlatformPrompts(server)
	server.AddReceivingMiddleware(middleware.MCPPromptVisibilityMiddleware(middleware.PromptVisibilityConfig{
		Authenticator: &staticAuth{email: email},
		ListVisible:   h.ListVisible,
		GetByName:     h.GetByName,
	}))
	return connectTestClient(t, server)
}

// The assembled-system proof for the bare-name identity contract (#1008): a
// caller whose personal prompt shares a name with a global prompt sees, through
// a real mcp.Server with the real prompt-visibility middleware, the personal
// prompt under the bare name, the global under its qualified fallback name, and
// static built-ins untouched; prompts/get agrees with prompts/list on every one
// of those names, and titles carry display names end to end.
func TestPromptServing_EndToEnd_PersonalShadowsGlobal(t *testing.T) {
	h, store := newTestHandle()
	h.operatorPrompts = []PromptSpec{{
		Name: "builtin-overview", DisplayName: "Builtin Overview",
		Description: "static", Content: "static body",
	}}
	store.prompts["report"] = &prompt.Prompt{
		Name: "report", Scope: prompt.ScopeGlobal, Content: "global body",
		Description: "the global one", Enabled: true,
	}
	store.prompts["report:sarah"] = &prompt.Prompt{
		Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com",
		DisplayName: "My Report", Content: "personal body", Enabled: true,
	}

	session, cleanup := connectServingClient(t, h, "sarah@example.com")
	defer cleanup()
	ctx := context.Background()

	listed, err := session.ListPrompts(ctx, nil)
	require.NoError(t, err)
	byName := map[string]*mcp.Prompt{}
	for _, pr := range listed.Prompts {
		byName[pr.Name] = pr
	}
	require.Len(t, listed.Prompts, 3, "static + bare winner + qualified shadowed, no duplicates")
	require.NotNil(t, byName["builtin-overview"], "static prompt keeps its bare name")
	assert.Equal(t, "Builtin Overview", byName["builtin-overview"].Title)
	require.NotNil(t, byName["report"], "personal prompt wins the bare name for its owner")
	assert.Equal(t, "My Report", byName["report"].Title, "title carries the display name through the protocol")
	require.NotNil(t, byName["global-report"], "shadowed global stays visible under its qualified name")
	assert.Contains(t, byName["global-report"].Description, "shadowed")

	getText := func(name string) string {
		res, err := session.GetPrompt(ctx, &mcp.GetPromptParams{Name: name})
		require.NoError(t, err, "prompts/get %s", name)
		require.Len(t, res.Messages, 1)
		tc, ok := res.Messages[0].Content.(*mcp.TextContent)
		require.True(t, ok)
		return tc.Text
	}
	assert.Equal(t, "personal body", getText("report"), "bare get agrees with the list")
	assert.Equal(t, "global body", getText("global-report"), "qualified get reaches the shadowed global")
	assert.Equal(t, "static body", getText("builtin-overview"), "static prompt still served by the registry")
}

// A different caller with no personal prompt sees the same global under its
// bare name through the same assembled stack: shadowing is per-viewer and
// promotion never renames what other viewers invoke.
func TestPromptServing_EndToEnd_UnshadowedViewer(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["report"] = &prompt.Prompt{
		Name: "report", Scope: prompt.ScopeGlobal, Content: "global body", Enabled: true,
	}
	store.prompts["report:sarah"] = &prompt.Prompt{
		Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com",
		Content: "personal body", Enabled: true,
	}

	session, cleanup := connectServingClient(t, h, "bob@example.com")
	defer cleanup()
	ctx := context.Background()

	listed, err := session.ListPrompts(ctx, nil)
	require.NoError(t, err)
	require.Len(t, listed.Prompts, 1)
	assert.Equal(t, "report", listed.Prompts[0].Name)

	res, err := session.GetPrompt(ctx, &mcp.GetPromptParams{Name: "report"})
	require.NoError(t, err)
	tc, ok := res.Messages[0].Content.(*mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "global body", tc.Text, "bob resolves the global, never sarah's personal prompt")
}
