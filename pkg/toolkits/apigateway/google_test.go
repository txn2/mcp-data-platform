package apigateway

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/secretref"
)

const (
	googleTestEmail = "reporting@acme-analytics.iam.gserviceaccount.com"
	googleTestScope = "https://www.googleapis.com/auth/display-video"
)

// googleUpstream is Google's token endpoint and one Google API behind it: the
// token endpoint issues a token only for an assertion carrying the scope as a
// claim, and the API answers that token with a 403 naming an API not enabled
// in the account's project.
type googleUpstream struct {
	token, api *httptest.Server
	mu         sync.Mutex
	forms      []string
}

func newGoogleUpstream(t *testing.T) *googleUpstream {
	t.Helper()
	g := &googleUpstream{}
	g.token = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			t.Errorf("token request: %v", err)
		}
		g.mu.Lock()
		g.forms = append(g.forms, r.PostForm.Encode())
		g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.PostForm.Get("scope") != "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_scope","error_description":"Invalid OAuth scope or ID token audience provided."}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"ya29.test","token_type":"Bearer","expires_in":3599,"scope":"` + googleTestScope + `"}`))
	}))
	g.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ya29.test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":403,"message":"Display & Video 360 API has not been used in project 1234567890 before or it is disabled.",` +
			`"status":"PERMISSION_DENIED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"SERVICE_DISABLED"}]}}`))
	}))
	t.Cleanup(g.token.Close)
	t.Cleanup(g.api.Close)
	return g
}

func googleTestKeyFile(t *testing.T, tokenURI string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "acme-analytics", "private_key_id": "k1",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email": googleTestEmail, "token_uri": tokenURI,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestGoogleServiceAccount_ThroughTheConnection is #2061 on the api kind: a
// connection holding nothing but the key file and its scope is issued a token,
// the connection test names the account and the scopes granted, and a Google
// API refusing the token's account says what the operator does next.
func TestGoogleServiceAccount_ThroughTheConnection(t *testing.T) {
	g := newGoogleUpstream(t)
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	if err := tk.AddConnection("dv360", map[string]any{
		"base_url":                    g.api.URL,
		"google_service_account_json": googleTestKeyFile(t, g.token.URL),
		"oauth_scope":                 googleTestScope,
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}

	res := tk.ProbeConnection(context.Background(), "dv360")
	if !res.OK || !strings.Contains(res.Detail, "issued a token for "+googleTestEmail+" with scopes "+googleTestScope) {
		t.Fatalf("connection test = %+v", res)
	}

	_, out := modelCall(t, tk, InvokeInput{Connection: "dv360", Method: http.MethodGet, Path: "/v4/advertisers"}, 1<<20)
	if out.Status != http.StatusForbidden || !strings.Contains(out.Hint, "not enabled in the service account's Google Cloud project") {
		t.Fatalf("status %d hint %q", out.Status, out.Hint)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, form := range g.forms {
		if strings.Contains(form, "scope=") {
			t.Fatalf("a token request carried the scope as a form parameter: %s", form)
		}
	}
}

// keySecrets is a store holding one Google key secret allowed on one
// connection.
type keySecrets struct {
	fakeSecrets
	value      string
	connection string
}

func (k keySecrets) ConnectionValue(_ context.Context, name, connection string) (string, error) {
	if name != "dv360-key" || connection != k.connection {
		return "", errors.New(`secret "dv360-key" may not be used by connection "` + connection + `"`)
	}
	return k.value, nil
}

var _ interface {
	Lookup(context.Context, string, string) secretref.Lookup
} = keySecrets{}

// The key named by a stored secret reaches the connection that names it,
// whether the secrets were wired before or after the connection was added, and
// no other connection.
func TestGoogleServiceAccount_KeyFromAStoredSecret(t *testing.T) {
	g := newGoogleUpstream(t)
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	cfg := func() map[string]any {
		return map[string]any{
			"base_url": g.api.URL, "oauth_scope": googleTestScope,
			"google_service_account_secret": "dv360-key", "oauth_token_url": g.token.URL,
		}
	}
	if err := tk.AddConnection("dv360", cfg()); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	if res := tk.ProbeConnection(context.Background(), "dv360"); res.OK || !strings.Contains(res.Error, "stored secrets are not available here") {
		t.Fatalf("a key secret with no store wired = %+v", res)
	}

	tk.SetSecrets(keySecrets{value: googleTestKeyFile(t, g.token.URL), connection: "dv360"})
	if res := tk.ProbeConnection(context.Background(), "dv360"); !res.OK || !strings.Contains(res.Detail, googleTestEmail) {
		t.Fatalf("connection test after the store was wired = %+v", res)
	}

	if err := tk.AddConnection("other", cfg()); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	if res := tk.ProbeConnection(context.Background(), "other"); res.OK || !strings.Contains(res.Error, `may not be used by connection "other"`) {
		t.Fatalf("a connection the secret does not allow = %+v", res)
	}
}
