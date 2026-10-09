package upstreamauth

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/txn2/mcp-data-platform/internal/secretref"
)

// storedSecrets is a secret store as a connection's configuration reads it
// (#2066): each secret allowed on the connections listed beside it. Values
// can be changed mid-test, which is what rotation is.
type storedSecrets struct {
	mu     sync.Mutex
	values map[string]string
	allow  map[string]string
}

func (s *storedSecrets) set(name, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[name] = value
}

func (s *storedSecrets) read(_ context.Context, name, connection string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[name]
	if !ok {
		return "", fmt.Errorf("secret %q does not exist", name)
	}
	if s.allow[name] != connection {
		return "", fmt.Errorf("secret %q may not be used by connection %q; it is allowed on %s", name, connection, s.allow[name])
	}
	return v, nil
}

// installSecrets installs a store answering for connection "conn". The
// source is process-wide, so these tests do not run in parallel.
func installSecrets(t *testing.T, values map[string]string) *storedSecrets {
	t.Helper()
	s := &storedSecrets{values: maps.Clone(values), allow: map[string]string{}}
	for name := range values {
		s.allow[name] = "conn"
	}
	prev := secretref.SetConnectionSource(s.read)
	t.Cleanup(func() { secretref.SetConnectionSource(prev) })
	return s
}

// parsed is a validated api connection named "conn".
func parsed(t *testing.T, cfg map[string]any) Config {
	t.Helper()
	c, err := Parse("api", "apigateway", "https://h.example.com", cfg)
	require.NoError(t, err)
	require.NoError(t, c.Validate())
	c.ConnectionName = "conn"
	return c
}

// applied is the request after the connection's authenticator ran.
func applied(ctx context.Context, t *testing.T, c Config) (*http.Request, error) {
	t.Helper()
	a, err := NewAuthenticator(c)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://h.example.com/x", http.NoBody)
	require.NoError(t, err)
	if err := a.Apply(req); err != nil {
		return req, fmt.Errorf("apply: %w", err)
	}
	return req, nil
}

func TestCredentialNamingAStoredSecretIsReadAtSend(t *testing.T) {
	store := installSecrets(t, map[string]string{"tok": "token-one", "user": "svc-user", "pw": "pass-one"})
	ctx, redactor := secretref.WithRedactor(t.Context())

	bearer := parsed(t, map[string]any{"auth_mode": "bearer", "credential": "{{secret:tok}}"})
	req, err := applied(ctx, t, bearer)
	require.NoError(t, err)
	assert.Equal(t, "Bearer token-one", req.Header.Get(AuthorizationHeader))
	// What the request carried is redacted from what comes back.
	assert.Equal(t, "echo "+secretref.Redaction("tok"), redactor.String("echo token-one"))

	// Rotation reaches the next request with nothing rebuilt.
	store.set("tok", "token-two")
	req, err = applied(ctx, t, bearer)
	require.NoError(t, err)
	assert.Equal(t, "Bearer token-two", req.Header.Get(AuthorizationHeader))

	header := parsed(t, map[string]any{"auth_mode": "api_key", "credential": "{{secret:tok}}", "api_key_header": "X-Key"})
	req, err = applied(ctx, t, header)
	require.NoError(t, err)
	assert.Equal(t, "token-two", req.Header.Get("X-Key"))

	query := parsed(t, map[string]any{"auth_mode": "api_key", "credential": "{{secret:tok}}", "api_key_placement": "query", "api_key_param": "key"})
	req, err = applied(ctx, t, query)
	require.NoError(t, err)
	assert.Equal(t, "token-two", req.URL.Query().Get("key"))

	basic := parsed(t, map[string]any{"auth_mode": "basic", "username": "{{secret:user}}", "password": "{{secret:pw}}"})
	req, err = applied(ctx, t, basic)
	require.NoError(t, err)
	assert.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte("svc-user:pass-one")), req.Header.Get(AuthorizationHeader))

	// Basic auth written in full keeps the header computed once.
	plain := parsed(t, map[string]any{"auth_mode": "basic", "username": "u", "password": "p"})
	req, err = applied(ctx, t, plain)
	require.NoError(t, err)
	assert.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte("u:p")), req.Header.Get(AuthorizationHeader))
}

func TestCredentialOutsideTheSecretsScopeIsRefused(t *testing.T) {
	store := installSecrets(t, map[string]string{"tok": "token-one", "user": "has:colon"})
	store.allow["tok"] = "elsewhere"

	bearer := parsed(t, map[string]any{"auth_mode": "bearer", "credential": "{{secret:tok}}"})
	_, err := applied(t.Context(), t, bearer)
	require.ErrorContains(t, err, `apigateway: secret "tok" may not be used by connection "conn"; it is allowed on elsewhere`)

	apiKey := parsed(t, map[string]any{"auth_mode": "api_key", "credential": "{{secret:missing}}"})
	_, err = applied(t.Context(), t, apiKey)
	require.ErrorContains(t, err, `secret "missing" does not exist`)

	// A username read from a secret is held to RFC 7617 when it is read.
	basic := parsed(t, map[string]any{"auth_mode": "basic", "username": "{{secret:user}}"})
	_, err = applied(t.Context(), t, basic)
	require.ErrorContains(t, err, `contain ":" in the username`)

	basicPW := parsed(t, map[string]any{"auth_mode": "basic", "username": "u", "password": "{{secret:missing}}"})
	_, err = applied(t.Context(), t, basicPW)
	require.ErrorContains(t, err, `secret "missing" does not exist`)
}

// The placeholder's own ':' is not a colon in the userid.
func TestBasicUsernamePlaceholderIsNotAColon(t *testing.T) {
	c, err := Parse("api", "apigateway", "https://h.example.com", map[string]any{"auth_mode": "basic", "username": "{{secret:user}}"})
	require.NoError(t, err)
	require.NoError(t, c.Validate())
	_, err = NewAuthenticator(c)
	require.NoError(t, err)

	c, err = Parse("api", "apigateway", "https://h.example.com", map[string]any{"auth_mode": "basic", "username": "a:{{secret:user}}"})
	require.NoError(t, err)
	require.ErrorContains(t, c.Validate(), "must not contain")
	_, err = NewAuthenticator(c)
	require.ErrorContains(t, err, "must not contain")
}

// echoServer answers with the request's headers, path and body, so a test
// reads what was sent.
func echoServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, _ = fmt.Fprintf(w, "path=%s x-sub=%s body=%s", r.URL.Path, r.Header.Get("X-Sub"), body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func get(ctx context.Context, t *testing.T, client *http.Client, target string) (string, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("X-Sub", "{{secret:sub}}")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b), nil
}

func TestStaticHeaderAndPathSecretAreReadAtSend(t *testing.T) {
	store := installSecrets(t, map[string]string{"sub": "sub-key-1", "hook": "hook-token-1"})
	srv := echoServer(t)
	c := parsed(t, map[string]any{
		"static_headers": map[string]any{"X-Sub": "{{secret:sub}}"},
		"path_secret":    "{{secret:hook}}",
	})
	client := NewHTTPClient(c)

	ctx, redactor := secretref.WithRedactor(t.Context())
	got, err := get(ctx, t, client, srv.URL+"/in")
	require.NoError(t, err)
	assert.Equal(t, "path=/in/hook-token-1 x-sub=sub-key-1 body=", got)
	assert.False(t, redactor.Empty(), "the values sent were recorded for redaction")

	store.set("hook", "hook-token-2")
	got, err = get(t.Context(), t, client, srv.URL+"/in")
	require.NoError(t, err)
	assert.Contains(t, got, "path=/in/hook-token-2")

	// A path secret whose value cannot be path segments is refused.
	store.set("hook", "has space")
	_, err = get(t.Context(), t, client, srv.URL+"/in")
	require.ErrorContains(t, err, "a path cannot carry")

	store.allow["sub"] = "elsewhere"
	store.set("hook", "hook-token-3")
	_, err = get(t.Context(), t, client, srv.URL+"/in")
	require.ErrorContains(t, err, `secret "sub" may not be used by connection "conn"`)
}

// A header with no placeholder is left alone, and a client with none to fill
// takes no extra transport.
func TestStaticHeadersWithoutPlaceholdersAreUntouched(t *testing.T) {
	installSecrets(t, map[string]string{})
	srv := echoServer(t)
	c := parsed(t, map[string]any{"static_headers": map[string]any{"X-Sub": "literal"}})
	assert.NotPanics(t, func() { _ = newSecretHeaderTransport(c, http.DefaultTransport) })
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/a", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("X-Sub", "literal")
	resp, err := NewHTTPClient(c).Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(b), "x-sub=literal")
}

func TestClientCredentialsSecretIsReadAtEachExchange(t *testing.T) {
	store := installSecrets(t, map[string]string{"cs": "client-secret-1", "cid": "client-id"})
	var mu sync.Mutex
	var seen []string
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		_ = r.ParseForm()
		mu.Lock()
		seen = append(seen, r.PostForm.Get("client_id")+"/"+r.PostForm.Get("client_secret"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"at","token_type":"bearer","expires_in":1}`)
	}))
	t.Cleanup(idp.Close)
	c := parsed(t, map[string]any{
		"auth_mode": "oauth", "oauth_grant": "client_credentials", "oauth_token_url": idp.URL,
		"oauth_client_id": "{{secret:cid}}", "oauth_client_secret": "{{secret:cs}}", "oauth_endpoint_auth_style": "params",
	})
	a := newOAuth2ClientCredentialsAuth(c, nowFixed)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://h.example.com", http.NoBody)
	require.NoError(t, err)
	require.NoError(t, a.Apply(req))
	assert.Equal(t, "Bearer at", req.Header.Get(AuthorizationHeader))

	store.set("cs", "client-secret-2")
	b := newOAuth2ClientCredentialsAuth(c, nowFixed)
	require.NoError(t, b.Apply(req))
	mu.Lock()
	assert.Equal(t, []string{"client-id/client-secret-1", "client-id/client-secret-2"}, seen)
	mu.Unlock()

	store.allow["cs"] = "elsewhere"
	err = newOAuth2ClientCredentialsAuth(c, nowFixed).Apply(req)
	require.ErrorContains(t, err, "oauth2 token fetch failed")

	// A client id in full is sent as written.
	store.allow["cs"] = "conn"
	cfg, err := Parse("api", "apigateway", "https://h.example.com", map[string]any{
		"auth_mode": "oauth", "oauth_grant": "client_credentials", "oauth_token_url": idp.URL,
		"oauth_client_id": "id", "oauth_client_secret": "plain", "oauth_endpoint_auth_style": "params",
	})
	require.NoError(t, err)
	cfg.ConnectionName = "conn"
	require.NoError(t, newOAuth2ClientCredentialsAuth(cfg, nowFixed).Apply(req))
}

func TestClientCredentialsIDOutOfScope(t *testing.T) {
	store := installSecrets(t, map[string]string{"cid": "client-id"})
	store.allow["cid"] = "elsewhere"
	c := parsed(t, map[string]any{
		"auth_mode": "oauth", "oauth_grant": "client_credentials", "oauth_token_url": "https://idp.example.com/token",
		"oauth_client_id": "{{secret:cid}}", "oauth_client_secret": "s",
	})
	_, err := exchangeClientCredentials(t.Context(), c, unsent(c))
	require.ErrorContains(t, err, `secret "cid" may not be used`)
}

// nowFixed is the clock the bounded-expiry wrapper stamps from.
func nowFixed() time.Time { return time.Unix(1_700_000_000, 0) }

// unsent is a client_credentials configuration for c that the test refuses
// before it is sent.
func unsent(c Config) *clientcredentials.Config {
	return &clientcredentials.Config{ClientID: c.OAuth2.ClientID, ClientSecret: c.OAuth2.ClientSecret, TokenURL: c.OAuth2.TokenURL}
}
