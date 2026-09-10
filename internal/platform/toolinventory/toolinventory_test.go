package toolinventory

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// inventoryToolkit names one set of tools and registers another, which is the
// shape every wiring-order fault takes: the name reaches the admin listing and
// the handler never reaches the server (#1675).
type inventoryToolkit struct {
	kind     string
	name     string
	named    []string
	register []string
}

func (tk inventoryToolkit) Kind() string       { return tk.kind }
func (tk inventoryToolkit) Name() string       { return tk.name }
func (tk inventoryToolkit) Connection() string { return tk.name }
func (tk inventoryToolkit) Tools() []string    { return tk.named }
func (tk inventoryToolkit) RegisterTools(s *mcp.Server) {
	for _, name := range tk.register {
		s.AddTool(&mcp.Tool{
			Name:        name,
			Description: name,
			InputSchema: &jsonschema.Schema{Type: "object"},
		}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})
	}
}
func (inventoryToolkit) SetSemanticProvider(semantic.Provider) {}
func (inventoryToolkit) SetQueryProvider(query.Provider)       {}
func (inventoryToolkit) Close() error                          { return nil }

// newDeps registers one toolkit's tools on a server and returns what Verify
// reads. The platform's own tools are named but never registered here, so
// each case says explicitly whether it expects them.
func newDeps(t *testing.T, tk inventoryToolkit, platformTools ...PlatformTool) Deps {
	t.Helper()
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(tk))
	server := mcp.NewServer(&mcp.Implementation{Name: "inventory", Version: "v0"}, nil)
	reg.RegisterAllTools(server)
	return Deps{Server: server, Registry: reg, PlatformTools: platformTools}
}

func TestVerifyPassesWhenEveryNamedToolIsRegistered(t *testing.T) {
	t.Parallel()
	d := newDeps(t, inventoryToolkit{
		kind:     "graphql",
		name:     "gql",
		named:    []string{"graphql_discover", "graphql_query", "graphql_export"},
		register: []string{"graphql_discover", "graphql_query", "graphql_export"},
	})
	require.NoError(t, Verify(context.Background(), d))
}

func TestVerifyFailsNamingTheToolTheServerLacks(t *testing.T) {
	t.Parallel()
	d := newDeps(t, inventoryToolkit{
		kind:     "graphql",
		name:     "gql",
		named:    []string{"graphql_discover", "graphql_query", "graphql_export"},
		register: []string{"graphql_discover", "graphql_query"},
	})
	err := Verify(context.Background(), d)
	require.Error(t, err)
	require.Contains(t, err.Error(), "graphql_export")
	require.Contains(t, err.Error(), "graphql/gql")
	require.NotContains(t, err.Error(), "graphql_query")
}

// A tool the operator hid with tools.deny is absent from the listing by
// intent, so its absence must not read as a wiring fault.
func TestVerifyAcceptsAToolTheOperatorHid(t *testing.T) {
	t.Parallel()
	d := newDeps(t, inventoryToolkit{
		kind:     "graphql",
		name:     "gql",
		named:    []string{"graphql_discover", "graphql_export"},
		register: []string{"graphql_discover"},
	})
	d.Visible = func(name string) bool { return name != "graphql_export" }
	require.NoError(t, Verify(context.Background(), d))
}

// The platform's own tools are held to the same rule as a toolkit's: the
// instruction baseline and the persona gates read them from the same list, so
// one that never reached the server is the same defect.
func TestVerifyCoversThePlatformsOwnTools(t *testing.T) {
	t.Parallel()
	d := newDeps(t,
		inventoryToolkit{kind: "graphql", name: "gql", named: []string{"graphql_query"}, register: []string{"graphql_query"}},
		PlatformTool{Name: "platform_info", Kind: "platform"},
	)
	err := Verify(context.Background(), d)
	require.Error(t, err)
	require.Contains(t, err.Error(), "platform: platform_info")
}

// The leak direction warns rather than failing: a name on the server that
// nothing claims is invisible to the admin listing and the persona gates, but
// refusing to boot over it would trade a listing defect for an outage.
func TestVerifyWarnsAboutAToolNothingClaims(t *testing.T) {
	d := newDeps(t, inventoryToolkit{
		kind:     "graphql",
		name:     "gql",
		named:    []string{"graphql_discover"},
		register: []string{"graphql_discover", "graphql_orphan"},
	})

	var logged bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(restore)

	require.NoError(t, Verify(context.Background(), d))
	require.Contains(t, logged.String(), "graphql_orphan")
}

// A platform assembled without an MCP server, or without a registry, holds no
// listing to be wrong about.
func TestVerifySkipsWhatItCannotRead(t *testing.T) {
	t.Parallel()
	require.NoError(t, Verify(context.Background(), Deps{}))

	server := mcp.NewServer(&mcp.Implementation{Name: "inventory", Version: "v0"}, nil)
	require.NoError(t, Verify(context.Background(), Deps{Server: server}))
}
