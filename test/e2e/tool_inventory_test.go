//go:build integration

package e2e

// The graphql kind's tool inventory, asserted on a running server against a
// real database (#1675, #1680).
//
// 1.131.0 listed graphql_export in the admin tool listing, with no title,
// while the MCP server had never registered it: the listing is built from each
// toolkit's Tools() and the titles come from the server's own tools/list, so
// the two halves of the same row disagreed and nothing compared them. The
// tools were asserted from the toolkit's list, and the standalone set
// assertion (#1644) covers a server with no connections at all.
//
// The export tool needs a portal, which needs a database, so this is where all
// three of the kind's tools exist at once.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/test/e2e/helpers"
)

func TestToolInventory_GraphQLKindIsRegisteredOnTheServerItIsListedOn(t *testing.T) {
	pgDSN := helpers.StartPostgres(t)
	ctx := context.Background()

	// One upstream for the GraphQL endpoint and the portal's S3 backend: both
	// refuse at once. Registration is what is under test, and a connection
	// registers its tools whether or not its endpoint answers.
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "refused", http.StatusForbidden)
	}))
	defer refusing.Close()

	cfg := helpers.FileDBAdminConfig(pgDSN)
	enabled := true
	cfg.Portal = platform.PortalConfig{Enabled: &enabled, S3Connection: "e2e", S3Bucket: "portal-assets"}
	cfg.Toolkits = map[string]any{
		"s3": map[string]any{"enabled": true, "instances": map[string]any{
			"e2e": map[string]any{
				"region": "us-east-1", "access_key_id": "a", "secret_access_key": "b",
				"endpoint": refusing.URL, "use_path_style": true,
			},
		}},
		"graphql": map[string]any{"enabled": true, "instances": map[string]any{
			"gql": map[string]any{"endpoint_url": refusing.URL, "connect_timeout": "2s", "call_timeout": "2s"},
		}},
	}

	p, err := platform.New(platform.WithConfig(cfg))
	if err != nil {
		t.Fatalf("creating platform: %v", err)
	}
	defer func() { _ = p.Close() }()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("starting platform: %v", err)
	}
	defer func() { _ = p.Stop(ctx) }()
	if err := p.WireRuntime(platform.RuntimeConfig{Transport: "http", Address: ":0"}); err != nil {
		t.Fatalf("runtime wiring refused the tool inventory: %v", err)
	}

	ts := httptest.NewServer(helpers.BuildAdminHandler(p))
	defer ts.Close()
	client := helpers.NewAdminClient(ts.URL, helpers.AdminAPIKey)

	want := []string{"graphql_discover", "graphql_query", "graphql_export"}

	t.Run("the MCP server carries every one of the kind's tools", func(t *testing.T) {
		// The set is asserted rather than a count, so a tool that went
		// missing is named (#1644). It is read through a real client
		// session, which is what a caller has.
		helpers.AssertToolSet(t, kindTools(t, listMCPTools(ctx, t, p.MCPServer()), "graphql_"), want...)
	})

	t.Run("the admin listing carries the same tools, each with its title", func(t *testing.T) {
		listing, status, err := client.ListTools()
		if err != nil {
			t.Fatalf("listing tools: %v", err)
		}
		if status != http.StatusOK {
			t.Fatalf("expected 200, got %d", status)
		}
		titles := make(map[string]string)
		var names []string
		for _, tool := range listing.Tools {
			titles[tool.Name] = tool.Title
			names = append(names, tool.Name)
		}
		helpers.AssertToolSet(t, kindTools(t, names, "graphql_"), want...)
		// A title comes from the server's own listing. An empty one is the
		// exact symptom #1675 was reported as.
		for _, name := range want {
			if titles[name] == "" {
				t.Errorf("%s is listed with no title, so the MCP server does not know it", name)
			}
		}
	})
}

// kindTools narrows a listing to the tools of one kind, by the prefix every
// one of its tools carries.
func kindTools(t *testing.T, names []string, prefix string) []string {
	t.Helper()
	var out []string
	for _, name := range names {
		if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			out = append(out, name)
		}
	}
	return out
}

// listMCPTools reads tools/list the way a client does.
func listMCPTools(ctx context.Context, t *testing.T, server *mcp.Server) []string {
	t.Helper()
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer func() { _ = serverSession.Close() }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "e2e-inventory", Version: "v0"}, nil).Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}
