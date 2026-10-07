package gatewayhttp

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// stubAuthenticator returns a fixed UserInfo/error for identity tests.
type stubAuthenticator struct {
	info *middleware.UserInfo
	err  error
}

func (s stubAuthenticator) Authenticate(context.Context) (*middleware.UserInfo, error) {
	return s.info, s.err
}

func TestIdentityResolver(t *testing.T) {
	tests := []struct {
		name string
		auth middleware.Authenticator
		want string
	}{
		{
			name: "api key name",
			auth: stubAuthenticator{info: &middleware.UserInfo{UserID: "apikey:nifi-etl", AuthType: middleware.AuthTypeAPIKey}},
			want: "nifi-etl",
		},
		{
			// A person is one value, not one series each (#1892): the
			// address never reaches a label.
			name: "oidc caller is the one oidc value",
			auth: stubAuthenticator{info: &middleware.UserInfo{UserID: "sub-123", Email: "jo@example.com", AuthType: middleware.AuthTypeOIDC}},
			want: "oidc",
		},
		{
			name: "oidc caller without email is the same value",
			auth: stubAuthenticator{info: &middleware.UserInfo{UserID: "sub-123", AuthType: middleware.AuthTypeOIDC}},
			want: "oidc",
		},
		{
			name: "oauth caller is oidc too",
			auth: stubAuthenticator{info: &middleware.UserInfo{UserID: "sub-9", AuthType: "oauth"}},
			want: "oidc",
		},
		{
			name: "auth error yields unknown",
			auth: stubAuthenticator{err: errors.New("bad token")},
			want: "unknown",
		},
		{
			name: "nil info yields unknown",
			auth: stubAuthenticator{},
			want: "unknown",
		},
		{
			name: "apikey without the prefix is not a key name",
			auth: stubAuthenticator{info: &middleware.UserInfo{UserID: "weird", AuthType: middleware.AuthTypeAPIKey}},
			want: "oidc",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewIdentityResolver(tc.auth).ResolveIdentity(context.Background()); got != tc.want {
				t.Errorf("ResolveIdentity() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIdentityResolver_NilAuthenticator(t *testing.T) {
	if got := NewIdentityResolver(nil).ResolveIdentity(context.Background()); got != "unknown" {
		t.Errorf("ResolveIdentity() with nil authn = %q, want unknown", got)
	}
}
