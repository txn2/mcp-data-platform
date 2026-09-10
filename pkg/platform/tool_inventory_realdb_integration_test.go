//go:build integration

package platform_test

// Real-Postgres proof that the tool inventory a client sees is the inventory
// the toolkits name (#1680).
//
// It builds the platform through the real startup path — New, Start,
// WireRuntime — with every connection kind the dev configuration carries and
// every optional dependency that changes which tools register: a database, a
// portal store with an S3 blob backend (which is what enables the three
// _export tools), and the api, mcp and graphql kinds that auto-enable behind
// the admin UI. It then lists tools through a real MCP client and compares.
//
// Both directions fail here. At startup only the first does, because a
// fan-out kind mid-reconcile must not take a deployment down; a test has no
// such constraint, so this is where the leak direction is a red result.
//
// 1.131.0 shipped graphql_export named by the toolkit and unknown to the
// server (#1675), with every unit gate green: the toolkit's own test asserted
// its Tools() list, and nothing compared that list with what an mcp.Server
// holds. Run under `make test-realdb`.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

func TestRealDB_EveryToolAToolkitNamesIsRegisteredOnTheServer(t *testing.T) {
	_, dsn := testdb.NewWithDSN(t)
	ctx := context.Background()

	// Upstreams that answer at once with a refusal. Nothing here calls a
	// tool: what is under test is which tools registered, and a connection
	// that cannot reach its endpoint registers its tools all the same.
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"errors":[{"message":"refused"}]}`, http.StatusForbidden)
	}))
	t.Cleanup(refusing.Close)

	cfg := &platform.Config{
		Server:   platform.ServerConfig{Name: "tool-inventory-it", Version: "1.0.0"},
		Database: platform.DatabaseConfig{DSN: dsn, MaxOpenConns: 5},
		Personas: platform.PersonasConfig{Definitions: map[string]platform.PersonaDef{"default": {
			DisplayName: "Default",
			Roles:       []string{auth.RoleAnonymous},
			Tools:       platform.ToolRulesDef{Allow: []string{"*"}},
			Connections: platform.ConnectionRulesDef{Allow: []string{"*"}},
		}}},
		Portal: platform.PortalConfig{
			Enabled:      new(true),
			S3Connection: "acme",
			S3Bucket:     "portal-assets",
		},
		Toolkits: map[string]any{
			"trino": map[string]any{"enabled": true, "instances": map[string]any{
				"acme": map[string]any{"host": "127.0.0.1", "port": 1, "user": "t"},
			}},
			"s3": map[string]any{"enabled": true, "instances": map[string]any{
				"acme": map[string]any{
					"region": "us-east-1", "access_key_id": "a", "secret_access_key": "b",
					"endpoint": refusing.URL, "use_path_style": true,
				},
			}},
			// The three kinds whose connections live in the database and are
			// added through the admin UI. Each registers its tools from the
			// kind being enabled, with or without a connection.
			"api": map[string]any{"enabled": true},
			"mcp": map[string]any{"enabled": true},
			"graphql": map[string]any{"enabled": true, "instances": map[string]any{
				"gql": map[string]any{"endpoint_url": refusing.URL, "connect_timeout": "2s", "call_timeout": "2s"},
			}},
		},
	}

	p, err := platform.New(platform.WithConfig(cfg))
	require.NoError(t, err)
	defer func() { _ = p.Close() }()
	require.NoError(t, p.Start(ctx))
	defer func() { _ = p.Stop(ctx) }()
	require.NoError(t, p.WireRuntime(platform.RuntimeConfig{Transport: "http", Address: ":0"}),
		"the platform refuses to start on a tool inventory it cannot honor")

	named := platform.RegisteredToolNames(p.ToolkitRegistry().AllTools(), p.PlatformTools())
	listed := listToolsOverAClient(ctx, t, p.MCPServer())

	// graphql_export is named explicitly: it is the tool #1675 shipped
	// unreachable, and a comparison that happened to pass because neither
	// side carried the name would prove nothing.
	require.Contains(t, named, "graphql_export", "the graphql toolkit names its export tool")

	missing, unexpected := diffNames(named, listed)
	require.Empty(t, missing, "named by a toolkit and absent from tools/list")
	require.Empty(t, unexpected, "carried by tools/list and claimed by no toolkit or platform tool")
}

// listToolsOverAClient reads tools/list the way a caller does, through a real
// client session over the in-memory transport.
func listToolsOverAClient(ctx context.Context, t *testing.T, server *mcp.Server) []string {
	t.Helper()
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	require.NoError(t, err)
	defer func() { _ = serverSession.Close() }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "inventory-it", Version: "v0"}, nil).Connect(ctx, t2, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	res, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	require.NoError(t, err)
	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// diffNames returns what the listing lacks and what it carries beyond the
// named set, both sorted so a failure reads the same on every run.
func diffNames(named, listed []string) (missing, unexpected []string) {
	inListing := make(map[string]bool, len(listed))
	for _, name := range listed {
		inListing[name] = true
	}
	isNamed := make(map[string]bool, len(named))
	for _, name := range named {
		isNamed[name] = true
		if !inListing[name] {
			missing = append(missing, name)
		}
	}
	for _, name := range listed {
		if !isNamed[name] {
			unexpected = append(unexpected, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(unexpected)
	return missing, unexpected
}
