package browsersession

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
)

// CSRFHeaderName is the request header that carries the CSRF token on
// state-changing requests authenticated by a session cookie. Because it is a
// custom header, a cross-origin page cannot set it on a form/navigation
// submit, and the browser blocks scripted cross-origin reads of the token,
// so only same-origin callers that legitimately fetched the token can send a
// valid value.
const CSRFHeaderName = "X-CSRF-Token" //nolint:gosec // header name, not a credential

// csrfTokenPrefix domain-separates the CSRF HMAC from the session-signing
// HMAC so the two can never produce interchangeable values despite sharing
// the same signing key.
const csrfTokenPrefix = "csrf:v1:"

// ErrCSRFInvalid is returned when a cookie-authenticated, state-changing
// request is missing a valid X-CSRF-Token header.
var ErrCSRFInvalid = errors.New("missing or invalid CSRF token")

// IssueCSRFToken derives a stateless CSRF token bound to the session subject.
//
// The token is an HMAC-SHA256 of the subject under the same signing key that
// protects the session cookie, so it needs no server-side storage and is
// verified by recomputation. It is safe to hand to the browser (e.g. in the
// /me response) because an attacker on another origin can neither read the
// same-origin response body (blocked by the browser) nor forge the HMAC
// without the signing key.
func (a *Authenticator) IssueCSRFToken(subject string) string {
	return computeCSRFToken(a.cfg.Key, subject)
}

// ValidateCSRFRequest enforces CSRF protection for a cookie-authenticated
// request. Safe (read-only) methods always pass. State-changing methods must
// carry an X-CSRF-Token header whose value matches the token bound to
// subject; otherwise ErrCSRFInvalid is returned.
//
// Token-authenticated (API-key / Bearer) requests do not reach this check —
// they are not vulnerable to CSRF because the credential is not attached
// automatically by the browser.
func (a *Authenticator) ValidateCSRFRequest(r *http.Request, subject string) error {
	if isSafeMethod(r.Method) {
		return nil
	}
	provided := r.Header.Get(CSRFHeaderName)
	if provided == "" {
		return ErrCSRFInvalid
	}
	expected := computeCSRFToken(a.cfg.Key, subject)
	if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		return ErrCSRFInvalid
	}
	return nil
}

// IsCrossSiteCookieMode reports whether the effective SameSite setting permits
// cross-site transmission of the session cookie (SameSite=None). In that mode
// the browser's built-in CSRF defense is disabled and the X-CSRF-Token layer
// becomes the sole protection, so callers should log a startup warning.
func (c *CookieConfig) IsCrossSiteCookieMode() bool {
	return c.effectiveSameSite() == http.SameSiteNoneMode
}

// computeCSRFToken returns base64url(HMAC-SHA256(key, prefix+subject)).
func computeCSRFToken(key []byte, subject string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(csrfTokenPrefix + subject))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// isSafeMethod reports whether an HTTP method is read-only per RFC 7231 and
// therefore exempt from CSRF enforcement.
func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}
