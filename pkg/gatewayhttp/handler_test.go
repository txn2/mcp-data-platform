package gatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// TestNewHandler_RequiresMCPServer guards the only construction-time
// invariant: a nil MCP server must be rejected up front so the
// handler can never reach a NewServer code path with a missing
// dependency.
func TestNewHandler_RequiresMCPServer(t *testing.T) {
	_, err := NewHandler(Deps{MCPServer: nil})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MCPServer is required")
}

func TestNewHandler_ReturnsMux(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	h, err := NewHandler(Deps{MCPServer: server})
	require.NoError(t, err)
	require.NotNil(t, h)
}

func TestBuildInvokeArgs(t *testing.T) {
	tests := []struct {
		name       string
		connection string
		req        invokeRequest
		want       map[string]any
	}{
		{
			name:       "minimal",
			connection: "acme",
			req:        invokeRequest{Method: "GET", Path: "/v1/things"},
			want: map[string]any{
				"connection": "acme",
				"method":     "GET",
				"path":       "/v1/things",
			},
		},
		{
			name:       "full",
			connection: "acme",
			req: invokeRequest{
				Method:         "POST",
				Path:           "/v1/things",
				QueryParams:    map[string]any{"q": "foo"},
				Headers:        map[string]string{"X-Trace": "abc"},
				Body:           map[string]any{"name": "thing"},
				TimeoutSeconds: 30,
			},
			want: map[string]any{
				"connection":      "acme",
				"method":          "POST",
				"path":            "/v1/things",
				"query_params":    map[string]any{"q": "foo"},
				"headers":         map[string]string{"X-Trace": "abc"},
				"body":            map[string]any{"name": "thing"},
				"timeout_seconds": 30,
			},
		},
		{
			name:       "empty maps and zero timeout are omitted",
			connection: "acme",
			req: invokeRequest{
				Method:         "GET",
				Path:           "/v1/things",
				QueryParams:    map[string]any{},
				Headers:        map[string]string{},
				TimeoutSeconds: 0,
			},
			want: map[string]any{
				"connection": "acme",
				"method":     "GET",
				"path":       "/v1/things",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildInvokeArgs(tc.connection, &tc.req)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDecodeInvokeRequest(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantErr  string
		wantReq  *invokeRequest
		bodyOnly bool
	}{
		{
			name:    "valid minimal",
			body:    `{"method":"GET","path":"/v1/things"}`,
			wantReq: &invokeRequest{Method: "GET", Path: "/v1/things"},
		},
		{
			name:    "empty body",
			body:    ``,
			wantErr: "request body is required",
		},
		{
			name:    "invalid JSON",
			body:    `{`,
			wantErr: "invalid JSON",
		},
		{
			name:    "missing method",
			body:    `{"path":"/v1/things"}`,
			wantErr: "method is required",
		},
		{
			name:    "missing path",
			body:    `{"method":"GET"}`,
			wantErr: "path is required",
		},
		{
			name:    "whitespace-only method",
			body:    `{"method":"   ","path":"/v1"}`,
			wantErr: "method is required",
		},
		{
			name:    "whitespace-only path",
			body:    `{"method":"GET","path":"   "}`,
			wantErr: "path is required",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/x", strings.NewReader(tc.body))
			got, err := decodeInvokeRequest(r)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantReq, got)
		})
	}
}

func TestDecodeInvokeRequest_BodySizeLimit(t *testing.T) {
	// Construct a payload that, after JSON encoding, exceeds the 1MiB
	// limit. The reader should truncate; the truncated bytes will not
	// parse as valid JSON, so the error path through json.Unmarshal
	// is what guards the boundary in practice.
	big := bytes.Repeat([]byte("a"), RequestBodyLimit+1)
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/x", bytes.NewReader(big))
	_, err := decodeInvokeRequest(r)
	require.Error(t, err)
}

func TestClassifyToolError(t *testing.T) {
	tests := []struct {
		name       string
		payload    string
		wantStatus int
		wantMsg    string
	}{
		{
			name:       "authentication failure → 401",
			payload:    `{"error":"authentication failed: invalid token"}`,
			wantStatus: http.StatusUnauthorized,
			wantMsg:    "authentication failed: invalid token",
		},
		{
			name:       "authorization failure → 403",
			payload:    `{"error":"not authorized: persona denies api_invoke_endpoint"}`,
			wantStatus: http.StatusForbidden,
			wantMsg:    "not authorized: persona denies api_invoke_endpoint",
		},
		{
			name:       "connection missing → 404",
			payload:    `{"error":"connection \"acme\" not found (use list_connections...)"}`,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "validation failure → 400",
			payload:    `{"error":"apigateway: method \"FOO\" not supported (want GET, POST, ...)"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "context deadline → 504 Gateway Timeout",
			payload:    `{"error":"Get \"https://api.example.com/x\": context deadline exceeded"}`,
			wantStatus: http.StatusGatewayTimeout,
		},
		{
			name:       "Client.Timeout exceeded → 504 Gateway Timeout",
			payload:    `{"error":"Get \"https://api.example.com/x\": net/http: request canceled (Client.Timeout exceeded while awaiting headers)"}`,
			wantStatus: http.StatusGatewayTimeout,
		},
		{
			name:       "i/o timeout → 504 Gateway Timeout",
			payload:    `{"error":"read tcp 10.0.0.1:443: i/o timeout"}`,
			wantStatus: http.StatusGatewayTimeout,
		},
		{
			name:       "DNS resolution failure → 502 Bad Gateway",
			payload:    `{"error":"Get \"https://nope.invalid/x\": dial tcp: lookup nope.invalid: no such host"}`,
			wantStatus: http.StatusBadGateway,
		},
		{
			name:       "TCP connection refused → 502 Bad Gateway",
			payload:    `{"error":"Get \"https://api.example.com/x\": dial tcp 10.0.0.1:443: connect: connection refused"}`,
			wantStatus: http.StatusBadGateway,
		},
		{
			name:       "TLS handshake failure → 502 Bad Gateway",
			payload:    `{"error":"Get \"https://api.example.com/x\": tls: handshake failure"}`,
			wantStatus: http.StatusBadGateway,
		},
		{
			name:       "EOF mid-stream → 502 Bad Gateway",
			payload:    `{"error":"Get \"https://api.example.com/x\": EOF"}`,
			wantStatus: http.StatusBadGateway,
		},
		{
			name:       "connection reset → 502 Bad Gateway",
			payload:    `{"error":"read tcp 10.0.0.1:443: connection reset by peer"}`,
			wantStatus: http.StatusBadGateway,
		},
		{
			name:       "transport phrase 'no such host' does NOT mismatch as 404 not found",
			payload:    `{"error":"dial tcp: lookup api.example.com: no such host"}`,
			wantStatus: http.StatusBadGateway,
		},
		// Issue #533: the auth/authz middleware emits a bare-string
		// payload (PlatformError), NOT a JSON envelope. These cases lock
		// in that a plain string is classified on its message, not
		// short-circuited to 500.
		{
			name:       "plain-string authorization denial → 403",
			payload:    `not authorized: connection not allowed for persona: example-persona`,
			wantStatus: http.StatusForbidden,
			wantMsg:    "not authorized: connection not allowed for persona: example-persona",
		},
		{
			name:       "plain-string authentication failure → 401",
			payload:    `authentication failed: invalid token`,
			wantStatus: http.StatusUnauthorized,
			wantMsg:    "authentication failed: invalid token",
		},
		{
			name:       "unrecognized non-JSON payload → 400 (non-retryable, not 500)",
			payload:    `garbage`,
			wantStatus: http.StatusBadRequest,
			wantMsg:    "garbage",
		},
		{
			name:       "JSON object without an error field → 400 with raw payload",
			payload:    `{"unexpected":"shape"}`,
			wantStatus: http.StatusBadRequest,
			wantMsg:    `{"unexpected":"shape"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, msg := classifyToolError(tc.payload)
			assert.Equal(t, tc.wantStatus, status)
			if tc.wantMsg != "" {
				assert.Equal(t, tc.wantMsg, msg)
			}
		})
	}
}

func TestReadRequestToken(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
		want   string
	}{
		{
			name:   "X-API-Key wins",
			header: http.Header{"X-Api-Key": []string{"key-1"}, "Authorization": []string{"Bearer tok-1"}},
			want:   "key-1",
		},
		{
			name:   "Bearer used when no X-API-Key",
			header: http.Header{"Authorization": []string{"Bearer tok-1"}},
			want:   "tok-1",
		},
		{
			name:   "Authorization without Bearer prefix is ignored",
			header: http.Header{"Authorization": []string{"Basic dXNlcjpwYXNz"}},
			want:   "",
		},
		{
			name:   "no auth → empty",
			header: http.Header{},
			want:   "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/x", http.NoBody)
			r.Header = tc.header
			got := readRequestToken(r)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFirstTextContent(t *testing.T) {
	got, ok := firstTextContent([]mcp.Content{&mcp.TextContent{Text: "hello"}})
	require.True(t, ok)
	assert.Equal(t, "hello", got)

	_, ok = firstTextContent(nil)
	assert.False(t, ok)

	_, ok = firstTextContent([]mcp.Content{})
	assert.False(t, ok)

	// A non-text content type produces ok=false (the handler only
	// understands TextContent payloads from api_invoke_endpoint).
	_, ok = firstTextContent([]mcp.Content{&mcp.ImageContent{Data: []byte{0}, MIMEType: "image/png"}})
	assert.False(t, ok)
}

// TestWriteToolResult covers the post-CallTool branches in isolation
// so the defensive checks against an upstream contract change
// (non-text content, malformed InvokeOutput JSON) are reachable from
// tests. The integration path can't trigger these because the
// apigateway toolkit's jsonResult always emits a TextContent with
// well-formed JSON; nevertheless, dropping the guards would mean a
// future SDK change could surface as a 5xx with no diagnostic.
func TestWriteToolResult(t *testing.T) {
	tests := []struct {
		name       string
		result     *mcp.CallToolResult
		wantStatus int
		wantBody   string
	}{
		{
			name: "non-text content → 500",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.ImageContent{Data: []byte{0}, MIMEType: "image/png"}},
			},
			wantStatus: http.StatusInternalServerError,
			wantBody:   "unexpected response shape",
		},
		{
			name: "malformed InvokeOutput JSON → 500",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: `{not valid json`}},
			},
			wantStatus: http.StatusInternalServerError,
			wantBody:   "failed to parse upstream invoke result",
		},
		{
			name: "tool error envelope (authn) → 401",
			result: &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: `{"error":"authentication failed: bad token"}`}},
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "successful payload → 200 with InvokeOutput body",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: `{"status":204,"duration_ms":5}`}},
			},
			wantStatus: http.StatusOK,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeToolResult(w, tc.result)
			assert.Equal(t, tc.wantStatus, w.Code)
			if tc.wantBody != "" {
				assert.Contains(t, w.Body.String(), tc.wantBody)
			}
		})
	}
}

// postJSON POSTs a JSON body to the gateway and returns the status
// code and the response body bytes. The response body is read and
// closed inside the helper so each call site stays a single line and
// the linter's body-close check is satisfied centrally.
func postJSON(t *testing.T, url, body string) (status int, respBody []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, bodyBytes
}

// TestIntegration_HappyPath wires a real MCP server with the
// apigateway toolkit, a fake upstream HTTP server, and the gateway
// handler. It POSTs through the gateway and asserts the upstream was
// called and the response surfaced. This is the test that proves the
// in-memory MCP session bridges REST to api_invoke_endpoint end to
// end — a unit test on the helper functions alone would not catch a
// wiring regression in CallTool, content extraction, or argument
// marshaling.
func TestIntegration_HappyPath(t *testing.T) {
	upstreamHits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/v1/items", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":1}]}`))
	}))
	defer upstream.Close()

	gateway := newGatewayHTTPServer(t, upstream.URL, "acme")
	defer gateway.Close()

	body := `{"method":"GET","path":"/v1/items"}`
	status, respBody := postJSON(t, gateway.URL+"/api/v1/gateway/acme/invoke", body)

	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, 1, upstreamHits, "upstream should be hit exactly once")

	var out apigatewaykit.InvokeOutput
	require.NoError(t, json.Unmarshal(respBody, &out))
	assert.Equal(t, 200, out.Status)
	bodyMap, ok := out.Body.(map[string]any)
	require.True(t, ok, "body must be a JSON object, got %T", out.Body)
	items, ok := bodyMap["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
}

// TestIntegration_UpstreamErrorIsBodyNotHTTP proves that an upstream
// error (e.g. the downstream API returns 500) surfaces inside the
// gateway's response body as InvokeOutput.Status, while the gateway
// itself still returns HTTP 200. This is the contract NiFi and other
// HTTP clients rely on to distinguish "platform-level failure" from
// "upstream returned an error".
func TestIntegration_UpstreamErrorIsBodyNotHTTP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"db down"}`))
	}))
	defer upstream.Close()

	gateway := newGatewayHTTPServer(t, upstream.URL, "acme")
	defer gateway.Close()

	status, respBody := postJSON(t, gateway.URL+"/api/v1/gateway/acme/invoke",
		`{"method":"GET","path":"/v1/x"}`)

	require.Equal(t, http.StatusOK, status,
		"platform call succeeded; upstream failure must not surface as a gateway 5xx")
	var out apigatewaykit.InvokeOutput
	require.NoError(t, json.Unmarshal(respBody, &out))
	assert.Equal(t, 500, out.Status,
		"upstream HTTP status must be returned in InvokeOutput.Status")
}

// TestIntegration_UpstreamTimeoutIsGatewayTimeout proves that when
// the apigateway's outbound call to the upstream exceeds its
// CallTimeout, the REST shim returns HTTP 504 Gateway Timeout, NOT
// 200 with a status=0 body, and NOT 500. This is the corrected gateway
// semantics from issue #432: the gateway failed to proxy, so it
// surfaces the failure at the wire layer so HTTP clients (NiFi,
// Airflow, curl) can use their built-in retry / failure routing
// instead of having to parse a JSON body to detect the failure.
func TestIntegration_UpstreamTimeoutIsGatewayTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		// Sleep longer than the call_timeout configured in
		// newGatewayHTTPServer's connection definition. The
		// apigateway will cancel the request and produce a
		// context-deadline-exceeded error which buildInvokeResult
		// classifies as upstream_timeout.
		time.Sleep(2 * time.Second)
	}))
	defer upstream.Close()

	gateway := newGatewayHTTPServerWithTimeout(t, upstream.URL, "acme", "200ms")
	defer gateway.Close()

	status, respBody := postJSON(t, gateway.URL+"/api/v1/gateway/acme/invoke",
		`{"method":"GET","path":"/v1/slow"}`)

	require.Equal(t, http.StatusGatewayTimeout, status,
		"upstream timeout must surface as 504 Gateway Timeout at the wire layer")
	var env errorEnvelope
	require.NoError(t, json.Unmarshal(respBody, &env))
	assert.NotEmpty(t, env.Error, "504 response must include the scrubbed transport error message")
}

// TestIntegration_TransportErrorIsBadGateway proves that when the
// upstream is unreachable (connection refused / TLS / DNS), the REST
// shim returns HTTP 502 Bad Gateway. Confirms the gateway-side
// failure path lands as a real 5xx that HTTP clients can route on.
func TestIntegration_TransportErrorIsBadGateway(t *testing.T) {
	// Stand up an httptest server then close it immediately so the
	// URL is well-formed but the TCP connect attempt fails. This
	// produces a "connect: connection refused" error from net/http
	// which the apigateway scrubber preserves and the classifier
	// maps to 502.
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	closedURL := upstream.URL
	upstream.Close()

	gateway := newGatewayHTTPServerWithTimeout(t, closedURL, "acme", "5s")
	defer gateway.Close()

	status, respBody := postJSON(t, gateway.URL+"/api/v1/gateway/acme/invoke",
		`{"method":"GET","path":"/v1/anything"}`)

	require.Equal(t, http.StatusBadGateway, status,
		"unreachable upstream must surface as 502 Bad Gateway at the wire layer")
	var env errorEnvelope
	require.NoError(t, json.Unmarshal(respBody, &env))
	assert.NotEmpty(t, env.Error, "502 response must include the scrubbed transport error message")
}

// newGatewayHTTPServerWithTimeout is a variant of newGatewayHTTPServer
// that takes an explicit call_timeout string, used by the timeout
// integration test to force a deterministic cancel without waiting
// the default 5 seconds.
func newGatewayHTTPServerWithTimeout(t *testing.T, upstreamURL, connName, callTimeout string) *httptest.Server {
	t.Helper()
	tk := apigatewaykit.New("apigateway")
	if upstreamURL != "" {
		require.NoError(t, tk.AddConnection(connName, map[string]any{
			"base_url":        upstreamURL,
			"auth_mode":       apigatewaykit.AuthModeNone,
			"call_timeout":    callTimeout,
			"connect_timeout": "2s",
		}))
	}
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	tk.RegisterTools(mcpServer)
	handler, err := NewHandler(Deps{MCPServer: mcpServer})
	require.NoError(t, err)
	srv := httptest.NewServer(handler)
	t.Cleanup(func() {
		time.Sleep(10 * time.Millisecond)
	})
	return srv
}

// TestIntegration_ConnectionNotFound exercises the 404 path: the
// caller posts to a connection name that is not registered with the
// toolkit. The platform refuses up front (no outbound call is made)
// and the gateway maps the tool error envelope to HTTP 404.
func TestIntegration_ConnectionNotFound(t *testing.T) {
	gateway := newGatewayHTTPServer(t, "", "registered-connection")
	defer gateway.Close()

	status, respBody := postJSON(t, gateway.URL+"/api/v1/gateway/missing/invoke",
		`{"method":"GET","path":"/x"}`)

	require.Equal(t, http.StatusNotFound, status)
	var env errorEnvelope
	require.NoError(t, json.Unmarshal(respBody, &env))
	assert.Contains(t, env.Error, "not found")
}

// TestIntegration_ValidationErrors covers the request-decoding branch
// (empty body, invalid JSON, missing required fields) end-to-end so
// the HTTP status mapping is verified through the real ServeHTTP
// path, not just the decoder helper.
func TestIntegration_ValidationErrors(t *testing.T) {
	gateway := newGatewayHTTPServer(t, "", "any")
	defer gateway.Close()

	tests := []struct {
		name string
		body string
		want string
	}{
		{"empty", "", "request body is required"},
		{"invalid_json", "{", "invalid JSON"},
		{"missing_method", `{"path":"/x"}`, "method is required"},
		{"missing_path", `{"method":"GET"}`, "path is required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, respBody := postJSON(t, gateway.URL+"/api/v1/gateway/any/invoke", tc.body)
			assert.Equal(t, http.StatusBadRequest, status)
			var env errorEnvelope
			require.NoError(t, json.Unmarshal(respBody, &env))
			assert.Contains(t, env.Error, tc.want)
		})
	}
}

// TestIntegration_ToolNotRegistered exercises the CallTool error
// path: when the MCP server has no api_invoke_endpoint tool
// registered, the in-memory session returns a transport-level error,
// which the handler maps to HTTP 500. This is the only branch in
// invoke() that distinguishes a platform-internal failure
// (mcp.CallTool returning err) from a tool-level error envelope.
func TestIntegration_ToolNotRegistered(t *testing.T) {
	// Build an MCP server with NO apigateway toolkit registered.
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	handler, err := NewHandler(Deps{MCPServer: mcpServer})
	require.NoError(t, err)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	status, _ := postJSON(t, srv.URL+"/api/v1/gateway/acme/invoke",
		`{"method":"GET","path":"/x"}`)
	assert.Equal(t, http.StatusInternalServerError, status)
}

// TestIntegration_MissingConnectionSegment exercises a routing edge
// case: the URL pattern requires a connection segment, so go's
// ServeMux returns 404 before our handler runs. This guards the
// behavior that the gateway never accepts a request without a
// connection name.
func TestIntegration_MissingConnectionSegment(t *testing.T) {
	gateway := newGatewayHTTPServer(t, "", "any")
	defer gateway.Close()

	// /api/v1/gateway/invoke is missing the {connection} segment; the
	// mux has no matching pattern and returns 404 Not Found.
	status, _ := postJSON(t, gateway.URL+"/api/v1/gateway//invoke",
		`{"method":"GET","path":"/x"}`)
	assert.NotEqual(t, http.StatusOK, status)
}

// TestIntegration_AuthHeaderForwardedToContext verifies the X-API-Key
// header is parsed and placed into the request context the in-memory
// MCP session inherits. Without an authenticator registered on the
// server, the token is not validated — but if it WERE registered, the
// extracted value is what the authenticator would see. The assertion
// here is a sanity check that readRequestToken integrates with the
// HTTP request path, not a substitute for an end-to-end auth test
// with a real authenticator.
func TestIntegration_AuthHeaderForwardedToContext(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	gateway := newGatewayHTTPServer(t, upstream.URL, "acme")
	defer gateway.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		gateway.URL+"/api/v1/gateway/acme/invoke",
		strings.NewReader(`{"method":"GET","path":"/x"}`))
	require.NoError(t, err)
	req.Header.Set("X-API-Key", "test-key")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestIntegration_HeadersAndQueryForwarded confirms the request
// fields the caller sets in the JSON body actually reach the upstream
// as HTTP headers, query params, and body. This is the test that
// would catch a regression in buildInvokeArgs's mapping into the
// api_invoke_endpoint argument schema.
func TestIntegration_HeadersAndQueryForwarded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "abc", r.Header.Get("X-Trace"))
		assert.Equal(t, "42", r.URL.Query().Get("limit"))
		bodyBytes, _ := io.ReadAll(r.Body)
		assert.Contains(t, string(bodyBytes), `"name":"thing"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	gateway := newGatewayHTTPServer(t, upstream.URL, "acme")
	defer gateway.Close()

	body := `{
		"method": "POST",
		"path": "/v1/things",
		"query_params": {"limit": 42},
		"headers": {"X-Trace": "abc"},
		"body": {"name": "thing"}
	}`
	status, _ := postJSON(t, gateway.URL+"/api/v1/gateway/acme/invoke", body)
	require.Equal(t, http.StatusOK, status)
}

// denyingAuthorizer always denies, mimicking a persona that does not
// permit the requested connection. It returns the exact (false,
// persona, reason) shape the real persona authorizer returns.
type denyingAuthorizer struct {
	persona string
	reason  string
}

func (d denyingAuthorizer) IsAuthorized(_ context.Context, _ string, _ []string, _, _ string) (authorized bool, personaName, reason string) {
	return false, d.persona, d.reason
}

// failingAuthenticator always fails authentication, mimicking a
// rejected credential.
type failingAuthenticator struct {
	err string
}

func (f failingAuthenticator) Authenticate(_ context.Context) (*middleware.UserInfo, error) {
	return nil, errors.New(f.err)
}

// newGatewayHTTPServerWithAuth wires the gateway over a real MCP server
// that has the apigateway toolkit AND the real MCPToolCallMiddleware
// attached. Auth/authz denials therefore travel the exact production
// path: middleware -> PlatformError (a BARE STRING, not a JSON
// envelope) -> in-memory MCP session -> REST classifier. This is what
// proves issue #533 end to end. A unit test on classifyToolError alone
// cannot, because it cannot demonstrate that the middleware's actual
// output reaches the classifier in the bare-string shape the
// classifier must handle — the original bug was precisely that the two
// were never tested together.
func newGatewayHTTPServerWithAuth(t *testing.T, upstreamURL, connName string, authn middleware.Authenticator, authz middleware.Authorizer) *httptest.Server {
	t.Helper()

	tk := apigatewaykit.New("apigateway")
	if upstreamURL != "" {
		require.NoError(t, tk.AddConnection(connName, map[string]any{
			"base_url":        upstreamURL,
			"auth_mode":       apigatewaykit.AuthModeNone,
			"call_timeout":    "5s",
			"connect_timeout": "2s",
		}))
	}

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	tk.RegisterTools(mcpServer)
	mcpServer.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		authn, authz, nil, middleware.ToolCallConfig{Transport: "http"}))

	handler, err := NewHandler(Deps{MCPServer: mcpServer})
	require.NoError(t, err)

	srv := httptest.NewServer(handler)
	t.Cleanup(func() {
		time.Sleep(10 * time.Millisecond)
	})
	return srv
}

// TestIntegration_AuthorizationDeniedIsForbidden is the regression test
// for issue #533: a persona denial originates in the auth/authz
// middleware as a bare-string PlatformError, flows through the
// in-memory MCP session, and must surface to the REST caller as HTTP
// 403 — NOT a retryable 500. Before the fix the gateway returned 500,
// causing upstream HTTP clients to retry-loop on a permanent denial.
func TestIntegration_AuthorizationDeniedIsForbidden(t *testing.T) {
	upstreamHits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits++
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	authz := denyingAuthorizer{
		persona: "example-persona",
		reason:  "connection not allowed for persona: example-persona",
	}
	gateway := newGatewayHTTPServerWithAuth(t, upstream.URL, "acme",
		&middleware.NoopAuthenticator{DefaultUserID: "u1", DefaultRoles: []string{"analyst"}},
		authz)
	defer gateway.Close()

	status, respBody := postJSON(t, gateway.URL+"/api/v1/gateway/acme/invoke",
		`{"method":"GET","path":"/v1/x"}`)

	require.Equal(t, http.StatusForbidden, status,
		"a persona denial must surface as 403, not a retryable 5xx (issue #533)")
	assert.Equal(t, 0, upstreamHits,
		"the denial must occur before any upstream call is made")
	var env errorEnvelope
	require.NoError(t, json.Unmarshal(respBody, &env))
	assert.Contains(t, env.Error, "not authorized",
		"the 403 body must carry the denial reason for the caller")
}

// TestIntegration_AuthenticationFailedIsUnauthorized is the companion
// regression test for issue #533 covering the 401 path: a rejected
// credential produces a bare-string "authentication failed" error from
// the middleware that must surface as HTTP 401, not 500.
func TestIntegration_AuthenticationFailedIsUnauthorized(t *testing.T) {
	gateway := newGatewayHTTPServerWithAuth(t, "", "acme",
		failingAuthenticator{err: "invalid token"},
		&middleware.NoopAuthorizer{})
	defer gateway.Close()

	status, respBody := postJSON(t, gateway.URL+"/api/v1/gateway/acme/invoke",
		`{"method":"GET","path":"/v1/x"}`)

	require.Equal(t, http.StatusUnauthorized, status,
		"a rejected credential must surface as 401, not a retryable 5xx (issue #533)")
	var env errorEnvelope
	require.NoError(t, json.Unmarshal(respBody, &env))
	assert.Contains(t, env.Error, "authentication failed",
		"the 401 body must indicate the authentication failure")
}

// TestIntegration_SourceTaggedRest proves the central goal of issue #x:
// requests that arrive through the REST shim land on the audit context
// with Source="rest", letting operators separate external automation
// traffic (NiFi, cronjobs) from real MCP agents that share the same
// api_invoke_endpoint tool. The same MCP server, invoked directly,
// produces Source="mcp" (the default), so this test also guards the
// negative case.
func TestIntegration_SourceTaggedRest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	tk := apigatewaykit.New("apigateway")
	require.NoError(t, tk.AddConnection("acme", map[string]any{
		"base_url":        upstream.URL,
		"auth_mode":       apigatewaykit.AuthModeNone,
		"call_timeout":    "5s",
		"connect_timeout": "2s",
	}))

	captured := &capturedAuditSource{}
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	tk.RegisterTools(mcpServer)

	// Capture middleware reads PlatformContext.Source after the tool-call
	// middleware has populated it. Wrapping order (LAST added runs FIRST):
	// we add capture first so it's INNER to MCPToolCallMiddleware, ensuring
	// the source override has been resolved by the time capture observes it.
	mcpServer.AddReceivingMiddleware(captured.middleware())
	mcpServer.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&middleware.NoopAuthenticator{},
		middleware.AllowAllAuthorizer(),
		nil,
		middleware.ToolCallConfig{Transport: "http"},
	))

	handler, err := NewHandler(Deps{MCPServer: mcpServer})
	require.NoError(t, err)
	gateway := httptest.NewServer(handler)
	defer gateway.Close()

	status, _ := postJSON(t, gateway.URL+"/api/v1/gateway/acme/invoke",
		`{"method":"GET","path":"/x"}`)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, middleware.SourceREST, captured.get(),
		"REST shim must tag the in-memory MCP session with Source=rest")
}

// capturedAuditSource records the PlatformContext.Source value seen by a
// receiving middleware so tests can assert on what the audit middleware
// would have written.
type capturedAuditSource struct {
	mu     sync.Mutex
	source string
}

func (c *capturedAuditSource) middleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if pc := middleware.GetPlatformContext(ctx); pc != nil {
				c.mu.Lock()
				c.source = pc.Source
				c.mu.Unlock()
			}
			return next(ctx, method, req)
		}
	}
}

func (c *capturedAuditSource) get() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.source
}

// newGatewayHTTPServer builds an MCP server with the apigateway
// toolkit, registers a connection named `connName` pointed at
// `upstreamURL`, mounts the gatewayhttp handler on a fresh
// httptest.Server, and returns it. When upstreamURL is empty, no
// connection is added (used to exercise "connection not found"
// paths).
func newGatewayHTTPServer(t *testing.T, upstreamURL, connName string) *httptest.Server {
	t.Helper()

	tk := apigatewaykit.New("apigateway")
	if upstreamURL != "" {
		require.NoError(t, tk.AddConnection(connName, map[string]any{
			"base_url":        upstreamURL,
			"auth_mode":       apigatewaykit.AuthModeNone,
			"call_timeout":    "5s",
			"connect_timeout": "2s",
		}))
	}

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	tk.RegisterTools(mcpServer)

	handler, err := NewHandler(Deps{MCPServer: mcpServer})
	require.NoError(t, err)

	srv := httptest.NewServer(handler)
	t.Cleanup(func() {
		// Give the in-memory transport a moment to drain before
		// shutting down. Without this, intermittent panics from the
		// SDK's session goroutine can race with test teardown.
		time.Sleep(10 * time.Millisecond)
	})
	return srv
}
