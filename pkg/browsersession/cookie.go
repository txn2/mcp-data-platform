package browsersession

import (
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// minKeyLength is the minimum HMAC key length in bytes (256 bits).
const minKeyLength = 32

// SignSession creates a signed JWT string from session claims.
func SignSession(claims SessionClaims, cfg *CookieConfig) (string, error) {
	if len(cfg.Key) < minKeyLength {
		return "", fmt.Errorf("signing key must be at least %d bytes", minKeyLength)
	}

	now := time.Now()
	ttl := cfg.effectiveTTL()

	mc := jwt.MapClaims{
		"sub":   claims.UserID,
		"email": claims.Email,
		"roles": claims.Roles,
		"iat":   now.Unix(),
		"exp":   now.Add(ttl).Unix(),
	}
	if claims.IDToken != "" {
		mc["idt"] = claims.IDToken
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, mc)

	signed, err := token.SignedString(cfg.Key)
	if err != nil {
		return "", fmt.Errorf("signing session token: %w", err)
	}

	return signed, nil
}

// VerifySession validates a signed JWT and returns the session claims.
func VerifySession(tokenString string, key []byte) (*SessionClaims, error) {
	if len(key) < minKeyLength {
		return nil, fmt.Errorf("signing key must be at least %d bytes", minKeyLength)
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return key, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parsing session token: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid session token")
	}

	mapClaims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("unexpected claims type")
	}

	return extractSessionClaims(mapClaims)
}

// extractSessionClaims pulls SessionClaims from jwt.MapClaims.
func extractSessionClaims(mc jwt.MapClaims) (*SessionClaims, error) {
	sub, _ := mc["sub"].(string)
	if sub == "" {
		return nil, fmt.Errorf("missing sub claim")
	}

	email, _ := mc["email"].(string)
	idToken, _ := mc["idt"].(string)

	var roles []string
	if rawRoles, ok := mc["roles"].([]any); ok {
		for _, r := range rawRoles {
			if s, ok := r.(string); ok {
				roles = append(roles, s)
			}
		}
	}

	return &SessionClaims{
		UserID:  sub,
		Email:   email,
		Roles:   roles,
		IDToken: idToken,
	}, nil
}

// SetCookie writes the session JWT as an HTTP-only cookie.
func SetCookie(w http.ResponseWriter, cfg *CookieConfig, tokenString string) {
	// Secure is cfg-driven (defaults true, opt-out for local dev without TLS).
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure is cfg-driven (defaults true, opt-out for local dev without TLS)
		Name:     cfg.effectiveName(),
		Value:    tokenString,
		Domain:   cfg.Domain,
		Path:     cfg.effectivePath(),
		MaxAge:   int(cfg.effectiveTTL().Seconds()),
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: cfg.effectiveSameSite(),
	})
}

// ClearCookie removes the session cookie.
func ClearCookie(w http.ResponseWriter, cfg *CookieConfig) {
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure is cfg-driven (defaults true, opt-out for local dev without TLS)
		Name:     cfg.effectiveName(),
		Value:    "",
		Domain:   cfg.Domain,
		Path:     cfg.effectivePath(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: cfg.effectiveSameSite(),
	})
}

// ParseFromRequest reads the session cookie from the request and verifies it.
func ParseFromRequest(r *http.Request, cfg *CookieConfig) (*SessionClaims, error) {
	cookie, err := r.Cookie(cfg.effectiveName())
	if err != nil {
		return nil, fmt.Errorf("reading session cookie: %w", err)
	}

	return VerifySession(cookie.Value, cfg.Key)
}
