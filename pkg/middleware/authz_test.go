package middleware

import (
	"context"
	"testing"
)

// mockAuthorizer implements Authorizer for testing.
type mockAuthorizer struct {
	isAuthorizedFunc func(ctx context.Context, userID string, roles []string, toolName string) (bool, string)
}

func (m *mockAuthorizer) IsAuthorized(ctx context.Context, userID string, roles []string, toolName string) (bool, string) {
	if m.isAuthorizedFunc != nil {
		return m.isAuthorizedFunc(ctx, userID, roles, toolName)
	}
	return true, ""
}

func TestNoopAuthorizer(t *testing.T) {
	authz := &NoopAuthorizer{}
	authorized, reason := authz.IsAuthorized(context.Background(), "user", []string{"role"}, "tool")
	if !authorized {
		t.Error("expected authorized to be true")
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %q", reason)
	}
}

func TestAllowAllAuthorizer(t *testing.T) {
	authz := AllowAllAuthorizer()
	authorized, reason := authz.IsAuthorized(context.Background(), "anyuser", []string{"anyrole"}, "anytool")
	if !authorized {
		t.Error("expected authorized to be true")
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %q", reason)
	}
}

// Verify interface compliance.
var _ Authorizer = (*NoopAuthorizer)(nil)
var _ Authorizer = (*mockAuthorizer)(nil)
