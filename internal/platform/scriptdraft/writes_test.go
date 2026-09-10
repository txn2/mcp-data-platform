package scriptdraft

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// writingServer assembles a server carrying one write-class platform tool and
// one proxied tool an upstream declares read-only, so a draft has something
// real for the barrier to admit and refuse.
func writingServer(t *testing.T, calls *[]string) *mcp.Server {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	// The stub tools take an open argument map, because these calls carry the
	// arguments the real tools take and the SDK validates against the declared
	// schema.
	record := func(name string) func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
		return func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			*calls = append(*calls, name)
			return &mcp.CallToolResult{}, map[string]any{"ok": true}, nil
		}
	}
	mcp.AddTool(s, &mcp.Tool{Name: "manage_resource"}, record("manage_resource"))
	mcp.AddTool(s, &mcp.Tool{Name: "manage_table"}, record("manage_table"))
	mcp.AddTool(s, &mcp.Tool{
		Name: "vendor__list_contacts", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, record("vendor__list_contacts"))
	return s
}

// TestRun_RefusesAWriteByDefault is #1664: a draft is a rehearsal, so the
// landing pipeline is exercised without landing.
func TestRun_RefusesAWriteByDefault(t *testing.T) {
	var calls []string
	outcome, err := New(writingServer(t, &calls), nil).Run(context.Background(), Request{
		Source:   `platform.call("manage_resource", {"action": "create", "filename": "daily.csv"})`,
		Name:     "ingest",
		Identity: jane,
	})

	require.NoError(t, err)
	require.True(t, outcome.Failed())
	assert.Contains(t, outcome.Err.Error(), "manage_resource action=create persists outside this run")
	assert.Empty(t, calls, "and the tool is never reached")
	require.NotNil(t, outcome.Result.RefusedWrite)
	assert.Equal(t, "manage_resource", outcome.Result.RefusedWrite.Tool)
}

// TestRun_AllowWritesLetsThePipelineLandAndReportsIt is the opt-in: the person
// asking owns what it writes, and the outcome says what that was.
func TestRun_AllowWritesLetsThePipelineLandAndReportsIt(t *testing.T) {
	var calls []string
	outcome, err := New(writingServer(t, &calls), nil).Run(context.Background(), Request{
		Source: `
platform.call("manage_resource", {"action": "create", "filename": "daily.csv"})
platform.call("manage_table", {"action": "list"})
platform.call("manage_table", {"action": "register", "name": "daily"})
`,
		Name: "ingest", Identity: jane, AllowWrites: true,
	})

	require.NoError(t, err)
	require.False(t, outcome.Failed(), outcome.Err)
	assert.Equal(t, []string{"manage_resource", "manage_table", "manage_table"}, calls)
	require.Len(t, outcome.Result.Writes, 2)
	assert.Equal(t, "manage_resource action=create", outcome.Result.Writes[0].Call)
	assert.Equal(t, "manage_table action=register", outcome.Result.Writes[1].Call)
}

// TestRun_ReadsAreNotRefused keeps the barrier from making a draft useless: the
// read half of the surface, and a proxied tool its upstream declares read-only,
// both go through.
func TestRun_ReadsAreNotRefused(t *testing.T) {
	var calls []string
	outcome, err := New(writingServer(t, &calls), nil).Run(context.Background(), Request{
		Source: `
platform.call("manage_table", {"action": "list"})
platform.call("vendor__list_contacts", {})
`,
		Name: "reader", Identity: jane,
	})

	require.NoError(t, err)
	require.False(t, outcome.Failed(), outcome.Err)
	assert.Equal(t, []string{"manage_table", "vendor__list_contacts"}, calls)
	assert.Empty(t, outcome.Result.Writes)
}

// methodToolkit is a registry toolkit that answers the operation-id lookup,
// standing in for the api gateway.
type methodToolkit struct {
	method string
	ok     bool
	asked  []string
}

func (m *methodToolkit) MethodForOperation(_, _, operationID string) (string, bool) {
	m.asked = append(m.asked, operationID)
	return m.method, m.ok
}

func (*methodToolkit) Kind() string                          { return "api" }
func (*methodToolkit) Name() string                          { return "crm" }
func (*methodToolkit) RegisterTools(*mcp.Server)             {}
func (*methodToolkit) Tools() []string                       { return nil }
func (*methodToolkit) SetSemanticProvider(semantic.Provider) {}
func (*methodToolkit) SetQueryProvider(query.Provider)       {}
func (*methodToolkit) Close() error                          { return nil }
func (*methodToolkit) Connection() string                    { return "crm" }

// plainToolkit implements the registry contract and NOT the lookup, so the
// resolver must skip it rather than stopping at it.
type plainToolkit struct{}

func (*plainToolkit) Kind() string                          { return "trino" }
func (*plainToolkit) Name() string                          { return "other" }
func (*plainToolkit) RegisterTools(*mcp.Server)             {}
func (*plainToolkit) Tools() []string                       { return nil }
func (*plainToolkit) SetSemanticProvider(semantic.Provider) {}
func (*plainToolkit) SetQueryProvider(query.Provider)       {}
func (*plainToolkit) Close() error                          { return nil }
func (*plainToolkit) Connection() string                    { return "other" }

func TestClassifierOver_ResolvesAnOperationIDThroughTheLiveToolkits(t *testing.T) {
	reg := registry.NewRegistry()
	gateway := &methodToolkit{method: "GET", ok: true}
	require.NoError(t, reg.Register(gateway))

	classifier := ClassifierOver(reg)
	got := classifier.Classify("api_invoke_endpoint", map[string]any{
		"connection": "crm", "operation_id": "listContacts",
	})

	assert.False(t, got.Writes, "a resolved GET reads")
	assert.Equal(t, []string{"listContacts"}, gateway.asked)
}

// TestClassifierOver_WithoutAGatewayClassifiesFromTheTableAlone is the shape of
// a deployment with no api connections: nothing resolves, and the operation-id
// form stays a write.
func TestClassifierOver_WithoutAGatewayClassifiesFromTheTableAlone(t *testing.T) {
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(&plainToolkit{}), "a deployment has toolkits; none of them answers this")

	got := ClassifierOver(reg).Classify("api_invoke_endpoint", map[string]any{
		"connection": "crm", "operation_id": "listContacts",
	})
	assert.True(t, got.Writes)
	assert.False(t, got.Declared)

	assert.False(t, ClassifierOver(nil).Classify("search", nil).Writes,
		"and a nil registry still classifies everything the table names")
}

// TestWithToolkits_ReachesTheBarrier is the wiring both composition roots use:
// the toolkits they hand over are what the draft's barrier classifies with, so
// a read addressed by operation id goes through instead of being refused.
func TestWithToolkits_ReachesTheBarrier(t *testing.T) {
	reg := registry.NewRegistry()
	gateway := &methodToolkit{method: "GET", ok: true}
	require.NoError(t, reg.Register(gateway))

	var calls []string
	server := writingServer(t, &calls)
	mcp.AddTool(server, &mcp.Tool{Name: "api_invoke_endpoint"},
		func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			calls = append(calls, "api_invoke_endpoint")
			return &mcp.CallToolResult{}, map[string]any{"status": 200}, nil
		})

	outcome, err := New(server, nil).WithToolkits(reg).Run(context.Background(), Request{
		Source:   `platform.call("api_invoke_endpoint", {"connection": "crm", "operation_id": "listContacts"})`,
		Name:     "pull",
		Identity: jane,
	})

	require.NoError(t, err)
	require.False(t, outcome.Failed(), outcome.Err)
	assert.Equal(t, []string{"api_invoke_endpoint"}, calls)
	assert.Equal(t, []string{"listContacts"}, gateway.asked)
}

// TestWithToolkits_OnANilRunnerIsSafe mirrors the other nil-receiver guards:
// the composition root chains it onto a Runner it did not check.
func TestWithToolkits_OnANilRunnerIsSafe(t *testing.T) {
	var r *Runner
	assert.Nil(t, r.WithToolkits(registry.NewRegistry()))
}
