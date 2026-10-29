package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

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

// A tool result that repeats the credential an mcp connection filled from a
// stored secret comes back with the value replaced, in its text and its
// structured content, and so does an error carrying it (#2357).
func TestMCPToolResultRedactsTheFilledSecret(t *testing.T) {
	installVendorSecrets(t, map[string]string{"vendor-token": "tok-2357-secret"})
	srv := mcp.NewServer(&mcp.Implementation{Name: "echo", Version: "0.0.1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "headers"},
		func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			auth := req.Extra.Header.Get("Authorization")
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "you sent " + auth}}},
				map[string]any{"authorization": auth}, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "fail"},
		func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return nil, nil, fmt.Errorf("rejected %s", req.Extra.Header.Get("Authorization"))
		})
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))
	t.Cleanup(func() {
		ts.CloseClientConnections()
		ts.Close()
	})

	client, err := dial(t.Context(), Config{
		Endpoint: ts.URL, ConnectionName: "vendor", AuthMode: AuthModeBearer, Credential: "{{secret:vendor-token}}",
	}, dialDeps{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.close() })

	res, err := client.callTool(t.Context(), "headers", map[string]any{})
	require.NoError(t, err)
	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "you sent Bearer "+secretref.Redaction("vendor-token"), text.Text)
	structured, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	assert.NotContains(t, string(structured), "tok-2357-secret")
	assert.Contains(t, string(structured), secretref.Redaction("vendor-token"))

	res, err = client.callTool(t.Context(), "fail", map[string]any{})
	if err != nil {
		assert.NotContains(t, err.Error(), "tok-2357-secret")
	} else {
		body, _ := json.Marshal(res.Content)
		assert.NotContains(t, string(body), "tok-2357-secret")
	}
}

func TestTokenErrorKeepsOnlyStatusAndCode(t *testing.T) {
	body := `{"error":"invalid_client","refresh_token":"leaked-rt"}`
	err := tokenError(&oauth2.RetrieveError{Response: &http.Response{StatusCode: 401}, Body: []byte(body), ErrorCode: "invalid_client"})
	assert.Equal(t, "gateway: oauth client_credentials: token endpoint answered 401 invalid_client", err.Error())
	err = tokenError(&oauth2.RetrieveError{Response: &http.Response{StatusCode: 500}, Body: []byte("leaked-rt")})
	assert.NotContains(t, err.Error(), "leaked-rt")
	err = tokenError(&url.Error{Op: "Post", URL: "https://id:pw@idp.example.com/token?x=y", Err: errors.New("dial tcp: refused")})
	assert.NotContains(t, err.Error(), "pw@")
	assert.NotContains(t, err.Error(), "x=y")
	assert.Contains(t, tokenError(errors.New("other https://a:b@c")).Error(), "details redacted")
}

func TestRedactResultEveryPart(t *testing.T) {
	r := &secretref.Redactor{}
	r.Add("k", "s3cret-2357")
	res := &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "x://1", Text: "has s3cret-2357"}},
		&mcp.EmbeddedResource{},
	}}
	redactResult(r, res)
	er, ok := res.Content[0].(*mcp.EmbeddedResource)
	require.True(t, ok)
	assert.Equal(t, "has "+secretref.Redaction("k"), er.Resource.Text)
	assert.Nil(t, res.StructuredContent)

	unencodable := &mcp.CallToolResult{StructuredContent: map[string]any{"f": func() {}}}
	redactResult(r, unencodable)
	assert.Nil(t, unencodable.StructuredContent, "content that cannot be read is dropped rather than passed on")

	redactResult(r, nil)
	untouched := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "s3cret-2357"}}}
	redactResult(&secretref.Redactor{}, untouched)
	text, ok := untouched.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "s3cret-2357", text.Text, "an empty redactor changes nothing")
}
