package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// TestScriptPortalIdentity covers the identity the portal script routes read
// their two entitlements from: the persona a listing is scoped to, and the
// admin membership that makes the surface unrestricted.
func TestScriptPortalIdentity(t *testing.T) {
	resolver := scriptPortalIdentity([]string{"admin"}, func(roles []string) *portal.PersonaInfo {
		if rolesIntersect(roles, []string{"analyst_role"}) {
			return &portal.PersonaInfo{Name: "analyst"}
		}
		return nil
	})

	// No user in context: nil identity, which the handlers answer as a 401.
	assert.Nil(t, resolver(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", http.NoBody)))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", http.NoBody)
	req = req.WithContext(portal.ContextWithUser(req.Context(), &portal.User{
		UserID: "u1", Email: "sarah@example.com", Roles: []string{"analyst_role"},
	}))
	id := resolver(req)
	require.NotNil(t, id)
	assert.Equal(t, "u1", id.UserID)
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
}

// A deployment with no persona registry resolves an identity with no persona
// rather than panicking inside the closure, which is what the nil guard in the
// mount is for.
func TestScriptPortalIdentity_NoPersonaResolver(t *testing.T) {
	resolver := scriptPortalIdentity(nil, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", http.NoBody)
	req = req.WithContext(portal.ContextWithUser(req.Context(), &portal.User{Email: "sarah@example.com"}))
	id := resolver(req)
	require.NotNil(t, id)
	assert.Empty(t, id.Persona)
	assert.False(t, id.IsAdmin)
}
