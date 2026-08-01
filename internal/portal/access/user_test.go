package access

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestContextWithUserRoundTrip pins the identity channel every portal handler
// reads: what ContextWithUser writes is exactly what GetUser returns, and a
// context that carries no user reads back nil rather than a zero User (a zero
// User would authorize as an anonymous principal with an empty id).
func TestContextWithUserRoundTrip(t *testing.T) {
	user := &User{UserID: "u1", Email: "alice@example.com", Roles: []string{"dp_admin"}, FromCookie: true}

	ctx := ContextWithUser(context.Background(), user)
	got := GetUser(ctx)
	require.NotNil(t, got)
	assert.Equal(t, user, got)

	assert.Nil(t, GetUser(context.Background()), "a context with no user must read back nil")
}

// TestGetUserIgnoresAForeignKey guards the unexported key type: a value stored
// under a same-named string key is not the portal's user and must not be read
// as one.
func TestGetUserIgnoresAForeignKey(t *testing.T) {
	ctx := context.WithValue(context.Background(), "portal_user", &User{UserID: "impostor"}) //nolint:staticcheck,revive // deliberately a bare string key
	assert.Nil(t, GetUser(ctx))
}
