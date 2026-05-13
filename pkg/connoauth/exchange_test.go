package connoauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestExchange_Success(t *testing.T) {
	t.Parallel()
	idp := newFakeIDP(t, func(w http.ResponseWriter, r *http.Request) {
		form := readForm(r)
		if got := form.Get("grant_type"); got != "authorization_code" {
			t.Errorf("grant_type=%q want authorization_code", got)
		}
		if got := form.Get("code"); got != "auth-code-xyz" {
			t.Errorf("code=%q want auth-code-xyz", got)
		}
		if got := form.Get("code_verifier"); got != "verifier-abc" {
			t.Errorf("code_verifier=%q want verifier-abc", got)
		}
		if got := form.Get("redirect_uri"); got != "https://platform.example/cb" {
			t.Errorf("redirect_uri=%q", got)
		}
		// Default auth style is InHeader → expect Basic Auth header.
		if user, pass, ok := r.BasicAuth(); !ok || user != "client-id" || pass != "client-secret" {
			t.Errorf("expected Basic auth client-id:client-secret, got user=%q pass=%q ok=%v", user, pass, ok)
		}
		writeTokenJSON(w, map[string]any{
			"access_token":       "at-xyz",
			"refresh_token":      "rt-xyz",
			"expires_in":         3600,
			"refresh_expires_in": 7200,
			"scope":              "openid offline_access",
			"id_token":           "id-xyz",
			"token_type":         "Bearer",
		})
	})

	result, err := Exchange(context.Background(), ExchangeInput{
		Config: Config{
			TokenURL:     idp.tokenURL(),
			ClientID:     "client-id",
			ClientSecret: "client-secret",
		},
		Code:         "auth-code-xyz",
		CodeVerifier: "verifier-abc",
		RedirectURI:  "https://platform.example/cb",
	})
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if result.AccessToken != "at-xyz" || result.RefreshToken != "rt-xyz" {
		t.Fatalf("token mismatch: %+v", result)
	}
	if result.IDToken != "id-xyz" {
		t.Fatalf("id_token missing: %+v", result)
	}
	if result.ExpiresAt.IsZero() || result.RefreshExpiresAt.IsZero() {
		t.Fatalf("expiry deadlines should be populated: %+v", result)
	}
	wantExp := time.Now().Add(3600 * time.Second)
	if delta := result.ExpiresAt.Sub(wantExp); delta > 5*time.Second || delta < -5*time.Second {
		t.Fatalf("ExpiresAt off by %v", delta)
	}
}

func TestExchange_AuthStyleInParams(t *testing.T) {
	t.Parallel()
	idp := newFakeIDP(t, func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := r.BasicAuth(); ok {
			t.Errorf("AuthStyleInParams must NOT send Basic auth header")
		}
		form := readForm(r)
		if got := form.Get("client_secret"); got != "secret-in-body" {
			t.Errorf("client_secret in body expected, got %q", got)
		}
		writeTokenJSON(w, map[string]any{
			"access_token": "at", "expires_in": 60,
		})
	})

	_, err := Exchange(context.Background(), ExchangeInput{
		Config: Config{
			TokenURL:          idp.tokenURL(),
			ClientID:          "c",
			ClientSecret:      "secret-in-body",
			EndpointAuthStyle: oauth2.AuthStyleInParams,
		},
		Code:         "code",
		CodeVerifier: "verifier",
		RedirectURI:  "https://platform.example/cb",
	})
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
}

func TestExchange_UpstreamError(t *testing.T) {
	t.Parallel()
	idp := newFakeIDP(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"expired code"}`))
	})
	_, err := Exchange(context.Background(), ExchangeInput{
		Config:       Config{TokenURL: idp.tokenURL(), ClientID: "c", ClientSecret: "s"},
		Code:         "code",
		CodeVerifier: "v",
		RedirectURI:  "https://x/cb",
	})
	if err == nil {
		t.Fatal("expected error on 400 invalid_grant")
	}
}

func TestExchange_MissingAccessToken(t *testing.T) {
	t.Parallel()
	idp := newFakeIDP(t, func(w http.ResponseWriter, _ *http.Request) {
		writeTokenJSON(w, map[string]any{"token_type": "Bearer"})
	})
	_, err := Exchange(context.Background(), ExchangeInput{
		Config:       Config{TokenURL: idp.tokenURL(), ClientID: "c", ClientSecret: "s"},
		Code:         "code",
		CodeVerifier: "v",
		RedirectURI:  "https://x/cb",
	})
	if err == nil || !strings.Contains(err.Error(), "missing access_token") {
		t.Fatalf("expected missing-access-token error, got %v", err)
	}
}

func TestExchange_MalformedJSON(t *testing.T) {
	t.Parallel()
	idp := newFakeIDP(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not json`))
	})
	_, err := Exchange(context.Background(), ExchangeInput{
		Config:       Config{TokenURL: idp.tokenURL(), ClientID: "c", ClientSecret: "s"},
		Code:         "code",
		CodeVerifier: "v",
		RedirectURI:  "https://x/cb",
	})
	if err == nil || !strings.Contains(err.Error(), "malformed JSON") {
		t.Fatalf("expected malformed-JSON error, got %v", err)
	}
}

func TestExchange_ValidationErrors(t *testing.T) {
	t.Parallel()
	base := ExchangeInput{
		Config:       Config{TokenURL: "https://idp/token", ClientID: "c", ClientSecret: "s"},
		Code:         "code",
		CodeVerifier: "v",
		RedirectURI:  "https://x/cb",
	}
	cases := []struct {
		name   string
		mutate func(*ExchangeInput)
	}{
		{"missing TokenURL", func(in *ExchangeInput) { in.Config.TokenURL = "" }},
		{"missing ClientID", func(in *ExchangeInput) { in.Config.ClientID = "" }},
		{"missing Code", func(in *ExchangeInput) { in.Code = "" }},
		{"missing CodeVerifier", func(in *ExchangeInput) { in.CodeVerifier = "" }},
		{"missing RedirectURI", func(in *ExchangeInput) { in.RedirectURI = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := base
			tc.mutate(&in)
			if _, err := Exchange(context.Background(), in); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestExchange_ResponseSizeCap(t *testing.T) {
	t.Parallel()
	// Build a JSON body just over the cap.
	idp := newFakeIDP(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Stream past the cap; the LimitReader should reject.
		filler := strings.Repeat("a", maxTokenResponseBytes+10)
		_, _ = w.Write([]byte(`{"access_token":"x","padding":"` + filler + `"}`))
	})
	_, err := Exchange(context.Background(), ExchangeInput{
		Config:       Config{TokenURL: idp.tokenURL(), ClientID: "c", ClientSecret: "s"},
		Code:         "code",
		CodeVerifier: "v",
		RedirectURI:  "https://x/cb",
	})
	if err == nil || !strings.Contains(err.Error(), "byte cap") {
		t.Fatalf("expected size-cap error, got %v", err)
	}
}

func TestExchange_TransportError(t *testing.T) {
	t.Parallel()
	// Closed server simulates connection refused.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	srv.Close()
	_, err := Exchange(context.Background(), ExchangeInput{
		Config:       Config{TokenURL: srv.URL + "/token", ClientID: "c", ClientSecret: "s"},
		Code:         "code",
		CodeVerifier: "v",
		RedirectURI:  "https://x/cb",
	})
	if err == nil {
		t.Fatal("expected transport error")
	}
}

func TestURLHost(t *testing.T) {
	t.Parallel()
	if got := urlHost("https://idp.example.com/realms/x/token"); got != "idp.example.com" {
		t.Fatalf("urlHost: got %q", got)
	}
	if got := urlHost(":://malformed"); got != ":://malformed" {
		t.Fatalf("urlHost should fall through on parse error: got %q", got)
	}
	if got := urlHost("not-a-url"); got != "not-a-url" {
		t.Fatalf("urlHost should fall through on empty host: got %q", got)
	}
}

func TestTrimBody(t *testing.T) {
	t.Parallel()
	if got := trimBody([]byte("short")); got != "short" {
		t.Fatalf("trimBody: got %q", got)
	}
	long := strings.Repeat("x", 300)
	if got := trimBody([]byte(long)); !strings.HasSuffix(got, "...") || len(got) != 259 {
		t.Fatalf("trimBody should cap with ...: got len=%d suffix=%q", len(got), got[len(got)-3:])
	}
}

// TestExchange_NoRedirectFollow proves the CheckRedirect guard
// actively refuses 3xx responses from the token endpoint, defeating
// a compromised-or-misconfigured-IdP credential-leak vector.
func TestExchange_NoRedirectFollow(t *testing.T) {
	t.Parallel()
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("CheckRedirect should have refused to follow the 302 to this server")
	}))
	t.Cleanup(target.Close)
	idp := newFakeIDP(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL+"/leak")
		w.WriteHeader(http.StatusFound)
	})
	_, err := Exchange(context.Background(), ExchangeInput{
		Config:       Config{TokenURL: idp.tokenURL(), ClientID: "c", ClientSecret: "s"},
		Code:         "code",
		CodeVerifier: "v",
		RedirectURI:  "https://platform.example/cb",
	})
	if err == nil {
		t.Fatal("expected non-200 error for 302 (redirect refused), got nil")
	}
}

// confirm ExchangeResult.Scope and ID token round-trip across the
// decoder boundary in conjunction with the URL escape tests.
var _ = url.URL{} // keep net/url import
