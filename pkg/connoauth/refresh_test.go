package connoauth

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

// TestRefresh_BasicAuth_SendsRawSecret is the regression test for
// the URL-encoding bug that caused refresh failures against
// IdPs whose client_secret contains characters URL-encoding
// rewrites (`+`, `/`, `=` mid-string, space, ...).
//
// Pre-fix, golang.org/x/oauth2's refresh path called
// req.SetBasicAuth(url.QueryEscape(id), url.QueryEscape(secret))
// per RFC 6749 §2.3.1 letter. Real production IdPs reject
// URL-encoded credentials in Basic auth and return invalid_client.
// Our exchange code uses plain SetBasicAuth, so the initial OAuth
// flow succeeds; the first refresh on the just-issued token fails.
//
// This test asserts the Authorization header carries the RAW
// secret value (decoded from base64), not the URL-encoded version.
func TestRefresh_BasicAuth_SendsRawSecret(t *testing.T) {
	t.Parallel()
	// A client_secret containing characters that URL-encoding
	// rewrites. Real production case: `+8r5xs3YLFC/LHK...`.
	const rawSecret = "+8r5xs3YLFC/LHK="
	const clientID = "test-client"

	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at","token_type":"Bearer","expires_in":3600}`))
	}))
	t.Cleanup(srv.Close)

	_, err := Refresh(context.Background(), RefreshInput{
		Config: Config{
			TokenURL:          srv.URL,
			ClientID:          clientID,
			ClientSecret:      rawSecret,
			EndpointAuthStyle: oauth2.AuthStyleInHeader,
		},
		RefreshToken: "rt-test",
	})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(clientID+":"+rawSecret))
	if captured != want {
		t.Errorf("Authorization header carried URL-encoded credentials.\n got: %s\nwant: %s", captured, want)
		decoded, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(captured, "Basic "))
		t.Errorf("decoded got:  %s", string(decoded))
		t.Errorf("decoded want: %s:%s", clientID, rawSecret)
	}
}

// TestRefresh_AuthStyleInParams_SecretInBody confirms the body
// path (when the connection config selects AuthStyleInParams)
// also delivers the raw secret. url.Values.Encode is standard
// form encoding (which the IdP URL-decodes server-side back to
// the raw value), so the round-trip preserves `+` / `/`. Without
// this test, a future change could break the body path silently.
func TestRefresh_AuthStyleInParams_SecretInBody(t *testing.T) {
	t.Parallel()
	const rawSecret = "+8r5xs3YLFC/LHK="
	const clientID = "test-client"

	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// G120: cap the request body before ParseForm. Tests
		// control the input so the cap is a formality, but the
		// gosec rule fires on the call shape, not the actual
		// risk surface.
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		_ = r.ParseForm()
		body = r.PostFormValue("client_secret")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at","token_type":"Bearer","expires_in":3600}`))
	}))
	t.Cleanup(srv.Close)

	_, err := Refresh(context.Background(), RefreshInput{
		Config: Config{
			TokenURL:          srv.URL,
			ClientID:          clientID,
			ClientSecret:      rawSecret,
			EndpointAuthStyle: oauth2.AuthStyleInParams,
		},
		RefreshToken: "rt-test",
	})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if body != rawSecret {
		t.Errorf("body client_secret = %q; want %q", body, rawSecret)
	}
}

// TestRefresh_IdPError_Returns_RetrieveError proves a non-200
// response is translated into an *oauth2.RetrieveError that the
// downstream classifyRefreshError pipeline can inspect. Without
// this, an IdP error from the new refresh path would skip the
// classify/sanitize pipeline entirely.
func TestRefresh_IdPError_Returns_RetrieveError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Token expired"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := Refresh(context.Background(), RefreshInput{
		Config: Config{
			TokenURL:          srv.URL,
			ClientID:          "c",
			ClientSecret:      "s",
			EndpointAuthStyle: oauth2.AuthStyleInHeader,
		},
		RefreshToken: "rt-test",
	})
	if err == nil {
		t.Fatal("expected error on 400 response")
	}
	if code := idpErrorCodeOf(err); code != "invalid_grant" {
		t.Errorf("idpErrorCodeOf = %q; want %q", code, "invalid_grant")
	}
}

// TestRefresh_PersistsRotatedToken proves a rotated refresh_token
// in the response is returned in the RefreshResult so the caller
// can persist it. Backstops the long-standing rotation-persistence
// fix.
func TestRefresh_PersistsRotatedToken(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-fresh","refresh_token":"rt-rotated","token_type":"Bearer","expires_in":3600,"refresh_expires_in":7200}`))
	}))
	t.Cleanup(srv.Close)

	res, err := Refresh(context.Background(), RefreshInput{
		Config: Config{
			TokenURL: srv.URL, ClientID: "c", ClientSecret: "s",
		},
		RefreshToken: "rt-original",
	})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if res.RefreshToken != "rt-rotated" {
		t.Errorf("RefreshToken = %q; want %q", res.RefreshToken, "rt-rotated")
	}
	if res.RefreshExpiresAt.IsZero() {
		t.Errorf("RefreshExpiresAt should be non-zero when refresh_expires_in is present")
	}
}

// TestRefresh_ValidateInput rejects malformed input before any
// network call.
func TestRefresh_ValidateInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   RefreshInput
	}{
		{"empty token_url", RefreshInput{Config: Config{ClientID: "c"}, RefreshToken: "rt"}},
		{"empty client_id", RefreshInput{Config: Config{TokenURL: "https://x/token"}, RefreshToken: "rt"}},
		{"empty refresh_token", RefreshInput{Config: Config{TokenURL: "https://x/token", ClientID: "c"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Refresh(context.Background(), tc.in)
			if err == nil {
				t.Errorf("expected validation error for %s", tc.name)
			}
		})
	}
}
