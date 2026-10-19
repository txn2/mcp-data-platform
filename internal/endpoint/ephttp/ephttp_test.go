package ephttp

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/botcheck"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
)

// runner resolves the bindings it holds and answers each request with the
// request it was handed, so a test reads what the surface passed on.
type runner struct {
	bindings map[string]*epbinding.Binding
	err      error
	served   []eprun.Request
}

func (f *runner) Resolve(_ context.Context, name string) (*eprun.Target, error) {
	if f.err != nil {
		return nil, f.err
	}
	b, ok := f.bindings[name]
	if !ok {
		return nil, epbinding.ErrNotFound
	}
	return &eprun.Target{Binding: b}, nil
}

func (f *runner) Serve(_ context.Context, _ *eprun.Target, req eprun.Request) eprun.Response {
	f.served = append(f.served, req)
	caller := ""
	if req.Caller != nil {
		caller = req.Caller.Email
	}
	body, _ := json.Marshal(map[string]any{"params": req.Params, "caller": caller, "body": string(req.Body)})
	return eprun.Response{Status: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}, "X-From": {"handler"}}, Body: body}
}

// checker accepts the answer "good" once per endpoint, refuses any other.
type checker struct {
	public    bool
	publicErr error
	verifyErr error
	used      map[string]bool
	issued    []string
	issueErr  error
}

func (c *checker) PublicAllowed(context.Context) (bool, error) { return c.public, c.publicErr }

func (c *checker) Issue(_ context.Context, endpoint string) (botcheck.Challenge, error) {
	c.issued = append(c.issued, endpoint)
	return botcheck.Challenge{Provider: botcheck.ProviderPoW, Token: "tok", Difficulty: 8}, c.issueErr
}

func (c *checker) Verify(_ context.Context, endpoint, answer string) error {
	if c.verifyErr != nil {
		return c.verifyErr
	}
	switch {
	case answer == "":
		return botcheck.ErrMissing
	case answer != "good":
		return botcheck.ErrInvalid
	case c.used[endpoint]:
		return botcheck.ErrReplayed
	}
	c.used[endpoint] = true
	return nil
}

func binding(name, mode string) *epbinding.Binding {
	return &epbinding.Binding{
		ID: name, Name: name, Methods: []string{"GET", "POST"}, Path: "/{id}", AuthMode: mode, Enabled: true,
		MaxBodyBytes: 64, RateLimit: 600, CallerRateLimit: 2, AllowedOrigins: []string{"https://forms.example.com"},
	}
}

type fixture struct {
	mux     *http.ServeMux
	runner  *runner
	checker *checker
}

func newFixture() *fixture {
	f := &fixture{
		runner: &runner{bindings: map[string]*epbinding.Binding{
			"contact": binding("contact", epbinding.AuthPublic),
			"orders":  binding("orders", epbinding.AuthSignedIn),
		}},
		checker: &checker{public: true, used: map[string]bool{}},
	}
	h := &Handler{Runner: f.runner, Checker: f.checker, Authenticate: func(r *http.Request) (*eprun.Caller, error) {
		switch r.Header.Get("Authorization") {
		case "Bearer ann-key":
			return &eprun.Caller{UserID: "ann", Email: "ann@example.com", AuthType: "apikey"}, nil
		case "Bearer bo-key":
			return &eprun.Caller{UserID: "bo", Email: "bo@example.com", AuthType: "apikey"}, nil
		case "Bearer csrf":
			return nil, ErrForbidden
		}
		if c, err := r.Cookie("session"); err == nil && c.Value == "ann" {
			return &eprun.Caller{UserID: "ann", Email: "ann@example.com", AuthType: "browser_session"}, nil
		}
		return nil, nil //nolint:nilnil // nobody: no credential and no refusal
	}}
	f.mux = http.NewServeMux()
	h.Mount(f.mux)
	return f
}

func (f *fixture) do(method, path string, header http.Header, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	maps.Copy(req.Header, header)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func fields(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	out := map[string]any{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out
}

// #2174: a public endpoint answers an anonymous request with a valid bot
// check answer, and refuses one without.
func TestPublic_AnswersOnlyARequestThatPassedTheBotCheck(t *testing.T) {
	f := newFixture()
	rec := f.do("POST", "/endpoints/contact/7", nil, "hi")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "no bot check answer")

	rec = f.do("POST", "/endpoints/contact/7", http.Header{HeaderBotCheck: {"good"}}, "hi")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	out := fields(t, rec)
	assert.Equal(t, map[string]any{"id": "7"}, out["params"])
	assert.Empty(t, out["caller"], "a public request has no caller")
	assert.Equal(t, "hi", out["body"])
	assert.Equal(t, "handler", rec.Header().Get("X-From"))
	assert.Equal(t, contentPolicy, rec.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

	rec = f.do("POST", "/endpoints/contact/7", http.Header{HeaderBotCheck: {"good"}}, "hi")
	assert.Equal(t, http.StatusForbidden, rec.Code, "the same answer sent again")
	assert.Contains(t, rec.Body.String(), "already used")
}

func TestPublic_OpenReadsLetAGetThroughWithoutACheck(t *testing.T) {
	f := newFixture()
	assert.Equal(t, http.StatusForbidden, f.do("GET", "/endpoints/contact/1", nil, "").Code)
	f.runner.bindings["contact"].OpenReads = true
	assert.Equal(t, http.StatusOK, f.do("GET", "/endpoints/contact/1", nil, "").Code)
	head := f.do("HEAD", "/endpoints/contact/1", nil, "")
	assert.Equal(t, http.StatusOK, head.Code)
	assert.Empty(t, head.Body.String(), "a HEAD is answered without a body")
	assert.Equal(t, http.StatusForbidden, f.do("POST", "/endpoints/contact/1", nil, "").Code, "a write still needs the check")
}

func TestPublic_RefusedWhenThereIsNoBotCheck(t *testing.T) {
	f := newFixture()
	f.checker.public = false
	rec := f.do("POST", "/endpoints/contact/1", http.Header{HeaderBotCheck: {"good"}}, "")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	f.checker.publicErr = errors.New("boom")
	assert.Equal(t, http.StatusServiceUnavailable, f.do("POST", "/endpoints/contact/1", nil, "").Code)

	f.checker.public, f.checker.publicErr, f.checker.verifyErr = true, nil, errors.New("provider down")
	rec = f.do("POST", "/endpoints/contact/1", http.Header{HeaderBotCheck: {"good"}}, "")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "a check that could not be made is not the caller's refusal")
}

// #2174: a signed_in endpoint answers an API key and a browser session,
// refuses an anonymous request with 401, and the handler reads who the
// credential named.
func TestSignedIn_AnswersAKeyAndASessionAndRefusesNobody(t *testing.T) {
	f := newFixture()
	rec := f.do("GET", "/endpoints/orders/1", http.Header{"Authorization": {"Bearer ann-key"}}, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "ann@example.com", fields(t, rec)["caller"])
	assert.Equal(t, "apikey", f.runner.served[0].Caller.AuthType)

	rec = f.do("GET", "/endpoints/orders/1", http.Header{"Cookie": {"session=ann"}}, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "browser_session", f.runner.served[1].Caller.AuthType)

	rec = f.do("GET", "/endpoints/orders/1", nil, "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("WWW-Authenticate"))

	assert.Equal(t, http.StatusForbidden, f.do("POST", "/endpoints/orders/1", http.Header{"Authorization": {"Bearer csrf"}}, "").Code)
}

// #2174: a signed-in caller past their own limit gets 429 while another
// caller is still answered.
func TestSignedIn_ACallerPastTheirLimitIsRefusedAndAnotherIsNot(t *testing.T) {
	f := newFixture()
	ann := http.Header{"Authorization": {"Bearer ann-key"}}
	for range 2 {
		require.Equal(t, http.StatusOK, f.do("GET", "/endpoints/orders/1", ann, "").Code)
	}
	rec := f.do("GET", "/endpoints/orders/1", ann, "")
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "30", rec.Header().Get("Retry-After"))
	assert.Equal(t, http.StatusOK, f.do("GET", "/endpoints/orders/1", http.Header{"Authorization": {"Bearer bo-key"}}, "").Code)
}

func TestEndpointLimitIsTheCeiling(t *testing.T) {
	f := newFixture()
	b := f.runner.bindings["contact"]
	b.RateLimit, b.OpenReads = 1, true
	assert.Equal(t, http.StatusOK, f.do("GET", "/endpoints/contact/1", nil, "").Code)
	assert.Equal(t, http.StatusTooManyRequests, f.do("GET", "/endpoints/contact/1", nil, "").Code)
	b.RateLimit = 5
	assert.Equal(t, http.StatusOK, f.do("GET", "/endpoints/contact/1", nil, "").Code, "a changed limit starts a new bucket")
}

// #2174: a browser fetch from an allowed origin passes its preflight and can
// read the response; from an origin that is not listed the preflight is
// refused.
func TestCORS_PreflightAndResponseFromTheBindingsOrigins(t *testing.T) {
	f := newFixture()
	pre := http.Header{
		"Origin": {"https://forms.example.com"}, "Access-Control-Request-Method": {"POST"},
		"Access-Control-Request-Headers": {"content-type"},
	}
	rec := f.do("OPTIONS", "/endpoints/orders/1", pre, "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "https://forms.example.com", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "GET, POST, HEAD, OPTIONS", rec.Header().Get("Access-Control-Allow-Methods"))
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Headers"), "Content-Type")
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"), "credentials never cross origins")

	rec = f.do("POST", "/endpoints/orders/1", http.Header{
		"Origin":        {"https://forms.example.com"},
		"Authorization": {"Bearer ann-key"}, "Content-Type": {"application/json"},
	}, `{"a":1}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "https://forms.example.com", rec.Header().Get("Access-Control-Allow-Origin"))

	pre["Origin"] = []string{"https://elsewhere.example.com"}
	rec = f.do("OPTIONS", "/endpoints/orders/1", pre, "")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))

	rec = f.do("GET", "/endpoints/orders/1", http.Header{"Origin": {"https://elsewhere.example.com"}, "Authorization": {"Bearer ann-key"}}, "")
	assert.Equal(t, http.StatusOK, rec.Code, "a same-site tool sending an Origin is answered; the browser withholds the response")
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))

	rec = f.do("OPTIONS", "/endpoints/orders/1", nil, "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "GET, POST, HEAD, OPTIONS", rec.Header().Get("Allow"))
}

// #2172: a request body past the limit gets 413 without running the script.
func TestBodyPastTheLimitIs413WithoutRunning(t *testing.T) {
	f := newFixture()
	ann := http.Header{"Authorization": {"Bearer ann-key"}}
	rec := f.do("POST", "/endpoints/orders/1", ann, strings.Repeat("x", 65))
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/endpoints/orders/1", strings.NewReader(strings.Repeat("x", 65)))
	req.Header.Set("Authorization", "Bearer ann-key")
	req.ContentLength = -1
	rec = httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, "a body of unknown length is cut at the limit")
	assert.Empty(t, f.runner.served)
}

func TestRouting(t *testing.T) {
	f := newFixture()
	ann := http.Header{"Authorization": {"Bearer ann-key"}}
	assert.Equal(t, http.StatusNotFound, f.do("GET", "/endpoints/nope", ann, "").Code)
	assert.Equal(t, http.StatusNotFound, f.do("GET", "/endpoints/orders", ann, "").Code, "the path template is /{id}")
	assert.Equal(t, http.StatusNotFound, f.do("GET", "/endpoints/orders/1/2", ann, "").Code)
	rec := f.do("DELETE", "/endpoints/orders/1", ann, "")
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, "GET, POST, HEAD, OPTIONS", rec.Header().Get("Allow"))

	f.runner.bindings["orders"].Enabled = false
	assert.Equal(t, http.StatusNotFound, f.do("GET", "/endpoints/orders/1", ann, "").Code, "a disabled binding answers nothing")

	f.runner.err = errors.New("boom")
	assert.Equal(t, http.StatusInternalServerError, f.do("GET", "/endpoints/orders/1", ann, "").Code)
}

func TestChallenge(t *testing.T) {
	f := newFixture()
	rec := f.do("GET", "/botcheck/challenge?endpoint=contact", http.Header{"Origin": {"https://forms.example.com"}}, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "tok", fields(t, rec)["token"])
	assert.Equal(t, "https://forms.example.com", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, []string{"contact"}, f.checker.issued)

	assert.Equal(t, http.StatusNotFound, f.do("GET", "/botcheck/challenge?endpoint=orders", nil, "").Code, "a signed-in endpoint has no challenge")
	assert.Equal(t, http.StatusNotFound, f.do("GET", "/botcheck/challenge?endpoint=nope", nil, "").Code)

	pre := http.Header{"Origin": {"https://forms.example.com"}, "Access-Control-Request-Method": {"GET"}}
	assert.Equal(t, http.StatusNoContent, f.do("OPTIONS", "/botcheck/challenge?endpoint=contact", pre, "").Code)
	assert.Equal(t, http.StatusNoContent, f.do("OPTIONS", "/botcheck/challenge?endpoint=contact", nil, "").Code)

	f.checker.issueErr = errors.New("no key")
	assert.Equal(t, http.StatusServiceUnavailable, f.do("GET", "/botcheck/challenge?endpoint=contact", nil, "").Code)

	f.runner.bindings["contact"].RateLimit = 1
	f.checker.issueErr = nil
	assert.Equal(t, http.StatusOK, f.do("GET", "/botcheck/challenge?endpoint=contact", nil, "").Code)
	assert.Equal(t, http.StatusTooManyRequests, f.do("GET", "/botcheck/challenge?endpoint=contact", nil, "").Code)
	assert.Equal(t, http.StatusOK, f.do("POST", "/endpoints/contact/1", http.Header{HeaderBotCheck: {"good"}}, "").Code,
		"a challenge is counted in a bucket of its own, so the request it guards is still answered")
}

func TestSolverIsServed(t *testing.T) {
	rec := newFixture().do("GET", "/botcheck/solver.js", nil, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/javascript; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), "PlatformBotCheck")
}

func TestWriteLeavesTheBodyOffA204(t *testing.T) {
	rec := httptest.NewRecorder()
	write(rec, httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody), eprun.Response{
		Status: http.StatusNoContent, Body: []byte("x"),
		Header: http.Header{"Cache-Control": {"max-age=60"}},
	})
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
	assert.Equal(t, "max-age=60", rec.Header().Get("Cache-Control"), "the handler's own cache policy is kept")
}
