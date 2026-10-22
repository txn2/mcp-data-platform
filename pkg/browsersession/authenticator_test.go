package browsersession

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthenticatorValidCookie(t *testing.T) {
	cfg := CookieConfig{Key: testKey(), TTL: time.Hour}
	claims := SessionClaims{UserID: "u1", Email: "u@b.com", Roles: []string{"admin"}}

	token, err := SignSession(claims, &cfg)
	if err != nil {
		t.Fatalf("SignSession: %v", err)
	}

	auth := NewAuthenticator(cfg)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: token, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})

	info, err := auth.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("AuthenticateHTTP: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil UserInfo")
	}
	if info.UserID != "u1" {
		t.Errorf("UserID = %q, want %q", info.UserID, "u1")
	}
	if info.Email != "u@b.com" {
		t.Errorf("Email = %q, want %q", info.Email, "u@b.com")
	}
	if info.AuthType != "browser_session" {
		t.Errorf("AuthType = %q, want %q", info.AuthType, "browser_session")
	}
	if len(info.Roles) != 1 || info.Roles[0] != "admin" {
		t.Errorf("Roles = %v, want [admin]", info.Roles)
	}
}

func TestAuthenticatorNoCookie(t *testing.T) {
	cfg := CookieConfig{Key: testKey()}
	auth := NewAuthenticator(cfg)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)

	info, err := auth.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Error("expected nil for no cookie")
	}
}

func TestAuthenticatorExpiredCookie(t *testing.T) {
	cfg := CookieConfig{Key: testKey(), TTL: -time.Hour}
	token, _ := SignSession(SessionClaims{UserID: "u"}, &cfg)

	auth := NewAuthenticator(CookieConfig{Key: testKey()})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: token, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})

	info, err := auth.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Error("expected nil for expired cookie")
	}
}

func TestAuthenticatorMalformedCookie(t *testing.T) {
	auth := NewAuthenticator(CookieConfig{Key: testKey()})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: "not-a-jwt", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})

	info, err := auth.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Error("expected nil for malformed cookie")
	}
}

func TestAuthenticatorWrongKey(t *testing.T) {
	cfg := CookieConfig{Key: testKey(), TTL: time.Hour}
	token, _ := SignSession(SessionClaims{UserID: "u"}, &cfg)

	wrongKey := []byte("different-key-at-least-32-bytes-long!!!")
	auth := NewAuthenticator(CookieConfig{Key: wrongKey})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: token, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})

	info, err := auth.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Error("expected nil for wrong key")
	}
}

func TestExtractIDTokenValidCookie(t *testing.T) {
	cfg := CookieConfig{Key: testKey(), TTL: time.Hour}
	claims := SessionClaims{
		UserID:  "u1",
		IDToken: "eyJhbGciOiJSUzI1NiJ9.test-id-token",
	}
	token, err := SignSession(claims, &cfg)
	if err != nil {
		t.Fatalf("SignSession: %v", err)
	}

	auth := NewAuthenticator(cfg)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: token, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})

	idToken := auth.ExtractIDToken(req)
	if idToken != claims.IDToken {
		t.Errorf("ExtractIDToken = %q, want %q", idToken, claims.IDToken)
	}
}

func TestExtractIDTokenNoCookie(t *testing.T) {
	auth := NewAuthenticator(CookieConfig{Key: testKey()})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)

	idToken := auth.ExtractIDToken(req)
	if idToken != "" {
		t.Errorf("ExtractIDToken = %q, want empty", idToken)
	}
}

func TestExtractIDTokenNoIDToken(t *testing.T) {
	cfg := CookieConfig{Key: testKey(), TTL: time.Hour}
	claims := SessionClaims{UserID: "u1"}
	token, err := SignSession(claims, &cfg)
	if err != nil {
		t.Fatalf("SignSession: %v", err)
	}

	auth := NewAuthenticator(cfg)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: token, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})

	idToken := auth.ExtractIDToken(req)
	if idToken != "" {
		t.Errorf("ExtractIDToken = %q, want empty for session without id_token", idToken)
	}
}

func TestExtractIDTokenExpiredCookie(t *testing.T) {
	cfg := CookieConfig{Key: testKey(), TTL: -time.Hour}
	token, _ := SignSession(SessionClaims{UserID: "u", IDToken: "some-token"}, &cfg)

	auth := NewAuthenticator(CookieConfig{Key: testKey()})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: token, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})

	idToken := auth.ExtractIDToken(req)
	if idToken != "" {
		t.Errorf("ExtractIDToken = %q, want empty for expired cookie", idToken)
	}
}

func TestAuthenticatorCustomCookieName(t *testing.T) {
	cfg := CookieConfig{Name: "custom_session", Key: testKey(), TTL: time.Hour}
	token, _ := SignSession(SessionClaims{UserID: "u"}, &cfg)

	auth := NewAuthenticator(cfg)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.AddCookie(&http.Cookie{Name: "custom_session", Value: token, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})

	info, err := auth.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("AuthenticateHTTP: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil UserInfo")
	}
	if info.UserID != "u" {
		t.Errorf("UserID = %q, want %q", info.UserID, "u")
	}
}

// A session judge refuses a validly signed cookie and replaces the roles it
// carries (#2092).
func TestAuthenticateHTTP_AdmitJudgesTheSession(t *testing.T) {
	cfg := CookieConfig{Name: "s", Key: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}
	signed, err := SignSession(SessionClaims{UserID: "ext:a1", Email: "x@example.com", Roles: []string{"old"}}, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	req := func() *http.Request {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		r.AddCookie(&http.Cookie{Name: "s", Value: signed, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
		return r
	}
	a := NewAuthenticator(cfg)
	if info, _ := a.AuthenticateHTTP(req()); info == nil || info.Roles[0] != "old" {
		t.Fatalf("without a judge: %+v", info)
	}
	a.SetAdmit(func(_ context.Context, _ string, _ []string) ([]string, bool) { return []string{"now"}, true })
	if info, _ := a.AuthenticateHTTP(req()); info == nil || info.Roles[0] != "now" {
		t.Fatalf("admitted: %+v, want the judge's roles", info)
	}
	a.SetAdmit(func(context.Context, string, []string) ([]string, bool) { return nil, false })
	if info, err := a.AuthenticateHTTP(req()); info != nil || err != nil {
		t.Fatalf("refused: %+v %v, want unauthenticated", info, err)
	}
}
