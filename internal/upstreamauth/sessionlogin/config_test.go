package sessionlogin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sessionUpstream models a Tableau-style upstream: a personal access token
// signed in at /auth/signin answers a token and a site id, every data call
// must carry the token in X-Tableau-Auth, and a revoked token is answered
// 401.
type sessionUpstream struct {
	t      *testing.T
	secret string

	mu       sync.Mutex
	valid    map[string]bool
	signIns  atomic.Int32
	signOuts []string
	bodies   []string
	attempts int
	// refuse answers every sign-in 401 with an error body quoting the
	// credential it was sent.
	refuse bool
	// alwaysReject answers every data call 401, a credential the upstream
	// signs in but whose sessions it will not accept.
	alwaysReject bool
	// gate, when set, holds every sign-in until it is closed.
	gate chan struct{}
}

func newSessionUpstream(t *testing.T) (*sessionUpstream, *httptest.Server) {
	t.Helper()
	u := &sessionUpstream{t: t, secret: `s3cr"et&<x>`, valid: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(u.serve))
	t.Cleanup(srv.Close)
	return u, srv
}

func (u *sessionUpstream) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/3.22/auth/signin":
		u.signIn(w, r)
	case "/api/3.22/auth/signout":
		u.mu.Lock()
		u.signOuts = append(u.signOuts, r.Header.Get("X-Tableau-Auth"))
		u.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		u.data(w, r)
	}
}

func (u *sessionUpstream) signIn(w http.ResponseWriter, r *http.Request) {
	if u.gate != nil {
		<-u.gate
	}
	u.mu.Lock()
	u.attempts++
	u.mu.Unlock()
	var body struct {
		Credentials struct {
			Name   string `json:"personalAccessTokenName"`
			Secret string `json:"personalAccessTokenSecret"`
		} `json:"credentials"`
	}
	raw, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(raw, &body); err != nil || body.Credentials.Secret != u.secret || u.refuse {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, `{"error":{"summary":"Signin Error","detail":"bad secret %s"}}`, body.Credentials.Secret)
		return
	}
	n := u.signIns.Add(1)
	token := fmt.Sprintf("tok-%d", n)
	u.mu.Lock()
	// One live session per credential: signing in ends the previous one.
	u.valid = map[string]bool{token: true}
	u.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"credentials":{"token":%q,"site":{"id":"site 1","contentUrl":"acme"}}}`, token)
}

func (u *sessionUpstream) data(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	ok := u.valid[r.Header.Get("X-Tableau-Auth")] && !u.alwaysReject
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		u.bodies = append(u.bodies, string(raw))
	}
	u.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":"401002","detail":"session expired for `+r.Header.Get("X-Tableau-Auth")+`"}}`)
		return
	}
	_, _ = io.WriteString(w, "path="+r.URL.EscapedPath())
}

// revoke ends every live session, as the upstream's own expiry would.
func (u *sessionUpstream) revoke() {
	u.mu.Lock()
	u.valid = map[string]bool{}
	u.mu.Unlock()
}

func sessionKeys(base string, extra map[string]any) map[string]any {
	cfg := map[string]any{
		"auth_mode":            AuthMode,
		"session_login_url":    "/api/3.22/auth/signin",
		"session_login_body":   `{"credentials":{"personalAccessTokenName":"platform","personalAccessTokenSecret":"{{secret}}","site":{"contentUrl":"acme"}}}`,
		"session_login_secret": `s3cr"et&<x>`,
		"session_token_source": "body:credentials.token",
		"session_token_header": "X-Tableau-Auth",
		"session_logout_url":   base + "/api/3.22/auth/signout",
		"session_capture":      map[string]any{"site_id": "body:credentials.site.id"},
	}
	maps.Copy(cfg, extra)
	return cfg
}

func sessionConfig(t *testing.T, base string, extra map[string]any) Config {
	t.Helper()
	c := Parse(base, sessionKeys(base, extra))
	require.NoError(t, c.Validate("apigateway"))
	return c
}

// NewHTTPClient is the session over the default transport, as
// internal/upstreamauth builds a session_login connection's client.
func NewHTTPClient(c Config) *http.Client {
	return &http.Client{Transport: NewTransport(c, "apigateway", http.DefaultTransport, http.DefaultTransport)}
}

func get(t *testing.T, client *http.Client, rawURL string) (status int, body string) {
	t.Helper()
	resp, err := client.Get(rawURL) //nolint:noctx // test request
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func TestSessionLoginSignsInOnceAndCarriesTheTokenAndCapturedValues(t *testing.T) {
	up, srv := newSessionUpstream(t)
	client := NewHTTPClient(sessionConfig(t, srv.URL, nil))

	status, body := get(t, client, srv.URL+"/api/3.22/sites/{session.site_id}/workbooks")
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "path=/api/3.22/sites/site%201/workbooks", body, "the captured site id is written into the path, escaped")

	status, body = get(t, client, srv.URL+"/api/3.22/sites/%7Bsession.site_id%7D/views")
	assert.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "path=/api/3.22/sites/site%201/views", body, "a percent-encoded reference is written the same way")
	assert.Equal(t, int32(1), up.signIns.Load(), "the session is reused across calls")
}

func TestSessionLoginOneSignInServesABurst(t *testing.T) {
	up, srv := newSessionUpstream(t)
	up.gate = make(chan struct{})
	client := NewHTTPClient(sessionConfig(t, srv.URL, nil))

	var wg sync.WaitGroup
	statuses := make([]int, 8)
	for i := range statuses {
		wg.Go(func() {
			statuses[i], _ = get(t, client, srv.URL+"/api/3.22/sites/{session.site_id}/workbooks")
		})
	}
	close(up.gate)
	wg.Wait()
	for _, s := range statuses {
		assert.Equal(t, http.StatusOK, s)
	}
	assert.Equal(t, int32(1), up.signIns.Load(), "concurrent callers wait for the one sign-in")
}

func TestSessionLoginSignsInAgainAndReplaysWhenTheSessionIsRejected(t *testing.T) {
	up, srv := newSessionUpstream(t)
	client := NewHTTPClient(sessionConfig(t, srv.URL, nil))
	status, _ := get(t, client, srv.URL+"/api/3.22/sites/x/workbooks")
	require.Equal(t, http.StatusOK, status)

	up.revoke()
	resp, err := client.Post(srv.URL+"/api/3.22/sites/x/workbooks", "application/json", strings.NewReader(`{"n":1}`)) //nolint:noctx // test request
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "the call is replayed on a fresh session")
	assert.Equal(t, int32(2), up.signIns.Load())
	up.mu.Lock()
	defer up.mu.Unlock()
	assert.Equal(t, []string{"", `{"n":1}`, `{"n":1}`}, up.bodies, "the replay sends the same body")
}

func TestSessionLoginReportsASecondRejectionAsTheCredentials(t *testing.T) {
	up, srv := newSessionUpstream(t)
	up.alwaysReject = true
	client := NewHTTPClient(sessionConfig(t, srv.URL, nil))

	_, err := client.Get(srv.URL + "/api/3.22/sites/x/workbooks") //nolint:noctx,bodyclose // the call fails
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSessionRejected)
	assert.True(t, IsSessionFailure(err))
	assert.Contains(t, err.Error(), "HTTP 401")
	assert.Contains(t, err.Error(), "session expired for [REDACTED]", "the upstream's text is quoted with the token taken out")
	assert.NotContains(t, err.Error(), "tok-")
	assert.Equal(t, int32(2), up.signIns.Load(), "one sign-in, one more for the replay, and no loop")

	// The next call inside the hold is answered as the upstream answers it,
	// without signing in again: the credential is what it refuses.
	status, _ := get(t, client, srv.URL+"/api/3.22/sites/x/workbooks")
	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Equal(t, int32(2), up.signIns.Load(), "a refused credential is not signed in with on every call")
}

func TestSessionLoginHoldsAFailedSignIn(t *testing.T) {
	up, srv := newSessionUpstream(t)
	up.refuse = true
	cfg := sessionConfig(t, srv.URL, nil)
	st := newSessionTransport(cfg, "apigateway", http.DefaultTransport, http.DefaultTransport)
	clock := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	st.now = func() time.Time { return clock }
	client := &http.Client{Transport: st}
	signInAttempts := func() int {
		up.mu.Lock()
		defer up.mu.Unlock()
		return up.attempts
	}

	for range 3 {
		_, err := client.Get(srv.URL + "/x") //nolint:noctx,bodyclose // the call fails
		require.ErrorIs(t, err, ErrSessionLogin)
	}
	assert.Equal(t, 1, signInAttempts(), "a refused sign-in is answered from the hold, not repeated at the upstream")

	up.refuse = false
	clock = clock.Add(failureHold)
	status, _ := get(t, client, srv.URL+"/x")
	assert.Equal(t, http.StatusOK, status, "past the hold the platform signs in again")
	assert.Equal(t, 2, signInAttempts())
}

func TestSessionLoginWaitingForASignInKeepsTheCallersDeadline(t *testing.T) {
	_, srv := newSessionUpstream(t)
	st := newSessionTransport(sessionConfig(t, srv.URL, nil), "apigateway", http.DefaultTransport, http.DefaultTransport)
	st.signing <- struct{}{} // another caller is signing in
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	body := &closeTracker{Reader: strings.NewReader("x")}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/x", body)
	require.NoError(t, err)
	_, err = st.RoundTrip(req) //nolint:bodyclose // the call fails
	require.ErrorIs(t, err, context.Canceled)
	assert.True(t, body.closed, "a request that is not sent still has its body closed")
}

// closeTracker records whether a request body was closed.
type closeTracker struct {
	io.Reader
	closed bool
}

func (c *closeTracker) Close() error {
	c.closed = true
	return nil
}

func TestSessionLoginTTLSignsOutTheSessionItRetires(t *testing.T) {
	up, srv := newSessionUpstream(t)
	cfg := sessionConfig(t, srv.URL, map[string]any{"session_ttl": "1h"})
	st := newSessionTransport(cfg, "apigateway", http.DefaultTransport, http.DefaultTransport)
	clock := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	st.now = func() time.Time { return clock }
	client := &http.Client{Transport: st}
	get(t, client, srv.URL+"/a")
	clock = clock.Add(2 * time.Hour)
	get(t, client, srv.URL+"/a")
	st.signOuts.Wait()
	up.mu.Lock()
	defer up.mu.Unlock()
	assert.Equal(t, []string{"tok-1"}, up.signOuts, "the session the lifetime retired is ended at the upstream")
}

func TestSessionLoginRefusedSignInIsScrubbed(t *testing.T) {
	up, srv := newSessionUpstream(t)
	up.refuse = true
	client := NewHTTPClient(sessionConfig(t, srv.URL, nil))

	_, err := client.Get(srv.URL + "/api/3.22/sites/x/workbooks") //nolint:noctx,bodyclose // the call fails
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSessionLogin)
	assert.Contains(t, err.Error(), "answered HTTP 401")
	assert.Contains(t, err.Error(), "bad secret [REDACTED]")
	assert.NotContains(t, err.Error(), `s3cr"et`, "the secret the upstream echoed is not quoted back")
}

func TestSessionLoginTTLSignsInAgainBeforeRejection(t *testing.T) {
	up, srv := newSessionUpstream(t)
	cfg := sessionConfig(t, srv.URL, map[string]any{"session_ttl": "1h"})
	st := newSessionTransport(cfg, "apigateway", http.DefaultTransport, http.DefaultTransport)
	clock := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	st.now = func() time.Time { return clock }
	client := &http.Client{Transport: st}

	get(t, client, srv.URL+"/a")
	clock = clock.Add(59 * time.Minute)
	get(t, client, srv.URL+"/a")
	assert.Equal(t, int32(1), up.signIns.Load(), "inside the lifetime the session is reused")
	clock = clock.Add(2 * time.Minute)
	get(t, client, srv.URL+"/a")
	assert.Equal(t, int32(2), up.signIns.Load(), "past it the platform signs in without waiting to be rejected")
}

func TestSessionLoginExpiredMarkerInA200(t *testing.T) {
	var signIns atomic.Int32
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			signIns.Add(1)
			w.Header().Set("X-Session", fmt.Sprintf("s%d", signIns.Load()))
			return
		}
		if calls.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"responseStatus":"FAILURE","errors":[{"type":"INVALID_SESSION_ID"}]}`)
			return
		}
		_, _ = io.WriteString(w, "ok "+r.Header.Get("Authorization"))
	}))
	t.Cleanup(srv.Close)
	cfg := sessionConfig(t, srv.URL, map[string]any{
		"session_login_url": "/login", "session_token_source": "header:X-Session",
		"session_token_header": "Authorization", "session_logout_url": "",
		"session_expired_marker": "INVALID_SESSION_ID", "session_capture": nil,
		"session_login_body": "username=svc&password={{secret}}", "session_login_content_type": "application/x-www-form-urlencoded",
	})
	_, body := get(t, NewHTTPClient(cfg), srv.URL+"/data")
	assert.Equal(t, "ok s2", body, "a named Authorization header with no prefix carries the raw token")
	assert.Equal(t, int32(2), signIns.Load())
}

func TestSessionLoginMarkerAbsentKeepsTheWholeBody(t *testing.T) {
	big := strings.Repeat("x", maxMarkerPeek+10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			_, _ = io.WriteString(w, `{"token":"t"}`)
			return
		}
		_, _ = io.WriteString(w, big)
	}))
	t.Cleanup(srv.Close)
	cfg := sessionConfig(t, srv.URL, map[string]any{
		"session_login_url": "/login", "session_token_source": "body:token", "session_token_header": "",
		"session_logout_url": "", "session_expired_marker": "EXPIRED", "session_capture": nil,
	})
	_, body := get(t, NewHTTPClient(cfg), srv.URL+"/data")
	assert.Equal(t, big, body, "the part read to look for the marker is put back in front of the rest")
}

func TestSessionLoginDefaultsToBearer(t *testing.T) {
	c := sessionConfig(t, "https://h.example.com/api", map[string]any{"session_token_header": ""})
	assert.Equal(t, authorizationHeader, c.TokenHeader)
	assert.Equal(t, "Bearer ", c.TokenPrefix)
	assert.Equal(t, "https://h.example.com/api/3.22/auth/signin", c.LoginURL, "a path resolves against the base URL")
	assert.Equal(t, []int{http.StatusUnauthorized}, c.ExpiredStatuses)
	assert.Equal(t, http.MethodPost, c.LoginMethod)
}

func TestSessionLoginSignsOutWhenTheClientIsReleased(t *testing.T) {
	up, srv := newSessionUpstream(t)
	client := NewHTTPClient(sessionConfig(t, srv.URL, nil))
	st, _ := client.Transport.(*sessionTransport)
	get(t, client, srv.URL+"/x")

	client.CloseIdleConnections()
	st.signOuts.Wait()
	up.mu.Lock()
	assert.Equal(t, []string{"tok-1"}, up.signOuts, "the session is ended at the upstream with its own token")
	up.mu.Unlock()

	client.CloseIdleConnections()
	st.signOuts.Wait()
	up.mu.Lock()
	assert.Len(t, up.signOuts, 1, "a client with no session has nothing to end")
	up.mu.Unlock()

	status, _ := get(t, client, srv.URL+"/x")
	assert.Equal(t, http.StatusOK, status, "a call after the release signs in again")
	assert.Equal(t, int32(2), up.signIns.Load())
}

func TestSessionLoginUnknownSessionValueIsRefused(t *testing.T) {
	_, srv := newSessionUpstream(t)
	client := NewHTTPClient(sessionConfig(t, srv.URL, nil))
	_, err := client.Get(srv.URL + "/api/3.22/sites/{session.user_id}/x") //nolint:noctx,bodyclose // the call fails
	require.ErrorIs(t, err, ErrUnknownSessionValue)
	assert.Contains(t, err.Error(), "{session.user_id}")
	assert.Contains(t, err.Error(), "it captures {session.site_id}")
}

func TestSessionLoginTokenNotFound(t *testing.T) {
	cases := map[string]struct {
		answer string
		source string
		want   string
	}{
		"missing key":   {`{"credentials":{}}`, "body:credentials.token", `no "token"`},
		"not json":      {`<tsResponse/>`, "body:credentials.token", "Accept: application/json"},
		"empty":         {`{"token":""}`, "body:token", "empty"},
		"object":        {`{"token":{"a":1}}`, "body:token", "not a string"},
		"bad index":     {`{"items":[{"t":"a"}]}`, "body:items.3.t", "not an index"},
		"below scalar":  {`{"token":"a"}`, "body:token.x", "not an object"},
		"no header":     {`{}`, "header:X-Token", "no X-Token header"},
		"crlf in token": {`{"token":"a\r\nb"}`, "body:token", "CR/LF"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, tc.answer)
			}))
			t.Cleanup(srv.Close)
			cfg := sessionConfig(t, srv.URL, map[string]any{
				"session_login_url": "/login", "session_token_source": tc.source, "session_capture": nil,
			})
			_, err := NewHTTPClient(cfg).Get(srv.URL + "/data") //nolint:noctx,bodyclose // the call fails
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrSessionLogin)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestSessionLoginTokenFromAListAndANumber(t *testing.T) {
	doc := map[string]any{"items": []any{map[string]any{"id": json.Number("42")}}, "ok": true}
	v, err := jsonPathString(doc, "items.0.id")
	require.NoError(t, err)
	assert.Equal(t, "42", v)
	v, err = jsonPathString(doc, "ok")
	require.NoError(t, err)
	assert.Equal(t, "true", v)
}

func TestSessionLoginUnreachableSignIn(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	cfg := sessionConfig(t, base, map[string]any{"session_login_url": base + "/login?key=abc"})
	_, err := NewHTTPClient(cfg).Get(base + "/data") //nolint:noctx,bodyclose // the call fails
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSessionLogin)
	assert.NotContains(t, err.Error(), "key=abc", "the sign-in URL is quoted without its query")
}

func TestSessionLoginBodyEscapesTheSecret(t *testing.T) {
	secret := `a"b&c<d`
	cases := map[string]string{
		"application/json":                  `a\"b&c<d`,
		"application/x-www-form-urlencoded": "a%22b%26c%3Cd",
		"application/xml":                   "a&#34;b&amp;c&lt;d",
		"text/plain":                        secret,
	}
	for ct, want := range cases {
		s := Config{LoginBody: "[{{secret}}]", LoginContentType: ct, Secret: secret}
		assert.Equal(t, "["+want+"]", s.renderBody(), ct)
	}
}

func TestValidateSessionLoginRefuses(t *testing.T) {
	cases := map[string]struct {
		keys map[string]any
		want string
	}{
		"no login url":         {map[string]any{"session_login_url": ""}, "session_login_url is required"},
		"bad login url":        {map[string]any{"session_login_url": "ftp://x/y"}, "session_login_url must be an http(s) URL"},
		"bad logout url":       {map[string]any{"session_logout_url": "ftp://x/y"}, "session_logout_url must be"},
		"bad method":           {map[string]any{"session_login_method": "TRACE"}, "invalid session_login_method"},
		"no credential":        {map[string]any{"session_login_body": "", "session_login_secret": ""}, "session_login_body or session_login_secret is required"},
		"secret nowhere":       {map[string]any{"session_login_body": `{"a":1}`}, "does not contain {{secret}}"},
		"placeholder no value": {map[string]any{"session_login_secret": ""}, "session_login_secret is empty"},
		"invalid json":         {map[string]any{"session_login_body": `{"a":{{secret}}}`}, "not valid JSON"},
		"no token source":      {map[string]any{"session_token_source": ""}, "session_token_source is required"},
		"bad token source":     {map[string]any{"session_token_source": "cookie:sid"}, "invalid session_token_source"},
		"empty path segment":   {map[string]any{"session_token_source": "body:a..b"}, "empty segment"},
		"bad source header":    {map[string]any{"session_token_source": "header:X Bad"}, "is not valid"},
		"bad capture name":     {map[string]any{"session_capture": map[string]any{"Site-ID": "body:a"}}, "lower-case"},
		"bad capture source":   {map[string]any{"session_capture": map[string]any{"site": "a"}}, "invalid session_capture"},
		"bad token header":     {map[string]any{"session_token_header": "Host"}, "session_token_header"},
		"prefix newline":       {map[string]any{"session_token_prefix": "a\nb"}, "session_token_prefix"},
		"bad login header":     {map[string]any{"session_login_headers": map[string]any{"X Bad": "1"}}, "session_login_headers name"},
		"login header crlf":    {map[string]any{"session_login_headers": map[string]any{"Accept": "a\r\nb"}}, "CR/LF"},
		"negative ttl":         {map[string]any{"session_ttl": "-1m"}, "session_ttl"},
		"non-error status":     {map[string]any{"session_expired_statuses": []any{401, 200}}, "contains 200"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Parse("https://h.example.com", sessionKeys("https://h.example.com", tc.keys))
			err := c.Validate("apigateway")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.True(t, strings.HasPrefix(err.Error(), "apigateway: "), "the refusal speaks in the kind's voice")
		})
	}
}

func TestSessionLoginIntList(t *testing.T) {
	assert.Equal(t, []int{401, 403}, intList([]any{401.0, "403", "x", 1.5}))
	assert.Equal(t, []int{401, 419}, intList("401, 419"))
	assert.Equal(t, []int{401}, intList([]int{401}))
	assert.Equal(t, []int{7}, intList([]any{json.Number("7"), int64(0)})[:1])
	assert.Nil(t, intList(map[string]any{}))
}

func TestSessionLoginResolveAgainst(t *testing.T) {
	assert.Equal(t, "https://h/api/3.22/auth/signin", resolveAgainst("https://h/api/3.22", "auth/signin"))
	assert.Equal(t, "https://h/auth/signin", resolveAgainst("https://h/api/3.22", "/auth/signin"))
	assert.Equal(t, "https://o/x", resolveAgainst("https://h/api", "https://o/x"))
	assert.Equal(t, "/x", resolveAgainst("not a url", "/x"))
	assert.Equal(t, "", resolveAgainst("https://h", " "))
}

func TestSessionLoginBodyThatCannotBeReplayedIsReturnedAsSent(t *testing.T) {
	up, srv := newSessionUpstream(t)
	client := NewHTTPClient(sessionConfig(t, srv.URL, nil))
	get(t, client, srv.URL+"/x")
	up.revoke()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/x", io.NopCloser(strings.NewReader("once"))) //nolint:noctx // test request
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "a body read once is not sent twice")

	status, _ := get(t, client, srv.URL+"/x")
	assert.Equal(t, http.StatusOK, status, "the next call signs in")
}

func TestSessionLoginSubstitutionLeavesOtherPathsAlone(t *testing.T) {
	u, err := url.Parse("https://h/a%2Fb/c")
	require.NoError(t, err)
	out, err := substituteSessionValues(*u, nil)
	require.NoError(t, err)
	assert.Equal(t, "/a%2Fb/c", out.EscapedPath())

	u, err = url.Parse("https://h/a%2Fb/%7Bsession.site%7D")
	require.NoError(t, err)
	out, err = substituteSessionValues(*u, map[string]string{"site": "s/1"})
	require.NoError(t, err)
	assert.Equal(t, "/a%2Fb/s%2F1", out.EscapedPath(), "an escaped path keeps its escapes and the value is escaped")

	_, err = substituteSessionValues(*u, map[string]string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "captures none")
}

func TestSessionLoginIsSessionMessage(t *testing.T) {
	for _, err := range []error{ErrSessionLogin, ErrSessionRejected, ErrUnknownSessionValue} {
		assert.True(t, IsSessionMessage(`Get "https://h/x": apigateway: `+err.Error()+": detail"), err.Error())
	}
	assert.False(t, IsSessionMessage(`Get "https://h/x": dial tcp: connection refused`))
}
