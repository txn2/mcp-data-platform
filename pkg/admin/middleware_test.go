package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

const (
	testRoleAdmin   = "admin"
	testRoleAnalyst = "analyst"
)

// --- mockAuthenticator ---

type mockAuthenticator struct {
	user *User
	err  error
}

func (m *mockAuthenticator) Authenticate(_ *http.Request) (*User, error) {
	return m.user, m.err
}

// Verify interface compliance.
var _ Authenticator = (*mockAuthenticator)(nil)

// --- mockPlatformAuthenticator ---

type mockMCPAuthenticator struct {
	info *middleware.UserInfo
	err  error
}

func (m *mockMCPAuthenticator) Authenticate(_ context.Context) (*middleware.UserInfo, error) {
	return m.info, m.err
}

var _ middleware.Authenticator = (*mockMCPAuthenticator)(nil)

// --- GetUser tests ---

func TestGetUser(t *testing.T) {
	t.Run("returns user when set in context", func(t *testing.T) {
		user := &User{UserID: "admin-1", Roles: []string{testRoleAdmin}}
		ctx := context.WithValue(context.Background(), adminUserKey, user)
		result := GetUser(ctx)
		require.NotNil(t, result)
		assert.Equal(t, "admin-1", result.UserID)
		assert.Equal(t, []string{testRoleAdmin}, result.Roles)
	})

	t.Run("returns nil when not set", func(t *testing.T) {
		result := GetUser(context.Background())
		assert.Nil(t, result)
	})

	t.Run("returns nil for wrong type in context", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), adminUserKey, "not-an-admin-user")
		result := GetUser(ctx)
		assert.Nil(t, result)
	})
}

// --- APIKeyAuthenticator tests ---

func TestAPIKeyAuthenticator_Authenticate(t *testing.T) {
	auth := &APIKeyAuthenticator{
		Keys: map[string]User{
			"valid-key-1": {UserID: "admin-1", Roles: []string{testRoleAdmin}},
			"viewer-key":  {UserID: "viewer-1", Roles: []string{"viewer"}},
		},
	}

	t.Run("authenticates with X-API-Key header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("X-API-Key", "valid-key-1")

		user, err := auth.Authenticate(req)
		require.NoError(t, err)
		require.NotNil(t, user)
		assert.Equal(t, "admin-1", user.UserID)
		assert.Equal(t, []string{testRoleAdmin}, user.Roles)
	})

	t.Run("authenticates with Bearer token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("Authorization", "Bearer viewer-key")

		user, err := auth.Authenticate(req)
		require.NoError(t, err)
		require.NotNil(t, user)
		assert.Equal(t, "viewer-1", user.UserID)
	})

	t.Run("X-API-Key takes priority over Authorization", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("X-API-Key", "valid-key-1")
		req.Header.Set("Authorization", "Bearer viewer-key")

		user, err := auth.Authenticate(req)
		require.NoError(t, err)
		require.NotNil(t, user)
		assert.Equal(t, "admin-1", user.UserID)
	})

	t.Run("returns nil for missing credentials", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)

		user, err := auth.Authenticate(req)
		require.NoError(t, err)
		assert.Nil(t, user)
	})

	t.Run("returns nil for invalid key", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("X-API-Key", "invalid-key")

		user, err := auth.Authenticate(req)
		require.NoError(t, err)
		assert.Nil(t, user)
	})

	t.Run("returns nil for non-Bearer authorization", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

		user, err := auth.Authenticate(req)
		require.NoError(t, err)
		assert.Nil(t, user)
	})

	t.Run("returns nil for empty Bearer token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("Authorization", "Bearer ")

		user, err := auth.Authenticate(req)
		require.NoError(t, err)
		assert.Nil(t, user)
	})
}

// --- RequireAdmin tests ---

func TestRequireAdmin(t *testing.T) {
	successHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUser(r.Context())
		writeJSON(w, http.StatusOK, map[string]string{"user_id": user.UserID})
	})

	t.Run("passes through with valid admin user", func(t *testing.T) {
		auth := &mockAuthenticator{
			user: &User{UserID: "admin-1", Roles: []string{testRoleAdmin}},
		}
		mid := RequireAdmin(auth)
		handler := mid(successHandler)

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body map[string]string
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, "admin-1", body["user_id"])
	})

	t.Run("sets admin user in context", func(t *testing.T) {
		var capturedUser *User
		checkHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			capturedUser = GetUser(r.Context())
		})

		auth := &mockAuthenticator{
			user: &User{UserID: "admin-ctx", Roles: []string{testRoleAdmin}},
		}
		mid := RequireAdmin(auth)
		handler := mid(checkHandler)

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		require.NotNil(t, capturedUser)
		assert.Equal(t, "admin-ctx", capturedUser.UserID)
	})

	t.Run("returns 401 for missing credentials", func(t *testing.T) {
		auth := &mockAuthenticator{user: nil, err: nil}
		mid := RequireAdmin(auth)
		handler := mid(successHandler)

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Equal(t, "authentication required", pd.Detail)
	})

	t.Run("returns 403 for non-admin roles", func(t *testing.T) {
		auth := &mockAuthenticator{
			user: &User{UserID: "viewer-1", Roles: []string{"viewer", testRoleAnalyst}},
		}
		mid := RequireAdmin(auth)
		handler := mid(successHandler)

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Equal(t, "admin role required", pd.Detail)
	})

	t.Run("returns 403 for empty roles", func(t *testing.T) {
		auth := &mockAuthenticator{
			user: &User{UserID: "no-roles", Roles: []string{}},
		}
		mid := RequireAdmin(auth)
		handler := mid(successHandler)

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("returns 500 for authentication error", func(t *testing.T) {
		auth := &mockAuthenticator{
			user: nil,
			err:  fmt.Errorf("oidc provider unreachable"),
		}
		mid := RequireAdmin(auth)
		handler := mid(successHandler)

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Equal(t, "authentication error", pd.Detail)
	})

	t.Run("admin role among multiple roles passes", func(t *testing.T) {
		auth := &mockAuthenticator{
			user: &User{UserID: "multi-role", Roles: []string{"viewer", testRoleAdmin, testRoleAnalyst}},
		}
		mid := RequireAdmin(auth)
		handler := mid(successHandler)

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// --- hasAdminRole tests ---

func TestHasAdminRole(t *testing.T) {
	tests := []struct {
		name     string
		roles    []string
		expected bool
	}{
		{"admin role present", []string{testRoleAdmin}, true},
		{"admin among many", []string{"viewer", testRoleAdmin, testRoleAnalyst}, true},
		{"no admin role", []string{"viewer", testRoleAnalyst}, false},
		{"empty roles", []string{}, false},
		{"nil roles", nil, false},
		{"similar but not admin", []string{"administrator", "superadmin"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, hasAdminRole(tt.roles))
		})
	}
}

// --- extractToken tests ---

func TestExtractToken(t *testing.T) {
	t.Run("extracts from X-API-Key", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("X-API-Key", "test-key")
		assert.Equal(t, "test-key", extractToken(req))
	})

	t.Run("extracts from Bearer token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("Authorization", "Bearer jwt-token")
		assert.Equal(t, "jwt-token", extractToken(req))
	})

	t.Run("X-API-Key takes priority", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("X-API-Key", "api-key")
		req.Header.Set("Authorization", "Bearer jwt-token")
		assert.Equal(t, "api-key", extractToken(req))
	})

	t.Run("returns empty for no credentials", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		assert.Equal(t, "", extractToken(req))
	})

	t.Run("returns empty for non-Bearer auth", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("Authorization", "Basic abc123")
		assert.Equal(t, "", extractToken(req))
	})
}

// --- PlatformAuthenticator tests ---

func TestPlatformAuthenticator_Authenticate(t *testing.T) {
	adminPersona := &persona.Persona{
		Name:  "admin",
		Roles: []string{"admin"},
		Tools: persona.ToolRules{Allow: []string{"*"}},
	}
	analystPersona := &persona.Persona{
		Name:  testRoleAnalyst,
		Roles: []string{testRoleAnalyst},
		Tools: persona.ToolRules{Allow: []string{"trino_*"}},
	}

	setupRegistry := func() *persona.Registry {
		reg := persona.NewRegistry()
		_ = reg.Register(adminPersona)
		_ = reg.Register(analystPersona)
		return reg
	}

	t.Run("valid admin API key", func(t *testing.T) {
		mcpAuth := &mockMCPAuthenticator{
			info: &middleware.UserInfo{UserID: "admin-1", Roles: []string{"admin"}},
		}
		reg := setupRegistry()
		pa := NewPlatformAuthenticator(mcpAuth, "admin", reg)

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("X-API-Key", "test-key")

		user, err := pa.Authenticate(req)
		require.NoError(t, err)
		require.NotNil(t, user)
		assert.Equal(t, "admin-1", user.UserID)
		assert.Equal(t, []string{"admin"}, user.Roles)
	})

	t.Run("valid non-admin user returns nil", func(t *testing.T) {
		mcpAuth := &mockMCPAuthenticator{
			info: &middleware.UserInfo{UserID: "analyst-1", Roles: []string{testRoleAnalyst}},
		}
		reg := setupRegistry()
		pa := NewPlatformAuthenticator(mcpAuth, "admin", reg)

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("X-API-Key", "test-key")

		user, err := pa.Authenticate(req)
		require.NoError(t, err)
		assert.Nil(t, user)
	})

	t.Run("no token returns nil", func(t *testing.T) {
		mcpAuth := &mockMCPAuthenticator{}
		reg := setupRegistry()
		pa := NewPlatformAuthenticator(mcpAuth, "admin", reg)

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)

		user, err := pa.Authenticate(req)
		require.NoError(t, err)
		assert.Nil(t, user)
	})

	t.Run("authenticator error treated as rejection", func(t *testing.T) {
		mcpAuth := &mockMCPAuthenticator{
			err: fmt.Errorf("invalid API key"),
		}
		reg := setupRegistry()
		pa := NewPlatformAuthenticator(mcpAuth, "admin", reg)

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("X-API-Key", "test-key")

		// Auth failures (invalid keys, expired tokens) return nil/nil
		// so RequirePersona responds 401, not 500.
		user, err := pa.Authenticate(req)
		require.NoError(t, err)
		assert.Nil(t, user)
	})

	t.Run("authenticator returns nil info", func(t *testing.T) {
		mcpAuth := &mockMCPAuthenticator{info: nil}
		reg := setupRegistry()
		pa := NewPlatformAuthenticator(mcpAuth, "admin", reg)

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("X-API-Key", "test-key")

		user, err := pa.Authenticate(req)
		require.NoError(t, err)
		assert.Nil(t, user)
	})

	t.Run("Bearer token authentication", func(t *testing.T) {
		mcpAuth := &mockMCPAuthenticator{
			info: &middleware.UserInfo{UserID: "admin-2", Roles: []string{"admin"}},
		}
		reg := setupRegistry()
		pa := NewPlatformAuthenticator(mcpAuth, "admin", reg)

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("Authorization", "Bearer jwt-token")

		user, err := pa.Authenticate(req)
		require.NoError(t, err)
		require.NotNil(t, user)
		assert.Equal(t, "admin-2", user.UserID)
	})

	t.Run("no matching persona returns nil", func(t *testing.T) {
		mcpAuth := &mockMCPAuthenticator{
			info: &middleware.UserInfo{UserID: "unknown", Roles: []string{"unknown_role"}},
		}
		reg := setupRegistry()
		pa := NewPlatformAuthenticator(mcpAuth, "admin", reg)

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("X-API-Key", "test-key")

		user, err := pa.Authenticate(req)
		require.NoError(t, err)
		assert.Nil(t, user)
	})
}

// --- RequirePersona tests ---

func TestRequirePersona(t *testing.T) {
	successHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUser(r.Context())
		writeJSON(w, http.StatusOK, map[string]string{"user_id": user.UserID})
	})

	t.Run("passes through with valid user", func(t *testing.T) {
		auth := &mockAuthenticator{
			user: &User{UserID: "admin-1", Roles: []string{testRoleAdmin}},
		}
		mid := RequirePersona(auth)
		handler := mid(successHandler)

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("returns 401 for nil user", func(t *testing.T) {
		auth := &mockAuthenticator{user: nil}
		mid := RequirePersona(auth)
		handler := mid(successHandler)

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("returns 500 for auth error", func(t *testing.T) {
		auth := &mockAuthenticator{err: fmt.Errorf("auth error")}
		mid := RequirePersona(auth)
		handler := mid(successHandler)

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("sets user in context", func(t *testing.T) {
		var capturedUser *User
		checkHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			capturedUser = GetUser(r.Context())
		})

		auth := &mockAuthenticator{
			user: &User{UserID: "test-user", Roles: []string{"admin"}},
		}
		mid := RequirePersona(auth)
		handler := mid(checkHandler)

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		require.NotNil(t, capturedUser)
		assert.Equal(t, "test-user", capturedUser.UserID)
	})
}

// --- Integration test: full Handler with RequireAdmin ---

func TestHandler_IntegrationWithAuth(t *testing.T) {
	store := &mockInsightStore{
		listResult: []mockListResult{{insights: nil, total: 0, err: nil}},
	}
	csStore := &mockChangesetStore{
		listResult: []mockChangesetListResult{{changesets: nil, total: 0, err: nil}},
	}
	kh := NewKnowledgeHandler(store, csStore, nil)

	apiAuth := &APIKeyAuthenticator{
		Keys: map[string]User{
			"admin-key": {UserID: "admin-1", Roles: []string{testRoleAdmin}},
		},
	}
	authMiddle := RequireAdmin(apiAuth)
	h := NewHandler(Deps{Knowledge: kh}, authMiddle)

	t.Run("authenticated admin can access insights", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/knowledge/insights", http.NoBody)
		req.Header.Set("X-API-Key", "admin-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("unauthenticated request is rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/knowledge/insights", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("non-admin key is forbidden", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/knowledge/insights", http.NoBody)
		req.Header.Set("X-API-Key", "unknown-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}
