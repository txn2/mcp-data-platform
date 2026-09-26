package resultbudget_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/resultbudget"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// The result budget through the assembled chain with a toolkit resolved from
// the registry the way the platform resolves it (#1878): the graphql kind,
// whose fitter withholds data a model cannot read whole.

const itBudget = 4096

type allowAll struct{}

func (allowAll) IsAuthorized(context.Context, string, []string, string, string) (authorized bool, persona, reason string) {
	return true, "analyst", ""
}

// wideAnswer is a GraphQL answer of n aliased fields, past itBudget.
func wideAnswer(n int) string {
	fields := make([]string, 0, n)
	for i := range n {
		fields = append(fields, fmt.Sprintf(`"f%04d_%s":"Query"`, i, strings.Repeat("w", 20)))
	}
	return `{"data":{` + strings.Join(fields, ",") + `}}`
}

func graphqlServer(t *testing.T, source string) *mcp.ClientSession {
	t.Helper()
	answer := wideAnswer(400)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(upstream.Close)

	cfg, err := graphqlkit.ParseConfig(map[string]any{"endpoint_url": upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnectionName = "gql"
	tk := graphqlkit.NewMulti(graphqlkit.MultiConfig{DefaultName: "gql", Instances: map[string]graphqlkit.Config{"gql": cfg}})
	if err := tk.SetSchema(context.Background(), "gql", []byte("type Query { name: String }")); err != nil {
		t.Fatal(err)
	}
	reg := registry.NewRegistry()
	if err := reg.Register(tk); err != nil {
		t.Fatal(err)
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	tk.RegisterTools(server)
	server.AddReceivingMiddleware(resultbudget.Capture())
	server.AddReceivingMiddleware(resultbudget.Middleware(resultbudget.Config{MaxBytes: itBudget}, resultbudget.RegistryLookup(reg)))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&middleware.NoopAuthenticator{DefaultUserID: "u1", DefaultRoles: []string{"analyst"}},
		allowAll{}, reg, middleware.ToolCallConfig{Transport: "http"}))

	ctx := context.Background()
	if source != "" {
		ctx = middleware.WithSource(ctx, source)
	}
	t1, t2 := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v1"}, nil).Connect(context.Background(), t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Close()
	})
	return cs
}

func queryWide(t *testing.T, cs *mcp.ClientSession, variables any) (res *mcp.CallToolResult, out map[string]any) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: graphqlkit.ToolQuery,
		Arguments: map[string]any{
			"connection": "gql",
			"query":      "query W($show: Boolean!) { name @include(if: $show) }",
			"variables":  variables,
		},
	})
	if err != nil {
		t.Fatalf("graphql_query: %v", err)
	}
	if res.IsError {
		t.Fatalf("graphql_query refused: %v", res.Content)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("first block is %T", res.Content[0])
	}
	if err := json.Unmarshal([]byte(tc.Text), &out); err != nil {
		t.Fatalf("result is not JSON (cut by the generic cut?): %v", err)
	}
	return res, out
}

// TestGraphQLQueryIsFittedInItsOwnShape: a model's graphql_query past the
// budget has its data withheld and the export call handed back, whichever
// form variables takes; a script's is returned whole.
func TestGraphQLQueryIsFittedInItsOwnShape(t *testing.T) {
	for form, variables := range map[string]any{"object": map[string]any{"show": true}, "string": `{"show":true}`} {
		t.Run("variables_as_"+form, func(t *testing.T) {
			res, out := queryWide(t, graphqlServer(t, ""), variables)
			if size := resultbudget.TextSize(res); size > itBudget {
				t.Errorf("result is %d characters; want it inside %d", size, itBudget)
			}
			if truncated, _ := out["data_truncated"].(bool); !truncated || out["data"] != nil || out["export_arguments"] == nil {
				t.Errorf("out = %v; want the data withheld and the export call", out)
			}
		})
	}
	t.Run("a script's result is whole", func(t *testing.T) {
		_, out := queryWide(t, graphqlServer(t, middleware.SourceScript), map[string]any{"show": true})
		if data, _ := out["data"].(map[string]any); len(data) != 400 {
			t.Errorf("data holds %d fields; want all 400", len(data))
		}
	})
}
