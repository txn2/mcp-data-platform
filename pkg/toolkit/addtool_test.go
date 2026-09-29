package toolkit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type listIn struct {
	Name string `json:"name,omitempty"`
}

type listOut struct {
	Items []string `json:"items"`
	Count int      `json:"count"`
}

// connectTools serves s over an in-memory transport and returns a client
// session on it.
func connectTools(t *testing.T, s *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
	require.NoError(t, err)
	return res
}

func structuredJSON(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	b, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	return string(b)
}

func newServer() *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{Name: "s", Version: "v0"}, nil)
}

// A typed tool's output reaches the client with an empty list as [], in the
// structured content and in the text block the SDK builds from it (#1832).
func TestAddTool_AnEmptyListReachesTheClientAsAnArray(t *testing.T) {
	s := newServer()
	AddTool(s, &mcp.Tool{Name: "list"}, func(context.Context, *mcp.CallToolRequest, listIn) (*mcp.CallToolResult, listOut, error) {
		return nil, listOut{}, nil
	})
	AddTool(s, &mcp.Tool{Name: "pointer"}, func(context.Context, *mcp.CallToolRequest, listIn) (*mcp.CallToolResult, *listOut, error) {
		return nil, nil, nil
	})
	cs := connectTools(t, s)

	for _, name := range []string{"list", "pointer"} {
		res := callTool(t, cs, name)
		require.False(t, res.IsError, name)
		assert.JSONEq(t, `{"items":[],"count":0}`, structuredJSON(t, res), name)
		require.Len(t, res.Content, 1, name)
		assert.JSONEq(t, `{"items":[],"count":0}`, resultText(t, res), name)
	}
}

// The advertised output schema is the one mcp.AddTool derives, so wrapping a
// tool changes nothing a client lists.
func TestAddTool_AdvertisesTheSchemaMCPAddToolDerives(t *testing.T) {
	handler := func(context.Context, *mcp.CallToolRequest, listIn) (*mcp.CallToolResult, *listOut, error) {
		return nil, &listOut{}, nil
	}
	ours, theirs := newServer(), newServer()
	AddTool(ours, &mcp.Tool{Name: "list"}, handler)
	mcp.AddTool(theirs, &mcp.Tool{Name: "list"}, handler)

	list := func(s *mcp.Server) *mcp.Tool {
		res, err := connectTools(t, s).ListTools(context.Background(), nil)
		require.NoError(t, err)
		require.Len(t, res.Tools, 1)
		return res.Tools[0]
	}
	a, b := list(ours), list(theirs)
	assert.Equal(t, b.OutputSchema, a.OutputSchema)
	assert.Equal(t, b.InputSchema, a.InputSchema)
	assert.NotNil(t, a.OutputSchema)
}

// A tool whose handler builds its own result and returns no output keeps its
// result as built, with no structured content added.
func TestAddTool_AToolWithNoOutputKeepsItsResult(t *testing.T) {
	s := newServer()
	AddTool(s, &mcp.Tool{Name: "text"}, func(context.Context, *mcp.CallToolRequest, listIn) (*mcp.CallToolResult, any, error) {
		return JSONResultTyped(listOut{})
	})
	res := callTool(t, connectTools(t, s), "text")
	assert.Nil(t, res.StructuredContent)
	require.Len(t, res.Content, 1)
	assert.JSONEq(t, `{"items":[],"count":0}`, resultText(t, res))
}

// A handler's error and an output the encoder cannot encode are reported to
// the caller as an error result.
func TestAddTool_ReportsFailures(t *testing.T) {
	s := newServer()
	AddTool(s, &mcp.Tool{Name: "fails"}, func(context.Context, *mcp.CallToolRequest, listIn) (*mcp.CallToolResult, any, error) {
		return nil, nil, errors.New("upstream down")
	})
	AddTool(s, &mcp.Tool{Name: "unencodable"}, func(context.Context, *mcp.CallToolRequest, listIn) (*mcp.CallToolResult, any, error) {
		return nil, map[string]any{"f": func() {}}, nil
	})
	cs := connectTools(t, s)

	res := callTool(t, cs, "fails")
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "upstream down")

	res = callTool(t, cs, "unencodable")
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "marshaling output")
}

// An output type with no schema is refused at registration, as mcp.AddTool
// refuses it.
func TestAddTool_PanicsOnAnOutputTypeWithNoSchema(t *testing.T) {
	assert.PanicsWithValue(t, `tool "bad": output schema: ForType(chan int): type chan int is unsupported by jsonschema`, func() {
		AddTool(newServer(), &mcp.Tool{Name: "bad"}, func(context.Context, *mcp.CallToolRequest, listIn) (*mcp.CallToolResult, chan int, error) {
			return nil, nil, nil
		})
	})
}
