package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	rtToolOK        = "rt_ok"
	rtToolBareError = "rt_bare_error"
	rtToolRefused   = "rt_refused"
	rtPromptOK      = "rt_prompt"
	rtResourceURI   = "test://rt/resource"
	rtManagedURI    = "test://rt/managed"
)

// newResultTypeServer assembles a server with the three result-producing
// paths the platform has: a handler result the SDK stamps itself, a bare
// error result the error contract replaces, and a refusal a middleware
// short-circuits before any handler runs. withStamp toggles the middleware
// under test so a negative control can prove the harness sees the defect.
func newResultTypeServer(withStamp bool) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "rt", Version: "v0"}, nil)
	server.AddTool(&mcp.Tool{Name: rtToolOK, InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
		})
	server.AddTool(&mcp.Tool{Name: rtToolBareError, InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "no approved version"}}}, nil
		})
	server.AddTool(&mcp.Tool{Name: rtToolRefused, InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "never reached"}}}, nil
		})
	server.AddPrompt(&mcp.Prompt{Name: rtPromptOK}, func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: "hi"}}}}, nil
	})
	server.AddResource(&mcp.Resource{URI: rtResourceURI, MIMEType: "text/plain"},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: rtResourceURI, Text: "body"}}}, nil
		})

	// Innermost: the error contract rebuilds the bare error result.
	server.AddReceivingMiddleware(MCPErrorContractMiddleware())
	// A gate-shaped middleware: refuses one tool with a fresh platform result
	// and serves one resource URI itself, never calling the handler.
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == methodToolsCall {
				if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok && p.Name == rtToolRefused {
					return UnauthorizedResult("refused by the gate", "ask an administrator"), nil
				}
			}
			if method == methodReadResource {
				if p, ok := req.GetParams().(*mcp.ReadResourceParams); ok && p.URI == rtManagedURI {
					return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: rtManagedURI, Text: "managed"}}}, nil
				}
			}
			return next(ctx, method, req)
		}
	})
	if withStamp {
		// Outermost, as the platform registers it.
		server.AddReceivingMiddleware(MCPResultTypeMiddleware())
	}
	return server
}

// wireResultType reports whether v, marshaled the way the SDK puts it on the
// wire, carries a resultType, and returns that value.
func wireResultType(t *testing.T, v any) (string, bool) {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &m))
	raw, ok := m["resultType"]
	if !ok {
		return "", false
	}
	var s string
	require.NoError(t, json.Unmarshal(raw, &s))
	return s, true
}

// TestMCPResultTypeMiddleware_EveryResultCarriesResultTypeForACurrentClient is
// the #1382/#1383 contract: on a session negotiated at 2026-07-28, a refusal
// a gate short-circuited, a bare error the contract rebuilt, a managed read a
// middleware served, and a handler result the SDK stamped itself all reach the
// client typed complete. The negative control proves the harness sees the
// defect: without the middleware the platform-built results are untyped.
func TestMCPResultTypeMiddleware_EveryResultCarriesResultTypeForACurrentClient(t *testing.T) {
	for _, withStamp := range []bool{true, false} {
		ctx := context.Background()
		server := newResultTypeServer(withStamp)
		st, ct := mcp.NewInMemoryTransports()
		ss, err := server.Connect(ctx, st, nil)
		require.NoError(t, err)
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, ct, nil)
		require.NoError(t, err)
		require.GreaterOrEqual(t, cs.InitializeResult().ProtocolVersion, resultTypeProtocolVersion,
			"the in-memory client negotiates the revision that requires resultType")

		// The SDK path is typed with or without the middleware.
		plain, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: rtToolOK})
		require.NoError(t, err)
		rt, has := wireResultType(t, plain)
		assert.True(t, has && rt == "complete", "SDK-stamped handler result (stamp=%v): %v", withStamp, rt)

		// The platform-built paths are typed only with it.
		platformBuilt := map[string]any{}
		bare, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: rtToolBareError})
		require.NoError(t, err)
		require.True(t, bare.IsError)
		platformBuilt["error contract rebuild"] = bare
		refused, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: rtToolRefused})
		require.NoError(t, err)
		require.True(t, refused.IsError)
		refusedText, isText := refused.Content[0].(*mcp.TextContent)
		require.True(t, isText)
		assert.Contains(t, refusedText.Text, "refused by the gate", "the refusal text reaches the client")
		platformBuilt["gate short-circuit"] = refused
		managed, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: rtManagedURI})
		require.NoError(t, err)
		platformBuilt["middleware-served resource"] = managed
		for name, res := range platformBuilt {
			rt, has := wireResultType(t, res)
			if withStamp {
				assert.True(t, has, "%s must carry resultType on the wire", name)
				assert.Equal(t, "complete", rt, name)
			} else {
				assert.False(t, has, "negative control: %s is untyped without the middleware", name)
			}
		}

		// And the SDK's own prompt and resource stamps are preserved, not
		// clobbered, by the outer pass.
		pr, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: rtPromptOK})
		require.NoError(t, err)
		rt, has = wireResultType(t, pr)
		assert.True(t, has && rt == "complete", "prompt (stamp=%v)", withStamp)
		rr, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: rtResourceURI})
		require.NoError(t, err)
		rt, has = wireResultType(t, rr)
		assert.True(t, has && rt == "complete", "resource (stamp=%v)", withStamp)

		require.NoError(t, cs.Close())
		require.NoError(t, ss.Close())
	}
}

// TestMCPResultTypeMiddleware_MirrorsTheSDKForOlderClients pins the
// middleware to the SDK's own rule for a client on an earlier revision: the
// SDK leaves a handler result untyped, and the platform-built results must be
// left the same way, so this server answers an older client exactly as the SDK
// would on every path. The session is pre-initialized at 2025-11-25 and driven
// over the raw transport, which is how a legacy client reaches it.
func TestMCPResultTypeMiddleware_MirrorsTheSDKForOlderClients(t *testing.T) {
	ctx := context.Background()
	server := newResultTypeServer(true)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, &mcp.ServerSessionOptions{State: &mcp.ServerSessionState{
		InitializeParams:  &mcp.InitializeParams{ProtocolVersion: "2025-11-25", ClientInfo: &mcp.Implementation{Name: "legacy"}},
		InitializedParams: &mcp.InitializedParams{},
	}})
	require.NoError(t, err)
	defer func() { _ = ss.Close() }()
	conn, err := ct.Connect(ctx)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	call := func(id int64, name string) map[string]json.RawMessage {
		t.Helper()
		params, err := json.Marshal(map[string]any{"name": name, "arguments": map[string]any{}})
		require.NoError(t, err)
		rid, err := jsonrpc.MakeID(float64(id))
		require.NoError(t, err)
		require.NoError(t, conn.Write(ctx, &jsonrpc.Request{ID: rid, Method: methodToolsCall, Params: params}))
		for {
			msg, err := conn.Read(ctx)
			require.NoError(t, err)
			resp, ok := msg.(*jsonrpc.Response)
			if !ok {
				continue // a notification or server->client request; not ours
			}
			require.NoError(t, resp.Error)
			var m map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(resp.Result, &m))
			return m
		}
	}

	for i, name := range []string{rtToolOK, rtToolBareError, rtToolRefused} {
		res := call(int64(i+1), name)
		_, has := res["resultType"]
		assert.False(t, has, "%s: an older client receives no resultType, as the SDK leaves it", name)
		require.NotEmpty(t, res["content"], "%s: the result body is intact", name)
	}
}

// TestStampComplete_PreservesEveryExportedFieldAndTheStashedError proves the
// stamp is a pure addition: the wire form before and after differ only in
// resultType, and the error stashed for GetError survives the copy.
func TestStampComplete_PreservesEveryExportedFieldAndTheStashedError(t *testing.T) {
	stashed := errors.New("stashed")
	res := &mcp.CallToolResult{
		Meta:              mcp.Meta{"k": "v"},
		Content:           []mcp.Content{&mcp.TextContent{Text: "body"}},
		StructuredContent: map[string]any{"a": 1},
		RequestState:      "state",
	}
	res.SetError(stashed)
	before, err := json.Marshal(res)
	require.NoError(t, err)

	stampComplete(res)

	after, err := json.Marshal(res)
	require.NoError(t, err)
	var b, a map[string]any
	require.NoError(t, json.Unmarshal(before, &b))
	require.NoError(t, json.Unmarshal(after, &a))
	assert.Equal(t, "complete", a["resultType"])
	delete(a, "resultType")
	assert.Equal(t, b, a, "the stamp changes nothing but resultType")
	assert.Same(t, stashed, res.GetError(), "the stashed error still feeds GetError")
	assert.True(t, res.IsError)

	// A result already carrying input requests is the SDK's input_required
	// answer and is left alone.
	ir := &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{"q": &mcp.ElicitParams{Message: "m"}}}
	stampComplete(ir)
	_, has := wireResultType(t, ir)
	assert.False(t, has, "an input_required result is not retyped complete")

	// Prompt and resource results are stamped the same way, and their
	// input_required answers are left alone the same way.
	irp := &mcp.GetPromptResult{InputRequests: mcp.InputRequestMap{"q": &mcp.ElicitParams{Message: "m"}}}
	stampComplete(irp)
	_, has = wireResultType(t, irp)
	assert.False(t, has)
	irr := &mcp.ReadResourceResult{InputRequests: mcp.InputRequestMap{"q": &mcp.ElicitParams{Message: "m"}}}
	stampComplete(irr)
	_, has = wireResultType(t, irr)
	assert.False(t, has)
	stampComplete(&mcp.ListToolsResult{}) // a result type the SDK never types is ignored
	pr := &mcp.GetPromptResult{Description: "d", Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: "m"}}}}
	stampComplete(pr)
	rt, has := wireResultType(t, pr)
	assert.True(t, has && rt == "complete")
	assert.Equal(t, "d", pr.Description)
	rr := &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "u", Text: "t"}}}
	stampComplete(rr)
	rt, has = wireResultType(t, rr)
	assert.True(t, has && rt == "complete")
	assert.Equal(t, "u", rr.Contents[0].URI)
}

// TestMustDecodeComplete_PanicsOnAnUndecodableType pins the init-time contract:
// a type the complete-result wire form cannot decode into is a programming
// error, surfaced at package initialization rather than silently as an
// untyped template.
func TestMustDecodeComplete_PanicsOnAnUndecodableType(t *testing.T) {
	assert.Panics(t, func() { mustDecodeComplete[int]() })
}

// TestMCPResultTypeMiddleware_PassesErrorsAndNilResultsThrough proves the
// middleware never manufactures a result: a protocol error and a nil result
// from the layer below reach the SDK exactly as returned.
func TestMCPResultTypeMiddleware_PassesErrorsAndNilResultsThrough(t *testing.T) {
	boom := errors.New("boom")
	failing := MCPResultTypeMiddleware()(func(context.Context, string, mcp.Request) (mcp.Result, error) { return nil, boom })
	res, err := failing(context.Background(), methodToolsCall, &mcp.CallToolRequest{})
	assert.Nil(t, res)
	assert.Same(t, boom, err)
	var none mcp.Result
	empty := MCPResultTypeMiddleware()(func(context.Context, string, mcp.Request) (mcp.Result, error) { return none, nil })
	res, err = empty(context.Background(), methodToolsCall, &mcp.CallToolRequest{})
	assert.Nil(t, res)
	assert.NoError(t, err)

	// A typed nil from the layer below is the absent result the SDK treats it
	// as; the stamp must not dereference it.
	assert.NotPanics(t, func() {
		stampComplete((*mcp.CallToolResult)(nil))
		stampComplete((*mcp.GetPromptResult)(nil))
		stampComplete((*mcp.ReadResourceResult)(nil))
	})
}

// TestClientRequiresResultType covers the session shapes the predicate sees:
// no session of the server kind, a session with no recorded initialize
// parameters (the SDK's latest-revision default), and both sides of the
// revision boundary.
func TestClientRequiresResultType(t *testing.T) {
	assert.False(t, clientRequiresResultType(nil))
	assert.False(t, clientRequiresResultType(&mcp.CallToolRequest{}), "a request with no server session is not stamped")

	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "rt", Version: "v0"}, nil)
	for _, tc := range []struct {
		name    string
		version string
		want    bool
	}{
		{"pre-revision client", "2025-11-25", false},
		{"revision client", "2026-07-28", true},
		{"later revision client", "2026-12-01", true},
	} {
		st, _ := mcp.NewInMemoryTransports()
		ss, err := server.Connect(ctx, st, &mcp.ServerSessionOptions{State: &mcp.ServerSessionState{
			InitializeParams: &mcp.InitializeParams{ProtocolVersion: tc.version},
		}})
		require.NoError(t, err)
		assert.Equal(t, tc.want, clientRequiresResultType(&mcp.CallToolRequest{Session: ss}), tc.name)
		_ = ss.Close()
	}
	st, _ := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	require.NoError(t, err)
	assert.True(t, clientRequiresResultType(&mcp.CallToolRequest{Session: ss}), "no initialize params means the latest revision")
	_ = ss.Close()
}
