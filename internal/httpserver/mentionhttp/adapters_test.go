package mentionhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal"
)

func TestIdentityResolver(t *testing.T) {
	resolver := IdentityResolver(func(roles []string) bool { return slices.Contains(roles, "admin") })

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
	assert.False(t, id.IsAdmin)

	req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", http.NoBody)
	req = req.WithContext(portal.ContextWithUser(req.Context(), &portal.User{
		Email: "root@example.com", Roles: []string{"admin"},
	}))
	elevated := resolver(req)
	require.NotNil(t, elevated)
	assert.True(t, elevated.IsAdmin, "an admin may read any target's audience")
}

// Without a database there is no audience to resolve: mentions stay text.
func TestAudience_NoDatabase(t *testing.T) {
	assert.Nil(t, Audience(nil))
}
