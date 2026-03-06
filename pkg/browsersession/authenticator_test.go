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
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: token})

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
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: token})

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
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: "not-a-jwt"})

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
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: token})

	info, err := auth.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Error("expected nil for wrong key")
	}
}

func TestAuthenticatorCustomCookieName(t *testing.T) {
	cfg := CookieConfig{Name: "custom_session", Key: testKey(), TTL: time.Hour}
	token, _ := SignSession(SessionClaims{UserID: "u"}, &cfg)

	auth := NewAuthenticator(cfg)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.AddCookie(&http.Cookie{Name: "custom_session", Value: token})

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
