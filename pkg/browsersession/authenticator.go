package browsersession

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// Authenticator checks for a valid session cookie and returns user info.
// It is designed to be used as the first step in an HTTP auth chain —
// if no cookie is present or the cookie is invalid, it returns nil
// (allowing fallback to token-based authentication).
type Authenticator struct {
	cfg CookieConfig
}

// NewAuthenticator creates a cookie-based authenticator.
func NewAuthenticator(cfg CookieConfig) *Authenticator {
	return &Authenticator{cfg: cfg}
}

// AuthenticateHTTP checks the HTTP request for a valid session cookie.
// Returns nil, nil when no valid cookie is found (no error, just unauthenticated).
func (a *Authenticator) AuthenticateHTTP(r *http.Request) (*middleware.UserInfo, error) {
	claims, err := ParseFromRequest(r, &a.cfg)
	if err != nil {
		return nil, nil //nolint:nilnil // no valid cookie → unauthenticated, not an error
	}

	return &middleware.UserInfo{
		UserID:   claims.UserID,
		Email:    claims.Email,
		Roles:    claims.Roles,
		AuthType: "browser_session",
	}, nil
}
