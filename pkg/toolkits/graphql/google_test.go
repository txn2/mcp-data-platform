package graphql

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
	"testing"
)

// A graphql connection whose Google service account key is a stored secret
// reads it through the read the platform wires (#2061), scoped to the
// connection's own name, whether the read is wired before or after the
// connection is served.
func TestGoogleServiceAccount_KeyFromAStoredSecret(t *testing.T) {
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"ya29.test","token_type":"Bearer","expires_in":3599}`))
	}))
	t.Cleanup(token.Close)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	file, err := json.Marshal(map[string]string{
		"type": "service_account", "private_key_id": "k1", "client_email": "graph@acme-analytics.iam.gserviceaccount.com",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "token_uri": token.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	u := newUpstream(t)
	tk := newToolkit(t, u, "", map[string]any{
		"google_service_account_secret": "graph-key", "oauth_scope": "https://www.googleapis.com/auth/cloud-platform",
		"oauth_token_url": token.URL,
	})
	t.Cleanup(func() { _ = tk.Close() })
	var asked []string
	tk.SetConnectionSecrets(func(_ context.Context, name, connection string) (string, error) {
		asked = append(asked, name+"@"+connection)
		if connection != "gql" {
			return "", errors.New("not allowed")
		}
		return string(file), nil
	})

	res := tk.ProbeConnection(context.Background(), "gql")
	if !res.OK || !strings.Contains(res.Detail, "issued a token for graph@acme-analytics.iam.gserviceaccount.com") {
		t.Fatalf("connection test = %+v", res)
	}
	if len(asked) != 1 || asked[0] != "graph-key@gql" {
		t.Fatalf("secret reads = %v", asked)
	}
	if got := u.headers[len(u.headers)-1].Get("Authorization"); got != "Bearer ya29.test" {
		t.Fatalf("Authorization = %q", got)
	}
}
