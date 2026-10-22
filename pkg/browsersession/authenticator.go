package browsersession

import (
	"context"
	"net/http"
	"sync/atomic"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// Authenticator checks for a valid session cookie and returns user info.
// It is designed to be used as the first step in an HTTP auth chain —
// if no cookie is present or the cookie is invalid, it returns nil
// (allowing fallback to token-based authentication).
type Authenticator struct {
	cfg   CookieConfig
	admit atomic.Pointer[AdmitFunc]
}

// AdmitFunc judges a valid cookie's session on every request: it returns the
// roles the request carries and whether to admit it. The platform's external
// accounts use it (#2092), so a suspended, removed or expired account is
// refused on its next request although its cookie is still validly signed.
type AdmitFunc func(ctx context.Context, userID string, roles []string) ([]string, bool)

// SetAdmit installs the session judge. Without one every validly signed
// cookie is admitted with the roles it carries.
func (a *Authenticator) SetAdmit(fn AdmitFunc) {
	a.admit.Store(&fn)
}

// NewAuthenticator creates a cookie-based authenticator.
func NewAuthenticator(cfg CookieConfig) *Authenticator {
	return &Authenticator{cfg: cfg}
}

// ExtractIDToken extracts the OIDC id_token from the session cookie.
// Returns empty string if no valid session exists or no id_token is stored.
func (a *Authenticator) ExtractIDToken(r *http.Request) string {
	claims, _ := ParseFromRequest(r, &a.cfg)
	if claims == nil {
		return ""
	}
	return claims.IDToken
}

// AuthenticateHTTP checks the HTTP request for a valid session cookie.
// Returns nil, nil when no valid cookie is found (no error, just unauthenticated).
func (a *Authenticator) AuthenticateHTTP(r *http.Request) (*middleware.UserInfo, error) {
	claims, _ := ParseFromRequest(r, &a.cfg)
	if claims == nil {
		return nil, nil //nolint:nilnil // no valid cookie → unauthenticated, not an error
	}
	roles := claims.Roles
	if fn := a.admit.Load(); fn != nil && *fn != nil {
		admitted, ok := (*fn)(r.Context(), claims.UserID, claims.Roles)
		if !ok {
			return nil, nil //nolint:nilnil // a refused session is unauthenticated, not an error
		}
		roles = admitted
	}

	return &middleware.UserInfo{
		UserID:   claims.UserID,
		Email:    claims.Email,
		Roles:    roles,
		AuthType: "browser_session",
	}, nil
}
