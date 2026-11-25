package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/refscheme"
	"github.com/txn2/mcp-data-platform/pkg/auth"
)

// TestReferenceSchemeReachesEveryDescription is #2818 through the assembled
// server: with reference_scheme set, no tool description or schema in
// tools/list says mcp:, generic forms included, and the platform's own
// instruction text says the deployment's scheme.
func TestReferenceSchemeReachesEveryDescription(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName, ReferenceScheme: "acme"},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Personas: PersonasConfig{Definitions: map[string]PersonaDef{"default": {
			DisplayName: "Default", Roles: []string{auth.RoleAnonymous}, Tools: ToolRulesDef{Allow: []string{"*"}},
		}}},
	}
	p, err := New(WithConfig(cfg))
	require.NoError(t, err)
	t.Cleanup(func() { _ = refscheme.Configure("", "") })
	require.NoError(t, p.Start(ctx))
	t.Cleanup(func() { _ = p.Stop(ctx) })

	t1, t2 := mcp.NewInMemoryTransports()
	ss, err := p.MCPServer().Connect(ctx, t1, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, t2, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	lt, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	require.NotEmpty(t, lt.Tools)
	for _, tool := range lt.Tools {
		raw, err := json.Marshal(tool)
		require.NoError(t, err)
		if i := bytes.Index(raw, []byte("mcp:")); i >= 0 {
			t.Errorf("%s still says mcp: ...%s...", tool.Name, string(raw)[max(0, i-60):min(len(raw), i+40)])
		}
	}
	if instr := cs.InitializeResult().Instructions; strings.Contains(instr, "mcp:") {
		t.Errorf("server instructions still say mcp:")
	}
}
