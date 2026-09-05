package platform

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/auditwiring"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// The call reference is the citation token a data call hands back, and which
// calls get one is the same rule the catalog decides by. This exercises the
// platform's own registration of that middleware rather than a copy of it: the
// chain entry builds the predicate from configuration, and a call the catalog
// declines to record must come back without a token that would resolve to
// nothing (#1614, #1624).
func TestCallReferenceRegistrationAppliesTheCatalogsOwnRule(t *testing.T) {
	for name, tc := range map[string]struct {
		persona string
		source  string
		want    bool
	}{
		"an ordinary caller":              {persona: "analyst", source: middleware.SourceMCP, want: true},
		"a persona the deployment named":  {persona: "ingest-service", source: middleware.SourceMCP, want: false},
		"a managed script run":            {persona: "admin", source: middleware.SourceScript, want: false},
		"a person in a script's persona":  {persona: "admin", source: middleware.SourceMCP, want: true},
		"a run under an ordinary persona": {persona: "analyst", source: middleware.SourceScript, want: false},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, referenceStampedByTheChain(t, tc.persona, tc.source))
		})
	}
}

// referenceStampedByTheChain registers the shipped call-reference chain entry
// over a server, calls a data tool through it as the given caller, and reports
// whether the result came back carrying its reference.
//
// The PlatformContext is injected by a middleware outer to the one under test,
// which is the position MCPToolCallMiddleware holds in the real chain and what
// it writes there: the event id the reference names, the toolkit kind that
// makes the call a data call, and the persona and source the rule reads.
func referenceStampedByTheChain(t *testing.T, persona, source string) bool {
	t.Helper()

	db, _, err := sqlmock.New()
	require.NoError(t, err)
	layer := auditwiring.Assemble(auditwiring.Config{DB: db, RetentionDays: 30})
	t.Cleanup(func() {
		_ = layer.Close()
		_ = db.Close()
	})
	require.True(t, layer.Recording(), "the chain entry registers only when audit records")

	p := &Platform{
		config: &Config{Calls: CallsConfig{ExcludePersonas: []string{"ingest-service"}}},
		audit:  layer,
		mcpServer: mcp.NewServer(&mcp.Implementation{
			Name: "call-reference-wiring", Version: "v0",
		}, nil),
	}
	mcp.AddTool(p.mcpServer, &mcp.Tool{Name: "trino_query", Description: "query"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "{}"}}}, nil, nil
		})

	for _, spec := range p.receivingMiddlewareChain() {
		if spec.Name == mwCallReference {
			spec.Register()
		}
	}
	// Added after, so it is outer: the reference middleware must see a context
	// that already carries the identity of the call.
	p.mcpServer.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			pc := middleware.NewPlatformContext("req-1")
			pc.EventID = "evt-1"
			pc.ToolkitKind = "trino"
			pc.PersonaName = persona
			pc.Source = source
			return next(middleware.WithPlatformContext(ctx, pc), method, req)
		}
	})

	ctx := t.Context()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	_, err = p.mcpServer.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).
		Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer func() { _ = sess.Close() }()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name: "trino_query", Arguments: map[string]any{},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "the call itself must answer")

	for _, content := range res.Content {
		text, ok := content.(*mcp.TextContent)
		if !ok || !strings.Contains(text.Text, middleware.CallReferenceKey) {
			continue
		}
		var block map[string]middleware.CallReference
		require.NoError(t, json.Unmarshal([]byte(text.Text), &block))
		assert.Equal(t, "evt-1", block[middleware.CallReferenceKey].CallID)
		return true
	}
	return false
}
