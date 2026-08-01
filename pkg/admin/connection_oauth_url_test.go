package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetConnectionRejectsMalformedOAuthEndpoint drives the real admin
// route end to end -- decode, per-kind ParseConfig, the shared
// connoauth endpoint validation -- and asserts the operator gets a 400
// naming the bad key AND that nothing reached the connection store.
//
// The unit test in pkg/connoauth proves the validator; this proves the
// validator is actually reachable from the HTTP surface an operator
// uses. A connection whose oauth_token_url never passed a check is
// exactly what turns the admin API into an arbitrary-request sink.
func TestSetConnectionRejectsMalformedOAuthEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		kind     string
		config   string
		wantBody string
	}{
		{
			name: "mcp gateway token url with embedded credentials",
			kind: "mcp",
			config: `{"endpoint":"https://upstream.example.com/mcp","auth_mode":"oauth",` +
				`"oauth_grant":"client_credentials",` +
				`"oauth_token_url":"https://svc:s3cr3t@idp.example.com/token",` +
				`"oauth_client_id":"platform"}`,
			wantBody: "must not embed credentials",
		},
		{
			name: "mcp gateway token url is not absolute",
			kind: "mcp",
			config: `{"endpoint":"https://upstream.example.com/mcp","auth_mode":"oauth",` +
				`"oauth_grant":"client_credentials",` +
				`"oauth_token_url":"idp.example.com/token",` +
				`"oauth_client_id":"platform"}`,
			wantBody: "must be an absolute URL",
		},
		{
			name: "mcp gateway authorization url has a non-http scheme",
			kind: "mcp",
			config: `{"endpoint":"https://upstream.example.com/mcp","auth_mode":"oauth",` +
				`"oauth_grant":"authorization_code",` +
				`"oauth_token_url":"https://idp.example.com/token",` +
				`"oauth_authorization_url":"javascript:alert(1)",` +
				`"oauth_client_id":"platform"}`,
			wantBody: "unsupported scheme",
		},
		{
			name: "api gateway token url with embedded credentials",
			kind: "api",
			config: `{"base_url":"https://api.example.com","auth_mode":"oauth",` +
				`"oauth_grant":"client_credentials",` +
				`"oauth_token_url":"https://svc:s3cr3t@idp.example.com/token",` +
				`"oauth_client_id":"platform"}`,
			wantBody: "must not embed credentials",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &mockConnectionStore{}
			h := NewHandler(Deps{
				Config:          testConfig(),
				ConnectionStore: store,
				ConfigStore:     &mockConfigStore{mode: "database"},
			}, nil)

			body := `{"config":` + tc.config + `,"description":"idp under test"}`
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
				"/api/v1/admin/connection-instances/"+tc.kind+"/acme", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			require.Equal(t, http.StatusBadRequest, w.Code,
				"a malformed OAuth endpoint must be refused at save time; body: %s", w.Body.String())
			assert.Contains(t, w.Body.String(), tc.wantBody)
			assert.Empty(t, store.setCalls,
				"a refused connection must not be persisted; a stored row would drive "+
					"the token exchange on every later refresh")
		})
	}
}

// TestSetConnectionAcceptsCleartextInternalOAuthEndpoint is the
// counterpart guard: the validation must not refuse a self-hosted IdP
// reached over plain http inside the cluster. Blocking that shape would
// break a supported deployment, which is why no private-address or
// allowlist rule is applied to the host.
func TestSetConnectionAcceptsCleartextInternalOAuthEndpoint(t *testing.T) {
	store := &mockConnectionStore{}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: store,
		ConfigStore:     &mockConfigStore{mode: "database"},
	}, nil)

	body := `{"config":{"endpoint":"https://upstream.example.com/mcp","auth_mode":"oauth",` +
		`"oauth_grant":"client_credentials",` +
		`"oauth_token_url":"http://keycloak.identity.svc.cluster.local:8080/realms/p/token",` +
		`"oauth_client_id":"platform","oauth_client_secret":"shh"},"description":"internal idp"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/connection-instances/mcp/internal", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "PUT response body: %s", w.Body.String())
	require.Len(t, store.setCalls, 1)
	assert.Equal(t, "http://keycloak.identity.svc.cluster.local:8080/realms/p/token",
		store.setCalls[0].Config["oauth_token_url"])
}
