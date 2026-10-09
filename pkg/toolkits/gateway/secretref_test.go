package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/secretref"
)

// vendorSecrets answers stored secrets allowed only on connection "vendor"
// (#2066). The source is process-wide, so these tests are not parallel.
type vendorSecrets struct {
	mu     sync.Mutex
	values map[string]string
}

func (v *vendorSecrets) read(_ context.Context, name, connection string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if connection != "vendor" {
		return "", fmt.Errorf("secret %q may not be used by connection %q", name, connection)
	}
	value, ok := v.values[name]
	if !ok {
		return "", fmt.Errorf("secret %q does not exist", name)
	}
	return value, nil
}

func installVendorSecrets(t *testing.T, values map[string]string) *vendorSecrets {
	t.Helper()
	v := &vendorSecrets{values: values}
	prev := secretref.SetConnectionSource(v.read)
	t.Cleanup(func() { secretref.SetConnectionSource(prev) })
	return v
}

// headerEcho answers with the auth headers it received.
func headerEcho(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "%s|%s", r.Header.Get("Authorization"), r.Header.Get("X-API-Key"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func send(t *testing.T, rt http.RoundTripper, target string) (string, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	require.NoError(t, err)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		return "", fmt.Errorf("round trip: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	return string(buf[:n]), nil
}

func TestMCPCredentialNamingAStoredSecretIsReadAtSend(t *testing.T) {
	store := installVendorSecrets(t, map[string]string{"vendor-token": "tok-1"})
	srv := headerEcho(t)

	bearer := &authRoundTripper{mode: AuthModeBearer, credential: "{{secret:vendor-token}}", connection: "vendor", base: http.DefaultTransport}
	got, err := send(t, bearer, srv.URL)
	require.NoError(t, err)
	assert.Equal(t, "Bearer tok-1|", got)

	store.mu.Lock()
	store.values["vendor-token"] = "tok-2"
	store.mu.Unlock()
	apiKey := &authRoundTripper{mode: AuthModeAPIKey, credential: "{{secret:vendor-token}}", connection: "vendor", base: http.DefaultTransport}
	got, err = send(t, apiKey, srv.URL)
	require.NoError(t, err)
	assert.Equal(t, "|tok-2", got, "a rotated secret is read on the next request")

	other := &authRoundTripper{mode: AuthModeBearer, credential: "{{secret:vendor-token}}", connection: "other", base: http.DefaultTransport}
	_, err = send(t, other, srv.URL)
	require.ErrorContains(t, err, `secret "vendor-token" may not be used by connection "other"`)
}

func TestMCPClientCredentialsReadStoredSecrets(t *testing.T) {
	installVendorSecrets(t, map[string]string{"vendor-cs": "cs-value"})
	var mu sync.Mutex
	var got string
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		_ = r.ParseForm()
		mu.Lock()
		got = r.PostForm.Get("client_secret")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"cc-1","token_type":"Bearer","expires_in":3600}`))
	}))
	t.Cleanup(idp.Close)

	tk := New("primary")
	_, cc := tk.tokenProviderFor("vendor", OAuthConfig{
		Grant: OAuthGrantClientCredentials, TokenURL: idp.URL, ClientID: "id",
		ClientSecret: "{{secret:vendor-cs}}", EndpointAuthStyle: "params",
	})
	require.NotNil(t, cc)
	tok, err := cc.Token(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "cc-1", tok)
	mu.Lock()
	assert.Equal(t, "cs-value", got)
	mu.Unlock()

	_, wrong := tk.tokenProviderFor("other", OAuthConfig{
		Grant: OAuthGrantClientCredentials, TokenURL: idp.URL, ClientID: "{{secret:vendor-cs}}", ClientSecret: "s",
	})
	_, err = wrong.Token(t.Context())
	require.ErrorContains(t, err, `may not be used by connection "other"`)

	_, idOK := tk.tokenProviderFor("vendor", OAuthConfig{
		Grant: OAuthGrantClientCredentials, TokenURL: idp.URL, ClientID: "id", ClientSecret: "{{secret:missing}}",
	})
	_, err = idOK.Token(t.Context())
	require.ErrorContains(t, err, `secret "missing" does not exist`)
}
