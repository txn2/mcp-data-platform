package middleware

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

// TestWithCaller: the authenticated identity is mirrored for a toolkit to
// read, and the shared identity of an anonymous or unauthenticated
// deployment is not (#2086).
func TestWithCaller(t *testing.T) {
	pc := &PlatformContext{UserID: "script:s", UserEmail: "o@example.com", Roles: []string{"admin"}, AuthType: AuthTypeScript, UserClaims: map[string]any{"k": "v"}}
	c, ok := mcpcontext.GetCaller(withCaller(context.Background(), pc))
	if !ok || c.UserID != "script:s" || c.Email != "o@example.com" || c.Roles[0] != "admin" || c.AuthType != AuthTypeScript || c.Claims["k"] != "v" {
		t.Errorf("GetCaller = %+v, %v", c, ok)
	}
	for _, shared := range []string{AuthTypeAnonymous, AuthTypeNoop} {
		if _, ok := mcpcontext.GetCaller(withCaller(context.Background(), &PlatformContext{UserID: "anonymous", AuthType: shared})); ok {
			t.Errorf("%s identity was recorded as a caller", shared)
		}
	}
}
