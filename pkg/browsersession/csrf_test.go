package browsersession

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testAuthenticator() *Authenticator {
	// 32-byte key satisfies minKeyLength.
	return NewAuthenticator(CookieConfig{Key: []byte("0123456789abcdef0123456789abcdef")})
}

func TestIssueCSRFToken(t *testing.T) {
	a := testAuthenticator()

	// Deterministic: same subject → same token.
	tok1 := a.IssueCSRFToken("user-1")
	tok2 := a.IssueCSRFToken("user-1")
	if tok1 != tok2 {
		t.Fatalf("expected deterministic token, got %q and %q", tok1, tok2)
	}
	if tok1 == "" {
		t.Fatal("expected non-empty token")
	}

	// Bound to subject: different subject → different token.
	if other := a.IssueCSRFToken("user-2"); other == tok1 {
		t.Fatal("expected different subjects to produce different tokens")
	}

	// Bound to signing key: different key → different token for same subject.
	b := NewAuthenticator(CookieConfig{Key: []byte("ffffffffffffffffffffffffffffffff")})
	if bTok := b.IssueCSRFToken("user-1"); bTok == tok1 {
		t.Fatal("expected different keys to produce different tokens")
	}
}

func TestValidateCSRFRequest(t *testing.T) {
	a := testAuthenticator()
	const subject = "user-1"
	validToken := a.IssueCSRFToken(subject)

	tests := []struct {
		name    string
		method  string
		header  string
		subject string
		wantErr bool
	}{
		{name: "GET is exempt without token", method: http.MethodGet, header: "", subject: subject, wantErr: false},
		{name: "HEAD is exempt", method: http.MethodHead, header: "", subject: subject, wantErr: false},
		{name: "OPTIONS is exempt", method: http.MethodOptions, header: "", subject: subject, wantErr: false},
		{name: "POST with valid token passes", method: http.MethodPost, header: validToken, subject: subject, wantErr: false},
		{name: "PUT with valid token passes", method: http.MethodPut, header: validToken, subject: subject, wantErr: false},
		{name: "PATCH with valid token passes", method: http.MethodPatch, header: validToken, subject: subject, wantErr: false},
		{name: "DELETE with valid token passes", method: http.MethodDelete, header: validToken, subject: subject, wantErr: false},
		{name: "POST without header is rejected", method: http.MethodPost, header: "", subject: subject, wantErr: true},
		{name: "POST with wrong token is rejected", method: http.MethodPost, header: "not-the-token", subject: subject, wantErr: true},
		{name: "POST token for another subject is rejected", method: http.MethodPost, header: validToken, subject: "user-2", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/api/v1/portal/assets", http.NoBody)
			if tc.header != "" {
				r.Header.Set(CSRFHeaderName, tc.header)
			}
			err := a.ValidateCSRFRequest(r, tc.subject)
			if tc.wantErr {
				if !errors.Is(err, ErrCSRFInvalid) {
					t.Fatalf("expected ErrCSRFInvalid, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
		})
	}
}

func TestIsCrossSiteCookieMode(t *testing.T) {
	tests := []struct {
		name     string
		sameSite http.SameSite
		want     bool
	}{
		{name: "unset defaults to Lax", sameSite: 0, want: false},
		{name: "Lax", sameSite: http.SameSiteLaxMode, want: false},
		{name: "Strict", sameSite: http.SameSiteStrictMode, want: false},
		{name: "None is cross-site", sameSite: http.SameSiteNoneMode, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &CookieConfig{SameSite: tc.sameSite}
			if got := c.IsCrossSiteCookieMode(); got != tc.want {
				t.Fatalf("IsCrossSiteCookieMode() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseSameSite(t *testing.T) {
	tests := []struct {
		in   string
		want http.SameSite
	}{
		{"lax", http.SameSiteLaxMode},
		{"Lax", http.SameSiteLaxMode},
		{"strict", http.SameSiteStrictMode},
		{"STRICT", http.SameSiteStrictMode},
		{"none", http.SameSiteNoneMode},
		{" None ", http.SameSiteNoneMode},
		{"", 0},
		{"bogus", 0},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := ParseSameSite(tc.in); got != tc.want {
				t.Fatalf("ParseSameSite(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
	// Empty/unrecognized must resolve to Lax through effectiveSameSite.
	c := &CookieConfig{SameSite: ParseSameSite("")}
	if got := c.effectiveSameSite(); got != http.SameSiteLaxMode {
		t.Fatalf("effectiveSameSite() = %v, want Lax", got)
	}
}

func TestIsSafeMethod(t *testing.T) {
	safe := []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace}
	unsafe := []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodConnect}
	for _, m := range safe {
		if !isSafeMethod(m) {
			t.Errorf("expected %s to be safe", m)
		}
	}
	for _, m := range unsafe {
		if isSafeMethod(m) {
			t.Errorf("expected %s to be unsafe", m)
		}
	}
}
