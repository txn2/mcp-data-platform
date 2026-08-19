//go:build integration

package platform_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// answeringTrino is a TrinoClient whose Query answers one row, so the real
// mcp-trino toolkit produces the success body a deployment's trino_query does.
// Every other method reports that nothing is behind it.
type answeringTrino struct{}

func (answeringTrino) Query(context.Context, string, trinoclient.QueryOptions) (*trinoclient.QueryResult, error) {
	return &trinoclient.QueryResult{
		Columns: []trinoclient.ColumnInfo{{Name: "n", Type: "integer"}},
		Rows:    []map[string]any{{"n": 1}},
		Stats:   trinoclient.QueryStats{RowCount: 1},
	}, nil
}

func (answeringTrino) Explain(context.Context, string, trinoclient.ExplainType) (*trinoclient.ExplainResult, error) {
	return nil, errors.New("no trino behind this test")
}

func (answeringTrino) ListCatalogs(context.Context) ([]string, error) {
	return nil, errors.New("no trino behind this test")
}

func (answeringTrino) ListSchemas(context.Context, string) ([]string, error) {
	return nil, errors.New("no trino behind this test")
}

func (answeringTrino) ListTables(context.Context, string, string) ([]trinoclient.TableInfo, error) {
	return nil, errors.New("no trino behind this test")
}

func (answeringTrino) DescribeTable(context.Context, string, string, string) (*trinoclient.TableInfo, error) {
	return nil, errors.New("no trino behind this test")
}

// rawTrinoToolkit registers the real mcp-trino toolkit, unwrapped, under the
// trino kind so the platform records its calls as data access (which is what
// stamps call_reference) and routes them by the connection name it carries.
type rawTrinoToolkit struct{ tk *trinotools.Toolkit }

func (rawTrinoToolkit) Kind() string                          { return "trino" }
func (rawTrinoToolkit) Name() string                          { return "acme" }
func (rawTrinoToolkit) Connection() string                    { return "acme" }
func (r rawTrinoToolkit) RegisterTools(s *mcp.Server)         { r.tk.RegisterAll(s) }
func (rawTrinoToolkit) SetSemanticProvider(semantic.Provider) {}
func (rawTrinoToolkit) SetQueryProvider(query.Provider)       {}
func (rawTrinoToolkit) Close() error                          { return nil }
func (rawTrinoToolkit) Tools() []string {
	names := make([]string, 0, len(trinotools.AllTools()))
	for _, n := range trinotools.AllTools() {
		names = append(names, string(n))
	}
	return names
}

// TestRealDB_TrinoQueryResultWithCallReferenceValidatesAgainstAdvertisedSchema
// is the deployment's exact #1381 failure, reproduced end to end: on a real
// database the audit pipeline records every data call, so the platform appends
// call_reference to a successful trino_query result; mcp-trino's typed handler
// advertises the closed schema the SDK inferred for QueryOutput; and a client
// that validates structuredContent against the schema it was advertised must
// accept the result. The test asserts the key is really present, so it cannot
// pass vacuously on a result the platform left alone.
func TestRealDB_TrinoQueryResultWithCallReferenceValidatesAgainstAdvertisedSchema(t *testing.T) {
	_, dsn := testdb.NewWithDSN(t)
	p, err := platform.New(platform.WithConfig(&platform.Config{
		Server:   platform.ServerConfig{Name: "schema-it", Version: "1.0.0"},
		Database: platform.DatabaseConfig{DSN: dsn, MaxOpenConns: 5},
		Personas: platform.PersonasConfig{Definitions: map[string]platform.PersonaDef{"default": {
			DisplayName: "Default",
			Roles:       []string{auth.RoleAnonymous},
			Tools:       platform.ToolRulesDef{Allow: []string{"*"}},
			Connections: platform.ConnectionRulesDef{Allow: []string{"*"}},
		}}},
		// The search-first gate would refuse trino_query before the toolkit
		// runs; this gate is about the result the toolkit's success produces.
		Workflow: platform.WorkflowConfig{RequireSearch: new(false)},
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	require.NoError(t, p.ToolkitRegistry().Register(rawTrinoToolkit{tk: trinotools.NewToolkit(answeringTrino{}, trinotools.Config{})}))

	ctx := t.Context()
	require.NoError(t, p.Start(ctx))
	t.Cleanup(func() { _ = p.Stop(ctx) })

	t1, t2 := mcp.NewInMemoryTransports()
	ss, err := p.MCPServer().Connect(ctx, t1, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, t2, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	// The schema this server advertised for trino_query, and whether it
	// advertises the purpose argument a real caller would state.
	lt, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	var advertised *jsonschema.Resolved
	takesPurpose := false
	for _, tool := range lt.Tools {
		if tool.Name != "trino_query" {
			continue
		}
		raw, err := json.Marshal(tool.OutputSchema)
		require.NoError(t, err)
		var s jsonschema.Schema
		require.NoError(t, json.Unmarshal(raw, &s))
		advertised, err = s.Resolve(nil)
		require.NoError(t, err)
		in, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		var inSchema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		require.NoError(t, json.Unmarshal(in, &inSchema))
		_, takesPurpose = inSchema.Properties["purpose"]
	}
	require.NotNil(t, advertised, "trino_query advertises an output schema")

	info, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "platform_info", Arguments: map[string]any{}})
	require.NoError(t, err)
	var minted struct {
		SessionID string `json:"session_id"`
	}
	b, err := json.Marshal(info.StructuredContent)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &minted))
	require.NotEmpty(t, minted.SessionID)

	args := map[string]any{"session_id": minted.SessionID, "sql": "SELECT 1 AS n", "connection": "acme"}
	if takesPurpose {
		args["purpose"] = "Proving a recorded data call's result validates against its advertised schema."
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "trino_query", Arguments: args})
	require.NoError(t, err)
	require.False(t, res.IsError, "the query succeeds: %v", res.Content)
	require.NotNil(t, res.StructuredContent)

	sc, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(sc, &body))
	require.Contains(t, body, middleware.CallReferenceKey, "a recorded data call carries its call_reference: %s", sc)
	assert.Contains(t, body, "columns", "the toolkit's own body is intact")

	var v any
	require.NoError(t, json.Unmarshal(sc, &v))
	assert.NoError(t, advertised.Validate(v), "structuredContent with call_reference validates against the advertised schema: %s", sc)
}
