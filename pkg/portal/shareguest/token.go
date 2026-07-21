package shareguest

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// otkBytes is the entropy of a one-time link token: 256 bits, matching the
// strength of the SHA-256 digest the store keeps.
const otkBytes = 32

// guestCookieName carries the guest session JWT. It is distinct from the
// browser-session cookie, so the portal's authenticator never sees it, and it
// is scoped to the public viewer path so it is never even sent to portal or
// API routes.
const guestCookieName = "mcp_share_guest"

// guestCookiePath scopes the guest cookie to the public viewer.
const guestCookiePath = "/portal/view/"

// guestTokenType is the value of the "typ" claim in a guest session JWT. Its
// presence is asserted on verify as defense in depth alongside the derived
// signing key.
const guestTokenType = "share-guest"

// mintOTK generates a one-time link token and the hash the store keeps. The
// plaintext token exists only in the emailed URL; the database never sees it.
func mintOTK() (token, hash string, err error) {
	b := make([]byte, otkBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generating one-time link token: %w", err)
	}
	token = hex.EncodeToString(b)
	return token, hashOTK(token), nil
}

// hashOTK returns the hex SHA-256 of a one-time link token.
func hashOTK(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Guest identifies an admitted guest viewer.
type Guest struct {
	// Email is the recipient address the share named when the guest session
	// was opened. Display-only; it grants nothing beyond the session itself.
	Email string
}

// signGuestSession mints the guest session JWT for a claimed link: the share
// it is scoped to, the recipient it was issued for, and an expiry capped at
// GuestSessionTTL.
func (s *Service) signGuestSession(shareID, email string) (string, error) {
	now := s.now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"typ": guestTokenType,
		"sid": shareID,
		"sub": email,
		"iat": now.Unix(),
		"exp": now.Add(GuestSessionTTL).Unix(),
	})
	signed, err := token.SignedString(s.guestKey)
	if err != nil {
		return "", fmt.Errorf("signing guest session: %w", err)
	}
	return signed, nil
}

// verifyGuestSession validates a guest session JWT and returns its share id
// and recipient email.
func (s *Service) verifyGuestSession(tokenString string) (shareID, email string, err error) {
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.guestKey, nil
	})
	if err != nil {
		return "", "", fmt.Errorf("parsing guest session: %w", err)
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return "", "", errors.New("invalid guest session")
	}
	if typ, _ := claims["typ"].(string); typ != guestTokenType {
		return "", "", errors.New("not a guest session token")
	}
	shareID, _ = claims["sid"].(string)
	if shareID == "" {
		return "", "", errors.New("guest session names no share")
	}
	email, _ = claims["sub"].(string)
	return shareID, email, nil
}

// setGuestCookie writes the guest session as a browser-session cookie (no
// Max-Age): the signed expiry caps its life at GuestSessionTTL, and closing
// the browser ends it sooner. SameSite=Lax lets the emailed link's top-level
// navigation carry the cookie it just set while blocking cross-site subresource
// use.
func (s *Service) setGuestCookie(w http.ResponseWriter, signed string) {
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure mirrors the browser-session cookie's cfg-driven setting
		Name:     guestCookieName,
		Value:    signed,
		Path:     guestCookiePath,
		HttpOnly: true,
		Secure:   s.secureCookie,
		SameSite: http.SameSiteLaxMode,
	})
}

// Admit returns the guest behind the request when it carries a valid guest
// session for exactly the given share, and nil otherwise. Nil-safe. The
// caller checks share availability first: a revoked or expired share refuses
// its guests immediately, without waiting for the cookie to age out.
func (s *Service) Admit(r *http.Request, shareID string) *Guest {
	if s == nil || len(s.guestKey) == 0 || shareID == "" {
		return nil
	}
	cookie, err := r.Cookie(guestCookieName)
	if err != nil {
		return nil
	}
	sid, email, err := s.verifyGuestSession(cookie.Value)
	if err != nil || sid != shareID {
		return nil
	}
	return &Guest{Email: email}
}

// claimWindow returns the current time and the start of the per-share cap
// window ending at it.
func (s *Service) claimWindow() (now, since time.Time) {
	now = s.now()
	return now, now.Add(-linkCapWindow)
}
