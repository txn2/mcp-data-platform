package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/admin"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

func TestRolesIntersect(t *testing.T) {
	assert.True(t, rolesIntersect([]string{"analyst", "admin"}, []string{"admin"}))
	assert.False(t, rolesIntersect([]string{"analyst"}, []string{"admin"}))
	assert.False(t, rolesIntersect(nil, []string{"admin"}))
	assert.False(t, rolesIntersect([]string{"analyst"}, nil))
}

// stubAdminAuth authenticates every request as one fixed admin user, driving
// the real admin middleware so adminEmail reads the real context key.
type stubAdminAuth struct{ user *admin.User }

func (a *stubAdminAuth) Authenticate(*http.Request) (*admin.User, error) {
	return a.user, nil
}

func TestAdminEmail(t *testing.T) {
	// Without an authenticated admin in context, the email is empty.
	assert.Empty(t, adminEmail(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", http.NoBody)))

	// Through the real RequirePersona middleware the email resolves.
	var got string
	wrap := admin.RequirePersona(&stubAdminAuth{user: &admin.User{Email: "root@example.com"}})
	h := wrap(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = adminEmail(r)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", http.NoBody))
	assert.Equal(t, "root@example.com", got)
}

func TestPortalIdentityResolver(t *testing.T) {
	resolver := portalIdentityResolver([]string{"admin"}, func(roles []string) *portal.PersonaInfo {
		if rolesIntersect(roles, []string{"analyst_role"}) {
			return &portal.PersonaInfo{Name: "analyst"}
		}
		return nil
	})

	// No user in context: nil identity.
	assert.Nil(t, resolver(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", http.NoBody)))

	// A portal user resolves email, persona, and admin membership.
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", http.NoBody)
	req = req.WithContext(portal.ContextWithUser(req.Context(), &portal.User{
		Email: "sarah@example.com", Roles: []string{"analyst_role"},
	}))
	id := resolver(req)
	require.NotNil(t, id)
	assert.Equal(t, "sarah@example.com", id.Email)
	assert.Equal(t, "analyst", id.Persona)
	assert.False(t, id.IsAdmin)

	req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", http.NoBody)
	req = req.WithContext(portal.ContextWithUser(req.Context(), &portal.User{
		Email: "root@example.com", Roles: []string{"admin"},
	}))
	id = resolver(req)
	require.NotNil(t, id)
	assert.True(t, id.IsAdmin)
	assert.Empty(t, id.Persona)
}
