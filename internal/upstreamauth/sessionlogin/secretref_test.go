package sessionlogin

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/secretref"
)

// tableauBody is the issue's sign-in body (#2066): the personal access token
// named as a stored secret, with no session_login_secret beside it.
const tableauBody = `{"credentials":{"personalAccessTokenName":"platform","personalAccessTokenSecret":"{{secret:tableau-rest}}","site":{"contentUrl":"acme"}}}`

// patStore holds the PAT as stored secret "tableau-rest", allowed only on
// connection "tableau". It is process-wide, so these tests are not parallel.
type patStore struct {
	mu    sync.Mutex
	value string
}

func (s *patStore) set(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value = v
}

func (s *patStore) read(_ context.Context, name, connection string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name != "tableau-rest" {
		return "", fmt.Errorf("secret %q does not exist", name)
	}
	if connection != "tableau" {
		return "", fmt.Errorf("secret %q may not be used by connection %q; it is allowed on tableau", name, connection)
	}
	return s.value, nil
}

func installPAT(t *testing.T, value string) *patStore {
	t.Helper()
	s := &patStore{value: value}
	prev := secretref.SetConnectionSource(s.read)
	t.Cleanup(func() { secretref.SetConnectionSource(prev) })
	return s
}

// storedSecretConfig is the Tableau connection signing in with the stored
// secret, as connection.
func storedSecretConfig(t *testing.T, base, connection string) Config {
	t.Helper()
	keys := sessionKeys(base, map[string]any{"session_login_body": tableauBody})
	delete(keys, "session_login_secret")
	c := Parse(base, keys)
	require.NoError(t, c.Validate("apigateway"))
	c.Connection = connection
	return c
}

func TestSignInReadsTheStoredSecretAndARotationReachesTheNextSignIn(t *testing.T) {
	up, srv := newSessionUpstream(t)
	store := installPAT(t, up.secret)
	client := NewHTTPClient(storedSecretConfig(t, srv.URL, "tableau"))

	status, body := get(t, client, srv.URL+"/api/3.22/sites/x/workbooks")
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, int32(1), up.signIns.Load())

	// The PAT is rotated at the upstream and in the store, and nothing about
	// the connection is saved: the next sign-in sends the new value.
	up.mu.Lock()
	up.secret = "rotated-pat-2"
	up.mu.Unlock()
	store.set("rotated-pat-2")
	up.revoke()
	status, body = get(t, client, srv.URL+"/api/3.22/sites/x/workbooks")
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, int32(2), up.signIns.Load())
}

func TestSignInRefusesASecretTheConnectionMayNotUse(t *testing.T) {
	up, srv := newSessionUpstream(t)
	installPAT(t, up.secret)
	client := NewHTTPClient(storedSecretConfig(t, srv.URL, "other"))

	resp, err := client.Get(srv.URL + "/api/3.22/sites/x/workbooks") //nolint:noctx // test request
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	assert.True(t, IsSessionFailure(err))
	assert.Contains(t, err.Error(), `secret "tableau-rest" may not be used by connection "other"`)
	up.mu.Lock()
	assert.Zero(t, up.attempts, "nothing was sent")
	up.mu.Unlock()
}

// An upstream that quotes the credential back in its refusal does not get it
// into the error the caller reads.
func TestSignInRefusalRedactsTheStoredSecret(t *testing.T) {
	_, srv := newSessionUpstream(t)
	installPAT(t, "wrong-pat-value")
	client := NewHTTPClient(storedSecretConfig(t, srv.URL, "tableau"))

	resp, err := client.Get(srv.URL + "/api/3.22/sites/x/workbooks") //nolint:noctx // test request
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "wrong-pat-value")
	assert.Contains(t, err.Error(), secretref.Redaction("tableau-rest"))
}

func TestSignInHeadersReadStoredSecrets(t *testing.T) {
	up, srv := newSessionUpstream(t)
	installPAT(t, up.secret)
	cfg := storedSecretConfig(t, srv.URL, "tableau")
	cfg.LoginHeaders = map[string]string{"X-Key": "{{secret:missing}}"}
	resp, err := NewHTTPClient(cfg).Get(srv.URL + "/api/3.22/sites/x/workbooks") //nolint:noctx // test request
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.ErrorContains(t, err, `secret "missing" does not exist`)
}

func TestSignInBodyFillsBothPlaceholderForms(t *testing.T) {
	lookup := func(name string) (string, error) { return `v"` + name, nil }
	form := Config{LoginBody: "user={{secret:u}}&pw={{secret}}", LoginContentType: "application/x-www-form-urlencoded", Secret: "{{secret:p}}"}
	got, err := form.signInBody(lookup)
	require.NoError(t, err)
	assert.Equal(t, "user=v%22u&pw=v%22p", got, "each value is escaped for the body's media type")

	xml := Config{LoginBody: "<pw>{{secret:p}}</pw>", LoginContentType: "application/xml"}
	got, err = xml.signInBody(lookup)
	require.NoError(t, err)
	assert.Equal(t, "<pw>v&#34;p</pw>", got)

	_, err = Config{LoginBody: "{{secret:p}}", Secret: "{{secret:bad"}.signInBody(lookup)
	require.Error(t, err)
}

func TestValidateAStoredSecretBody(t *testing.T) {
	keys := func(body, secret string) Config {
		k := sessionKeys("https://h.example.com", map[string]any{"session_login_body": body})
		k["session_login_secret"] = secret
		if secret == "" {
			delete(k, "session_login_secret")
		}
		return Parse("https://h.example.com", k)
	}
	require.NoError(t, keys(tableauBody, "").Validate("apigateway"), "a named secret needs no session_login_secret")

	err := keys(tableauBody, "left-over").Validate("apigateway")
	require.ErrorContains(t, err, "needs no session_login_secret, so clear it")

	err = keys(`{"s":"{{secret:Upper}}"}`, "").Validate("apigateway")
	require.ErrorContains(t, err, "not {{secret:<name>}}")
}
