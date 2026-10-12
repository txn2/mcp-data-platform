package httpauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/selfcall"
	"github.com/txn2/mcp-data-platform/pkg/admin"
	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// nobody is an authenticator that admits no token.
type nobody struct{}

func (nobody) Authenticate(context.Context) (*middleware.UserInfo, error) { return nil, nil } //nolint:nilnil // nobody

// TestRequirePersona_ARequestServedInProcess is #2086 at the admin API: a
// request the platform-admin connection serves in process is taken as the
// caller it carries, admitted when that caller's roles reach the admin
// persona and refused 403 naming the persona and its roles when not. The
// mock authenticator is never asked: the request has no token.
func TestRequirePersona_ARequestServedInProcess(t *testing.T) {
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{Name: "admin", Roles: []string{"admin"}}))
	require.NoError(t, reg.Register(&persona.Persona{Name: "analyst", Roles: []string{"analyst"}}))
	var served *admin.User
	next := admin.NewPlatformAuthenticator(nobody{}, "admin", reg)
	h := admin.RequirePersona(AdminSelfCalls(next, reg, "admin"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = admin.GetUser(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	serve := func(roles ...string) *httptest.ResponseRecorder {
		served = nil
		ctx := selfcall.With(context.Background(), mcpcontext.Caller{UserID: "script:s", Email: "owner@example.com", Roles: roles})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody))
		return rec
	}

	rec := serve("admin")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	require.NotNil(t, served)
	assert.Equal(t, admin.User{UserID: "script:s", Email: "owner@example.com", Roles: []string{"admin"}}, *served)

	rec = serve("analyst")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Nil(t, served)
	assert.Contains(t, rec.Body.String(), `the admin API requires the persona \"admin\", which the roles [admin] reach; script:s presents [analyst]`)
}

// TestAdminSelfCalls_OtherRequestsAreTheWrappedAuthenticators: a request not
// served in process is answered by the authenticator it wraps.
func TestAdminSelfCalls_OtherRequestsAreTheWrappedAuthenticators(t *testing.T) {
	reg := persona.NewRegistry()
	u, err := AdminSelfCalls(admin.NewPlatformAuthenticator(nobody{}, "admin", reg), reg, "admin").
		Authenticate(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody))
	assert.NoError(t, err)
	assert.Nil(t, u)
}
