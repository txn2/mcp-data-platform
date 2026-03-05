package browsersession

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testKey() []byte {
	return []byte("test-key-that-is-at-least-32-bytes-long!!")
}

func TestSignAndVerifySession(t *testing.T) {
	cfg := &CookieConfig{Key: testKey(), TTL: time.Hour}
	claims := SessionClaims{
		UserID: "user-123",
		Email:  "test@example.com",
		Roles:  []string{"admin", "analyst"},
	}

	token, err := SignSession(claims, cfg)
	if err != nil {
		t.Fatalf("SignSession: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	got, err := VerifySession(token, cfg.Key)
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}

	if got.UserID != claims.UserID {
		t.Errorf("UserID = %q, want %q", got.UserID, claims.UserID)
	}
	if got.Email != claims.Email {
		t.Errorf("Email = %q, want %q", got.Email, claims.Email)
	}
	if len(got.Roles) != len(claims.Roles) {
		t.Errorf("Roles length = %d, want %d", len(got.Roles), len(claims.Roles))
	}
	for i, r := range got.Roles {
		if r != claims.Roles[i] {
			t.Errorf("Roles[%d] = %q, want %q", i, r, claims.Roles[i])
		}
	}
}

func TestSignSessionShortKey(t *testing.T) {
	cfg := &CookieConfig{Key: []byte("short")}
	_, err := SignSession(SessionClaims{UserID: "u"}, cfg)
	if err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestVerifySessionShortKey(t *testing.T) {
	_, err := VerifySession("anything", []byte("short"))
	if err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestVerifySessionExpired(t *testing.T) {
	cfg := &CookieConfig{
		Key: testKey(),
		TTL: -time.Hour, // already expired
	}
	token, err := SignSession(SessionClaims{UserID: "u"}, cfg)
	if err != nil {
		t.Fatalf("SignSession: %v", err)
	}

	_, err = VerifySession(token, cfg.Key)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestVerifySessionTampered(t *testing.T) {
	cfg := &CookieConfig{Key: testKey(), TTL: time.Hour}
	token, err := SignSession(SessionClaims{UserID: "u"}, cfg)
	if err != nil {
		t.Fatalf("SignSession: %v", err)
	}

	// Tamper with the token
	tampered := token + "x"
	_, err = VerifySession(tampered, cfg.Key)
	if err == nil {
		t.Fatal("expected error for tampered token")
	}
}

func TestVerifySessionWrongKey(t *testing.T) {
	cfg := &CookieConfig{Key: testKey(), TTL: time.Hour}
	token, err := SignSession(SessionClaims{UserID: "u"}, cfg)
	if err != nil {
		t.Fatalf("SignSession: %v", err)
	}

	wrongKey := []byte("wrong-key-that-is-at-least-32-bytes-long!!")
	_, err = VerifySession(token, wrongKey)
	if err == nil {
		t.Fatal("expected error for wrong key")
	}
}

func TestVerifySessionMissingSub(t *testing.T) {
	// Create a token without sub claim by signing manually
	cfg := &CookieConfig{Key: testKey(), TTL: time.Hour}
	// Sign with empty UserID
	token, err := SignSession(SessionClaims{UserID: ""}, cfg)
	if err != nil {
		t.Fatalf("SignSession: %v", err)
	}

	_, err = VerifySession(token, cfg.Key)
	if err == nil {
		t.Fatal("expected error for missing sub")
	}
}

func TestSetAndClearCookie(t *testing.T) {
	cfg := &CookieConfig{
		Name:   "test_session",
		Domain: "example.com",
		Path:   "/app",
		Secure: true,
		Key:    testKey(),
		TTL:    2 * time.Hour,
	}

	w := httptest.NewRecorder()
	SetCookie(w, cfg, "jwt-token-value")

	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}

	c := cookies[0]
	if c.Name != "test_session" {
		t.Errorf("Name = %q, want %q", c.Name, "test_session")
	}
	if c.Value != "jwt-token-value" {
		t.Errorf("Value = %q, want %q", c.Value, "jwt-token-value")
	}
	if c.Domain != "example.com" {
		t.Errorf("Domain = %q, want %q", c.Domain, "example.com")
	}
	if c.Path != "/app" {
		t.Errorf("Path = %q, want %q", c.Path, "/app")
	}
	if !c.HttpOnly {
		t.Error("expected HttpOnly")
	}
	if !c.Secure {
		t.Error("expected Secure")
	}
	if c.MaxAge != 7200 {
		t.Errorf("MaxAge = %d, want 7200", c.MaxAge)
	}

	// Clear cookie
	w2 := httptest.NewRecorder()
	ClearCookie(w2, cfg)
	cleared := w2.Result().Cookies()
	if len(cleared) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cleared))
	}
	if cleared[0].MaxAge != -1 {
		t.Errorf("MaxAge = %d, want -1", cleared[0].MaxAge)
	}
}

func TestSetCookieDefaults(t *testing.T) {
	cfg := &CookieConfig{Key: testKey()}

	w := httptest.NewRecorder()
	SetCookie(w, cfg, "val")

	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}

	c := cookies[0]
	if c.Name != DefaultCookieName {
		t.Errorf("Name = %q, want %q", c.Name, DefaultCookieName)
	}
	if c.Path != DefaultCookiePath {
		t.Errorf("Path = %q, want %q", c.Path, DefaultCookiePath)
	}
}

func TestParseFromRequest(t *testing.T) {
	cfg := &CookieConfig{Key: testKey(), TTL: time.Hour}
	claims := SessionClaims{UserID: "u1", Email: "a@b.com", Roles: []string{"r1"}}

	token, err := SignSession(claims, cfg)
	if err != nil {
		t.Fatalf("SignSession: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: token})

	got, err := ParseFromRequest(req, cfg)
	if err != nil {
		t.Fatalf("ParseFromRequest: %v", err)
	}
	if got.UserID != "u1" {
		t.Errorf("UserID = %q, want %q", got.UserID, "u1")
	}
}

func TestParseFromRequestNoCookie(t *testing.T) {
	cfg := &CookieConfig{Key: testKey()}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)

	_, err := ParseFromRequest(req, cfg)
	if err == nil {
		t.Fatal("expected error for missing cookie")
	}
}

func TestVerifySessionInvalidJWT(t *testing.T) {
	_, err := VerifySession("not.a.jwt", testKey())
	if err == nil {
		t.Fatal("expected error for invalid JWT")
	}
}

func TestCookieConfigDefaults(t *testing.T) {
	cfg := &CookieConfig{}

	if cfg.effectiveName() != DefaultCookieName {
		t.Errorf("effectiveName = %q, want %q", cfg.effectiveName(), DefaultCookieName)
	}
	if cfg.effectivePath() != DefaultCookiePath {
		t.Errorf("effectivePath = %q, want %q", cfg.effectivePath(), DefaultCookiePath)
	}
	if cfg.effectiveTTL() != DefaultTTL {
		t.Errorf("effectiveTTL = %v, want %v", cfg.effectiveTTL(), DefaultTTL)
	}
	if cfg.effectiveSameSite() != http.SameSiteLaxMode {
		t.Errorf("effectiveSameSite = %v, want %v", cfg.effectiveSameSite(), http.SameSiteLaxMode)
	}
}

func TestCookieConfigCustomValues(t *testing.T) {
	cfg := &CookieConfig{
		Name:     "custom",
		Path:     "/custom",
		TTL:      4 * time.Hour,
		SameSite: http.SameSiteStrictMode,
	}

	if cfg.effectiveName() != "custom" {
		t.Errorf("effectiveName = %q, want %q", cfg.effectiveName(), "custom")
	}
	if cfg.effectivePath() != "/custom" {
		t.Errorf("effectivePath = %q, want %q", cfg.effectivePath(), "/custom")
	}
	if cfg.effectiveTTL() != 4*time.Hour {
		t.Errorf("effectiveTTL = %v, want %v", cfg.effectiveTTL(), 4*time.Hour)
	}
	if cfg.effectiveSameSite() != http.SameSiteStrictMode {
		t.Errorf("effectiveSameSite = %v, want %v", cfg.effectiveSameSite(), http.SameSiteStrictMode)
	}
}

func TestSignSessionNilRoles(t *testing.T) {
	cfg := &CookieConfig{Key: testKey(), TTL: time.Hour}
	claims := SessionClaims{UserID: "u", Roles: nil}

	token, err := SignSession(claims, cfg)
	if err != nil {
		t.Fatalf("SignSession: %v", err)
	}

	got, err := VerifySession(token, cfg.Key)
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if got.Roles != nil {
		t.Errorf("Roles = %v, want nil", got.Roles)
	}
}
