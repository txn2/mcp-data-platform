package tokengrant

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/txn2/mcp-data-platform/internal/outbound"
)

func TestIsRefusal(t *testing.T) {
	for code, want := range map[string]bool{
		// invalid_scope is the connection's oauth_scope, which the operator
		// fixes, and what Google answers a misspelled scope URL with (#2061).
		"invalid_grant": true, "invalid_client": true, "unauthorized_client": true, "invalid_scope": true,
		"invalid_request": false, "server_error": false, "": false,
	} {
		assert.Equal(t, want, IsRefusal(code), code)
	}
}

func TestDescription(t *testing.T) {
	assert.Equal(t, "a b c", Description("  a\tb\r\n\x00c  "))
	long := strings.Repeat("é", MaxDescriptionRunes+20)
	assert.Equal(t, strings.Repeat("é", MaxDescriptionRunes)+"...", Description(long))
	assert.Empty(t, Description(""))
}

func TestWithoutSecrets(t *testing.T) {
	assert.Equal(t, "a [REDACTED] b [REDACTED]", WithoutSecrets("a pw b sec", "pw", "", "sec"))
}

// TestScrub walks the error shapes the oauth2 library hands back; each
// branch keeps a credential out of the message.
func TestScrub(t *testing.T) {
	err := Scrub(&oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusForbidden}, Body: []byte(`{"secret":"leaked"}`)})
	assert.Equal(t, "oauth2 token fetch failed: status=403", err.Error())

	inner := errors.New("dial tcp: connection refused")
	err = Scrub(&url.Error{Op: "Post", URL: "https://user:s3cret@idp.example/token?client_secret=also", Err: inner})
	assert.Contains(t, err.Error(), "https://idp.example/token")
	assert.NotContains(t, err.Error(), "s3cret")
	assert.NotContains(t, err.Error(), "also")
	assert.ErrorIs(t, err, inner)

	err = Scrub(&url.Error{Op: "Post", URL: "://not a url", Err: errors.New("boom")})
	assert.Contains(t, err.Error(), "oauth2 token fetch Post")
	assert.NotContains(t, err.Error(), "not a url")

	assert.Equal(t, "oauth2 token fetch failed (details redacted)", Scrub(errors.New("to https://u:p@idp/token")).Error())
	assert.Equal(t, "oauth2 token fetch failed: context deadline exceeded", Scrub(errors.New("context deadline exceeded")).Error())
}

func TestNewClient(t *testing.T) {
	c := NewClient("crm", "not pem")
	chain, ok := outbound.Wraps(c.Transport)
	require.True(t, ok, "the token client is on the outbound chain")
	assert.Equal(t, outbound.KindOAuth, chain.Kind)
	assert.Equal(t, FetchTimeout, c.Timeout)
}

// idp answers the password and refresh_token grants by respond, recording
// each form.
type idp struct {
	mu      sync.Mutex
	forms   []url.Values
	auth    []string
	respond func(n int, form url.Values) (int, map[string]any)
}

func (p *idp) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		p.mu.Lock()
		n := len(p.forms)
		p.forms = append(p.forms, r.PostForm)
		p.auth = append(p.auth, r.Header.Get("Authorization"))
		p.mu.Unlock()
		status, body := p.respond(n, r.PostForm)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (p *idp) grants() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.forms))
	for _, f := range p.forms {
		out = append(out, f.Get("grant_type"))
	}
	return out
}

func token(access, refresh string, extra map[string]any) map[string]any {
	body := map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 1}
	if refresh != "" {
		body["refresh_token"] = refresh
	}
	maps.Copy(body, extra)
	return body
}

func asIs(_ context.Context, v string) (string, error) { return v, nil }

func source(t *testing.T, endpoint string, mod func(*Password)) *PasswordSource {
	t.Helper()
	p := Password{TokenURL: endpoint, ClientID: "cid", ClientSecret: "csec", Username: "u", Password: "p", Scopes: []string{"read"}, Fill: asIs}
	if mod != nil {
		mod(&p)
	}
	return NewPasswordSource(context.Background(), p)
}

func TestPasswordSource_RenewsWithTheRefreshTokenThenThePassword(t *testing.T) {
	up := &idp{respond: func(n int, f url.Values) (int, map[string]any) {
		switch {
		case f.Get("grant_type") == "password":
			return http.StatusOK, token("at-pw", "rt-1", map[string]any{"scope": "read write"})
		case n == 1:
			return http.StatusOK, token("at-refreshed", "", nil)
		default:
			return http.StatusBadRequest, map[string]any{"error": "invalid_grant"}
		}
	}}
	s := source(t, up.serve(t), nil)
	_, _, ok := s.Granted()
	assert.False(t, ok)
	for _, want := range []string{"at-pw", "at-refreshed", "at-pw"} {
		tok, err := s.Token()
		require.NoError(t, err)
		assert.Equal(t, want, tok.AccessToken)
		assert.Empty(t, tok.RefreshToken, "the cache above never holds the refresh token")
	}
	assert.Equal(t, []string{"password", "refresh_token", "refresh_token", "password"}, up.grants())
	assert.Equal(t, "rt-1", up.forms[2].Get("refresh_token"), "a refresh with no new refresh token keeps the old one")
	user, scopes, ok := s.Granted()
	assert.True(t, ok)
	assert.Equal(t, "u", user)
	assert.Equal(t, []string{"read", "write"}, scopes)
}

func TestPasswordSource_AFailedRefreshThatIsNotARefusalKeepsTheRefreshToken(t *testing.T) {
	up := &idp{respond: func(_ int, f url.Values) (int, map[string]any) {
		if f.Get("grant_type") == "password" {
			return http.StatusOK, token("at", "rt", nil)
		}
		return http.StatusServiceUnavailable, map[string]any{"error": "temporarily_unavailable"}
	}}
	s := source(t, up.serve(t), nil)
	_, err := s.Token()
	require.NoError(t, err)
	_, err = s.Token()
	require.Error(t, err)
	_, err = s.Token()
	require.Error(t, err)
	assert.Equal(t, []string{"password", "refresh_token", "refresh_token"}, up.grants())
}

func TestPasswordSource_ClientPlacement(t *testing.T) {
	up := &idp{respond: func(int, url.Values) (int, map[string]any) { return http.StatusOK, token("at", "", nil) }}
	endpoint := up.serve(t)
	for _, tc := range []struct {
		name   string
		mod    func(*Password)
		header bool
	}{
		{"header", nil, true},
		{"params", func(p *Password) { p.InParams = true }, false},
		{"no secret", func(p *Password) { p.ClientSecret = "" }, false},
	} {
		up.forms, up.auth = nil, nil
		_, err := source(t, endpoint, tc.mod).Token()
		require.NoError(t, err, tc.name)
		assert.Equal(t, tc.header, up.auth[0] != "", tc.name)
		assert.Equal(t, !tc.header, up.forms[0].Get("client_id") == "cid", tc.name)
	}
}

func TestPasswordSource_AFillFailureSendsNothing(t *testing.T) {
	up := &idp{respond: func(int, url.Values) (int, map[string]any) { return http.StatusOK, token("at", "", nil) }}
	endpoint := up.serve(t)
	for _, field := range []string{"client_id", "client_secret", "username", "password"} {
		fail := func(_ context.Context, v string) (string, error) {
			if v == "{{secret:"+field+"}}" {
				return "", errors.New("no secret " + field)
			}
			return v, nil
		}
		s := source(t, endpoint, func(p *Password) {
			p.Fill = fail
			switch field {
			case "client_id":
				p.ClientID = "{{secret:client_id}}"
			case "client_secret":
				p.ClientSecret = "{{secret:client_secret}}"
			case "username":
				p.Username = "{{secret:username}}"
			default:
				p.Password = "{{secret:password}}"
			}
		})
		_, err := s.Token()
		require.ErrorContains(t, err, "no secret "+field)
	}
	assert.Empty(t, up.grants())
}

func TestPasswordSource_RedactsWhatItSent(t *testing.T) {
	up := &idp{respond: func(int, url.Values) (int, map[string]any) { return http.StatusOK, token("at", "", nil) }}
	s := source(t, up.serve(t), func(p *Password) { p.Password, p.ClientSecret = "pw-sent", "sec-sent" })
	assert.Equal(t, "x pw-sent", s.Redact("x pw-sent"), "nothing sent yet")
	_, err := s.Token()
	require.NoError(t, err)
	assert.Equal(t, "x [REDACTED] [REDACTED]", s.Redact("x pw-sent sec-sent"))
}
