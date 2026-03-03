package portal

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mw "github.com/txn2/mcp-data-platform/pkg/middleware"
)

type mockAuthenticator struct {
	info *mw.UserInfo
	err  error
}

func (m *mockAuthenticator) Authenticate(_ context.Context) (*mw.UserInfo, error) {
	return m.info, m.err
}

func TestGetUserNil(t *testing.T) {
	user := GetUser(context.Background())
	assert.Nil(t, user)
}

func TestGetUserFromContext(t *testing.T) {
	u := &User{UserID: "test", Roles: []string{"analyst"}}
	ctx := context.WithValue(context.Background(), portalUserKey, u)
	got := GetUser(ctx)
	require.NotNil(t, got)
	assert.Equal(t, "test", got.UserID)
}

func TestPortalAuthenticatorNoToken(t *testing.T) {
	pa := NewAuthenticator(&mockAuthenticator{})
	r := httptest.NewRequest("GET", "/", http.NoBody)
	user, err := pa.Authenticate(r)
	assert.NoError(t, err)
	assert.Nil(t, user)
}

func TestPortalAuthenticatorAPIKey(t *testing.T) {
	pa := NewAuthenticator(&mockAuthenticator{
		info: &mw.UserInfo{UserID: "user1", Roles: []string{"analyst"}},
	})
	r := httptest.NewRequest("GET", "/", http.NoBody)
	r.Header.Set("X-API-Key", "test-key")
	user, err := pa.Authenticate(r)
	assert.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, "user1", user.UserID)
}

func TestPortalAuthenticatorBearer(t *testing.T) {
	pa := NewAuthenticator(&mockAuthenticator{
		info: &mw.UserInfo{UserID: "user2", Roles: []string{"admin"}},
	})
	r := httptest.NewRequest("GET", "/", http.NoBody)
	r.Header.Set("Authorization", "Bearer mytoken")
	user, err := pa.Authenticate(r)
	assert.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, "user2", user.UserID)
}

func TestPortalAuthenticatorAuthError(t *testing.T) {
	pa := NewAuthenticator(&mockAuthenticator{
		err: fmt.Errorf("auth failed"),
	})
	r := httptest.NewRequest("GET", "/", http.NoBody)
	r.Header.Set("X-API-Key", "test")
	user, err := pa.Authenticate(r)
	assert.NoError(t, err) // errors are treated as unauthenticated
	assert.Nil(t, user)
}

func TestPortalAuthenticatorNilInfo(t *testing.T) {
	pa := NewAuthenticator(&mockAuthenticator{
		info: nil,
	})
	r := httptest.NewRequest("GET", "/", http.NoBody)
	r.Header.Set("X-API-Key", "test")
	user, err := pa.Authenticate(r)
	assert.NoError(t, err)
	assert.Nil(t, user)
}

func TestRequirePortalAuthSuccess(t *testing.T) {
	pa := NewAuthenticator(&mockAuthenticator{
		info: &mw.UserInfo{UserID: "user1", Roles: []string{"analyst"}},
	})

	var capturedUser *User
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedUser = GetUser(r.Context())
	})

	authMW := RequirePortalAuth(pa)(inner)
	r := httptest.NewRequest("GET", "/", http.NoBody)
	r.Header.Set("X-API-Key", "test")
	w := httptest.NewRecorder()
	authMW.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, capturedUser)
	assert.Equal(t, "user1", capturedUser.UserID)
}

func TestRequirePortalAuthNoCredentials(t *testing.T) {
	pa := NewAuthenticator(&mockAuthenticator{})

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	authMW := RequirePortalAuth(pa)(inner)
	r := httptest.NewRequest("GET", "/", http.NoBody)
	w := httptest.NewRecorder()
	authMW.ServeHTTP(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestExtractPortalTokenAPIKey(t *testing.T) {
	r := httptest.NewRequest("GET", "/", http.NoBody)
	r.Header.Set("X-API-Key", "mykey")
	assert.Equal(t, "mykey", extractPortalToken(r))
}

func TestExtractPortalTokenBearer(t *testing.T) {
	r := httptest.NewRequest("GET", "/", http.NoBody)
	r.Header.Set("Authorization", "Bearer mytoken")
	assert.Equal(t, "mytoken", extractPortalToken(r))
}

func TestExtractPortalTokenEmpty(t *testing.T) {
	r := httptest.NewRequest("GET", "/", http.NoBody)
	assert.Equal(t, "", extractPortalToken(r))
}
