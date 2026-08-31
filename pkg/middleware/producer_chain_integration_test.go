package middleware_test

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/producedby"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// The producer relation is written by store funnels that sit two layers below
// the MCP chain and read the producer off the request context. A unit test of
// stampProducer proves the rule; it cannot prove that what the middleware
// stamps is what a tool handler -- and therefore the store it calls -- actually
// receives. That is what this exercises: the real assembled chain, a real
// in-memory session, a real tool call, and the store's own recording helper
// invoked from inside the handler.

// producerProbe is the store a tool handler writes through, standing in for the
// portal asset store and the managed-resource writer alike: both reach the
// record through producedby.Note with the handler's own context.
type producerProbe struct{ writes []producedby.Write }

func (p *producerProbe) Record(_ context.Context, w producedby.Write) error {
	p.writes = append(p.writes, w)
	return nil
}

func (*producerProbe) ListByTarget(context.Context, string, string) ([]producedby.Row, error) {
	return nil, nil
}

func (*producerProbe) ListByProducer(context.Context, string, string, int) ([]producedby.Row, error) {
	return nil, nil
}

// producerChainServer wires the tool-call middleware the way the platform does
// and registers a tool whose handler records a write, exactly as save_asset's
// store does.
func producerChainServer(t *testing.T, probe *producerProbe, user *middleware.UserInfo) *mcp.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "producer-chain-test", Version: "v0"}, nil)

	mcp.AddTool(server, &mcp.Tool{Name: "save_asset", Description: "save"},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ shSQLInput) (*mcp.CallToolResult, any, error) {
			producedby.Note(ctx, probe, producedby.Write{
				TargetKind: producedby.TargetAsset, TargetID: "asset-1", Created: true,
			})
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
		})

	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&fakeAuthn{user: user},
		&fakeAuthz{persona: "analyst"},
		&fakeLookup{kind: "portal", name: "portal", conn: "portal"},
		middleware.ToolCallConfig{Transport: "http", AdminPersona: "admin"},
	))
	return server
}

// TestIntegration_AgentSaveRecordsItsSession is acceptance criterion 5 through
// the real chain: what the middleware stamps reaches the handler's context and
// the store it writes through.
func TestIntegration_AgentSaveRecordsItsSession(t *testing.T) {
	ctx := context.Background()
	probe := &producerProbe{}
	server := producerChainServer(t, probe, &middleware.UserInfo{
		UserID: "user-1", Email: "analyst@example.com", Roles: []string{"analyst"},
	})

	// The session id an SSE or stateless-HTTP agent's initialize established,
	// which is what resolveSessionID hands the chain for an ordinary call.
	ctx = pkgsession.WithAwareSessionID(ctx, "sess-abc")
	sess := mustConnect(ctx, t, server)
	defer func() { _ = sess.Close() }()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name: "save_asset", Arguments: map[string]any{"sql": "unused"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "the save must execute: %v", res)

	require.Len(t, probe.writes, 1, "the handler's context must have named a producer")
	got := probe.writes[0].Producer
	assert.Equal(t, producedby.KindSession, got.Kind)
	assert.Equal(t, "sess-abc", got.ID)
	assert.Equal(t, "asset-1", probe.writes[0].TargetID)
	assert.True(t, probe.writes[0].Created)
}

// TestIntegration_CallWithNoSessionRecordsTheCaller pins the fallback: a
// stateless HTTP call carries no session at all, and filing its write under an
// empty session would collide every such write into one row.
func TestIntegration_CallWithNoSessionRecordsTheCaller(t *testing.T) {
	ctx := context.Background()
	probe := &producerProbe{}
	server := producerChainServer(t, probe, &middleware.UserInfo{
		UserID: "user-1", Email: "analyst@example.com", Roles: []string{"analyst"},
	})
	sess := mustConnect(ctx, t, server)
	defer func() { _ = sess.Close() }()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name: "save_asset", Arguments: map[string]any{"sql": "unused"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "the save must execute: %v", res)

	require.Len(t, probe.writes, 1)
	assert.Equal(t, producedby.KindPerson, probe.writes[0].Producer.Kind)
	assert.Equal(t, "user-1", probe.writes[0].Producer.ID)
}

// TestIntegration_ScriptRunKeepsItsScriptProducer proves the precedence through
// the chain rather than in isolation: a run stamps its script id on the session
// context it opens, and the middleware -- which sees only script:<name> -- must
// leave that stamp standing all the way to the handler.
func TestIntegration_ScriptRunKeepsItsScriptProducer(t *testing.T) {
	probe := &producerProbe{}
	server := producerChainServer(t, probe, &middleware.UserInfo{
		UserID:     "script:daily-sales",
		Email:      "owner@example.com",
		Roles:      []string{"analyst"},
		AuthType:   middleware.AuthTypeScript,
		OnBehalfOf: "owner@example.com",
	})

	// Exactly what scriptexec's runner establishes before opening the session.
	runCtx := producedby.With(context.Background(), producedby.Producer{
		Kind: producedby.KindScript, ID: "script-uuid-1", Label: "daily-sales",
	})
	sess := mustConnect(runCtx, t, server)
	defer func() { _ = sess.Close() }()

	res, err := sess.CallTool(runCtx, &mcp.CallToolParams{
		Name: "save_asset", Arguments: map[string]any{"sql": "unused"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "the run's save must execute: %v", res)

	require.Len(t, probe.writes, 1)
	got := probe.writes[0].Producer
	assert.Equal(t, producedby.KindScript, got.Kind)
	assert.Equal(t, "script-uuid-1", got.ID,
		"the run's script id must survive the chain; script:<name> does not survive a rename")
	assert.Equal(t, "daily-sales", got.Label)
}
