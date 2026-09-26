package gatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/resultbudget"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// The context budget is a property of the MCP response to a model (#1878).
// These tests assemble the server in the platform's order -- the tool-call
// middleware writing PlatformContext, the error contract, the result budget,
// a layer appending to the result the way the call reference does, and the
// capture next to the handler -- with the api gateway toolkit and a tool
// with no fitter of its own, which is never cut, and send one over-budget
// payload through each
// surface: a model's MCP client, the REST gateway, and a managed script's
// session.

const (
	budgetTestBudget   = 4096
	budgetTestReadCap  = 64 * 1024
	budgetTestConn     = "feed"
	budgetTestTextTool = "big_text"
)

// budgetTestPayload is a JSON document well past the budget and well
// inside the read cap.
var budgetTestPayload = `{"files":[` + strings.TrimSuffix(strings.Repeat(`{"name":"export-2026-09-25.csv","size":1048576},`, 400), ",") + `]}`

type allowAllAuthorizer struct{}

func (allowAllAuthorizer) IsAuthorized(context.Context, string, []string, string, string) (authorized bool, persona, reason string) {
	return true, "analyst", ""
}

type budgetTestServer struct {
	mcp  *mcp.Server
	rest *httptest.Server
}

// newBudgetTestServer assembles the chain against an upstream serving
// body, and a text tool with no fitter that returns 20 KB of text.
func newBudgetTestServer(t *testing.T, body string) budgetTestServer {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(upstream.Close)

	tk := apigatewaykit.New("apigateway")
	require.NoError(t, tk.AddConnection(budgetTestConn, map[string]any{
		"base_url":           upstream.URL,
		"auth_mode":          apigatewaykit.AuthModeNone,
		"call_timeout":       "5s",
		"max_response_bytes": budgetTestReadCap,
	}))

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	tk.RegisterTools(server)
	server.AddTool(&mcp.Tool{Name: budgetTestTextTool, InputSchema: json.RawMessage(`{"type":"object"}`)},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("line of text\n", 1600)}}}, nil
		})

	lookup := func(tool string) toolkit.ResultFitter {
		if tool == apigatewaykit.ToolInvokeEndpoint {
			return tk
		}
		return nil
	}
	// Innermost first, as AddReceivingMiddleware wraps.
	server.AddReceivingMiddleware(resultbudget.Capture())
	server.AddReceivingMiddleware(appendLikeTheCallReference())
	server.AddReceivingMiddleware(resultbudget.Middleware(resultbudget.Config{MaxBytes: budgetTestBudget}, lookup))
	server.AddReceivingMiddleware(middleware.MCPErrorContractMiddleware())
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&middleware.NoopAuthenticator{DefaultUserID: "u1", DefaultRoles: []string{"analyst"}},
		allowAllAuthorizer{}, nil, middleware.ToolCallConfig{Transport: "http"}))

	handler, err := NewHandler(Deps{MCPServer: server})
	require.NoError(t, err)
	rest := httptest.NewServer(handler)
	t.Cleanup(func() {
		rest.Close()
		time.Sleep(10 * time.Millisecond)
	})
	return budgetTestServer{mcp: server, rest: rest}
}

// budgetTestAppended is the block appendLikeTheCallReference adds.
var budgetTestAppended = `{"call_reference":"mcp:call:` + strings.Repeat("0", 64) + `"}`

// appendLikeTheCallReference stands between the result budget and the
// handler the way the call reference and enrichment do: it appends a text
// block to a successful result and mirrors a key into its structured
// content. The budget has to cover both.
func appendLikeTheCallReference() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			res, ok := result.(*mcp.CallToolResult)
			if err != nil || !ok || res.IsError {
				return result, err
			}
			res.Content = append(res.Content, &mcp.TextContent{Text: budgetTestAppended})
			var m map[string]any
			if toolkit.DecodeStructured(res.StructuredContent, &m) {
				m["call_reference"] = "mcp:call:" + strings.Repeat("0", 64)
				res.StructuredContent = m
			}
			return res, nil
		}
	}
}

// session opens an in-memory MCP session whose server side carries source,
// as a model's transport ("") or a managed script's host does.
func (s budgetTestServer) session(t *testing.T, source string) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	if source != "" {
		ctx = middleware.WithSource(ctx, source)
	}
	t1, t2 := mcp.NewInMemoryTransports()
	ss, err := s.mcp.Connect(ctx, t1, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1"}, nil)
	cs, err := client.Connect(context.Background(), t2, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Close()
	})
	return cs
}

func callInvoke(t *testing.T, cs *mcp.ClientSession) (*mcp.CallToolResult, apigatewaykit.InvokeOutput) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      apigatewaykit.ToolInvokeEndpoint,
		Arguments: map[string]any{"connection": budgetTestConn, "method": "GET", "path": "/files"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "api_invoke_endpoint failed: %v", res.Content)
	var out apigatewaykit.InvokeOutput
	require.NoError(t, json.Unmarshal([]byte(blockText(t, res.Content[0])), &out))
	return res, out
}

// blockText is a content block's text, failing the test when it is not text.
func blockText(t *testing.T, c mcp.Content) string {
	t.Helper()
	tc, ok := c.(*mcp.TextContent)
	if !ok {
		t.Fatalf("content %v is not text", c)
	}
	return tc.Text
}

func postInvoke(t *testing.T, srv *httptest.Server) (status int, body []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/api/v1/gateway/"+budgetTestConn+"/invoke", bytes.NewReader([]byte(`{"method":"GET","path":"/files"}`)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

// TestBudget_OneOverBudgetPayloadThroughEverySurface is the #1878
// regression: the same over-budget payload is fitted for a model and
// returned whole to a REST gateway client and to a managed script.
func TestBudget_OneOverBudgetPayloadThroughEverySurface(t *testing.T) {
	s := newBudgetTestServer(t, budgetTestPayload)

	t.Run("a model's MCP client is fitted", func(t *testing.T) {
		res, out := callInvoke(t, s.session(t, ""))
		total := 0
		for _, c := range res.Content {
			total += len(blockText(t, c))
		}
		if total > budgetTestBudget {
			t.Errorf("result text is %d characters; want the whole of it, the appended block included, inside the %d budget", total, budgetTestBudget)
		}
		if last := blockText(t, res.Content[len(res.Content)-1]); last != budgetTestAppended {
			t.Errorf("last block = %q; want the block the outer layer appended kept whole", last)
		}
		var structured map[string]any
		if !toolkit.DecodeStructured(res.StructuredContent, &structured) {
			t.Fatalf("structured content %v is unreadable", res.StructuredContent)
		}
		if truncated, _ := structured["body_truncated"].(bool); structured["call_reference"] == nil || !truncated {
			t.Errorf("structured = %v; want the fitted output with the outer layer's key kept", structured)
		}
		if !out.BodyTruncated || out.BodyBytes != int64(len(budgetTestPayload)) {
			t.Errorf("body_truncated=%v body_bytes=%d; want the cut flagged and the %d bytes read reported", out.BodyTruncated, out.BodyBytes, len(budgetTestPayload))
		}
		if structured, _ := json.Marshal(res.StructuredContent); len(structured) > budgetTestBudget {
			t.Errorf("structured content is %d bytes; want the fitted copy", len(structured))
		}
	})

	t.Run("the REST gateway returns the whole body", func(t *testing.T) {
		status, body := postInvoke(t, s.rest)
		require.Equal(t, http.StatusOK, status, "body: %s", body)
		var out apigatewaykit.InvokeOutput
		require.NoError(t, json.Unmarshal(body, &out))
		if out.BodyTruncated || out.Hint != "" {
			t.Errorf("body_truncated=%v hint=%q; want the body returned whole", out.BodyTruncated, out.Hint)
		}
		whole, err := json.Marshal(out.Body)
		require.NoError(t, err)
		if len(whole) != len(budgetTestPayload) {
			t.Errorf("body re-encodes to %d bytes; want all %d", len(whole), len(budgetTestPayload))
		}
	})

	t.Run("a managed script is never fitted", func(t *testing.T) {
		cs := s.session(t, middleware.SourceScript)
		_, out := callInvoke(t, cs)
		if out.BodyTruncated {
			t.Error("a script's api_invoke_endpoint result was cut")
		}
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: budgetTestTextTool})
		require.NoError(t, err)
		if n := len(blockText(t, res.Content[0])); n != 1600*len("line of text\n") || len(res.Content) != 2 {
			t.Errorf("a script's %s result is %d characters in %d blocks; want it whole, then the appended block", budgetTestTextTool, n, len(res.Content))
		}
	})

	t.Run("a tool with no fitter reaches a model whole", func(t *testing.T) {
		res, err := s.session(t, "").CallTool(context.Background(), &mcp.CallToolParams{Name: budgetTestTextTool})
		require.NoError(t, err)
		if n := len(blockText(t, res.Content[0])); n != 1600*len("line of text\n") || len(res.Content) != 2 {
			t.Errorf("a model's %s result is %d characters in %d blocks; want it whole, then the appended block", budgetTestTextTool, n, len(res.Content))
		}
	})
}

// TestBudget_RESTPastTheReadCapIs413: a REST caller whose response is
// past the connection's max_response_bytes is refused with 413 naming
// the cap, never handed a prefix under a 200 (#1878).
func TestBudget_RESTPastTheReadCapIs413(t *testing.T) {
	big := `{"data":"` + strings.Repeat("x", budgetTestReadCap) + `"}`
	s := newBudgetTestServer(t, big)

	status, body := postInvoke(t, s.rest)
	require.Equal(t, http.StatusRequestEntityTooLarge, status, "body: %s", body)
	for _, want := range []string{apigatewaykit.ErrCodeBodyTooLarge, `"limit_bytes":65536`, "max_response_bytes"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("413 body %s lacks %q", body, want)
		}
	}
}
