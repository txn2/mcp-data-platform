package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	gatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/gateway"
)

// upstreamMCP stands up an in-process MCP server with one "ping" tool and
// returns (url, cleanup-ready test-handle). The cleanup runs via t.Cleanup.
func upstreamMCP(t *testing.T) string {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "up", Version: "0.0.1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "ping", Description: "pong"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "pong"}},
			}, nil, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(func() {
		ts.CloseClientConnections()
		ts.Close()
	})
	return ts.URL
}

// gatewayHandlerDeps builds a Handler backed by a real gateway toolkit so
// refresh can actually mutate live state.
func gatewayHandlerDeps(t *testing.T, store ConnectionStore) (*Handler, *gatewaykit.Toolkit) {
	t.Helper()
	tk := gatewaykit.New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	reg := &mockToolkitRegistry{rawToolkits: []registry.Toolkit{tk}}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: store,
		ToolkitRegistry: reg,
		ConfigStore:     &mockConfigStore{mode: "database"},
	}, nil)
	return h, tk
}

func TestTestGatewayConnection_Success(t *testing.T) {
	url := upstreamMCP(t)
	h, _ := gatewayHandlerDeps(t, &mockConnectionStore{})

	body, _ := json.Marshal(testGatewayConnectionRequest{
		Config: map[string]any{
			"endpoint":        url,
			"connection_name": "crm",
			"connect_timeout": "3s",
			"call_timeout":    "3s",
		},
	})
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/crm/test",
		bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp testGatewayConnectionResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.True(t, resp.Healthy)
	assert.NotEmpty(t, resp.Tools)
	found := false
	for _, tool := range resp.Tools {
		if tool.Name == "ping" {
			found = true
			assert.Equal(t, "crm__ping", tool.LocalName)
		}
	}
	assert.True(t, found, "ping tool should be discovered")
}

func TestTestGatewayConnection_UnreachableReturns502(t *testing.T) {
	h, _ := gatewayHandlerDeps(t, &mockConnectionStore{})
	body, _ := json.Marshal(testGatewayConnectionRequest{
		Config: map[string]any{
			"endpoint":        "http://127.0.0.1:1/mcp",
			"connect_timeout": "200ms",
			"call_timeout":    "1s",
		},
	})
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/broken/test",
		bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadGateway, w.Code)
	var resp testGatewayConnectionResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.False(t, resp.Healthy)
	assert.NotEmpty(t, resp.Error)
}

func TestTestGatewayConnection_BadConfigReturns400(t *testing.T) {
	h, _ := gatewayHandlerDeps(t, &mockConnectionStore{})
	body, _ := json.Marshal(testGatewayConnectionRequest{
		Config: map[string]any{}, // missing endpoint
	})
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/x/test",
		bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTestGatewayConnection_InvalidJSONReturns400(t *testing.T) {
	h, _ := gatewayHandlerDeps(t, &mockConnectionStore{})
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/x/test",
		bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTestGatewayConnection_RedactedMergedFromStore(t *testing.T) {
	url := upstreamMCP(t)
	// Existing DB row has a real credential; the admin UI re-posts the config
	// with "[REDACTED]" as a placeholder for the hidden value.
	store := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: gatewaykit.Kind, Name: "crm",
			Config: map[string]any{
				"endpoint":        url,
				"auth_mode":       "bearer",
				"credential":      "real-token",
				"connection_name": "crm",
			},
		},
	}
	h, _ := gatewayHandlerDeps(t, store)

	body, _ := json.Marshal(testGatewayConnectionRequest{
		Config: map[string]any{
			"endpoint":        url,
			"auth_mode":       "bearer",
			"credential":      "[REDACTED]",
			"connection_name": "crm",
			"connect_timeout": "3s",
			"call_timeout":    "3s",
		},
	})
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/crm/test",
		bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// "bearer" with merged credential should validate and dial successfully.
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRefreshGatewayConnection_AddsFresh(t *testing.T) {
	url := upstreamMCP(t)
	store := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: gatewaykit.Kind, Name: "crm",
			Config: map[string]any{
				"endpoint":        url,
				"connection_name": "crm",
				"connect_timeout": "3s",
				"call_timeout":    "3s",
			},
		},
	}
	h, tk := gatewayHandlerDeps(t, store)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/crm/refresh", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp refreshGatewayConnectionResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.True(t, resp.Healthy)
	assert.True(t, tk.HasConnection("crm"))
	assert.Contains(t, resp.Tools, "crm__ping")
}

func TestRefreshGatewayConnection_ReplacesExisting(t *testing.T) {
	url := upstreamMCP(t)
	cfg := map[string]any{
		"endpoint":        url,
		"connection_name": "crm",
		"connect_timeout": "3s",
		"call_timeout":    "3s",
	}
	store := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: gatewaykit.Kind, Name: "crm", Config: cfg,
		},
	}
	h, tk := gatewayHandlerDeps(t, store)

	// Pre-seed the live toolkit.
	require.NoError(t, tk.AddConnection("crm", cfg))
	assert.True(t, tk.HasConnection("crm"))

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/crm/refresh", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, tk.HasConnection("crm"))
}

func TestRefreshGatewayConnection_NotFoundIn404(t *testing.T) {
	store := &mockConnectionStore{getErr: platform.ErrConnectionNotFound}
	h, _ := gatewayHandlerDeps(t, store)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/missing/refresh", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRefreshGatewayConnection_UpstreamUnreachableReturns502(t *testing.T) {
	store := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: gatewaykit.Kind, Name: "broken",
			Config: map[string]any{
				"endpoint":        "http://127.0.0.1:1/mcp",
				"connection_name": "broken",
				"connect_timeout": "200ms",
				"call_timeout":    "1s",
			},
		},
	}
	h, _ := gatewayHandlerDeps(t, store)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/broken/refresh", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadGateway, w.Code)
}

func TestRefreshGatewayConnection_InternalErrorFromStoreReturns500(t *testing.T) {
	store := &mockConnectionStore{getErr: errors.New("db exploded")}
	h, _ := gatewayHandlerDeps(t, store)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/x/refresh", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRefreshGatewayConnection_NoLiveToolkitReturns409(t *testing.T) {
	url := upstreamMCP(t)
	store := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: gatewaykit.Kind, Name: "crm",
			Config: map[string]any{
				"endpoint":        url,
				"connection_name": "crm",
				"connect_timeout": "3s",
				"call_timeout":    "3s",
			},
		},
	}
	// Deps without the gateway toolkit registered.
	reg := &mockToolkitRegistry{rawToolkits: []registry.Toolkit{}}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: store,
		ToolkitRegistry: reg,
		ConfigStore:     &mockConfigStore{mode: "database"},
	}, nil)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/crm/refresh", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestConnectionTools_NonListerReturnsNil(t *testing.T) {
	// A ConnectionManager implementation without ConnectionLister should
	// produce nil from connectionTools.
	cm := &stubConnManager{}
	if got := connectionTools(cm, "any"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

type stubConnManager struct{}

func (*stubConnManager) AddConnection(_ string, _ map[string]any) error { return nil }
func (*stubConnManager) RemoveConnection(_ string) error                { return nil }
func (*stubConnManager) HasConnection(_ string) bool                    { return false }

func TestRegisterGatewayRoutes_ImmutableSkipsRegistration(t *testing.T) {
	url := upstreamMCP(t)
	// File-mode config store → handler is immutable → gateway routes skip.
	reg := &mockToolkitRegistry{rawToolkits: []registry.Toolkit{gatewaykit.New("primary")}}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: &mockConnectionStore{},
		ToolkitRegistry: reg,
		ConfigStore:     &mockConfigStore{mode: "file"},
	}, nil)

	body, _ := json.Marshal(testGatewayConnectionRequest{
		Config: map[string]any{"endpoint": url},
	})
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/x/test",
		bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	// 404 from the mux: the route wasn't registered.
	assert.Equal(t, http.StatusNotFound, w.Code)
}
