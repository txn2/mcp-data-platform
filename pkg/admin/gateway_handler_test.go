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

func TestGetGatewayConnectionStatus_NotFound(t *testing.T) {
	h, _ := gatewayHandlerDeps(t, &mockConnectionStore{})
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/v1/admin/gateway/connections/missing/status", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetGatewayConnectionStatus_NoToolkitReturns409(t *testing.T) {
	reg := &mockToolkitRegistry{rawToolkits: []registry.Toolkit{}}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: &mockConnectionStore{},
		ToolkitRegistry: reg,
		ConfigStore:     &mockConfigStore{mode: "database"},
	}, nil)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/v1/admin/gateway/connections/x/status", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestGetGatewayConnectionStatus_ReturnsStatus(t *testing.T) {
	url := upstreamMCP(t)
	store := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: gatewaykit.Kind, Name: "live",
			Config: map[string]any{
				"endpoint":        url,
				"connection_name": "live",
				"connect_timeout": "3s",
				"call_timeout":    "3s",
			},
		},
	}
	h, tk := gatewayHandlerDeps(t, store)
	require.NoError(t, tk.AddConnection("live", store.getResult.Config))

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/v1/admin/gateway/connections/live/status", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var status gatewaykit.ConnectionStatus
	require.NoError(t, json.NewDecoder(w.Body).Decode(&status))
	assert.Equal(t, "live", status.Name)
	assert.True(t, status.Healthy)
	assert.Equal(t, gatewaykit.AuthModeNone, status.AuthMode)
	assert.Nil(t, status.OAuth)
}

// TestConnectionHasOAuthToken_NoToolkitReturnsFalse covers the early-out
// path where the gateway toolkit isn't registered at all (e.g. config-mode
// deployments). The check must conservatively report "no token" so the
// short-circuit message still tells the operator how to proceed.
func TestConnectionHasOAuthToken_NoToolkitReturnsFalse(t *testing.T) {
	reg := &mockToolkitRegistry{rawToolkits: []registry.Toolkit{}}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: &mockConnectionStore{},
		ToolkitRegistry: reg,
		ConfigStore:     &mockConfigStore{mode: "database"},
	}, nil)
	assert.False(t, h.connectionHasOAuthToken("anything"))
}

// TestConnectionHasOAuthToken_UnknownConnectionReturnsFalse covers the case
// where the toolkit exists but the named connection has not been added —
// Status() returns nil and we must report "no token".
func TestConnectionHasOAuthToken_UnknownConnectionReturnsFalse(t *testing.T) {
	h, _ := gatewayHandlerDeps(t, &mockConnectionStore{})
	assert.False(t, h.connectionHasOAuthToken("does-not-exist"))
}

// TestConnectionHasOAuthToken_NonOAuthConnectionReturnsFalse covers a
// bearer/api_key/none connection: Status returns OAuth=nil and we treat
// that as "not authorized" for the purposes of the test short-circuit.
func TestConnectionHasOAuthToken_NonOAuthConnectionReturnsFalse(t *testing.T) {
	url := upstreamMCP(t)
	h, tk := gatewayHandlerDeps(t, &mockConnectionStore{})
	require.NoError(t, tk.AddConnection("plain", map[string]any{
		"endpoint":        url,
		"connection_name": "plain",
		"connect_timeout": "3s",
		"call_timeout":    "3s",
	}))
	assert.False(t, h.connectionHasOAuthToken("plain"))
}

// TestTestGatewayConnection_AuthCodeUnauthorizedReturnsFriendlyMessage
// proves the admin UX fix: a Test-connection click on an OAuth
// authorization_code connection that has no stored token must return a
// 200 with a clear "click Connect first" message instead of letting the
// upstream dial fail with a cryptic OAuth error.
func TestTestGatewayConnection_AuthCodeUnauthorizedReturnsFriendlyMessage(t *testing.T) {
	cfg := map[string]any{
		"endpoint":                "https://upstream.example.com/mcp",
		"connection_name":         "vendor",
		"auth_mode":               gatewaykit.AuthModeOAuth,
		"oauth_grant":             gatewaykit.OAuthGrantAuthorizationCode,
		"oauth_token_url":         "https://idp.example.com/token",
		"oauth_authorization_url": "https://idp.example.com/authorize",
		"oauth_client_id":         "id",
		"oauth_client_secret":     "sec",
		"connect_timeout":         "1s",
		"call_timeout":            "1s",
	}
	store := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: gatewaykit.Kind, Name: "vendor", Config: cfg,
		},
	}
	h, tk := gatewayHandlerDeps(t, store)
	// Mirror the post-save state: AddConnection records a placeholder
	// because dial fails (no token yet).
	require.NoError(t, tk.AddConnection("vendor", cfg))

	body, _ := json.Marshal(testGatewayConnectionRequest{Config: cfg})
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/vendor/test",
		bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"unauthorized authcode is a domain-level outcome, not an HTTP failure")
	var resp testGatewayConnectionResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.False(t, resp.Healthy)
	assert.Contains(t, resp.Error, "Connect",
		"error message must point the operator at the Connect button")
	assert.Empty(t, resp.Tools)
}

func TestReacquireGatewayOAuth_NotFound(t *testing.T) {
	h, _ := gatewayHandlerDeps(t, &mockConnectionStore{})
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/missing/reacquire-oauth", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestReacquireGatewayOAuth_NotConfiguredReturns502(t *testing.T) {
	url := upstreamMCP(t)
	store := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: gatewaykit.Kind, Name: "noauth",
			Config: map[string]any{
				"endpoint":        url,
				"connection_name": "noauth",
				"connect_timeout": "3s",
				"call_timeout":    "3s",
			},
		},
	}
	h, tk := gatewayHandlerDeps(t, store)
	require.NoError(t, tk.AddConnection("noauth", store.getResult.Config))

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/noauth/reacquire-oauth", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadGateway, w.Code)
}

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
