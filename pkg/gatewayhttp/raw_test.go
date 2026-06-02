package gatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// newRawGatewayServer wires a gateway whose /invoke-raw route enforces
// the given all-or-nothing rawMax.
func newRawGatewayServer(t *testing.T, upstreamURL, connName string, rawMax int64) *httptest.Server {
	t.Helper()
	tk := apigatewaykit.New("apigateway")
	require.NoError(t, tk.AddConnection(connName, map[string]any{
		"base_url":        upstreamURL,
		"auth_mode":       apigatewaykit.AuthModeNone,
		"call_timeout":    "5s",
		"connect_timeout": "2s",
	}))
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	tk.RegisterTools(mcpServer)
	handler, err := NewHandler(Deps{MCPServer: mcpServer, RawMaxBytes: rawMax})
	require.NoError(t, err)
	srv := httptest.NewServer(handler)
	t.Cleanup(func() { time.Sleep(10 * time.Millisecond) })
	return srv
}

// newBudgetGatewayServer wires a gateway whose buffered /invoke route
// reserves against the supplied (caller-controlled) memory budget, so a
// test can pre-exhaust it and observe the 429 path.
func newBudgetGatewayServer(t *testing.T, upstreamURL, connName string, budget *apigatewaykit.MemBudget) *httptest.Server {
	t.Helper()
	tk := apigatewaykit.New("apigateway")
	require.NoError(t, tk.AddConnection(connName, map[string]any{
		"base_url":        upstreamURL,
		"auth_mode":       apigatewaykit.AuthModeNone,
		"call_timeout":    "5s",
		"connect_timeout": "2s",
	}))
	tk.SetMemBudget(budget)
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	tk.RegisterTools(mcpServer)
	handler, err := NewHandler(Deps{MCPServer: mcpServer})
	require.NoError(t, err)
	srv := httptest.NewServer(handler)
	t.Cleanup(func() { time.Sleep(10 * time.Millisecond) })
	return srv
}

// TestIntegration_RawPassthrough_StreamsBinary proves the end-to-end
// raw path: a binary upstream body is streamed verbatim through the
// REST shim with the upstream status and Content-Type, NOT wrapped in
// the JSON envelope.
func TestIntegration_RawPassthrough_StreamsBinary(t *testing.T) {
	payload := bytes.Repeat([]byte{0x00, 0x10, 0x7f, 0xff}, 1024)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer upstream.Close()

	gateway := newRawGatewayServer(t, upstream.URL, "acme", 1<<20)
	defer gateway.Close()

	status, body, header := postRaw(t, gateway.URL+"/api/v1/gateway/acme/invoke-raw",
		`{"method":"GET","path":"/blob"}`)

	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "application/octet-stream", header.Get("Content-Type"))
	assert.True(t, bytes.Equal(payload, body), "raw body must be streamed verbatim (got %d bytes, want %d)", len(body), len(payload))
}

// TestIntegration_RawPassthrough_TooLarge413 proves the all-or-nothing
// 413: an upstream Content-Length over the configured raw cap is
// rejected with HTTP 413 and the structured envelope, before any bytes
// are streamed. 413 is non-retryable per the #533/#535 retry policy.
func TestIntegration_RawPassthrough_TooLarge413(t *testing.T) {
	big := bytes.Repeat([]byte("A"), 5000)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(big)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(big)
	}))
	defer upstream.Close()

	gateway := newRawGatewayServer(t, upstream.URL, "acme", 1000)
	defer gateway.Close()

	status, body, _ := postRaw(t, gateway.URL+"/api/v1/gateway/acme/invoke-raw",
		`{"method":"GET","path":"/big"}`)

	require.Equal(t, http.StatusRequestEntityTooLarge, status)
	var env map[string]any
	require.NoError(t, json.Unmarshal(body, &env))
	assert.Equal(t, apigatewaykit.ErrCodeBodyTooLarge, env["error"])
	assert.Equal(t, float64(5000), env["actual_bytes"], "structured 413 must carry the actual size")
	assert.Equal(t, float64(1000), env["limit_bytes"], "structured 413 must carry the limit")
}

// TestIntegration_BufferedBudget429 proves the buffered /invoke route
// surfaces a global budget exhaustion as HTTP 429 with a Retry-After
// header (retryable-with-backoff per the #533/#535 retry policy) and
// the structured envelope.
func TestIntegration_BufferedBudget429(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 100))
	}))
	defer upstream.Close()

	budget := apigatewaykit.NewMemBudget(1000)
	require.True(t, budget.Acquire(950), "pre-exhaust the budget")

	gateway := newBudgetGatewayServer(t, upstream.URL, "acme", budget)
	defer gateway.Close()

	status, body, header := postRaw(t, gateway.URL+"/api/v1/gateway/acme/invoke",
		`{"method":"GET","path":"/items"}`)

	require.Equal(t, http.StatusTooManyRequests, status)
	assert.NotEmpty(t, header.Get("Retry-After"), "429 must advertise Retry-After so clients back off")
	var env map[string]any
	require.NoError(t, json.Unmarshal(body, &env))
	assert.Equal(t, apigatewaykit.ErrCodeBudgetExhausted, env["error"])
	assert.Equal(t, float64(1000), env["limit_bytes"])
}

func TestClassifyToolError_MemoryCodes(t *testing.T) {
	tests := []struct {
		name       string
		payload    string
		wantStatus int
	}{
		{
			name:       "body too large -> 413",
			payload:    `{"error":"` + apigatewaykit.ErrCodeBodyTooLarge + `","actual_bytes":5000}`,
			wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name:       "budget exhausted -> 429",
			payload:    `{"error":"` + apigatewaykit.ErrCodeBudgetExhausted + `","limit_bytes":1000}`,
			wantStatus: http.StatusTooManyRequests,
		},
		{
			name:       "body not inlineable -> 415",
			payload:    `{"error":"` + apigatewaykit.ErrCodeBodyNotInlineable + `","content_type":"application/zip"}`,
			wantStatus: http.StatusUnsupportedMediaType,
		},
		{
			name:       "auth still classifies first",
			payload:    `{"error":"authentication failed: bad token"}`,
			wantStatus: http.StatusUnauthorized,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, _ := classifyToolError(tc.payload)
			assert.Equal(t, tc.wantStatus, status)
		})
	}
}

func TestWriteClassifiedError_PreservesStructuredEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	payload := `{"error":"upstream_body_too_large","limit_bytes":1000,"actual_bytes":5000,"connection":"acme","path":"/big"}`
	writeClassifiedError(rec, http.StatusRequestEntityTooLarge, payload, "upstream_body_too_large")

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	var env map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	// Extra diagnostic fields must survive (not be flattened to {"error"}).
	assert.Equal(t, float64(5000), env["actual_bytes"])
	assert.Equal(t, "acme", env["connection"])
}

func TestWriteClassifiedError_WrapsBareStringAndSetsRetryAfter(t *testing.T) {
	rec := httptest.NewRecorder()
	// A bare (non-JSON) payload, as the auth/authz middleware emits.
	writeClassifiedError(rec, http.StatusTooManyRequests, "not authorized: nope", "not authorized: nope")
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("Retry-After"))
	var env errorEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.Equal(t, "not authorized: nope", env.Error)
}

// TestIntegration_RawPassthrough_ValidationError proves a malformed
// raw request is rejected at the decode stage with 400 before any MCP
// session work.
func TestIntegration_RawPassthrough_ValidationError(t *testing.T) {
	gateway := newRawGatewayServer(t, "https://x.example.com", "acme", 0)
	defer gateway.Close()

	status, body, _ := postRaw(t, gateway.URL+"/api/v1/gateway/acme/invoke-raw", ``)
	require.Equal(t, http.StatusBadRequest, status)
	var env errorEnvelope
	require.NoError(t, json.Unmarshal(body, &env))
	assert.Contains(t, env.Error, "request body is required")
}

// TestIntegration_RawPassthrough_ToolNotRegistered drives the CallTool
// error branch: with no api_invoke_endpoint tool on the server, the
// in-memory session returns a transport-level error and, since nothing
// was streamed, the raw handler maps it to HTTP 500.
func TestIntegration_RawPassthrough_ToolNotRegistered(t *testing.T) {
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	handler, err := NewHandler(Deps{MCPServer: mcpServer})
	require.NoError(t, err)
	srv := httptest.NewServer(handler)
	t.Cleanup(func() {
		time.Sleep(10 * time.Millisecond)
		srv.Close()
	})

	status, _, _ := postRaw(t, srv.URL+"/api/v1/gateway/acme/invoke-raw",
		`{"method":"GET","path":"/x"}`)
	require.Equal(t, http.StatusInternalServerError, status)
}

func TestWriteRawToolError_NoTextContent(t *testing.T) {
	rec := httptest.NewRecorder()
	writeRawToolError(rec, &mcp.CallToolResult{IsError: true})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "unexpected response shape")
}

func TestWriteRawToolError_NonErrorResultIsContractViolation(t *testing.T) {
	rec := httptest.NewRecorder()
	writeRawToolError(rec, &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: `{"raw_streamed":true}`}},
	})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "no streamed response")
}

func TestWriteRawToolError_ClassifiesStructuredError(t *testing.T) {
	rec := httptest.NewRecorder()
	payload := `{"error":"` + apigatewaykit.ErrCodeBodyTooLarge + `","actual_bytes":9}`
	writeRawToolError(rec, &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: payload}},
	})
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

// postRaw is like postJSON but also returns the response headers, which
// the raw/413/429 assertions need (Content-Type passthrough,
// Retry-After).
func postRaw(t *testing.T, url, body string) (status int, respBody []byte, header http.Header) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, b, resp.Header
}
