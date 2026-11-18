package upstreamauth

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/connstate"
	"github.com/txn2/mcp-data-platform/internal/secretref"
)

// passwordIDP is a token endpoint that speaks the password and refresh_token
// grants the way a legacy upstream does, recording every request it gets.
type passwordIDP struct {
	t        *testing.T
	mu       sync.Mutex
	requests []url.Values
	auth     []string
	// respond answers request n (0-based) with a status and a JSON body.
	respond func(n int, form url.Values) (int, map[string]any)
}

func (p *passwordIDP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	require.NoError(p.t, r.ParseForm())
	p.mu.Lock()
	n := len(p.requests)
	p.requests = append(p.requests, r.PostForm)
	p.auth = append(p.auth, r.Header.Get(AuthorizationHeader))
	p.mu.Unlock()
	status, body := p.respond(n, r.PostForm)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (p *passwordIDP) grants() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.requests))
	for _, f := range p.requests {
		out = append(out, f.Get("grant_type"))
	}
	return out
}

func (p *passwordIDP) form(n int) url.Values {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests[n]
}

// startPasswordIDP serves respond and returns the endpoint and its record.
func startPasswordIDP(t *testing.T, respond func(n int, form url.Values) (int, map[string]any)) (idp *passwordIDP, endpoint string) {
	t.Helper()
	idp = &passwordIDP{t: t, respond: respond}
	srv := httptest.NewServer(idp)
	t.Cleanup(srv.Close)
	return idp, srv.URL
}

// issued is a token response. expires_in 1 is inside the oauth2 library's
// ten-second expiry margin, so the next Apply renews: no test waits.
func issued(access, refresh string, expiresIn int) map[string]any {
	body := map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": expiresIn}
	if refresh != "" {
		body["refresh_token"] = refresh
	}
	return body
}

func passwordConfig(t *testing.T, tokenURL string, extra map[string]any) Config {
	t.Helper()
	cfg := map[string]any{
		"auth_mode":           "oauth",
		"oauth_grant":         "password",
		"oauth_token_url":     tokenURL,
		"oauth_client_id":     "legacy-client",
		"oauth_client_secret": "client-s3cret",
		"oauth_username":      "api-user",
		"oauth_password":      "pw-Hunter2!",
		"oauth_scope":         "read write",
	}
	maps.Copy(cfg, extra)
	return parsed(t, cfg)
}

func applyBearer(t *testing.T, a Authenticator) (string, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://h.example.com/x", http.NoBody)
	require.NoError(t, err)
	if err := a.Apply(req); err != nil {
		return "", fmt.Errorf("apply: %w", err)
	}
	return req.Header.Get(AuthorizationHeader), nil
}

func TestPasswordGrant_RequestCarriesTheUserAndTheClient(t *testing.T) {
	idp, tokenURL := startPasswordIDP(t, func(int, url.Values) (int, map[string]any) {
		return http.StatusOK, issued("at-1", "rt-1", 3600)
	})
	a, err := NewAuthenticator(passwordConfig(t, tokenURL, nil))
	require.NoError(t, err)
	assert.Empty(t, GrantDetail(a), "nothing is issued before the first request")

	for range 2 {
		bearer, err := applyBearer(t, a)
		require.NoError(t, err)
		assert.Equal(t, "Bearer at-1", bearer)
	}
	require.Equal(t, []string{"password"}, idp.grants(), "a token inside its lifetime is reused")
	assert.Equal(t, "the token endpoint issued a token for api-user with scopes read write; ", GrantDetail(a))
	form := idp.form(0)
	assert.Equal(t, "api-user", form.Get("username"))
	assert.Equal(t, "pw-Hunter2!", form.Get("password"))
	assert.Equal(t, "read write", form.Get("scope"))
	assert.Empty(t, form.Get("client_secret"), "the client secret travels in the header by default")
	user, pass, ok := (&http.Request{Header: http.Header{AuthorizationHeader: {idp.auth[0]}}}).BasicAuth()
	require.True(t, ok)
	assert.Equal(t, "legacy-client", user)
	assert.Equal(t, "client-s3cret", pass)
}

func TestPasswordGrant_RenewsWithTheRefreshToken(t *testing.T) {
	idp, tokenURL := startPasswordIDP(t, func(_ int, form url.Values) (int, map[string]any) {
		switch form.Get("grant_type") {
		case "password":
			return http.StatusOK, issued("at-pw", "rt-1", 1)
		case "refresh_token":
			if form.Get("refresh_token") != "rt-1" {
				return http.StatusBadRequest, map[string]any{"error": "invalid_grant"}
			}
			// No refresh token in the answer: the one sent stays valid.
			return http.StatusOK, issued("at-refreshed", "", 1)
		}
		return http.StatusBadRequest, map[string]any{"error": "unsupported_grant_type"}
	})
	a, err := NewAuthenticator(passwordConfig(t, tokenURL, nil))
	require.NoError(t, err)

	want := []string{"Bearer at-pw", "Bearer at-refreshed", "Bearer at-refreshed"}
	for _, w := range want {
		bearer, err := applyBearer(t, a)
		require.NoError(t, err)
		assert.Equal(t, w, bearer)
	}
	assert.Equal(t, []string{"password", "refresh_token", "refresh_token"}, idp.grants())
}

func TestPasswordGrant_RefusedRefreshFallsBackToThePassword(t *testing.T) {
	idp, tokenURL := startPasswordIDP(t, func(n int, form url.Values) (int, map[string]any) {
		if form.Get("grant_type") == "refresh_token" {
			return http.StatusBadRequest, map[string]any{"error": "invalid_grant", "error_description": "Token is not active"}
		}
		return http.StatusOK, issued("at-"+form.Get("grant_type")+strconv.Itoa(n), "rt", 1)
	})
	a, err := NewAuthenticator(passwordConfig(t, tokenURL, nil))
	require.NoError(t, err)

	_, err = applyBearer(t, a)
	require.NoError(t, err)
	bearer, err := applyBearer(t, a)
	require.NoError(t, err)
	assert.Equal(t, "Bearer at-password2", bearer)
	assert.Equal(t, []string{"password", "refresh_token", "password"}, idp.grants())
}

func TestPasswordGrant_NoRefreshTokenAsksWithThePasswordAgain(t *testing.T) {
	idp, tokenURL := startPasswordIDP(t, func(int, url.Values) (int, map[string]any) {
		return http.StatusOK, issued("at", "", 1)
	})
	a, err := NewAuthenticator(passwordConfig(t, tokenURL, nil))
	require.NoError(t, err)
	for range 2 {
		_, err = applyBearer(t, a)
		require.NoError(t, err)
	}
	assert.Equal(t, []string{"password", "password"}, idp.grants())
}

func TestPasswordGrant_UnreachableRefreshKeepsTheRefreshToken(t *testing.T) {
	idp, tokenURL := startPasswordIDP(t, func(n int, form url.Values) (int, map[string]any) {
		if form.Get("grant_type") == "password" {
			return http.StatusOK, issued("at", "rt-1", 1)
		}
		if n == 1 {
			return http.StatusServiceUnavailable, map[string]any{"error": "temporarily_unavailable"}
		}
		return http.StatusOK, issued("at-2", "", 1)
	})
	a, err := NewAuthenticator(passwordConfig(t, tokenURL, nil))
	require.NoError(t, err)
	_, err = applyBearer(t, a)
	require.NoError(t, err)
	_, err = applyBearer(t, a)
	require.Error(t, err, "a 503 on the refresh is not a refusal")
	bearer, err := applyBearer(t, a)
	require.NoError(t, err)
	assert.Equal(t, "Bearer at-2", bearer)
	assert.Equal(t, []string{"password", "refresh_token", "refresh_token"}, idp.grants())
	assert.Equal(t, "rt-1", idp.form(2).Get("refresh_token"))
}

func TestPasswordGrant_WrongPasswordIsAuthFailedWithTheUpstreamReason(t *testing.T) {
	connstate.Forget("api", "conn")
	t.Cleanup(func() { connstate.Forget("api", "conn") })
	_, tokenURL := startPasswordIDP(t, func(int, url.Values) (int, map[string]any) {
		// A server that repeats the submitted password in its description.
		return http.StatusUnauthorized, map[string]any{
			"error":             "invalid_grant",
			"error_description": "Invalid user credentials for pw-Hunter2!",
		}
	})
	a, err := NewAuthenticator(passwordConfig(t, tokenURL, nil))
	require.NoError(t, err)

	_, err = applyBearer(t, a)
	require.ErrorIs(t, err, ErrPasswordRefused)
	assert.Contains(t, err.Error(), "invalid_grant: Invalid user credentials for [REDACTED]")
	assert.NotContains(t, err.Error(), "pw-Hunter2!")
	assert.NotContains(t, err.Error(), "client-s3cret")
	state, ok := connstate.State("api", "conn")
	require.True(t, ok)
	assert.Equal(t, connstate.AuthFailed, state)
}

func TestPasswordGrant_RefusalWithNoDescription(t *testing.T) {
	_, tokenURL := startPasswordIDP(t, func(int, url.Values) (int, map[string]any) {
		return http.StatusBadRequest, map[string]any{"error": "unauthorized_client"}
	})
	a, err := NewAuthenticator(passwordConfig(t, tokenURL, nil))
	require.NoError(t, err)
	_, err = applyBearer(t, a)
	require.ErrorIs(t, err, ErrPasswordRefused)
	assert.True(t, strings.HasSuffix(err.Error(), ": unauthorized_client"), err.Error())
}

func TestPasswordGrant_OtherFailuresAreScrubbed(t *testing.T) {
	_, tokenURL := startPasswordIDP(t, func(int, url.Values) (int, map[string]any) {
		return http.StatusInternalServerError, map[string]any{"error": "server_error", "error_description": "pw-Hunter2!"}
	})
	a, err := NewAuthenticator(passwordConfig(t, tokenURL, nil))
	require.NoError(t, err)
	_, err = applyBearer(t, a)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrPasswordRefused)
	assert.Contains(t, err.Error(), "status=500")
	assert.NotContains(t, err.Error(), "pw-Hunter2!")
}

func TestPasswordGrant_NoClientSecretSendsTheClientIDAsAParameter(t *testing.T) {
	idp, tokenURL := startPasswordIDP(t, func(int, url.Values) (int, map[string]any) {
		return http.StatusOK, issued("at", "", 3600)
	})
	c := passwordConfig(t, tokenURL, map[string]any{"oauth_client_secret": ""})
	a, err := NewAuthenticator(c)
	require.NoError(t, err)
	_, err = applyBearer(t, a)
	require.NoError(t, err)
	assert.Empty(t, idp.auth[0])
	assert.Equal(t, "legacy-client", idp.form(0).Get("client_id"))
}

func TestPasswordGrant_FieldsNamingStoredSecretsAreReadPerRequest(t *testing.T) {
	store := installSecrets(t, map[string]string{"user": "svc-user", "pw": "pass-one"})
	idp, tokenURL := startPasswordIDP(t, func(int, url.Values) (int, map[string]any) {
		return http.StatusOK, issued("at", "", 1)
	})
	c := passwordConfig(t, tokenURL, map[string]any{"oauth_username": "{{secret:user}}", "oauth_password": "{{secret:pw}}"})
	a, err := NewAuthenticator(c)
	require.NoError(t, err)
	_, err = applyBearer(t, a)
	require.NoError(t, err)
	store.set("pw", "pass-two")
	_, err = applyBearer(t, a)
	require.NoError(t, err)
	assert.Equal(t, "svc-user", idp.form(0).Get("username"))
	assert.Equal(t, "pass-one", idp.form(0).Get("password"))
	assert.Equal(t, "pass-two", idp.form(1).Get("password"))
	assert.False(t, secretref.HasPlaceholder(idp.form(1).Get("password")))
}

func TestPasswordGrant_MissingStoredSecretFailsTheRequest(t *testing.T) {
	installSecrets(t, map[string]string{})
	idp, tokenURL := startPasswordIDP(t, func(int, url.Values) (int, map[string]any) {
		return http.StatusOK, issued("at", "", 1)
	})
	for _, key := range []string{"oauth_username", "oauth_password", "oauth_client_id", "oauth_client_secret"} {
		a, err := NewAuthenticator(passwordConfig(t, tokenURL, map[string]any{key: "{{secret:absent}}"}))
		require.NoError(t, err)
		_, err = applyBearer(t, a)
		require.Error(t, err, key)
		assert.Contains(t, err.Error(), "absent", key)
	}
	assert.Empty(t, idp.grants(), "nothing is sent without the secret")
}

func TestPasswordGrant_Validation(t *testing.T) {
	base := map[string]any{
		"auth_mode": "oauth", "oauth_grant": "password", "oauth_token_url": "https://idp.example.com/token",
		"oauth_username": "u", "oauth_password": "p",
	}
	cases := []struct {
		name string
		drop string
		set  map[string]any
		want string
	}{
		{name: "token url", drop: "oauth_token_url", want: "oauth_token_url is required"},
		{name: "username", drop: "oauth_username", want: "oauth_username is required"},
		{name: "password", drop: "oauth_password", want: "oauth_password is required"},
		{name: "secret without id", set: map[string]any{"oauth_client_secret": "s"}, want: "oauth_client_id is required"},
		{name: "auth style", set: map[string]any{"oauth_endpoint_auth_style": "body"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]any{}
			for k, v := range base {
				if k != tc.drop {
					cfg[k] = v
				}
			}
			maps.Copy(cfg, tc.set)
			c, err := Parse("api", "apigateway", "https://h.example.com", cfg)
			if err == nil {
				err = c.Validate()
			}
			require.Error(t, err)
			if tc.want != "" {
				assert.Contains(t, err.Error(), tc.want)
				assert.Contains(t, err.Error(), `oauth_grant is "password"`)
			}
			_, nerr := newPasswordAuth(c)
			require.Error(t, nerr)
		})
	}
	ok := parsed(t, base)
	assert.Equal(t, "u", ok.OAuth2.Username)
	assert.Equal(t, "p", ok.OAuth2.Password)
}

// TestPasswordGrant_AnEchoedStoredSecretIsRedacted: a password that names a
// stored secret is redacted from the upstream's refusal as it was sent, not
// only as the placeholder.
func TestPasswordGrant_AnEchoedStoredSecretIsRedacted(t *testing.T) {
	installSecrets(t, map[string]string{"pw": "filled-Secret-9"})
	_, tokenURL := startPasswordIDP(t, func(_ int, form url.Values) (int, map[string]any) {
		return http.StatusUnauthorized, map[string]any{"error": "invalid_grant", "error_description": "bad password " + form.Get("password")}
	})
	a, err := NewAuthenticator(passwordConfig(t, tokenURL, map[string]any{"oauth_password": "{{secret:pw}}"}))
	require.NoError(t, err)
	_, err = applyBearer(t, a)
	require.ErrorIs(t, err, ErrPasswordRefused)
	assert.NotContains(t, err.Error(), "filled-Secret-9")
	assert.Contains(t, err.Error(), "bad password [REDACTED]")
}
